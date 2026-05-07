package server

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/mark3labs/mcp-go/mcp"

	codexexec "github.com/thebtf/aimux/pkg/executor/codex"
)

// lookupCodexBinary returns the full path to the codex binary if available.
// It checks the PATH for "codex" (Unix) or "codex.exe" (Windows).
func lookupCodexBinary() (string, error) {
	path, err := exec.LookPath("codex")
	if err != nil {
		return "", fmt.Errorf("codex not found on PATH: %w", err)
	}
	return path, nil
}

// registerCodexTools adds the 5 Codex MCP tool handlers (AIMUX-18 FR-1..FR-5).
//
// When s.codexHandlers is nil (codex binary absent), stub handlers that return
// an actionable error are registered so tool discovery still enumerates them.
func (s *Server) registerCodexTools() {
	// codex_task (FR-1)
	s.mcp.AddTool(
		mcp.NewTool("codex_task",
			mcp.WithDescription("[delegate — CLI, async] Submit a free-form coding task to Codex. "+
				"Returns task_id immediately; poll with codex_status. "+
				"Uses a Loom-backed task (SQLite-persisted, crash-safe). "+
				"sandbox_class defaults to \"task\" (read-only, never asks for approval). "+
				"Use sandbox_class=\"write-task\" for changes that modify files. "+
				"resume_task_id: resume from a prior codex_task's thread (O(1) — no scan)."),
			mcp.WithString("prompt",
				mcp.Required(),
				mcp.Description("Task description passed to Codex as the first turn message."),
			),
			mcp.WithString("project_id",
				mcp.Description("Scopes the Codex process pool entry. Defaults to active session's ProjectContext.ID."),
			),
			mcp.WithString("sandbox_class",
				mcp.Description("Sandbox policy: \"review\"|\"task\" (read-only), \"write-task\" (workspace-write), \"danger\" (danger-full-access). Default: task."),
				mcp.Enum(codexexec.JobClassReview, codexexec.JobClassTask, codexexec.JobClassWriteTask, codexexec.JobClassDanger),
				mcp.DefaultString(codexexec.JobClassTask),
			),
			mcp.WithString("model",
				mcp.Description("Model to pass to Codex. Defaults to user's configured default (~/.codex/config.toml). Do not pass empty string."),
			),
			mcp.WithString("resume_task_id",
				mcp.Description("Loom task_id of a prior codex_task. Worker resumes the thread from that task's metadata (ADR-008)."),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(false),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(false),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			}),
		),
		s.codexTaskHandler(),
	)

	// codex_review (FR-2)
	s.mcp.AddTool(
		mcp.NewTool("codex_review",
			mcp.WithDescription("[delegate — CLI, async] Submit a structured code review to Codex. "+
				"Returns task_id immediately; poll with codex_status. "+
				"Uses turn/start + outputSchema (NOT review/start per audit §A.8). "+
				"Sandbox is read-only/never (review class, ADR-006). "+
				"Result contains JSON findings: [{severity, file, line, body}] + summary."),
			mcp.WithString("target",
				mcp.Required(),
				mcp.Description("Git ref, diff text, or file path to review."),
			),
			mcp.WithString("project_id",
				mcp.Description("Scopes the Codex process pool entry."),
			),
			mcp.WithString("model",
				mcp.Description("Model override."),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(false),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(false),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			}),
		),
		s.codexReviewHandler(),
	)

	// codex_status (FR-3)
	s.mcp.AddTool(
		mcp.NewTool("codex_status",
			mcp.WithDescription("[manage — async task status] Query the status of a Codex Loom task. "+
				"Default response is brief (~4k). "+
				"include_content=true returns full Codex output. "+
				"tail=N returns the last N characters of output. "+
				"Status values: pending, dispatched, running, completed, failed, failed_crash."),
			mcp.WithString("task_id",
				mcp.Required(),
				mcp.Description("Task ID returned by codex_task or codex_review."),
			),
			mcp.WithBoolean("include_content",
				mcp.Description("Return full task output (default false)."),
			),
			mcp.WithNumber("tail",
				mcp.Description("Return last N characters of output without fetching full content."),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(true),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(true),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			}),
		),
		s.codexStatusHandler(),
	)

	// codex_cancel (FR-4)
	s.mcp.AddTool(
		mcp.NewTool("codex_cancel",
			mcp.WithDescription("[manage — idempotent] Cancel a running or queued Codex task. "+
				"Idempotent: returns success if the task is already in a terminal state. "+
				"Response: {task_id, previous_status, current_status, cancelled_at}."),
			mcp.WithString("task_id",
				mcp.Required(),
				mcp.Description("Task ID to cancel."),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(false),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(true),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			}),
		),
		s.codexCancelHandler(),
	)

	// codex_review_gate (FR-5 — mandatory)
	s.mcp.AddTool(
		mcp.NewTool("codex_review_gate",
			mcp.WithDescription("[delegate — synchronous blocking] Submit a code review and wait for ALLOW/BLOCK decision. "+
				"Intended for use from CC Stop hooks (ADR-012). Fails open on timeout. "+
				"Default timeout: 300s. "+
				"Response: {decision: \"allow\"|\"block\", reason: string, task_id: string}. "+
				"Timeout → {decision: \"allow\", reason: \"timeout — gate did not complete in time\"}."),
			mcp.WithString("target",
				mcp.Required(),
				mcp.Description("Git ref, diff text, or file path to review."),
			),
			mcp.WithString("project_id",
				mcp.Description("Scopes the Codex process pool entry."),
			),
			mcp.WithNumber("timeout_seconds",
				mcp.Description("Maximum seconds to wait for a decision (default 300)."),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(false),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(false),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			}),
		),
		s.codexReviewGateHandler(),
	)
}

// --- handler wrappers ---
// Each wrapper returns a bound handler function that delegates to s.codexHandlers
// when available, or returns an actionable error when codex is not installed.

func (s *Server) codexTaskHandler() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.codexHandlers == nil {
			return mcp.NewToolResultError("codex is not available: binary not found on PATH"), nil
		}
		return s.codexHandlers.HandleCodexTask(ctx, req)
	}
}

func (s *Server) codexReviewHandler() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.codexHandlers == nil {
			return mcp.NewToolResultError("codex is not available: binary not found on PATH"), nil
		}
		return s.codexHandlers.HandleCodexReview(ctx, req)
	}
}

func (s *Server) codexStatusHandler() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.codexHandlers == nil {
			return mcp.NewToolResultError("codex is not available: binary not found on PATH"), nil
		}
		return s.codexHandlers.HandleCodexStatus(ctx, req)
	}
}

func (s *Server) codexCancelHandler() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.codexHandlers == nil {
			return mcp.NewToolResultError("codex is not available: binary not found on PATH"), nil
		}
		return s.codexHandlers.HandleCodexCancel(ctx, req)
	}
}

func (s *Server) codexReviewGateHandler() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.codexHandlers == nil {
			return mcp.NewToolResultError("codex is not available: binary not found on PATH"), nil
		}
		return s.codexHandlers.HandleCodexReviewGate(ctx, req)
	}
}
