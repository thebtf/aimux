package server

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/think/harness"
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
	case "start":
		return s.handleThinkHarnessStart(ctx, request)
	case "step":
		return s.handleThinkHarnessStep(ctx, request)
	case "finalize":
		return s.handleThinkHarnessFinalize(ctx, request)
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

func (s *Server) handleThinkHarnessStart(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := s.thinkController().Start(ctx, harness.StartRequest{
		Task:           request.GetString("task", ""),
		Goal:           request.GetString("goal", ""),
		ContextSummary: request.GetString("context_summary", ""),
		SuccessSignal:  request.GetString("success_signal", ""),
	})
	if err != nil {
		return marshalThinkHarnessError(err)
	}
	return marshalToolResult(resp)
}

func (s *Server) handleThinkHarnessStep(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	evidence, evidenceErr := parseThinkHarnessEvidence(request.GetRawArguments())
	if evidenceErr != nil {
		return marshalToolErrorResult(map[string]any{
			"status":    "error",
			"code":      harness.ErrorCodeInvalidInput,
			"message":   evidenceErr.Error(),
			"next_step": "Pass evidence as an array of objects with kind, ref, and summary fields.",
		})
	}
	execute := request.GetBool("execute", true)
	resp, err := s.thinkController().Step(ctx, harness.StepRequest{
		SessionID:        request.GetString("session_id", ""),
		ChosenMove:       request.GetString("chosen_move", ""),
		WorkProduct:      request.GetString("work_product", ""),
		Evidence:         evidence,
		CallerConfidence: request.GetFloat("confidence", 0),
		Execute:          &execute,
	})
	if err != nil {
		return marshalThinkHarnessError(err)
	}
	return marshalToolResult(resp)
}

func (s *Server) handleThinkHarnessFinalize(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := s.thinkController().Finalize(ctx, harness.FinalizeRequest{
		SessionID:      request.GetString("session_id", ""),
		ProposedAnswer: request.GetString("proposed_answer", ""),
		ForceFinalize:  request.GetBool("force_finalize", false),
	})
	if err != nil {
		return marshalThinkHarnessError(err)
	}
	return marshalToolResult(resp)
}

func (s *Server) thinkController() *harness.Controller {
	if s.thinkHarness == nil {
		s.thinkHarness = harness.NewController(harness.NewInMemoryStore())
	}
	return s.thinkHarness
}

func marshalThinkHarnessError(err error) (*mcp.CallToolResult, error) {
	var harnessErr *harness.HarnessError
	if errors.As(err, &harnessErr) {
		return marshalToolErrorResult(map[string]any{
			"status":    "error",
			"code":      harnessErr.Code,
			"message":   harnessErr.Message,
			"next_step": harnessErr.NextStep,
		})
	}
	return mcp.NewToolResultError(err.Error()), nil
}

func parseThinkHarnessEvidence(rawArgs any) ([]harness.EvidenceRef, error) {
	args, ok := rawArgs.(map[string]any)
	if !ok {
		return nil, nil
	}
	raw, exists := args["evidence"]
	if !exists || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, errors.New("evidence must be an array")
	}
	evidence := make([]harness.EvidenceRef, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("evidence items must be objects")
		}
		ref := harness.EvidenceRef{
			Kind:               stringField(obj, "kind"),
			Ref:                stringField(obj, "ref"),
			Summary:            stringField(obj, "summary"),
			VerificationStatus: stringField(obj, "verification_status"),
		}
		if ref.Kind == "" || ref.Ref == "" || ref.Summary == "" {
			return nil, errors.New("evidence items require kind, ref, and summary")
		}
		evidence = append(evidence, ref)
	}
	return evidence, nil
}

func stringField(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}

func marshalToolErrorResult(payload map[string]any) (*mcp.CallToolResult, error) {
	result, err := marshalToolResult(payload)
	if result != nil {
		result.IsError = true
	}
	return result, err
}
