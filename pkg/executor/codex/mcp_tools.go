package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/loom"
)

// CodexHandlers provides the 5 MCP tool handler functions for the Codex executor
// surface (AIMUX-18 FR-1 through FR-5). Handlers are wired into the MCP server
// via Server.registerTools() in Phase 6.
//
// All handler methods satisfy the mcp-go handler signature:
//
//	func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)
//
// Handlers are safe for concurrent use; all mutable state lives in the pool and
// loom engine, which are themselves concurrency-safe.
type CodexHandlers struct {
	pool *CodexPool
	loom loomSubmitter
}

// loomSubmitter is the subset of LoomEngine used by the handlers.
// Extracted as an interface so tests can inject a fake without the full engine.
type loomSubmitter interface {
	Submit(ctx context.Context, req loom.TaskRequest) (string, error)
	Get(taskID string) (*loom.Task, error)
	Cancel(taskID string) error
}

// NewCodexHandlers constructs a CodexHandlers.
// Returns an error if pool or l is nil.
func NewCodexHandlers(pool *CodexPool, l loomSubmitter) (*CodexHandlers, error) {
	if pool == nil {
		return nil, fmt.Errorf("codex: NewCodexHandlers: pool must not be nil")
	}
	if l == nil {
		return nil, fmt.Errorf("codex: NewCodexHandlers: loom must not be nil")
	}
	return &CodexHandlers{pool: pool, loom: l}, nil
}

// --- FR-1: codex_task ---

// HandleCodexTask submits a free-form Codex task and returns a task_id immediately.
// The caller polls via HandleCodexStatus.
func (h *CodexHandlers) HandleCodexTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	prompt, err := req.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError("prompt is required"), nil
	}

	projectID := req.GetString("project_id", "")
	model := req.GetString("model", "")
	sandboxClass := req.GetString("sandbox_class", JobClassTask)
	resumeTaskID := req.GetString("resume_task_id", "")

	// Validate sandbox class.
	if _, err := ForClass(sandboxClass); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid sandbox_class %q: %v", sandboxClass, err)), nil
	}

	meta := map[string]any{
		"job_class": sandboxClass,
	}
	if resumeTaskID != "" {
		meta["resume_task_id"] = resumeTaskID
	}

	taskReq := loom.TaskRequest{
		WorkerType: WorkerTypeCodex,
		ProjectID:  projectID,
		Prompt:     prompt,
		Model:      model,
		Metadata:   meta,
	}
	taskID, err := h.loom.Submit(ctx, taskReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("codex_task: submit failed: %v", err)), nil
	}

	return marshalCodexResult(map[string]any{
		"task_id":    taskID,
		"project_id": projectID,
		"status":     string(loom.TaskStatusPending),
		"invoked_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// --- FR-2: codex_review ---

// HandleCodexReview submits a structured code-review task and returns a task_id
// immediately. Uses turn/start + outputSchema (NOT review/start — audit §A.8).
func (h *CodexHandlers) HandleCodexReview(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := req.RequireString("target")
	if err != nil {
		return mcp.NewToolResultError("target is required"), nil
	}

	projectID := req.GetString("project_id", "")
	model := req.GetString("model", "")

	// Prompt instructs the agent to produce JSON findings + ALLOW/BLOCK decision.
	prompt := buildReviewPrompt(target)

	meta := map[string]any{
		"job_class":     JobClassReview,
		"review_target": target,
		"output_schema": reviewOutputSchema,
	}

	taskReq := loom.TaskRequest{
		WorkerType: WorkerTypeCodex,
		ProjectID:  projectID,
		Prompt:     prompt,
		Model:      model,
		Metadata:   meta,
	}
	taskID, err := h.loom.Submit(ctx, taskReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("codex_review: submit failed: %v", err)), nil
	}

	return marshalCodexResult(map[string]any{
		"task_id":    taskID,
		"project_id": projectID,
		"status":     string(loom.TaskStatusPending),
		"invoked_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// --- FR-3: codex_status ---

// HandleCodexStatus returns the current state of a Codex Loom task.
// Supports include_content and tail budget params (FR-3).
func (h *CodexHandlers) HandleCodexStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID, err := req.RequireString("task_id")
	if err != nil {
		return mcp.NewToolResultError("task_id is required"), nil
	}

	includeContent := req.GetBool("include_content", false)
	tail := req.GetInt("tail", 0)

	task, err := h.loom.Get(taskID)
	if err != nil {
		if errors.Is(err, loom.ErrTaskNotFound) {
			return mcp.NewToolResultError(fmt.Sprintf("task %q not found", taskID)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("codex_status: get failed: %v", err)), nil
	}
	// Guard: only expose status of Codex tasks via this tool.
	if task.WorkerType != WorkerTypeCodex {
		return mcp.NewToolResultError(fmt.Sprintf("task %q is not a Codex task (worker_type=%q)", taskID, task.WorkerType)), nil
	}

	return marshalCodexResult(buildCodexStatusResult(task, includeContent, tail))
}

// --- FR-4: codex_cancel ---

// HandleCodexCancel cancels a Codex task. Idempotent: if the task is already
// terminal, the current state is returned without error.
func (h *CodexHandlers) HandleCodexCancel(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	taskID, err := req.RequireString("task_id")
	if err != nil {
		return mcp.NewToolResultError("task_id is required"), nil
	}

	task, err := h.loom.Get(taskID)
	if err != nil {
		if errors.Is(err, loom.ErrTaskNotFound) {
			return mcp.NewToolResultError(fmt.Sprintf("task %q not found", taskID)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("codex_cancel: get failed: %v", err)), nil
	}
	// Guard: only allow cancellation of Codex tasks. This prevents codex_cancel
	// from terminating unrelated Loom tasks submitted by other workers.
	if task.WorkerType != WorkerTypeCodex {
		return mcp.NewToolResultError(fmt.Sprintf("task %q is not a Codex task (worker_type=%q)", taskID, task.WorkerType)), nil
	}
	previousStatus := task.Status

	if !task.Status.IsTerminal() {
		if cancelErr := h.loom.Cancel(taskID); cancelErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("cancel failed: %v", cancelErr)), nil
		}
		// Re-fetch to reflect the new status.
		if updated, getErr := h.loom.Get(taskID); getErr == nil {
			task = updated
		}
	}

	return marshalCodexResult(map[string]any{
		"task_id":         taskID,
		"previous_status": string(previousStatus),
		"current_status":  string(task.Status),
		"cancelled_at":    time.Now().UTC().Format(time.RFC3339),
	})
}

// --- FR-5: codex_review_gate ---

// HandleCodexReviewGate submits a code-review task and blocks synchronously until
// the task completes or the timeout fires (ADR-012). Fail-open on timeout.
//
// Response: {decision: "allow"|"block", reason: string, task_id: string}.
// Timeout → {decision: "allow", reason: "timeout — gate did not complete in time"}.
func (h *CodexHandlers) HandleCodexReviewGate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := req.RequireString("target")
	if err != nil {
		return mcp.NewToolResultError("target is required"), nil
	}

	projectID := req.GetString("project_id", "")
	timeoutSec := req.GetInt("timeout_seconds", 300)
	if timeoutSec <= 0 {
		timeoutSec = 300
	}

	prompt := buildReviewPrompt(target)
	meta := map[string]any{
		"job_class":     JobClassReview,
		"review_target": target,
		"review_gate":   true,
		"output_schema": reviewOutputSchema,
	}
	taskReq := loom.TaskRequest{
		WorkerType: WorkerTypeCodex,
		ProjectID:  projectID,
		Prompt:     prompt,
		Metadata:   meta,
	}
	taskID, err := h.loom.Submit(ctx, taskReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("codex_review_gate: submit failed: %v", err)), nil
	}

	// Block until terminal or timeout (ADR-012: fail-open on timeout).
	gateCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	decision, reason := pollGateResult(gateCtx, h.loom, taskID)
	return marshalCodexResult(map[string]any{
		"decision": decision,
		"reason":   reason,
		"task_id":  taskID,
	})
}

// --- internal helpers ---

// pollGateResult polls Loom until the task reaches a terminal state or ctx expires.
// Returns ("allow"/"block", reason). Fail-open on timeout or unrecoverable error.
//
// An initial check is performed immediately before starting the ticker so that
// tasks that complete very quickly (or are already terminal on entry) do not
// incur a full 500ms delay.
func pollGateResult(ctx context.Context, l loomSubmitter, taskID string) (string, string) {
	const pollInterval = 500 * time.Millisecond

	// Check once immediately to avoid unnecessary delay for fast/already-terminal tasks.
	if task, err := l.Get(taskID); err == nil && task.Status.IsTerminal() {
		if task.Status == loom.TaskStatusFailed || task.Status == loom.TaskStatusFailedCrash {
			return "allow", fmt.Sprintf("gate error: task %s", string(task.Status))
		}
		return parseGateDecision(task.Result)
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "allow", "timeout — gate did not complete in time"
		case <-ticker.C:
			task, err := l.Get(taskID)
			if err != nil {
				// Fail-open: cannot fetch task state.
				return "allow", fmt.Sprintf("gate error: task fetch failed: %v", err)
			}
			if !task.Status.IsTerminal() {
				continue
			}
			if task.Status == loom.TaskStatusFailed || task.Status == loom.TaskStatusFailedCrash {
				return "allow", fmt.Sprintf("gate error: task %s", string(task.Status))
			}
			return parseGateDecision(task.Result)
		}
	}
}

// reviewOutputSchema is the JSON Schema passed to TurnStartParams.OutputSchema for review jobs.
// It constrains Codex output to the structured findings/decision shape so that parseGateDecision
// receives well-formed JSON even when the model is verbose.
var reviewOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"findings": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"severity": map[string]any{"type": "string", "enum": []string{"error", "warning", "info"}},
					"file":     map[string]any{"type": "string"},
					"line":     map[string]any{"type": []any{"integer", "null"}},
					"body":     map[string]any{"type": "string"},
				},
				"required": []string{"severity", "body"},
			},
		},
		"summary":  map[string]any{"type": "string"},
		"decision": map[string]any{"type": "string", "enum": []string{"ALLOW", "BLOCK"}},
		"reason":   map[string]any{"type": "string"},
	},
	"required": []string{"findings", "summary", "decision", "reason"},
}

// gateDecisionResponse is the JSON schema produced by buildReviewPrompt for gate decisions.
type gateDecisionResponse struct {
	Findings []any  `json:"findings"`
	Summary  string `json:"summary"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// parseGateDecision extracts ALLOW/BLOCK decision from agent JSON output.
// The agent is instructed to embed decision and reason inside the JSON object.
// Unrecognised or invalid output → fail-open ("allow", reason with parse error).
func parseGateDecision(content string) (string, string) {
	// Find the JSON object in the content (agent may output preamble before JSON).
	start := strings.Index(content, "{")
	if start < 0 {
		return "allow", "gate output did not contain a JSON object"
	}
	jsonStr := content[start:]
	var resp gateDecisionResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		// Fail-open: malformed JSON should not block the gate.
		return "allow", fmt.Sprintf("gate output parse error: %v", err)
	}
	upper := strings.ToUpper(strings.TrimSpace(resp.Decision))
	switch upper {
	case "ALLOW":
		return "allow", resp.Reason
	case "BLOCK":
		return "block", resp.Reason
	default:
		return "allow", fmt.Sprintf("gate decision field %q not recognised (expected ALLOW or BLOCK)", resp.Decision)
	}
}

// buildCodexStatusResult constructs the status response map from a Loom task.
func buildCodexStatusResult(task *loom.Task, includeContent bool, tail int) map[string]any {
	result := map[string]any{
		"task_id":        task.ID,
		"status":         string(task.Status),
		"progress_tail":  task.LastOutputLine,
		"progress_lines": task.ProgressLines,
		"invoked_at":     task.CreatedAt.UTC().Format(time.RFC3339),
	}
	if task.CompletedAt != nil {
		result["completed_at"] = task.CompletedAt.UTC().Format(time.RFC3339)
	}
	if task.DispatchedAt != nil {
		result["dispatched_at"] = task.DispatchedAt.UTC().Format(time.RFC3339)
	}
	if task.Status.IsTerminal() {
		runes := []rune(task.Result)
		contentLen := len(runes)
		if tail > 0 {
			out := string(runes)
			if len(runes) > tail {
				out = string(runes[len(runes)-tail:])
			}
			result["content_tail"] = out
			result["content_length"] = contentLen
		} else if includeContent {
			result["content"] = task.Result
			result["content_length"] = contentLen
		} else {
			result["content_length"] = contentLen
		}
		if task.Error != "" {
			result["error"] = task.Error
		}
	}
	// FR-12: expose compaction metadata when include_content=true.
	if includeContent && task.Metadata != nil {
		var meta CodexTaskMeta
		if b, err := json.Marshal(task.Metadata); err == nil {
			if err := json.Unmarshal(b, &meta); err == nil {
				if meta.LastInputTokens > 0 {
					result["last_input_tokens"] = meta.LastInputTokens
				}
				if meta.CompactionCount > 0 {
					result["compaction_count"] = meta.CompactionCount
				}
			}
		}
	}
	return result
}

// buildReviewPrompt wraps the review target in a structured instruction so the
// agent produces a single JSON object with findings and a decision field.
// Uses turn/start with outputSchema — NOT review/start (audit §A.8).
//
// The decision is embedded in the JSON to avoid contradicting "raw JSON only"
// with a separate trailing decision line (which parseGateDecision cannot parse
// from a valid JSON-only response).
func buildReviewPrompt(target string) string {
	return fmt.Sprintf(`You are a code reviewer. Review the following target and produce structured findings.

Target:
%s

Output a single JSON object with the following shape (no markdown, raw JSON only, no trailing text):
{"findings":[{"severity":"error"|"warning"|"info","file":"<path>","line":<number>|null,"body":"<description>"}],"summary":"<overall summary>","decision":"ALLOW"|"BLOCK","reason":"<one-line reason>"}

decision must be "ALLOW" when no blocking issues are found, or "BLOCK" when blocking issues exist.
reason must be a single line explaining the decision.`, target)
}

// marshalCodexResult serialises m to JSON and returns an MCP text tool result.
func marshalCodexResult(m map[string]any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("internal error: response serialization failed: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
