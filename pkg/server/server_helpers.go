package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/guidance"
)

// marshalToolResult marshals data to JSON and returns an MCP tool result.
// Returns an error result if marshaling fails instead of silently returning empty.
func marshalToolResult(data any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("internal error: response serialization failed: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

// buildGuidancePlan resolves the policy for tool from the server registry and
// computes the NextActionPlan. Returns the plan and a boolean indicating
// whether plan.State is "report_ready". Falls back to a zero plan when no
// policy is registered for the tool. s may be nil during tests or early init.
func (s *Server) buildGuidancePlan(tool, action string, stateSnapshot, rawResult any) (guidance.NextActionPlan, bool) {
	zero := guidance.NextActionPlan{}
	if s == nil || s.guidanceReg == nil {
		return zero, false
	}
	policy, ok := s.guidanceReg.Get(tool)
	if !ok {
		return zero, false
	}
	plan, err := policy.BuildPlan(guidance.PolicyInput{
		Action:        action,
		StateSnapshot: stateSnapshot,
		RawResult:     rawResult,
	})
	if err != nil {
		return zero, false
	}
	return plan, plan.State == "report_ready"
}

// marshalGuidedToolResult assembles the guided response envelope by computing
// the policy plan first, then delegating to marshalGuidedToolResultWithPlan.
func (s *Server) marshalGuidedToolResult(tool, action string, stateSnapshot any, rawResult any) (*mcp.CallToolResult, error) {
	plan, _ := s.buildGuidancePlan(tool, action, stateSnapshot, rawResult)
	return s.marshalGuidedToolResultWithPlan(plan, tool, action, stateSnapshot, rawResult)
}

// marshalGuidedToolResultWithPlan assembles the guided response envelope using
// a pre-computed plan, avoiding a redundant BuildPlan call when the caller
// already computed it.
func (s *Server) marshalGuidedToolResultWithPlan(plan guidance.NextActionPlan, tool, action string, stateSnapshot, rawResult any) (*mcp.CallToolResult, error) {
	payload := guidance.NewResponseBuilder().BuildPayload(plan, guidance.HandlerResult{
		Tool:   tool,
		Action: action,
		State:  stateSnapshot,
		Result: rawResult,
	})
	return marshalToolResult(payload)
}

// projectIDFromContext returns the project ID stored in the request context.
// Empty string when no muxcore.ProjectContext is present.
func projectIDFromContext(ctx context.Context) string {
	if pc, ok := ProjectContextFromContext(ctx); ok {
		return pc.ID
	}
	return ""
}

// sessionEnvFromContext returns the per-session environment (API keys) from
// the muxcore.ProjectContext. Nil when the context has no project attached.
func sessionEnvFromContext(ctx context.Context) map[string]string {
	if pc, ok := ProjectContextFromContext(ctx); ok {
		return pc.Env
	}
	return nil
}
