package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/dialogue"
	"github.com/thebtf/aimux/pkg/executor/picker"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/parser"
	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/workflow"
)

func workflowRecipeWorkerTypeFromID(workflowID string) loom.WorkerType {
	if strings.TrimSpace(workflowID) == "" {
		return ""
	}
	return workflowRecipeWorkerType
}

const workflowRecipeWorkerType loom.WorkerType = "recipe_workflow"

type workflowRecipeDispatchFunc func(ctx context.Context, cli string, spec picker.TaskSpec, metadata map[string]any) (string, string, error)

type workflowRecipeWorker struct {
	server     *Server
	defaultCLI string
	dispatch   workflowRecipeDispatchFunc
	patternFn  func(name string, input map[string]any) (map[string]any, error)
	dialogue   workflow.DialogueRunner
}

func (w workflowRecipeWorker) Type() loom.WorkerType { return workflowRecipeWorkerType }

func (w workflowRecipeWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	if task == nil {
		return nil, extypes.NewUserInputError("workflow recipe worker task is nil", nil)
	}
	workflowID, ok := metadataString(task.Metadata, "recipe_workflow_id")
	if !ok || strings.TrimSpace(workflowID) == "" {
		return nil, extypes.NewUserInputError("workflow recipe worker requires recipe_workflow_id metadata", nil)
	}
	stepsFn, ok := workflow.Registry[strings.TrimSpace(workflowID)]
	if !ok {
		return nil, extypes.NewUserInputError(fmt.Sprintf("workflow recipe %q is not registered", workflowID), nil)
	}

	sender := &workflowRecipeExecutorSender{
		server:     w.server,
		task:       task,
		defaultCLI: w.effectiveDefaultCLI(task),
		dispatch:   w.effectiveDispatch(),
	}
	dlg := w.dialogue
	if dlg == nil {
		dlg = dialogue.New()
	}
	patternFn := w.patternFn
	if patternFn == nil {
		patternFn = workflowPatternFn
	}

	engine := workflow.New(sender, dlg, patternFn, sender.Participant)
	started := time.Now()
	result, err := engine.Execute(ctx, stepsFn(), workflowInputFromTask(task))
	if err != nil {
		return nil, err
	}
	if result.Status != "completed" {
		return nil, workflowRecipeStatusError(result)
	}

	metadata := cloneTaskMetadata(task.Metadata)
	metadata["worker_type"] = string(workflowRecipeWorkerType)
	metadata["workflow_result_status"] = result.Status
	metadata["workflow_step_count"] = len(result.Steps)
	metadata["workflow_step_statuses"] = workflowStepStatuses(result.Steps)
	metadata["duration_ms"] = time.Since(started).Milliseconds()

	return &loom.WorkerResult{
		Content:    formatWorkflowResult(result),
		Metadata:   metadata,
		DurationMS: time.Since(started).Milliseconds(),
	}, nil
}

func (w workflowRecipeWorker) effectiveDefaultCLI(task *loom.Task) string {
	if task != nil {
		if cli := strings.TrimSpace(task.CLI); cli != "" {
			return cli
		}
		if cli, ok := metadataString(task.Metadata, recipePolicySelectedCLIMetadata); ok && strings.TrimSpace(cli) != "" {
			return strings.TrimSpace(cli)
		}
		if cli, ok := metadataString(task.Metadata, "driver_cli_override"); ok && strings.TrimSpace(cli) != "" {
			return strings.TrimSpace(cli)
		}
	}
	if strings.TrimSpace(w.defaultCLI) != "" {
		return strings.TrimSpace(w.defaultCLI)
	}
	return "codex"
}

func (w workflowRecipeWorker) effectiveDispatch() workflowRecipeDispatchFunc {
	if w.dispatch != nil {
		return w.dispatch
	}
	return w.dispatchViaServer
}

func (w workflowRecipeWorker) dispatchViaServer(ctx context.Context, cli string, spec picker.TaskSpec, metadata map[string]any) (string, string, error) {
	if w.server == nil {
		return "", cli, extypes.NewCapabilityMismatch("workflow recipe worker requires server dispatch", nil)
	}
	if w.server.fallbackPicker != nil {
		result, err := w.server.fallbackPicker.RunPrimary(ctx, cli, spec, fallbackOptionsFromTaskMetadata(metadata), w.server.taskDispatch)
		if err != nil {
			return "", cli, err
		}
		return result.Content, result.SelectedCLI, nil
	}
	raw, err := w.server.taskDispatch(ctx, cli, spec)
	return raw, cli, err
}

type workflowRecipeExecutorSender struct {
	server     *Server
	task       *loom.Task
	defaultCLI string
	dispatch   workflowRecipeDispatchFunc
}

type workflowRecipeExecutorHandle struct {
	requested string
	cli       string
}

func (h workflowRecipeExecutorHandle) ExecutorName() string { return h.cli }

func (s *workflowRecipeExecutorSender) Get(_ context.Context, name string) (workflow.ExecutorHandle, error) {
	requested := strings.TrimSpace(name)
	cli := requested
	if cli == "" || !s.hasProfile(cli) {
		cli = s.defaultCLI
	}
	if strings.TrimSpace(cli) == "" {
		return nil, extypes.NewCapabilityMismatch("workflow recipe executor has no CLI", nil)
	}
	return workflowRecipeExecutorHandle{requested: requested, cli: cli}, nil
}

func (s *workflowRecipeExecutorSender) Send(ctx context.Context, h workflow.ExecutorHandle, msg types.Message) (*types.Response, error) {
	handle, ok := h.(workflowRecipeExecutorHandle)
	if !ok {
		return nil, extypes.NewCapabilityMismatch(fmt.Sprintf("workflow recipe executor got unsupported handle %T", h), nil)
	}
	prompt := workflowMessagePrompt(msg)
	if handle.requested != "" && handle.requested != handle.cli {
		prompt = fmt.Sprintf("Requested workflow role/executor: %s. Execute this step using the available CLI %s.\n\n%s", handle.requested, handle.cli, prompt)
	}
	spec := picker.TaskSpec{
		TaskClass:      "review",
		Prompt:         prompt,
		CWD:            s.task.CWD,
		Env:            cloneEnv(s.task.Env),
		Model:          s.task.Model,
		Effort:         s.task.Effort,
		Sandbox:        "read-only",
		TimeoutSeconds: s.task.Timeout,
		OnOutput:       s.progressSink(),
	}
	if sandbox, ok := metadataString(s.task.Metadata, "sandbox"); ok && strings.TrimSpace(sandbox) != "" {
		spec.Sandbox = strings.TrimSpace(sandbox)
	}
	raw, selectedCLI, err := s.dispatch(ctx, handle.cli, spec, s.task.Metadata)
	if err != nil {
		return nil, err
	}
	profile, profileErr := s.profile(selectedCLI)
	if profileErr != nil {
		return &types.Response{Content: raw}, nil
	}
	parsed, _ := parserParseContent(raw, profile)
	return &types.Response{Content: parsed}, nil
}

func (s *workflowRecipeExecutorSender) Participant(name string) (dialogue.Participant, error) {
	handle, err := s.Get(context.Background(), name)
	if err != nil {
		return nil, err
	}
	return workflowRecipeParticipant{name: strings.TrimSpace(name), role: "workflow participant", sender: s, handle: handle}, nil
}

func (s *workflowRecipeExecutorSender) hasProfile(cli string) bool {
	if s == nil || s.server == nil || s.server.registry == nil || strings.TrimSpace(cli) == "" {
		return false
	}
	profile, err := s.server.registry.Get(cli)
	return err == nil && profile != nil
}

func (s *workflowRecipeExecutorSender) profile(cli string) (*config.CLIProfile, error) {
	if s == nil || s.server == nil || s.server.registry == nil {
		return nil, fmt.Errorf("server registry unavailable")
	}
	return s.server.registry.Get(cli)
}

func (s *workflowRecipeExecutorSender) progressSink() func(string) {
	if s == nil || s.server == nil || s.server.loom == nil || s.task == nil || s.task.ID == "" {
		return nil
	}
	return func(line string) {
		if strings.TrimSpace(line) == "" {
			return
		}
		_ = s.server.loom.AppendProgress(s.task.ID, line)
	}
}

type workflowRecipeParticipant struct {
	name   string
	role   string
	sender *workflowRecipeExecutorSender
	handle workflow.ExecutorHandle
}

func (p workflowRecipeParticipant) Name() string {
	if p.name != "" {
		return p.name
	}
	return p.handle.ExecutorName()
}

func (p workflowRecipeParticipant) Role() string { return p.role }

func (p workflowRecipeParticipant) Respond(ctx context.Context, prompt string, history []dialogue.DialogueTurn) (string, error) {
	resp, err := p.sender.Send(ctx, p.handle, types.Message{Content: dialoguePrompt(prompt, history)})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func workflowPatternFn(name string, input map[string]any) (map[string]any, error) {
	handler := think.GetPattern(name)
	if handler == nil {
		return nil, fmt.Errorf("unknown think pattern %q", name)
	}
	validInput, err := handler.Validate(normalizeWorkflowPatternInput(handler, input))
	if err != nil {
		return nil, err
	}
	result, err := handler.Handle(validInput, "")
	if err != nil {
		return nil, err
	}
	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		summary = think.GenerateSummary(result, "workflow")
	}
	text := workflowPatternResultText(result, summary, input)
	out := map[string]any{
		"pattern":  result.Pattern,
		"status":   result.Status,
		"summary":  summary,
		"result":   text,
		"analysis": text,
		"data":     result.Data,
	}
	return out, nil
}

func normalizeWorkflowPatternInput(handler think.PatternHandler, input map[string]any) map[string]any {
	out := cloneAnyMap(input)
	fallback := firstWorkflowString(out)
	for name, field := range handler.SchemaFields() {
		value, exists := out[name]
		if exists {
			if field.Type == "array" {
				if text, ok := value.(string); ok && !strings.HasPrefix(strings.TrimSpace(text), "[") {
					out[name] = []any{map[string]any{"name": "workflow_context", "description": text}}
				}
			}
			continue
		}
		if !field.Required {
			continue
		}
		switch field.Type {
		case "array":
			out[name] = []any{map[string]any{"name": "workflow_context", "description": fallback}}
		case "object":
			out[name] = map[string]any{"workflow_context": fallback}
		default:
			out[name] = fallback
		}
	}
	if _, ok := out["decision"]; !ok {
		if thought, ok := out["thought"].(string); ok && strings.TrimSpace(thought) != "" {
			out["decision"] = thought
		}
	}
	return out
}

func workflowInputFromTask(task *loom.Task) workflow.WorkflowInput {
	target, _ := metadataString(task.Metadata, "target")
	extra := map[string]any{
		"task_id": task.ID,
		"target":  target,
	}
	return workflow.WorkflowInput{
		Topic: task.Prompt,
		Focus: target,
		Files: workflowFilesFromTarget(target),
		Extra: extra,
	}
}

func workflowFilesFromTarget(target string) []string {
	if strings.TrimSpace(target) == "" || strings.EqualFold(strings.TrimSpace(target), "HEAD") {
		return nil
	}
	parts := strings.Split(target, ",")
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			files = append(files, part)
		}
	}
	return files
}

func workflowRecipeStatusError(result *workflow.WorkflowResult) error {
	if result == nil {
		return extypes.NewUserInputError("workflow recipe execution failed without a result", nil)
	}
	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		summary = "workflow did not complete"
	}
	return extypes.NewUserInputError(fmt.Sprintf("workflow recipe ended with status %q: %s", result.Status, summary), nil)
}

func workflowPatternResultText(result *think.ThinkResult, summary string, input map[string]any) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Think pattern: ")
	b.WriteString(result.Pattern)
	if strings.TrimSpace(result.Status) != "" {
		b.WriteString("\nStatus: ")
		b.WriteString(result.Status)
	}
	if strings.TrimSpace(summary) != "" {
		b.WriteString("\nSummary: ")
		b.WriteString(summary)
	}
	appendWorkflowPatternContext(&b, input)
	if len(result.Data) > 0 {
		if payload, err := json.MarshalIndent(result.Data, "", "  "); err == nil {
			b.WriteString("\nData:\n")
			b.Write(payload)
		}
	}
	return strings.TrimSpace(b.String())
}

func appendWorkflowPatternContext(b *strings.Builder, input map[string]any) {
	if b == nil || len(input) == 0 {
		return
	}
	for _, key := range []string{"topic", "focus"} {
		value, ok := input[key].(string)
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(strings.ToUpper(key[:1]))
		b.WriteString(key[1:])
		b.WriteString(": ")
		b.WriteString(value)
	}
}

func formatWorkflowResult(result *workflow.WorkflowResult) string {
	if result == nil {
		return "Workflow status: failed\nSummary: no workflow result"
	}
	var b strings.Builder
	b.WriteString("Workflow status: ")
	b.WriteString(result.Status)
	if strings.TrimSpace(result.Summary) != "" {
		b.WriteString("\n\nSummary:\n")
		b.WriteString(result.Summary)
	}
	if len(result.Steps) > 0 {
		b.WriteString("\n\nSteps:")
		for _, step := range result.Steps {
			b.WriteString("\n- ")
			b.WriteString(step.Name)
			b.WriteString(" [")
			b.WriteString(step.Status)
			b.WriteString("]")
			if strings.TrimSpace(step.Content) != "" {
				b.WriteString(": ")
				b.WriteString(summarizeLeafOutput(step.Content))
			}
		}
	}
	return b.String()
}

func workflowStepStatuses(steps []workflow.StepResult) []string {
	out := make([]string, 0, len(steps))
	for _, step := range steps {
		out = append(out, step.Name+"="+step.Status)
	}
	return out
}

func workflowMessagePrompt(msg types.Message) string {
	if len(msg.History) == 0 {
		return msg.Content
	}
	var b strings.Builder
	for _, turn := range msg.History {
		if strings.TrimSpace(turn.Content) == "" {
			continue
		}
		b.WriteString("Prior context (")
		b.WriteString(turn.Role)
		b.WriteString("):\n")
		b.WriteString(turn.Content)
		b.WriteString("\n\n")
	}
	b.WriteString(msg.Content)
	return b.String()
}

func dialoguePrompt(prompt string, history []dialogue.DialogueTurn) string {
	if len(history) == 0 {
		return prompt
	}
	var b strings.Builder
	for _, turn := range history {
		b.WriteString(fmt.Sprintf("<dialogue-turn participant=%q role=%q>\n%s\n</dialogue-turn>\n", turn.Participant, turn.Role, turn.Content))
	}
	b.WriteString("\n")
	b.WriteString(prompt)
	return b.String()
}

func cloneAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstWorkflowString(values map[string]any) string {
	for _, key := range []string{"thought", "topic", "focus", "issue", "components"} {
		if value, ok := values[key]; ok {
			if text := anyWorkflowString(value); text != "" {
				return text
			}
		}
	}
	for _, value := range values {
		if text := anyWorkflowString(value); text != "" {
			return text
		}
	}
	return "workflow context"
}

func anyWorkflowString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		payload, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(payload))
	}
}

func parserParseContent(raw string, profile *config.CLIProfile) (string, string) {
	return parser.ParseContent(raw, profile.OutputFormat)
}
