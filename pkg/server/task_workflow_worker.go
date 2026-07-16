package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/dialogue"
	"github.com/thebtf/aimux/pkg/executor/picker"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/parser"
	"github.com/thebtf/aimux/pkg/recipes"
	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/workerruntime"
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
	server         *Server
	defaultCLI     string
	dispatch       workflowRecipeDispatchFunc
	patternFn      func(name string, input map[string]any) (map[string]any, error)
	dialogue       workflow.DialogueRunner
	newEventWriter func(taskID string) (*workerruntime.EventWriter, error)
}

func (w workflowRecipeWorker) Type() loom.WorkerType { return workflowRecipeWorkerType }

func (w workflowRecipeWorker) eventWriter(taskID string) (*workerruntime.EventWriter, error) {
	if w.newEventWriter != nil {
		return w.newEventWriter(taskID)
	}
	if w.server == nil || w.server.loom == nil {
		return nil, nil
	}
	return workerruntime.NewEventWriter(workerruntime.DefaultEventWriterConfig(loomTaskEventSink{
		engine: w.server.loom,
		taskID: taskID,
	}))
}

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

	sessionRequest, sessionErr := taskSessionRequestFromMetadata(task.Metadata)
	if sessionErr != nil {
		return nil, extypes.NewUserInputError(fmt.Sprintf("workflow recipe worker has invalid internal Worker Session request: %v", sessionErr), sessionErr)
	}

	// writer/truncated are additive: a nil writer (no server.loom, e.g. unit
	// tests with a custom dispatch) keeps Send()'s prior OnOutput-only
	// behavior, preserving custom/test dispatch compatibility.
	writer, writerErr := w.eventWriter(task.ID)
	if writerErr != nil {
		return nil, artifactSinkUnavailableError(writerErr)
	}
	defaultCLI := w.effectiveDefaultCLI(task)
	sender := &workflowRecipeExecutorSender{
		server:     w.server,
		task:       task,
		defaultCLI: defaultCLI,
		session:    sessionRequest,
		dispatch:   w.effectiveDispatch(),
		writer:     writer,
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

	if writer != nil {
		status := "failed"
		if err == nil && result != nil && result.Status == "completed" {
			status = "completed"
		}
		workerruntime.PublishFinalTerminal(writer, defaultCLI, status, sender.truncated.Load())
		flushCtx, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
		flushErr := writer.CloseAndFlush(flushCtx)
		cancelFlush()
		if flushErr != nil {
			return nil, artifactSinkUnavailableError(flushErr)
		}
	}

	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, workflowRecipeStatusError(nil)
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
	dispatch := w.server.taskDispatch
	if workflowRecipeForcesReadOnly(metadata) {
		dispatch = func(ctx context.Context, selectedCLI string, selectedSpec picker.TaskSpec) (string, error) {
			if err := requireWorkflowRecipeReadOnlyParticipant(w.server, metadata, selectedCLI); err != nil {
				return "", err
			}
			return w.server.taskDispatch(ctx, selectedCLI, selectedSpec)
		}
	}
	if w.server.fallbackPicker != nil {
		result, err := w.server.fallbackPicker.RunPrimary(ctx, cli, spec, fallbackOptionsFromTaskMetadata(metadata), dispatch)
		if err != nil {
			return "", cli, err
		}
		return result.Content, result.SelectedCLI, nil
	}
	raw, err := dispatch(ctx, cli, spec)
	return raw, cli, err
}

type workflowRecipeExecutorSender struct {
	server     *Server
	task       *loom.Task
	session    taskSessionRequest
	defaultCLI string
	dispatch   workflowRecipeDispatchFunc
	writer     *workerruntime.EventWriter
	truncated  atomic.Bool
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
	if workflowRecipeForcesReadOnly(s.task.Metadata) {
		if err := s.requireReadOnlyParticipant(cli); err != nil {
			return nil, err
		}
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
	outputFormat := ""
	if profile, err := s.profile(handle.cli); err == nil && profile != nil {
		outputFormat = profile.OutputFormat
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
		TaskID:         s.task.ID,
		TenantID:       s.task.TenantID,
		ProjectID:      s.task.ProjectID,
	}
	applyTaskSessionRequestToSpec(&spec, s.session)
	if s.writer != nil {
		attemptSink := workerruntime.NewAttemptExecutorEventSink(s.writer, handle.cli, outputFormat, func(line string) {
			if progressLine := normalizeProgressLine(outputFormat, line); progressLine != "" {
				appendNormalizedRuntimeOutput(s.server.loom, s.task.ID, outputFormat, progressLine)
			}
		})
		spec.EventSink = attemptSink
		defer func() {
			if attemptSink.Truncated() {
				s.truncated.Store(true)
			}
		}()
	} else {
		spec.OnOutput = s.progressSink(outputFormat)
	}
	if !workflowRecipeForcesReadOnly(s.task.Metadata) {
		if sandbox, ok := metadataString(s.task.Metadata, "sandbox"); ok && strings.TrimSpace(sandbox) != "" {
			spec.Sandbox = strings.TrimSpace(sandbox)
		}
	}
	raw, selectedCLI, err := s.dispatch(ctx, handle.cli, spec, s.task.Metadata)
	if err != nil {
		return nil, err
	}
	actualCLI := strings.TrimSpace(selectedCLI)
	if actualCLI == "" {
		actualCLI = handle.cli
	}
	if workflowRecipeForcesReadOnly(s.task.Metadata) {
		if err := s.requireReadOnlyParticipant(actualCLI); err != nil {
			return nil, err
		}
	}
	profile, profileErr := s.profile(actualCLI)
	if profileErr != nil || profile == nil {
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

func (s *workflowRecipeExecutorSender) progressSink(outputFormat string) func(string) {
	if s == nil || s.server == nil || s.server.loom == nil || s.task == nil || s.task.ID == "" {
		return nil
	}
	return func(line string) {
		if strings.TrimSpace(line) == "" {
			return
		}
		appendNormalizedRuntimeOutput(s.server.loom, s.task.ID, outputFormat, line)
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

func workflowRecipeForcesReadOnly(metadata map[string]any) bool {
	recipeID, ok := metadataString(metadata, "recipe_id")
	if !ok || strings.TrimSpace(recipeID) == "" {
		return false
	}
	recipe, ok := recipes.Resolve(recipeID)
	return ok && recipe.ReadOnly
}

func requireWorkflowRecipeReadOnlyParticipant(server *Server, metadata map[string]any, cli string) error {
	if server == nil || server.registry == nil {
		return extypes.NewCapabilityMismatch("workflow read-only recipe participant registry unavailable", nil)
	}
	profile, err := server.registry.Get(cli)
	if err != nil || profile == nil {
		if err == nil {
			err = fmt.Errorf("profile is nil")
		}
		return extypes.NewCapabilityMismatch(fmt.Sprintf("workflow read-only recipe participant %q profile unavailable", cli), err)
	}
	recipeID, ok := metadataString(metadata, "recipe_id")
	if !ok || strings.TrimSpace(recipeID) == "" {
		return extypes.NewCapabilityMismatch("workflow read-only recipe participant policy requires recipe_id metadata", nil)
	}
	recipe, ok := recipes.Resolve(recipeID)
	if !ok {
		return newUnsupportedRecipeIDError(recipeID)
	}
	taskClass, ok := metadataString(metadata, "task_class")
	if !ok || strings.TrimSpace(taskClass) == "" {
		taskClass = recipe.TaskClass
	}
	result := recipes.ValidatePolicy(recipe, providerCapabilitiesFromProfile(cli, taskClass, profile))
	if !result.OK {
		return newTaskRecipePolicyError(result, nil)
	}
	return nil
}

func (s *workflowRecipeExecutorSender) requireReadOnlyParticipant(cli string) error {
	if s == nil || s.task == nil {
		return extypes.NewCapabilityMismatch("workflow read-only recipe participant task metadata unavailable", nil)
	}
	return requireWorkflowRecipeReadOnlyParticipant(s.server, s.task.Metadata, cli)
}

func workflowRecipeStatusError(result *workflow.WorkflowResult) error {
	if result == nil {
		return extypes.NewUnknown("workflow recipe execution failed without a result", nil)
	}
	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		summary = "workflow did not complete"
	}
	msg := fmt.Sprintf("workflow recipe ended with status %q: %s", result.Status, summary)
	switch result.Status {
	case "gated":
		return extypes.NewUserInputError(msg, nil)
	case "failed":
		return extypes.NewUnknown(msg, nil)
	default:
		return extypes.NewUnknown(msg, nil)
	}
}

func workflowPatternResultText(result *think.ThinkResult, summary string, input map[string]any) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	if verdict := workflowRootCauseVerdict(result, input); verdict != "" {
		b.WriteString(verdict)
		b.WriteString("\n")
	}
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

var workflowRootCauseNegativeMarkers = []string{
	"root cause not identified",
	"no root cause identified",
	"unable to identify",
	"cannot identify",
	"could not identify",
	"unknown root cause",
	"root cause unknown",
	"insufficient evidence",
	"not enough evidence",
	"no definitive cause",
	"no definitive root cause",
	"no conclusive cause",
	"not definitively identified",
	"collect more logs",
	"need more logs",
	"more logs are needed",
	"needs more investigation",
	"requires more investigation",
	"cannot determine",
	"could not determine",
	"not determined",
	"undetermined",
	"unclear",
	"possibly",
	"possible cause",
}

var workflowRootCauseAffirmativeMarkers = []string{
	"root cause:",
	"root cause is",
	"root cause was",
	"root cause =",
	"root cause -",
	"root cause —",
	"root cause identified",
	"identified root cause",
	"confirmed root cause",
	"the cause is",
	"the cause was",
	"caused by",
	"is caused by",
	"was caused by",
	"failure stems from",
	"failure stemmed from",
	"traced to",
}

func workflowRootCauseVerdict(result *think.ThinkResult, input map[string]any) string {
	if result == nil || result.Pattern != "decision_framework" || !workflowBool(input, "workflow_root_cause_gate") {
		return ""
	}
	if verdict := workflowRootCauseVerdictFromText(result.Summary); verdict != "" {
		return verdict
	}
	if verdict := workflowRootCauseVerdictFromData(result.Data); verdict != "" {
		return verdict
	}
	if verdict := workflowRootCauseVerdictFromEvidence(input["workflow_root_cause_evidence"]); verdict != "" {
		return verdict
	}
	return "Root cause not identified: decision_framework output did not identify a root cause."
}

func workflowBool(input map[string]any, key string) bool {
	if input == nil {
		return false
	}
	value, _ := input[key].(bool)
	return value
}

func workflowRootCauseVerdictFromData(data map[string]any) string {
	for _, key := range []string{"root_cause", "rootCause", "cause", "conclusion", "recommendation"} {
		text, _ := data[key].(string)
		if verdict := workflowRootCauseVerdictFromText(text); verdict != "" {
			return verdict
		}
	}
	return ""
}

func workflowRootCauseVerdictFromEvidence(value any) string {
	evidence, ok := value.(map[string]any)
	if !ok || len(evidence) == 0 {
		return ""
	}
	status := strings.ToLower(strings.TrimSpace(fmt.Sprint(evidence["status"])))
	switch status {
	case "identified", "confirmed":
		cause := strings.TrimSpace(fmt.Sprint(evidence["cause"]))
		if cause == "" || cause == "<nil>" {
			return "Root cause not identified: structured workflow evidence omitted the cause."
		}
		return "Root cause: " + cause
	case "not_identified", "inconclusive", "unknown":
		reason := strings.TrimSpace(fmt.Sprint(evidence["reason"]))
		if reason == "" || reason == "<nil>" {
			reason = "structured workflow evidence did not identify a cause"
		}
		return "Root cause not identified: " + reason + "."
	default:
		return ""
	}
}

func workflowRootCauseVerdictFromText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	verdict := firstRootCauseVerdictLine(text)
	if verdict == "" {
		return ""
	}
	lower := strings.ToLower(verdict)
	for _, marker := range workflowRootCauseNegativeMarkers {
		if strings.Contains(lower, marker) {
			return "Root cause not identified: " + firstWorkflowRootCauseLine(verdict)
		}
	}
	if cause := extractWorkflowRootCauseAffirmation(verdict, lower); cause != "" {
		return "Root cause: " + cause
	}
	return ""
}

func extractWorkflowRootCauseAffirmation(text, lower string) string {
	for _, marker := range workflowRootCauseAffirmativeMarkers {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		start := idx
		switch marker {
		case "root cause:", "root cause is", "root cause was", "root cause =", "root cause -", "root cause —", "root cause identified", "identified root cause", "confirmed root cause", "the cause is", "the cause was", "caused by", "is caused by", "was caused by", "failure stems from", "failure stemmed from", "traced to":
			start = idx + len(marker)
		}
		cause := firstWorkflowRootCauseLine(text[start:])
		return trimWorkflowRootCauseText(cause)
	}
	return ""
}

func firstRootCauseVerdictLine(text string) string {
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = cleanRootCauseVerdictLine(line)
		if strings.HasPrefix(strings.ToLower(line), "root cause") {
			return line
		}
	}
	return ""
}

func cleanRootCauseVerdictLine(line string) string {
	line = strings.TrimSpace(line)
	for {
		before := line
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#>")
		line = strings.TrimSpace(line)
		line = trimWorkflowListPrefix(line)
		line = strings.TrimSpace(line)
		line = trimWorkflowMarkdownEdges(line)
		line = strings.TrimSpace(line)
		if line == before {
			break
		}
	}
	return strings.TrimSpace(line)
}

func trimWorkflowMarkdownEdges(line string) string {
	line = strings.TrimSpace(line)
	for {
		before := line
		line = trimPairedWorkflowMarkdown(line, "**")
		line = trimPairedWorkflowMarkdown(line, "__")
		line = trimPairedWorkflowMarkdown(line, "`")
		line = trimPairedWorkflowMarkdown(line, "*")
		line = trimPairedWorkflowMarkdown(line, "_")
		line = strings.TrimSpace(line)
		if line == before {
			break
		}
	}
	return line
}

func trimPairedWorkflowMarkdown(line, marker string) string {
	for strings.HasPrefix(line, marker) && strings.HasSuffix(line, marker) && len(line) >= len(marker)*2 {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, marker), marker))
	}
	return line
}

func trimWorkflowRootCauseText(text string) string {
	text = strings.TrimSpace(strings.TrimLeft(text, ":= -—"))
	for {
		before := text
		text = strings.TrimSpace(text)
		for _, marker := range []string{"**", "__", "`", "*", "_"} {
			text = strings.TrimSpace(strings.TrimPrefix(text, marker))
			text = strings.TrimSpace(strings.TrimSuffix(text, marker))
		}
		text = trimWorkflowMarkdownEdges(text)
		if text == before {
			break
		}
	}
	return strings.TrimSpace(text)
}

func trimWorkflowListPrefix(line string) string {
	if strings.HasPrefix(line, "- [") {
		if idx := strings.Index(line, "]"); idx >= 0 && idx+1 < len(line) {
			return strings.TrimSpace(line[idx+1:])
		}
	}
	for _, prefix := range []string{"- ", "* ", "+ ", "• "} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && i < len(line) && (line[i] == '.' || line[i] == ')') {
		return strings.TrimSpace(line[i+1:])
	}
	return line
}

func firstWorkflowRootCauseLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for _, sep := range []string{"\r\n", "\n", "\r"} {
		if idx := strings.Index(text, sep); idx >= 0 {
			text = text[:idx]
			break
		}
	}
	if idx := strings.Index(text, ". "); idx >= 0 {
		text = text[:idx+1]
	}
	return strings.TrimSpace(text)
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
	if value == nil {
		return ""
	}
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
	format := ""
	if profile != nil {
		format = profile.OutputFormat
	}
	return parser.ParseContent(raw, format)
}
