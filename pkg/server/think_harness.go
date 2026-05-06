package server

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) registerThinkHarnessTool() {
	s.mcp.AddTool(
		mcp.NewTool("think",
			mcp.WithDescription("[think harness — caller-centered] Guide the calling model through start, step, and finalize actions. The caller owns the final answer; aimux supplies process state, visible work products, evidence, confidence, and gate feedback without hidden reasoning persistence."),
			mcp.WithString("action",
				mcp.Description("Harness action: start, step, or finalize. Omit only when calling legacy think(thought=...), which returns a migration error."),
				mcp.Enum("start", "step", "finalize"),
			),
			mcp.WithString("session_id",
				mcp.Description("Thinking session ID returned by think(action=start); required for step and finalize."),
			),
			mcp.WithString("task",
				mcp.Description("Visible task statement for think(action=start)."),
			),
			mcp.WithString("context_summary",
				mcp.Description("Visible context summary for think(action=start); do not include hidden reasoning."),
			),
			mcp.WithString("chosen_move",
				mcp.Description("Cognitive move selected by the caller for think(action=step)."),
			),
			mcp.WithString("work_product",
				mcp.Description("Visible work product produced by the caller or selected cognitive move."),
			),
			mcp.WithArray("evidence",
				mcp.Description("Visible evidence references supporting the work product."),
				mcp.Items(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind": map[string]any{
							"type":        "string",
							"description": "Evidence kind such as file, command, source, or observation.",
						},
						"ref": map[string]any{
							"type":        "string",
							"description": "Evidence locator such as file path, command, URL, or artifact ID.",
						},
						"summary": map[string]any{
							"type":        "string",
							"description": "Short visible summary of what the evidence proves.",
						},
						"verification_status": map[string]any{
							"type":        "string",
							"description": "Verification status such as verified, inferred, stale, blocked, or unknown.",
						},
					},
				}),
			),
			mcp.WithNumber("confidence",
				mcp.Description("Caller confidence as a normalized value from 0.0 to 1.0."),
			),
			mcp.WithBoolean("execute",
				mcp.Description("When false, return guidance only and do not mark a move executed."),
			),
			mcp.WithString("proposed_answer",
				mcp.Description("Caller-owned proposed final answer for think(action=finalize)."),
			),
			mcp.WithBoolean("force_finalize",
				mcp.Description("Request finalization even with unresolved non-critical objections; critical blockers still fail closed."),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(false),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(false),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			}),
		),
		s.handleThinkHarness,
	)
}

func (s *Server) handleThinkHarness(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx

	if args, ok := request.Params.Arguments.(map[string]any); ok {
		if _, hasLegacyThought := args["thought"]; hasLegacyThought && request.GetString("action", "") == "" {
			return marshalToolErrorResult(map[string]any{
				"status":    "error",
				"code":      "legacy_thought_not_supported",
				"message":   "think(thought=...) no longer routes to thinking patterns or creates implicit sessions.",
				"next_step": "Call think(action=\"start\", task=..., context_summary=...) to begin a caller-centered thinking session.",
			})
		}
	}

	action := request.GetString("action", "")
	if action == "" {
		return marshalToolErrorResult(map[string]any{
			"status":    "error",
			"code":      "missing_action",
			"message":   "think requires action=start, action=step, or action=finalize.",
			"next_step": "Start with think(action=\"start\", task=..., context_summary=...).",
		})
	}

	switch action {
	case "start", "step", "finalize":
		return marshalToolErrorResult(map[string]any{
			"status":    "error",
			"code":      "controller_unavailable",
			"message":   "This think harness action requires session controller state that is not available in this binary.",
			"next_step": "Use the low-level cognitive move tools directly, or use a build with the think session controller wired.",
			"action":    action,
		})
	default:
		return marshalToolErrorResult(map[string]any{
			"status":    "error",
			"code":      "invalid_action",
			"message":   "think action must be start, step, or finalize.",
			"next_step": "Use action=start, action=step, or action=finalize.",
			"action":    action,
		})
	}
}

func marshalToolErrorResult(payload map[string]any) (*mcp.CallToolResult, error) {
	result, err := marshalToolResult(payload)
	if result != nil {
		result.IsError = true
	}
	return result, err
}
