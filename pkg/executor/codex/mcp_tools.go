package codex

import (
	"context"
	"encoding/json"
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
		return mcp.NewToolResultError(fmt.Sprintf("task %q not found", taskID)), nil
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
		return mcp.NewToolResultError(fmt.Sprintf("task %q not found", taskID)), nil
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
func pollGateResult(ctx context.Context, l loomSubmitter, taskID string) (string, string) {
	const pollInterval = 500 * time.Millisecond

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

// parseGateDecision extracts ALLOW/BLOCK decision from agent output.
// Scans for the first line prefixed with "ALLOW:" or "BLOCK:" (case-insensitive).
// Unrecognised output → fail-open ("allow", "gate output did not contain ALLOW/BLOCK decision").
func parseGateDecision(content string) (string, string) {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "ALLOW:") {
			return "allow", strings.TrimSpace(trimmed[6:])
		}
		if strings.HasPrefix(upper, "BLOCK:") {
			return "block", strings.TrimSpace(trimmed[6:])
		}
	}
	return "allow", "gate output did not contain ALLOW/BLOCK decision"
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
		contentLen := len(task.Result)
		if tail > 0 {
			out := task.Result
			if len(out) > tail {
				out = out[len(out)-tail:]
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
	return result
}

// buildReviewPrompt wraps the review target in a structured instruction so the
// agent produces JSON findings in the expected schema (FR-2, audit §A.8).
// Uses turn/start with outputSchema — NOT review/start.
func buildReviewPrompt(target string) string {
	return fmt.Sprintf(`You are a code reviewer. Review the following target and produce structured findings.

Target:
%s

Output a JSON object with the following shape (no markdown, raw JSON only):
{"findings":[{"severity":"error"|"warning"|"info","file":"<path>","line":<number>|null,"body":"<description>"}],"summary":"<overall summary>"}

Decision line (last line, required):
ALLOW: <one-line reason>   — if no blocking issues found
BLOCK: <one-line reason>   — if blocking issues exist`, target)
}

// marshalCodexResult serialises m to JSON and returns an MCP text tool result.
func marshalCodexResult(m map[string]any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("internal error: response serialization failed: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
