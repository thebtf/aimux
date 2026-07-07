package server

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/server/classifier"
)

// registerSpecTool registers the dedicated `spec` MCP facade over the task backbone.
func (s *Server) registerSpecTool() {
	s.registerContractedTool(
		toolContract{Name: "spec", Classification: "async_mandatory", AdapterKind: "loom"},
		mcp.NewTool("spec",
			mcp.WithDescription("[delegate — Loom routed, async] Submit specification work through the existing task backbone. "+
				"Use prompt+target to request requirements/specification output for a feature, artifact, PR, or change record. "+
				"Returns the same accepted JSON TaskResult, task_id/job_id, status polling command, cancel command, and task resource URIs as task."),
			mcp.WithString("prompt",
				mcp.Required(),
				mcp.Description("Specification prompt routed through the task backbone."),
			),
			mcp.WithString("target",
				mcp.Required(),
				mcp.Description("Specification target, such as a feature slug, requirement bundle, artifact path, HEAD, diff, or PR ref."),
			),
			mcp.WithString("resume_id",
				mcp.Description("Loom root task_id to resume."),
			),
			mcp.WithNumber("timeout_seconds",
				mcp.Description("Worker timeout in seconds for long-running spec workers."),
			),
			mcp.WithBoolean("fallback_enabled",
				mcp.Description("Worker fallback policy hint. Default: true."),
			),
			mcp.WithNumber("max_attempts",
				mcp.Description("Worker fallback attempt hint. 0 = worker default."),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(true),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(false),
				OpenWorldHint:   mcp.ToBoolPtr(true),
			}),
		),
		s.handleSpec,
	)
}

// handleSpec is the MCP handler for the dedicated `spec` facade.
func (s *Server) handleSpec(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dispatchCtx, cancel := taskSubmitContext(ctx)
	defer cancel()
	taskReq, parseErr := parseSpecToolRequest(dispatchCtx, req)
	if parseErr != nil {
		return taskToolError(TaskResult{}, parseErr)
	}
	return s.dispatchTaskRequest(dispatchCtx, taskReq)
}

func parseSpecToolRequest(ctx context.Context, req mcp.CallToolRequest) (TaskRequest, error) {
	args := cloneToolArguments(req)
	args["task_class"] = classifier.TaskClassSpec
	if strings.TrimSpace(req.GetString("cli", "")) != "" {
		return TaskRequest{}, extypes.NewUserInputError("spec: cli override is not supported by spec workers", nil)
	}
	if strings.TrimSpace(req.GetString("navigator", "")) != "" {
		return TaskRequest{}, extypes.NewUserInputError("spec: navigator override is not supported by spec workers", nil)
	}
	if strings.TrimSpace(req.GetString("sandbox", "")) != "" {
		return TaskRequest{}, extypes.NewUserInputError("spec: sandbox is not supported by spec workers", nil)
	}
	if req.GetBool("gate", false) {
		return TaskRequest{}, extypes.NewUserInputError("spec: gate is not supported; run review(..., gate=true) after spec output exists", nil)
	}
	if recipeID := strings.TrimSpace(req.GetString("recipe_id", "")); recipeID != "" {
		return TaskRequest{}, extypes.NewUserInputError("spec: recipe_id is not supported by the public spec facade; use task(recipe_id=...) for curated recipes", nil)
	}
	req.Params.Arguments = args
	return parseTaskToolRequest(ctx, req)
}
