package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/code"
	"github.com/thebtf/aimux/pkg/executor/fallback"
	"github.com/thebtf/aimux/pkg/executor/picker"
	"github.com/thebtf/aimux/pkg/executor/review"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/parser"
	"github.com/thebtf/aimux/pkg/tenant"
)

type leafOutputAdapter func(task *loom.Task, parsed string) (string, map[string]any, error)

type profileTaskWorker struct {
	server     *Server
	workerType loom.WorkerType
	taskClass  string
	defaultCLI string
	adapt      leafOutputAdapter
}

type tenantAwareSubtaskLoom struct {
	engine   *loom.LoomEngine
	quotaFor func(tenantID string) *loom.TenantQuotaConfig
}

func (l tenantAwareSubtaskLoom) Submit(ctx context.Context, req loom.TaskRequest) (string, error) {
	if l.engine == nil {
		return "", extypes.NewCapabilityMismatch("tenant-aware subtask loom requires engine", nil)
	}
	if req.TenantID != "" {
		return loom.NewTenantScopedEngine(l.engine, req.TenantID, l.quota(req.TenantID)).Submit(ctx, req)
	}
	return l.engine.Submit(ctx, req)
}

func (l tenantAwareSubtaskLoom) Get(taskID string) (*loom.Task, error) {
	if l.engine == nil {
		return nil, extypes.NewCapabilityMismatch("tenant-aware subtask loom requires engine", nil)
	}
	return l.engine.Get(taskID)
}

func (l tenantAwareSubtaskLoom) GetContext(ctx context.Context, taskID string) (*loom.Task, error) {
	if l.engine == nil {
		return nil, extypes.NewCapabilityMismatch("tenant-aware subtask loom requires engine", nil)
	}
	if tc, ok := tenant.FromContext(ctx); ok && strings.TrimSpace(tc.TenantID) != "" {
		return loom.NewTenantScopedEngine(l.engine, tc.TenantID, l.quota(tc.TenantID)).GetContext(ctx, taskID)
	}
	return l.engine.GetContext(ctx, taskID)
}

func (l tenantAwareSubtaskLoom) Cancel(taskID string) error {
	if l.engine == nil {
		return extypes.NewCapabilityMismatch("tenant-aware subtask loom requires engine", nil)
	}
	return l.engine.Cancel(taskID)
}

func (l tenantAwareSubtaskLoom) quota(tenantID string) *loom.TenantQuotaConfig {
	if l.quotaFor == nil {
		return nil
	}
	return l.quotaFor(tenantID)
}

func (w profileTaskWorker) Type() loom.WorkerType {
	return w.workerType
}

func (w profileTaskWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	if task == nil {
		return nil, extypes.NewUserInputError("profile task worker task is nil", nil)
	}
	if w.server == nil || w.server.registry == nil {
		return nil, extypes.NewCapabilityMismatch("profile task worker requires server registry", nil)
	}
	cli := strings.TrimSpace(task.CLI)
	if cli == "" {
		cli = w.defaultCLI
	}
	if cli == "" {
		return nil, extypes.NewCapabilityMismatch(fmt.Sprintf("%s worker requires a CLI", w.workerType), nil)
	}
	profile, err := w.server.registry.Get(cli)
	if err != nil || profile == nil {
		if err == nil {
			err = fmt.Errorf("profile is nil")
		}
		return nil, extypes.NewBinaryNotFound(fmt.Sprintf("CLI %q profile unavailable: %v", cli, err), err)
	}

	spec := picker.TaskSpec{
		TaskClass:      w.taskClass,
		Prompt:         task.Prompt,
		CWD:            task.CWD,
		Env:            cloneEnv(task.Env),
		Model:          task.Model,
		Effort:         task.Effort,
		Sandbox:        sandboxFromTaskMetadata(task.Metadata),
		SessionID:      sessionIDFromTaskMetadata(task.Metadata),
		SessionResume:  sessionResumeFromTaskMetadata(task.Metadata),
		TimeoutSeconds: task.Timeout,
		OnOutput:       w.progressSink(task.ID, profile.OutputFormat),
	}
	raw, selectedCLI, failedAttempts, err := w.dispatch(ctx, cli, task.Metadata, spec)
	if err != nil {
		return nil, err
	}
	selectedProfile := profile
	if selectedCLI != cli {
		selectedProfile, err = w.server.registry.Get(selectedCLI)
		if err != nil || selectedProfile == nil {
			if err == nil {
				err = fmt.Errorf("profile is nil")
			}
			return nil, extypes.NewBinaryNotFound(fmt.Sprintf("CLI %q profile unavailable after fallback: %v", selectedCLI, err), err)
		}
	}
	parsed, sessionID := parser.ParseContent(raw, selectedProfile.OutputFormat)
	content := parsed
	metadata := map[string]any{}
	if w.adapt != nil {
		content, metadata, err = w.adapt(task, parsed)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(content) == "" {
		return nil, extypes.NewUnknown(fmt.Sprintf("%s worker produced empty output", w.workerType), nil)
	}

	metadata["worker_type"] = string(w.workerType)
	metadata["cli"] = selectedCLI
	metadata["output_format"] = selectedProfile.OutputFormat
	if len(failedAttempts) > 0 {
		metadata["failed_attempts"] = failedAttempts
	}
	if sessionID != "" {
		metadata["cli_session_id"] = sessionID
	}
	return &loom.WorkerResult{
		Content:  content,
		Metadata: metadata,
	}, nil
}

// progressSink returns an OnOutput callback that forwards each leaf-CLI stdout
// line into runtime-event artifacts and loom.AppendProgress for the running
// task, so the stall detector's ProgressUpdatedAt reflects genuine last-output
// time instead of staying nil (which would make legitimate long-running work
// look like a stall — the exact false positive #359 surfaced). Returns nil when
// no engine is available so the dispatch path skips the SpawnArgs.OnOutput
// wiring entirely.
//
// The callback is best-effort: empty lines are skipped (no signal), and runtime
// or progress append errors are swallowed — progress recording must never break
// the streaming loop or fail the task. AppendProgress is safe for concurrent use
// and no-ops for unknown/cancelled task IDs.
func (w profileTaskWorker) progressSink(taskID, outputFormat string) func(string) {
	if w.server == nil || w.server.loom == nil || taskID == "" {
		return nil
	}
	engine := w.server.loom
	return func(line string) {
		if strings.TrimSpace(line) == "" {
			return
		}
		appendRuntimeEventsForLine(engine, taskID, outputFormat, line)
		progressLine := normalizeProgressLine(outputFormat, line)
		if progressLine == "" {
			progressLine = line
		}
		_ = engine.AppendProgress(taskID, progressLine)
	}
}

func appendRuntimeEventsForLine(engine *loom.LoomEngine, taskID, outputFormat, line string) {
	if engine == nil || strings.TrimSpace(taskID) == "" || strings.TrimSpace(line) == "" {
		return
	}
	for _, event := range runtimeEventsFromOutputLine(outputFormat, line) {
		_, _ = engine.AppendRuntimeEvent(taskID, event)
	}
}

func runtimeEventsFromOutputLine(outputFormat, line string) []loom.TaskRuntimeEventAppend {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if outputFormat != "jsonl" || !strings.HasPrefix(line, "{") {
		return []loom.TaskRuntimeEventAppend{runtimeRawEvent(line, nil)}
	}
	var frame map[string]any
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		return []loom.TaskRuntimeEventAppend{runtimeRawEvent(line, nil)}
	}
	frameType, _ := frame["type"].(string)
	switch frameType {
	case "agent_message":
		if text := runtimeFrameText(frame["content"]); text != "" {
			return []loom.TaskRuntimeEventAppend{runtimeTextEvent(text)}
		}
	case "item.completed":
		if item, ok := frame["item"].(map[string]any); ok {
			itemType, _ := item["type"].(string)
			switch itemType {
			case "agent_message", "message":
				if text := runtimeFrameText(item["text"]); text != "" {
					return []loom.TaskRuntimeEventAppend{runtimeTextEvent(text)}
				}
			case "tool_call", "function_call":
				return []loom.TaskRuntimeEventAppend{runtimeToolEvent("tool_call", item)}
			case "tool_result", "tool_output", "function_result":
				return []loom.TaskRuntimeEventAppend{runtimeToolEvent("tool_result", item)}
			}
		}
	case "thread.started", "turn.started", "turn.completed", "turn.failed":
		return []loom.TaskRuntimeEventAppend{runtimeStatusEvent(frameType, frame)}
	}
	return []loom.TaskRuntimeEventAppend{runtimeRawEvent(line, frame)}
}

func runtimeFrameText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		if text, _ := typed["text"].(string); text != "" {
			return text
		}
	}
	return ""
}

func runtimeTextEvent(text string) loom.TaskRuntimeEventAppend {
	return loom.TaskRuntimeEventAppend{
		EventType:     "text_delta",
		Channel:       "stdout",
		Summary:       text,
		Payload:       map[string]any{"text": text},
		ContentLength: int64(len(text)),
	}
}

func runtimeStatusEvent(frameType string, frame map[string]any) loom.TaskRuntimeEventAppend {
	return loom.TaskRuntimeEventAppend{
		EventType: "status",
		Channel:   "stdout",
		Summary:   frameType,
		Payload: map[string]any{
			"frame_type": frameType,
			"frame":      frame,
		},
	}
}

func runtimeToolEvent(eventType string, item map[string]any) loom.TaskRuntimeEventAppend {
	summary := eventType
	if name, _ := item["name"].(string); name != "" {
		summary = name
	} else if id, _ := item["id"].(string); id != "" {
		summary = id
	}
	return loom.TaskRuntimeEventAppend{
		EventType: eventType,
		Channel:   "tool",
		Summary:   summary,
		Payload: map[string]any{
			"item": item,
		},
	}
}

func runtimeRawEvent(line string, frame map[string]any) loom.TaskRuntimeEventAppend {
	payload := map[string]any{"line": line}
	if frame != nil {
		payload["frame"] = frame
	}
	return loom.TaskRuntimeEventAppend{
		EventType:     "raw",
		Channel:       "stdout",
		Summary:       line,
		Payload:       payload,
		ContentLength: int64(len(line)),
	}
}

func (w profileTaskWorker) dispatch(ctx context.Context, primaryCLI string, metadata map[string]any, spec picker.TaskSpec) (string, string, []fallback.FailedAttempt, error) {
	if w.server != nil && w.server.fallbackPicker != nil {
		result, err := w.server.fallbackPicker.RunPrimary(ctx, primaryCLI, spec, fallbackOptionsFromTaskMetadata(metadata), w.server.taskDispatch)
		if err != nil {
			return "", primaryCLI, nil, err
		}
		return result.Content, result.SelectedCLI, result.FailedAttempts, nil
	}
	raw, err := w.server.taskDispatch(ctx, primaryCLI, spec)
	return raw, primaryCLI, nil, err
}

func fallbackOptionsFromTaskMetadata(metadata map[string]any) fallback.RunOptions {
	var opts fallback.RunOptions
	if enabled, ok := metadata["fallback_enabled"].(bool); ok {
		opts.FallbackEnabled = &enabled
	}
	if maxAttempts, ok := metadataInt(metadata, "max_attempts"); ok {
		opts.MaxAttempts = maxAttempts
	}
	return opts
}

func sandboxFromTaskMetadata(metadata map[string]any) string {
	if sandbox, ok := metadataString(metadata, "sandbox"); ok {
		return strings.TrimSpace(sandbox)
	}
	return ""
}

func sessionIDFromTaskMetadata(metadata map[string]any) string {
	for _, key := range []string{code.MetadataThreadID, "cli_session_id"} {
		if value, ok := metadataString(metadata, key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sessionResumeFromTaskMetadata(metadata map[string]any) bool {
	return sessionIDFromTaskMetadata(metadata) != ""
}

func (s *Server) registerTaskWorkers() {
	if s == nil || s.loom == nil {
		return
	}
	s.loom.RegisterWorker(deepResearchWorkerType, deepResearchWorker{progress: s.loom})

	s.loom.RegisterWorker(workflowRecipeWorkerType, workflowRecipeWorker{server: s, defaultCLI: "codex"})
	subtaskLoom := tenantAwareSubtaskLoom{engine: s.loom}
	var pairSelector code.PairSelector
	if s.fallbackPicker != nil {
		pairSelector = s.fallbackPicker
	}

	codeWorker, codeErr := code.NewCodeWorker(code.CodeWorkerConfig{
		Loom:         subtaskLoom,
		PairSelector: pairSelector,
	})
	if codeErr != nil {
		s.log.Warn("task workers: code worker init failed: %v", codeErr)
	} else {
		s.loom.RegisterWorker(code.WorkerTypeCode, codeWorker)
		s.loom.RegisterWorker(code.WorkerTypeCodeDriver, profileTaskWorker{
			server:     s,
			workerType: code.WorkerTypeCodeDriver,
			taskClass:  "code",
			defaultCLI: "codex",
		})
		s.loom.RegisterWorker(code.WorkerTypeCodeNavigator, profileTaskWorker{
			server:     s,
			workerType: code.WorkerTypeCodeNavigator,
			taskClass:  "code",
			defaultCLI: "claude",
			adapt:      adaptNavigatorOutput,
		})
	}

	reviewWorker, reviewErr := review.NewReviewWorker(review.ReviewWorkerConfig{
		Loom:                  subtaskLoom,
		DefaultTimeoutSeconds: serverDefaultTimeoutSeconds(s),
	})
	if reviewErr != nil {
		s.log.Warn("task workers: review worker init failed: %v", reviewErr)
		return
	}
	s.loom.RegisterWorker(review.WorkerTypeReview, reviewWorker)
	for _, workerType := range []loom.WorkerType{
		review.WorkerTypeReviewStructural,
		review.WorkerTypeReviewBehavioural,
		review.WorkerTypeReviewAdversarial,
	} {
		s.loom.RegisterWorker(workerType, profileTaskWorker{
			server:     s,
			workerType: workerType,
			taskClass:  "review",
			defaultCLI: "codex",
			adapt:      adaptReviewPassOutput,
		})
	}
}

func serverDefaultTimeoutSeconds(s *Server) int {
	if s == nil || s.cfg == nil {
		return 0
	}
	return s.cfg.Server.DefaultTimeoutSeconds
}

func adaptReviewPassOutput(task *loom.Task, parsed string) (string, map[string]any, error) {
	trimmed := strings.TrimSpace(parsed)
	var passJSON struct {
		Findings []review.Finding `json:"findings"`
		Summary  string           `json:"summary"`
	}
	if err := json.Unmarshal([]byte(trimmed), &passJSON); err == nil && strings.TrimSpace(passJSON.Summary) != "" {
		return trimmed, map[string]any{}, nil
	}

	pass := reviewPassFromTask(task)
	return "", nil, extypes.NewUserInputError(
		fmt.Sprintf("%s review pass output must be structured JSON with a non-empty summary", pass),
		nil,
	)
}

func adaptNavigatorOutput(_ *loom.Task, parsed string) (string, map[string]any, error) {
	trimmed := strings.TrimSpace(parsed)
	if extracted, ok := tryParseNavigatorVerdict(trimmed); ok {
		return extracted, map[string]any{}, nil
	}
	if extracted := extractJSONFromMarkdown(trimmed); extracted != "" {
		if result, ok := tryParseNavigatorVerdict(extracted); ok {
			return result, map[string]any{"navigator_output_normalized": true}, nil
		}
	}

	content, err := json.Marshal(map[string]any{
		"verdict":    "ESCALATE",
		"confidence": 0,
		"feedback":   "navigator output was not structured JSON",
		"evidence":   summarizeLeafOutput(trimmed),
	})
	if err != nil {
		return "", nil, extypes.NewUnknown("navigator verdict serialization failed", err)
	}
	return string(content), map[string]any{"navigator_output_normalized": true}, nil
}

func tryParseNavigatorVerdict(text string) (string, bool) {
	var verdict struct {
		Verdict    string  `json:"verdict"`
		Action     string  `json:"action"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(text), &verdict); err != nil {
		return "", false
	}
	action := strings.TrimSpace(verdict.Verdict)
	if action == "" {
		action = strings.TrimSpace(verdict.Action)
	}
	if action == "" {
		return "", false
	}
	return text, true
}

func extractJSONFromMarkdown(text string) string {
	for _, fence := range []string{"```json\n", "```\n"} {
		start := strings.Index(text, fence)
		if start < 0 {
			continue
		}
		body := text[start+len(fence):]
		end := strings.Index(body, "\n```")
		if end < 0 {
			continue
		}
		candidate := strings.TrimSpace(body[:end])
		if len(candidate) > 0 && candidate[0] == '{' {
			return candidate
		}
	}
	return ""
}

func reviewPassFromTask(task *loom.Task) review.PassName {
	if task != nil {
		if pass, ok := metadataText(task.Metadata, "review_pass"); ok && strings.TrimSpace(pass) != "" {
			return review.PassName(strings.TrimSpace(pass))
		}
		switch task.WorkerType {
		case review.WorkerTypeReviewStructural:
			return review.PassStructural
		case review.WorkerTypeReviewBehavioural:
			return review.PassBehavioural
		case review.WorkerTypeReviewAdversarial:
			return review.PassAdversarial
		}
	}
	return "review"
}

func metadataText(metadata map[string]any, key string) (string, bool) {
	if metadata == nil {
		return "", false
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return "", false
	}
	return fmt.Sprint(value), true
}

func summarizeLeafOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "empty output"
	}
	const max = 240
	if len(output) <= max {
		return output
	}
	return output[:max] + "..."
}
