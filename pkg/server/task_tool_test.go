package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	_ "modernc.org/sqlite"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/executor/code"
	"github.com/thebtf/aimux/pkg/executor/fallback"
	"github.com/thebtf/aimux/pkg/executor/picker"
	"github.com/thebtf/aimux/pkg/executor/review"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/server/classifier"
	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/workflow"
)

func TestHandleTaskValidCallRoutesThroughRouter(t *testing.T) {
	t.Parallel()

	srv, codeWorker, _ := newTaskToolServer(t)
	result := callTaskTool(t, srv, map[string]any{
		"prompt":     "Implement pkg/server/task_tool.go router delegation.",
		"task_class": "code",
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	payload := decodeTaskToolResult(t, result)
	if payload["task_class"] != classifier.TaskClassCode {
		t.Fatalf("task_class = %v, want code; payload=%v", payload["task_class"], payload)
	}
	if payload["content"] != nil {
		t.Fatalf("content = %v, want omitted from accepted response", payload["content"])
	}
	taskID, _ := payload["task_id"].(string)
	if taskID == "" || payload["job_id"] != taskID {
		t.Fatalf("task/job id missing or mismatched: %v", payload)
	}
	if payload["status"] != string(loom.TaskStatusDispatched) {
		t.Fatalf("status = %v, want dispatched; payload=%v", payload["status"], payload)
	}
	if payload["status_command"] == "" || payload["cancel_command"] == "" || payload["task_uri"] == "" || payload["progress_uri"] == "" {
		t.Fatalf("accepted payload missing observation fields: %v", payload)
	}

	task := codeWorker.onlyTask(t)
	if task.WorkerType != code.WorkerTypeCode {
		t.Fatalf("worker_type = %s, want %s", task.WorkerType, code.WorkerTypeCode)
	}
	assertMetadataString(t, task.Metadata, "task_class", classifier.TaskClassCode)
}

func TestTaskSubmitContextDetachesCancellationAndAppliesTimeout(t *testing.T) {
	t.Parallel()

	type ctxKey string
	parent, parentCancel := context.WithCancel(context.WithValue(context.Background(), ctxKey("tenant"), "tenant-a"))
	parentCancel()

	submitCtx, cancel := taskSubmitContext(parent)
	defer cancel()

	if got := submitCtx.Value(ctxKey("tenant")); got != "tenant-a" {
		t.Fatalf("submitCtx.Value(tenant) = %v, want tenant-a", got)
	}
	deadline, ok := submitCtx.Deadline()
	if !ok {
		t.Fatal("submitCtx.Deadline() missing, want bounded detached timeout")
	}
	remaining := time.Until(deadline)
	if remaining < 20*time.Second || remaining > taskToolDetachedSubmitTimeout+time.Second {
		t.Fatalf("submitCtx deadline = %v from now, want bounded timeout near %v", remaining, taskToolDetachedSubmitTimeout)
	}
	select {
	case <-submitCtx.Done():
		t.Fatal("submitCtx canceled by parent, want detached submit window")
	default:
	}
}

func TestHandleTaskCanceledCallerStillSubmitsTask(t *testing.T) {
	t.Parallel()

	srv, codeWorker, _ := newTaskToolServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := srv.handleTask(ctx, makeRequest("task", map[string]any{
		"prompt":     "Implement pkg/server/task_tool.go router delegation.",
		"task_class": "code",
	}))
	if err != nil {
		t.Fatalf("handleTask returned Go error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	payload := decodeTaskToolResult(t, result)
	taskID, _ := payload["task_id"].(string)
	if taskID == "" || payload["job_id"] != taskID {
		t.Fatalf("task/job id missing or mismatched: %v", payload)
	}
	if payload["status"] != string(loom.TaskStatusDispatched) {
		t.Fatalf("status = %v, want dispatched; payload=%v", payload["status"], payload)
	}
	task := codeWorker.onlyTask(t)
	if task.WorkerType != code.WorkerTypeCode {
		t.Fatalf("worker_type = %s, want %s", task.WorkerType, code.WorkerTypeCode)
	}
}

func TestHandleTaskMissingPromptReturnsUserInputError(t *testing.T) {
	t.Parallel()

	srv, _, _ := newTaskToolServer(t)
	result := callTaskTool(t, srv, map[string]any{"task_class": "code"})
	if !result.IsError {
		t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeUserInputError.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeUserInputError)
	}
}

func TestHandleTaskNilLoomReturnsCapabilityMismatch(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	result := callTaskTool(t, srv, map[string]any{
		"prompt":     "review HEAD",
		"task_class": "review",
		"target":     "HEAD",
	})
	if !result.IsError {
		t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeCapabilityMismatch.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeCapabilityMismatch)
	}
	if payload.Retryable {
		t.Fatal("retryable = true, want false for missing Loom")
	}
	if !strings.Contains(payload.Message, "restart aimux") {
		t.Fatalf("message = %q, want restart remediation hint", payload.Message)
	}
}

func TestHandleTaskReviewRequiresTarget(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	result := callTaskTool(t, srv, map[string]any{
		"prompt":     "review this",
		"task_class": "review",
	})
	if !result.IsError {
		t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeUserInputError.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeUserInputError)
	}
	if got := reviewWorker.taskCount(); got != 0 {
		t.Fatalf("review task count = %d, want 0", got)
	}
}

func TestHandleTaskModeParamUnavailable(t *testing.T) {
	t.Parallel()

	srv, codeWorker, _ := newTaskToolServer(t)
	result := callTaskTool(t, srv, map[string]any{
		"prompt": "rewrite this prompt",
		"mode":   "universal",
	})
	if !result.IsError {
		t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeUserInputError.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeUserInputError)
	}
	if got := codeWorker.taskCount(); got != 0 {
		t.Fatalf("code task count = %d, want 0", got)
	}
}

func TestHandleTaskReviewGateMetadata(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	result := callTaskTool(t, srv, map[string]any{
		"prompt":          "review HEAD",
		"task_class":      "review",
		"target":          "HEAD",
		"gate":            true,
		"timeout_seconds": 17,
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	task := reviewWorker.onlyTask(t)
	assertMetadataString(t, task.Metadata, "target", "HEAD")
	assertMetadataString(t, task.Metadata, "review_target", "HEAD")
	assertMetadataBool(t, task.Metadata, "gate", true)
	assertMetadataBool(t, task.Metadata, "review_gate", true)
	if task.Timeout != 17 {
		t.Fatalf("timeout_seconds = %d, want 17", task.Timeout)
	}
}

func TestHandleReviewStandardRoutesThroughReviewBackbone(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	result := callReviewTool(t, srv, map[string]any{
		"prompt": "review HEAD for regressions",
		"target": "HEAD",
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	payload := decodeTaskToolResult(t, result)
	if payload["task_class"] != classifier.TaskClassReview {
		t.Fatalf("task_class = %v, want review; payload=%v", payload["task_class"], payload)
	}
	task := reviewWorker.onlyTask(t)
	if task.WorkerType != review.WorkerTypeReview {
		t.Fatalf("worker_type = %s, want %s", task.WorkerType, review.WorkerTypeReview)
	}
	assertMetadataString(t, task.Metadata, "target", "HEAD")
	assertMetadataString(t, task.Metadata, "review_target", "HEAD")
	if _, ok := task.Metadata["gate"]; ok {
		t.Fatalf("standard review set gate metadata; want non-gate review: %v", task.Metadata)
	}
}

func TestHandleReviewGateRoutesThroughReviewBackbone(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	result := callReviewTool(t, srv, map[string]any{
		"prompt":          "review HEAD for release readiness",
		"target":          "HEAD",
		"gate":            true,
		"timeout_seconds": 23,
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	task := reviewWorker.onlyTask(t)
	assertMetadataString(t, task.Metadata, "target", "HEAD")
	assertMetadataString(t, task.Metadata, "review_target", "HEAD")
	assertMetadataBool(t, task.Metadata, "gate", true)
	assertMetadataBool(t, task.Metadata, "review_gate", true)
	if task.Timeout != 23 {
		t.Fatalf("timeout_seconds = %d, want 23", task.Timeout)
	}
}

func TestHandleSpecStandardRoutesThroughSpecBackbone(t *testing.T) {
	t.Parallel()

	srv, specWorker := newSpecTaskToolServer(t)
	result := callSpecTool(t, srv, map[string]any{
		"prompt":          "write a feature spec with acceptance criteria",
		"target":          "AIMUX-9 CR-007",
		"timeout_seconds": 29,
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	payload := decodeTaskToolResult(t, result)
	if payload["task_class"] != classifier.TaskClassSpec {
		t.Fatalf("task_class = %v, want spec; payload=%v", payload["task_class"], payload)
	}
	task := specWorker.onlyTask(t)
	if task.WorkerType != specWorkerType {
		t.Fatalf("worker_type = %s, want %s", task.WorkerType, specWorkerType)
	}
	assertMetadataString(t, task.Metadata, "target", "AIMUX-9 CR-007")
	assertMetadataString(t, task.Metadata, "spec_target", "AIMUX-9 CR-007")
	if _, ok := task.Metadata["review_target"]; ok {
		t.Fatalf("spec task set review_target metadata; want spec-local target metadata: %v", task.Metadata)
	}
	if _, ok := task.Metadata["gate"]; ok {
		t.Fatalf("spec task set gate metadata; want review gate deferred: %v", task.Metadata)
	}
	if task.Timeout != 29 {
		t.Fatalf("timeout_seconds = %d, want 29", task.Timeout)
	}
}

func TestHandleTaskSpecClassRoutesThroughSpecWorker(t *testing.T) {
	t.Parallel()

	srv, specWorker := newSpecTaskToolServer(t)
	result := callTaskTool(t, srv, map[string]any{
		"prompt":     "write requirements and acceptance criteria",
		"task_class": "spec",
		"target":     "docs/specs/aimux9-cr007.md",
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	task := specWorker.onlyTask(t)
	assertMetadataString(t, task.Metadata, "task_class", classifier.TaskClassSpec)
	assertMetadataString(t, task.Metadata, "target", "docs/specs/aimux9-cr007.md")
	assertMetadataString(t, task.Metadata, "spec_target", "docs/specs/aimux9-cr007.md")
}

func TestHandleSpecRejectsReviewGate(t *testing.T) {
	t.Parallel()

	srv, specWorker := newSpecTaskToolServer(t)
	result := callSpecTool(t, srv, map[string]any{
		"prompt": "write a feature spec",
		"target": "AIMUX-9 CR-007",
		"gate":   true,
	})
	if !result.IsError {
		t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeUserInputError.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeUserInputError)
	}
	if !strings.Contains(payload.Message, "review(..., gate=true)") {
		t.Fatalf("message = %q, want review gate guidance", payload.Message)
	}
	if got := specWorker.taskCount(); got != 0 {
		t.Fatalf("spec task count = %d, want 0", got)
	}
}

func TestHandleSpecRejectsDeferredFacadeKnobs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		argKey      string
		argValue    any
		wantMessage string
	}{
		{name: "cli", argKey: "cli", argValue: "codex", wantMessage: "cli override is not supported"},
		{name: "navigator", argKey: "navigator", argValue: "none", wantMessage: "navigator override is not supported"},
		{name: "sandbox", argKey: "sandbox", argValue: "read-only", wantMessage: "sandbox is not supported"},
		{name: "recipe", argKey: "recipe_id", argValue: "code-review", wantMessage: "recipe_id is not supported"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, specWorker := newSpecTaskToolServer(t)
			args := map[string]any{
				"prompt": "write a feature spec",
				"target": "AIMUX-9 CR-007",
			}
			args[tc.argKey] = tc.argValue
			result := callSpecTool(t, srv, args)
			if !result.IsError {
				t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
			}
			payload := decodeTaskToolError(t, result)
			if payload.Code != extypes.CLIErrorCodeUserInputError.String() {
				t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeUserInputError)
			}
			if !strings.Contains(payload.Message, tc.wantMessage) {
				t.Fatalf("message = %q, want %q", payload.Message, tc.wantMessage)
			}
			if got := specWorker.taskCount(); got != 0 {
				t.Fatalf("spec task count = %d, want 0", got)
			}
		})
	}
}

func TestHandleReviewCodeReviewRecipeUsesReviewBackbone(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	result := callReviewTool(t, srv, map[string]any{
		"prompt":    "review HEAD for regressions",
		"target":    "HEAD",
		"recipe_id": "code-review",
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	task := reviewWorker.onlyTask(t)
	assertMetadataString(t, task.Metadata, "recipe_id", "code-review")
	assertMetadataBool(t, task.Metadata, "recipe_read_only", true)
	assertMetadataBool(t, task.Metadata, "gate", true)
	assertMetadataBool(t, task.Metadata, "review_gate", true)
}

func TestHandleReviewRejectsWorkflowBackedRecipeFamily(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	result := callReviewTool(t, srv, map[string]any{
		"prompt":    "run a security audit",
		"target":    "HEAD",
		"recipe_id": "security-audit",
	})
	if !result.IsError {
		t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeUserInputError.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeUserInputError)
	}
	if !strings.Contains(payload.Message, "use task(recipe_id=...)") {
		t.Fatalf("message = %q, want task(recipe_id=...) guidance", payload.Message)
	}
	if !stringSlicesEqual(payload.AvailableRecipes, []string{"code-review"}) {
		t.Fatalf("available_recipes = %#v, want only public review recipe", payload.AvailableRecipes)
	}
	if stringSliceContains(payload.AvailableRecipes, "security-audit") {
		t.Fatalf("available_recipes = %v, want review-only recipes", payload.AvailableRecipes)
	}
	if got := reviewWorker.taskCount(); got != 0 {
		t.Fatalf("review task count = %d, want 0", got)
	}
}

func TestHandleReviewRejectsSecondOpinionRecipeUntilSurfaceWidens(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	result := callReviewTool(t, srv, map[string]any{
		"prompt":    "give an independent assessment",
		"target":    "HEAD",
		"recipe_id": "second-opinion",
	})
	if !result.IsError {
		t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeUserInputError.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeUserInputError)
	}
	if !strings.Contains(payload.Message, "use task(recipe_id=...)") {
		t.Fatalf("message = %q, want task(recipe_id=...) guidance", payload.Message)
	}
	if !stringSlicesEqual(payload.AvailableRecipes, []string{"code-review"}) {
		t.Fatalf("available_recipes = %#v, want only public review recipe", payload.AvailableRecipes)
	}
	if got := reviewWorker.taskCount(); got != 0 {
		t.Fatalf("review task count = %d, want 0", got)
	}
}

func TestHandleReviewRejectsCLIOverrideUntilSupported(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	result := callReviewTool(t, srv, map[string]any{
		"prompt": "review HEAD",
		"target": "HEAD",
		"cli":    "gemini",
	})
	if !result.IsError {
		t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeUserInputError.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeUserInputError)
	}
	if !strings.Contains(payload.Message, "cli override is not supported") {
		t.Fatalf("message = %q, want unsupported cli guidance", payload.Message)
	}
	if got := reviewWorker.taskCount(); got != 0 {
		t.Fatalf("review task count = %d, want 0", got)
	}
}

func TestHandleReviewNilLoomMatchesTaskCapabilityError(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	result := callReviewTool(t, srv, map[string]any{
		"prompt": "review HEAD",
		"target": "HEAD",
	})
	if !result.IsError {
		t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeCapabilityMismatch.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeCapabilityMismatch)
	}
	if payload.Retryable {
		t.Fatal("retryable = true, want false for missing Loom")
	}
	if !strings.Contains(payload.Message, "restart aimux") {
		t.Fatalf("message = %q, want restart remediation hint", payload.Message)
	}
}

func TestReviewToolRegisteredAsDedicatedSurface(t *testing.T) {
	t.Parallel()

	srv := testServerWithLoom(t)
	response := srv.mcp.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response %s: %v", raw, err)
	}
	result, ok := decoded["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result missing or wrong type: %s", raw)
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools missing or wrong type: %s", raw)
	}
	for _, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("tool item type = %T, want map", item)
		}
		if tool["name"] == "review" {
			description, _ := tool["description"].(string)
			if !strings.Contains(description, "task/review backbone") {
				t.Fatalf("review description = %q, want task/review backbone guidance", description)
			}
			reviewToolRaw, err := json.Marshal(tool)
			if err != nil {
				t.Fatalf("marshal review tool: %v", err)
			}
			if strings.Contains(string(reviewToolRaw), "second-opinion") {
				t.Fatalf("review tool schema must not expose second-opinion: %s", reviewToolRaw)
			}
			return
		}
	}
	t.Fatalf("review tool not registered; tools=%v", tools)
}

func TestSpecToolRegisteredAsDedicatedSurface(t *testing.T) {
	t.Parallel()

	srv := testServerWithLoom(t)
	response := srv.mcp.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response %s: %v", raw, err)
	}
	result, ok := decoded["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result missing or wrong type: %s", raw)
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools missing or wrong type: %s", raw)
	}
	for _, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("tool item type = %T, want map", item)
		}
		if tool["name"] == "spec" {
			description, _ := tool["description"].(string)
			if !strings.Contains(description, "specification work through the existing task backbone") {
				t.Fatalf("spec description = %q, want task backbone guidance", description)
			}
			inputSchema, ok := tool["inputSchema"].(map[string]any)
			if !ok {
				t.Fatalf("spec inputSchema missing or wrong type: %v", tool["inputSchema"])
			}
			properties, ok := inputSchema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("spec inputSchema.properties missing or wrong type: %v", inputSchema["properties"])
			}
			for _, forbidden := range []string{"recipe_id", "gate", "cli", "navigator", "sandbox"} {
				if _, ok := properties[forbidden]; ok {
					t.Fatalf("spec tool schema must not expose %s: %v", forbidden, properties)
				}
			}
			return
		}
	}
	t.Fatalf("spec tool not registered; tools=%v", tools)
}

func TestHandleTaskRecipeCodeReviewRoutesAsReviewWithMetadata(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	result := callTaskTool(t, srv, map[string]any{
		"prompt":    "review HEAD for regressions",
		"recipe_id": "code-review",
		"target":    "HEAD",
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	payload := decodeTaskToolResult(t, result)
	if payload["task_class"] != classifier.TaskClassReview {
		t.Fatalf("task_class = %v, want review; payload=%v", payload["task_class"], payload)
	}
	task := reviewWorker.onlyTask(t)
	if task.WorkerType != review.WorkerTypeReview {
		t.Fatalf("worker_type = %s, want %s", task.WorkerType, review.WorkerTypeReview)
	}
	assertMetadataString(t, task.Metadata, "target", "HEAD")
	assertMetadataBool(t, task.Metadata, "gate", true)
	assertMetadataBool(t, task.Metadata, "review_gate", true)
	assertMetadataString(t, task.Metadata, "recipe_id", "code-review")
	assertMetadataString(t, task.Metadata, "recipe_title", "Code Review")
	assertMetadataBool(t, task.Metadata, "recipe_read_only", true)
	assertMetadataStringSlice(t, task.Metadata, "recipe_phases", []string{"structural", "behavioural", "adversarial"})
	assertMetadataStringSlice(t, task.Metadata, "recipe_output_resources", []string{"task_snapshot", "task_events", "task_progress"})
}

func TestHandleTaskRecipeSecondOpinionUsesAggregateReviewMode(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	result := callTaskTool(t, srv, map[string]any{
		"prompt":    "give a second opinion on HEAD",
		"recipe_id": "second-opinion",
		"target":    "HEAD",
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	task := reviewWorker.onlyTask(t)
	assertMetadataString(t, task.Metadata, "recipe_id", "second-opinion")
	assertMetadataString(t, task.Metadata, "recipe_title", "Second Opinion")
	if _, ok := task.Metadata["gate"]; ok {
		t.Fatalf("second-opinion recipe set gate metadata; want aggregate review mode: %v", task.Metadata)
	}
	if _, ok := task.Metadata["review_gate"]; ok {
		t.Fatalf("second-opinion recipe set review_gate metadata; want aggregate review mode: %v", task.Metadata)
	}
}

func TestHandleTaskWorkflowBackedRecipesRouteWithWorkflowMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id         string
		title      string
		workflowID string
		steps      []workflow.WorkflowStep
	}{
		{id: "security-audit", title: "Security Audit", workflowID: "secaudit", steps: workflow.SecurityAuditSteps()},
		{id: "debug-investigation", title: "Debug Investigation", workflowID: "debug", steps: workflow.DebugSteps()},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()
			srv, _, reviewWorker := newTaskToolServer(t)
			result := callTaskTool(t, srv, map[string]any{
				"prompt":    "investigate HEAD with a workflow-backed recipe",
				"recipe_id": tt.id,
				"target":    "HEAD",
			})
			if result.IsError {
				t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
			}

			payload := decodeTaskToolResult(t, result)
			if payload["task_class"] != classifier.TaskClassReview {
				t.Fatalf("task_class = %v, want review; payload=%v", payload["task_class"], payload)
			}
			taskID, _ := payload["task_id"].(string)
			task := waitTaskToolStatus(t, srv, taskID, loom.TaskStatusCompleted)
			if got := reviewWorker.taskCount(); got != 0 {
				t.Fatalf("review task count = %d, want workflow worker route without review worker", got)
			}
			if task.WorkerType != workflowRecipeWorkerType {
				t.Fatalf("worker_type = %s, want %s", task.WorkerType, workflowRecipeWorkerType)
			}
			assertMetadataString(t, task.Metadata, "target", "HEAD")
			assertMetadataString(t, task.Metadata, "recipe_id", tt.id)
			assertMetadataString(t, task.Metadata, "recipe_title", tt.title)
			assertMetadataBool(t, task.Metadata, "recipe_read_only", true)
			assertMetadataStringSlice(t, task.Metadata, "recipe_output_resources", []string{"task_snapshot", "task_events", "task_progress"})
			assertMetadataString(t, task.Metadata, "recipe_workflow_id", tt.workflowID)
			if source := metadataStringValue(t, task.Metadata, "recipe_workflow_source"); !strings.Contains(source, "pkg/workflow/") {
				t.Fatalf("recipe_workflow_source = %q, want pkg/workflow source", source)
			}
			assertMetadataStringSlice(t, task.Metadata, "recipe_workflow_steps", recipeWorkflowStepNames(tt.steps))
			assertMetadataString(t, task.Metadata, "workflow_result_status", "completed")
			assertMetadataInt(t, task.Metadata, "workflow_step_count", len(tt.steps))
			assertMetadataStringSlice(t, task.Metadata, "workflow_step_statuses", workflowStepStatusesFromSteps(tt.steps))
			if !strings.Contains(task.Result, "Workflow status: completed") {
				t.Fatalf("workflow result missing completed status: %s", task.Result)
			}
			if !strings.Contains(task.Result, "workflow step complete") {
				t.Fatalf("workflow result missing executed step output: %s", task.Result)
			}
		})
	}
}

func TestHandleTaskWorkflowBackedReadOnlyRecipeForcesDispatchSandbox(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	sandboxes := []string{}
	dispatch := func(_ context.Context, cli string, spec picker.TaskSpec, _ map[string]any) (string, string, error) {
		mu.Lock()
		sandboxes = append(sandboxes, spec.Sandbox)
		mu.Unlock()
		if strings.TrimSpace(spec.Prompt) == "" {
			return "", cli, errors.New("empty workflow step prompt")
		}
		return `{"type":"agent_message","content":"workflow step complete"}` + "\n", cli, nil
	}
	srv, _, reviewWorker := newTaskToolServerWithWorkflowHooks(t, defaultRecipeProfile(), dispatch, defaultWorkflowRecipePattern)

	result := callTaskTool(t, srv, map[string]any{
		"prompt":    "audit HEAD with a workflow-backed read-only recipe",
		"recipe_id": "security-audit",
		"target":    "HEAD",
		"sandbox":   "workspace-write",
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	payload := decodeTaskToolResult(t, result)
	task := waitTaskToolStatus(t, srv, payload["task_id"].(string), loom.TaskStatusCompleted)
	if got := reviewWorker.taskCount(); got != 0 {
		t.Fatalf("review task count = %d, want workflow worker route without review worker", got)
	}
	if task.WorkerType != workflowRecipeWorkerType {
		t.Fatalf("worker_type = %s, want %s", task.WorkerType, workflowRecipeWorkerType)
	}

	mu.Lock()
	captured := append([]string(nil), sandboxes...)
	mu.Unlock()
	if len(captured) == 0 {
		t.Fatal("workflow dispatch was not called")
	}
	for i, sandbox := range captured {
		if sandbox != "read-only" {
			t.Fatalf("dispatch sandbox[%d] = %q; want read-only for workflow-backed read-only recipe", i, sandbox)
		}
	}
}

func TestWorkflowRecipeExecutorSenderSendFallsBackToRawContentWhenSelectedProfileIsNil(t *testing.T) {
	t.Parallel()

	registry := driver.NewRegistry(map[string]*config.CLIProfile{"codex": nil})
	registry.SetAvailable("codex", true)
	sender := &workflowRecipeExecutorSender{
		server:     &Server{registry: registry},
		task:       &loom.Task{Metadata: map[string]any{}},
		defaultCLI: "codex",
		dispatch: func(_ context.Context, cli string, _ picker.TaskSpec, _ map[string]any) (string, string, error) {
			return `{"type":"agent_message","content":"workflow step complete"}` + "\n", cli, nil
		},
	}
	handle, err := sender.Get(context.Background(), "codex")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	response, err := sender.Send(context.Background(), handle, types.Message{Content: "run workflow step"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if response.Content != `{"type":"agent_message","content":"workflow step complete"}`+"\n" {
		t.Fatalf("content = %q, want raw content when selected CLI profile is nil", response.Content)
	}
}

func TestWorkflowRecipeExecutorSenderSendRejectsFallbackSelectedReadOnlyPolicyMismatch(t *testing.T) {
	t.Parallel()

	registry := driver.NewRegistry(map[string]*config.CLIProfile{
		"codex":  defaultRecipeProfile(),
		"claude": limitedRecipeProfile(),
	})
	registry.SetAvailable("codex", true)
	registry.SetAvailable("claude", true)
	dispatchCalled := false
	sender := &workflowRecipeExecutorSender{
		server: &Server{registry: registry},
		task: &loom.Task{Metadata: map[string]any{
			"recipe_id":  "security-audit",
			"task_class": "review",
		}},
		defaultCLI: "codex",
		dispatch: func(_ context.Context, cli string, _ picker.TaskSpec, _ map[string]any) (string, string, error) {
			dispatchCalled = true
			if cli != "codex" {
				t.Fatalf("dispatch CLI = %q, want codex", cli)
			}
			return `{"type":"agent_message","content":"workflow step complete"}` + "\n", "claude", nil
		},
	}

	handle, err := sender.Get(context.Background(), "codex")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_, err = sender.Send(context.Background(), handle, types.Message{Content: "run workflow step"})
	if err == nil {
		t.Fatal("Send returned nil error, want recipe policy rejection for fallback-selected non-read-only CLI")
	}
	if !dispatchCalled {
		t.Fatal("dispatch was not called; test must cover fallback-selected post-dispatch validation")
	}
	if !strings.Contains(err.Error(), "recipe policy cannot be enforced by selected provider") {
		t.Fatalf("Send error = %v, want recipe policy enforcement error", err)
	}
}

func TestWorkflowRecipeExecutorSenderSendDoesNotInvokeFallbackSelectedReadOnlyPolicyMismatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	marker := filepath.Join(dir, "fallback-selected-cli-invoked")
	codexPath := fakeExecutableWithContents(t, dir, "codex-slow",
		"#!/bin/sh\nsleep 5\n",
		"@echo off\r\nping -n 6 127.0.0.1 > nul\r\nexit /b 0\r\n",
	)
	claudePath := fakeExecutableWithContents(t, dir, "claude-marker",
		"#!/bin/sh\nprintf invoked > \"$AIMUX_FALLBACK_MARKER\"\nprintf '%s\n' '{\"type\":\"agent_message\",\"content\":\"fallback executed\"}'\n",
		"@echo off\r\necho invoked> \"%AIMUX_FALLBACK_MARKER%\"\r\necho {\"type\":\"agent_message\",\"content\":\"fallback executed\"}\r\nexit /b 0\r\n",
	)

	codex := defaultRecipeProfile()
	codex.Binary = codexPath
	codex.ResolvedPath = codexPath
	codex.TimeoutSeconds = 1
	claude := limitedRecipeProfile()
	claude.Name = "claude"
	claude.Binary = claudePath
	claude.ResolvedPath = claudePath
	claude.TimeoutSeconds = 5
	registry := driver.NewRegistry(map[string]*config.CLIProfile{
		"codex":  codex,
		"claude": claude,
	})
	registry.SetAvailable("codex", true)
	registry.SetAvailable("claude", true)
	srv := &Server{cfg: &config.Config{}, registry: registry}
	srv.fallbackPicker = buildFallbackPicker(srv)
	if srv.fallbackPicker == nil {
		t.Fatal("buildFallbackPicker returned nil")
	}

	sender := &workflowRecipeExecutorSender{
		server: srv,
		task: &loom.Task{
			Env:     map[string]string{"AIMUX_FALLBACK_MARKER": marker},
			Timeout: 1,
			Metadata: map[string]any{
				"recipe_id":  "security-audit",
				"task_class": "review",
			},
		},
		defaultCLI: "codex",
		dispatch:   workflowRecipeWorker{server: srv}.dispatchViaServer,
	}

	handle, err := sender.Get(context.Background(), "codex")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_, err = sender.Send(context.Background(), handle, types.Message{Content: "run workflow step"})
	if err == nil {
		t.Fatal("Send returned nil error, want unsupported fallback-selected CLI to be rejected")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("fallback-selected CLI without read-only support was executed; marker file was created")
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("stat fallback marker: %v", statErr)
	}
}

func TestWorkflowPatternFnExposesDataAsResultText(t *testing.T) {
	t.Parallel()
	patterns.RegisterAll()

	result, err := workflowPatternFn("debugging_approach", map[string]any{
		"issue": "cache replay loses the workflow hypothesis chain",
	})
	if err != nil {
		t.Fatalf("workflowPatternFn: %v", err)
	}
	text, ok := result["result"].(string)
	if !ok || strings.TrimSpace(text) == "" {
		t.Fatalf("result text = %#v, want non-empty string", result["result"])
	}
	if text == "success" || text == "debugging_approach" {
		t.Fatalf("result text collapsed to status/pattern only: %q", text)
	}
	for _, want := range []string{"hypothesisCount", "cache replay loses the workflow hypothesis chain"} {
		if !strings.Contains(text, want) {
			t.Fatalf("result text missing %q: %s", want, text)
		}
	}
}

func TestWorkflowPatternResultTextPrependsRootCauseVerdictFromStructuredEvidence(t *testing.T) {
	t.Parallel()
	result := &think.ThinkResult{
		Pattern: "decision_framework",
		Status:  "success",
		Summary: "Decision matrix selected the highest-scoring candidate.",
	}

	text := workflowPatternResultText(result, result.Summary, map[string]any{
		"workflow_root_cause_gate": true,
		"workflow_root_cause_evidence": map[string]any{
			"status": "identified",
			"cause":  "the real workflow step returned an explicit source-truth verdict",
		},
	})

	if !strings.HasPrefix(text, "Root cause: the real workflow step returned an explicit source-truth verdict\n") {
		t.Fatalf("result text did not start with structured root-cause verdict: %s", text)
	}
}

func TestWorkflowPatternResultTextDoesNotInferRootCauseFromSyntheticSummary(t *testing.T) {
	t.Parallel()
	result := &think.ThinkResult{
		Pattern: "decision_framework",
		Status:  "success",
		Summary: "Decision matrix ranked options; wrapper context says a cause may exist upstream.",
	}

	text := workflowPatternResultText(result, result.Summary, map[string]any{
		"workflow_root_cause_gate": true,
	})

	if !strings.HasPrefix(text, "Root cause not identified: decision_framework output did not identify a root cause.\n") {
		t.Fatalf("result text inferred a root cause without source-truth verdict: %s", text)
	}
}

func TestHandleTaskWorkflowBackedRecipePropagatesThinkPatternDataToNextStep(t *testing.T) {
	t.Parallel()
	patterns.RegisterAll()

	var mu sync.Mutex
	prompts := []string{}
	dispatch := func(_ context.Context, cli string, spec picker.TaskSpec, _ map[string]any) (string, string, error) {
		mu.Lock()
		prompts = append(prompts, spec.Prompt)
		mu.Unlock()
		if strings.TrimSpace(spec.Prompt) == "" {
			return "", cli, errors.New("empty workflow step prompt")
		}
		content := "workflow step complete"
		switch {
		case strings.Contains(spec.Prompt, "Capture and structure the following error symptom"):
			content = "Observed: workflow cache replay loses the hypothesis chain before root-cause selection. Expected: replay preserves the workflow context for later steps."
		case strings.Contains(spec.Prompt, "Gather evidence for and against each debugging hypothesis"):
			content = "Evidence gathered from replay metadata: the root cause is stale workflow step result text; the cache propagation boundary drops the think pattern data before root_cause."
		case strings.Contains(spec.Prompt, "Generate a concrete fix plan"):
			content = "Fix plan: preserve think pattern result text through workflow execution and verify replay metadata after the root cause gate passes."
		}
		return fmt.Sprintf("{\"type\":\"agent_message\",\"content\":%q}\n", content), cli, nil
	}
	srv, _, reviewWorker := newTaskToolServerWithWorkflowHooks(t, defaultRecipeProfile(), dispatch, workflowPatternFn)
	issue := "debug flaky workflow cache propagation"
	result := callTaskTool(t, srv, map[string]any{
		"prompt":    issue,
		"recipe_id": "debug-investigation",
		"target":    "HEAD",
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolResult(t, result)
	task := waitTaskToolStatus(t, srv, payload["task_id"].(string), loom.TaskStatusCompleted)
	if got := reviewWorker.taskCount(); got != 0 {
		t.Fatalf("review task count = %d, want workflow worker route without review worker", got)
	}
	assertMetadataStringSlice(t, task.Metadata, "workflow_step_statuses", workflowStepStatusesFromSteps(workflow.DebugSteps()))
	for _, want := range []string{"Workflow status: completed", "root_cause [completed]: Root cause:", "fix_plan [completed]"} {
		if !strings.Contains(task.Result, want) {
			t.Fatalf("workflow result missing %q: %s", want, task.Result)
		}
	}

	mu.Lock()
	captured := append([]string(nil), prompts...)
	mu.Unlock()
	for _, prompt := range captured {
		if !strings.Contains(prompt, "Gather evidence for and against each debugging hypothesis") {
			continue
		}
		for _, want := range []string{"hypothesisCount", issue} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("evidence-gather prompt missing %q: %s", want, prompt)
			}
		}
		return
	}
	t.Fatalf("did not capture evidence-gather prompt; prompts=%#v", captured)
}

func TestHandleTaskWorkflowBackedDebugRecipeGatesUncertainRootCauseBeforeFixPlan(t *testing.T) {
	t.Parallel()
	patterns.RegisterAll()

	var mu sync.Mutex
	prompts := []string{}
	dispatch := func(_ context.Context, cli string, spec picker.TaskSpec, _ map[string]any) (string, string, error) {
		mu.Lock()
		prompts = append(prompts, spec.Prompt)
		mu.Unlock()
		if strings.TrimSpace(spec.Prompt) == "" {
			return "", cli, errors.New("empty workflow step prompt")
		}
		content := "workflow step complete"
		if strings.Contains(spec.Prompt, "Gather evidence for and against each debugging hypothesis") {
			content = "Evidence gathered from replay metadata is inconclusive: root cause unclear, need more logs before selecting a fix."
		}
		return fmt.Sprintf("{\"type\":\"agent_message\",\"content\":%q}\n", content), cli, nil
	}
	srv, _, _ := newTaskToolServerWithWorkflowHooks(t, defaultRecipeProfile(), dispatch, workflowPatternFn)
	result := callTaskTool(t, srv, map[string]any{
		"prompt":    "debug intermittent workflow cache miss without enough logs",
		"recipe_id": "debug-investigation",
		"target":    "HEAD",
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolResult(t, result)
	task := waitTaskToolStatus(t, srv, payload["task_id"].(string), loom.TaskStatusFailed)
	if !strings.Contains(task.Error, "workflow recipe ended with status \"gated\"") {
		t.Fatalf("failed task error = %q, want gated workflow failure", task.Error)
	}

	mu.Lock()
	captured := append([]string(nil), prompts...)
	mu.Unlock()
	for _, prompt := range captured {
		if strings.Contains(prompt, "Generate a concrete fix plan") {
			t.Fatalf("fix_plan prompt should not execute when root cause is uncertain; prompts=%#v", captured)
		}
	}
}

func TestHandleTaskWorkflowBackedRecipeFailureMarksTaskFailedAndMissesReplay(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	calls := 0
	dispatch := func(_ context.Context, cli string, _ picker.TaskSpec, _ map[string]any) (string, string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return "", cli, errors.New("forced workflow step failure")
	}
	srv, _, _ := newTaskToolServerWithWorkflowHooks(t, defaultRecipeProfile(), dispatch, workflowPatternFn)
	args := map[string]any{
		"prompt":     "debug a recipe workflow failure",
		"recipe_id":  "debug-investigation",
		"target":     "HEAD",
		"project_id": "proj-workflow-failed-replay",
	}

	first := callTaskTool(t, srv, args)
	if first.IsError {
		t.Fatalf("first call unexpected error: %s", taskToolResultText(t, first))
	}
	firstPayload := decodeTaskToolResult(t, first)
	firstTaskID := firstPayload["task_id"].(string)
	firstTask := waitTaskToolStatus(t, srv, firstTaskID, loom.TaskStatusFailed)
	if !strings.Contains(firstTask.Error, "workflow recipe ended with status \"failed\"") {
		t.Fatalf("failed task error = %q, want workflow status failure", firstTask.Error)
	}

	second := callTaskTool(t, srv, args)
	if second.IsError {
		t.Fatalf("second call unexpected error: %s", taskToolResultText(t, second))
	}
	secondPayload := decodeTaskToolResult(t, second)
	secondTaskID := secondPayload["task_id"].(string)
	if secondTaskID == firstTaskID {
		t.Fatalf("failed workflow task replayed as cache hit: %s", secondTaskID)
	}
	waitTaskToolStatus(t, srv, secondTaskID, loom.TaskStatusFailed)
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("workflow dispatch calls = %d, want 2 fresh failed executions", gotCalls)
	}
}

func TestWorkflowRecipeStatusErrorClassifiesGateVsFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		status   string
		wantCode extypes.CLIErrorCode
	}{
		{name: "gated", status: "gated", wantCode: extypes.CLIErrorCodeUserInputError},
		{name: "failed", status: "failed", wantCode: extypes.CLIErrorCodeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := workflowRecipeStatusError(&workflow.WorkflowResult{Status: tc.status, Summary: "workflow summary"})
			var cliErr *extypes.CLIError
			if !errors.As(err, &cliErr) {
				t.Fatalf("error %T does not wrap CLIError: %v", err, err)
			}
			if cliErr.Code != tc.wantCode {
				t.Fatalf("CLIError code = %s, want %s", cliErr.Code, tc.wantCode)
			}
		})
	}
}

func TestRecipeReplayRejectsCompletedWorkflowWithFailedStatus(t *testing.T) {
	t.Parallel()

	if recipeReplayWorkflowResultSuccessful(map[string]any{"workflow_result_status": "failed"}) {
		t.Fatal("failed workflow_result_status must not be replay eligible")
	}
	if !recipeReplayWorkflowResultSuccessful(map[string]any{"workflow_result_status": "completed"}) {
		t.Fatal("completed workflow_result_status should remain replay eligible")
	}
	if !recipeReplayWorkflowResultSuccessful(map[string]any{}) {
		t.Fatal("non-workflow task without workflow_result_status should remain replay eligible")
	}
}

func TestHandleTaskRecipeReplayCacheHitReusesCompletedTask(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	args := map[string]any{
		"prompt":     "review HEAD for regressions",
		"recipe_id":  "code-review",
		"target":     "HEAD",
		"project_id": "proj-replay-hit",
	}
	first := callTaskTool(t, srv, args)
	if first.IsError {
		t.Fatalf("first call unexpected error: %s", taskToolResultText(t, first))
	}
	firstPayload := decodeTaskToolResult(t, first)
	firstTaskID, _ := firstPayload["task_id"].(string)
	if firstTaskID == "" {
		t.Fatalf("first task_id missing: %v", firstPayload)
	}
	waitTaskToolStatus(t, srv, firstTaskID, loom.TaskStatusCompleted)
	firstMetadata := taskToolPayloadMetadata(t, firstPayload)
	assertMetadataBool(t, firstMetadata, "recipe_replay_cache_hit", false)
	assertMetadataString(t, firstMetadata, "recipe_replay_key_version", "v1")
	firstFingerprint := metadataStringValue(t, firstMetadata, "recipe_replay_fingerprint")

	second := callTaskTool(t, srv, args)
	if second.IsError {
		t.Fatalf("second call unexpected error: %s", taskToolResultText(t, second))
	}
	secondPayload := decodeTaskToolResult(t, second)
	if secondPayload["task_id"] != firstTaskID {
		t.Fatalf("second task_id = %v, want replay source %s; payload=%v", secondPayload["task_id"], firstTaskID, secondPayload)
	}
	if got := reviewWorker.taskCount(); got != 1 {
		t.Fatalf("review task count = %d, want cache hit without duplicate submit", got)
	}
	secondMetadata := taskToolPayloadMetadata(t, secondPayload)
	assertMetadataBool(t, secondMetadata, "recipe_replay_cache_hit", true)
	assertMetadataString(t, secondMetadata, "recipe_replay_source_task_id", firstTaskID)
	assertMetadataString(t, secondMetadata, "recipe_replay_fingerprint", firstFingerprint)

	snapshot := readTaskSnapshotResource(t, srv, context.Background(), "aimux://tasks/"+firstTaskID)
	snapshotMetadata, ok := snapshot["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot metadata type = %T, want map; payload=%v", snapshot["metadata"], snapshot)
	}
	assertTaskResourceMetadataBool(t, snapshotMetadata, "recipe_replay_cache_hit", false)
	assertTaskResourceMetadata(t, snapshotMetadata, "recipe_replay_fingerprint", firstFingerprint)
}

func TestHandleTaskRecipeReplayChangedTargetMissesCache(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	first := callTaskTool(t, srv, map[string]any{
		"prompt":     "review HEAD for regressions",
		"recipe_id":  "code-review",
		"target":     "HEAD",
		"project_id": "proj-replay-target",
	})
	if first.IsError {
		t.Fatalf("first call unexpected error: %s", taskToolResultText(t, first))
	}
	firstPayload := decodeTaskToolResult(t, first)
	waitTaskToolStatus(t, srv, firstPayload["task_id"].(string), loom.TaskStatusCompleted)
	firstFingerprint := metadataStringValue(t, taskToolPayloadMetadata(t, firstPayload), "recipe_replay_fingerprint")

	second := callTaskTool(t, srv, map[string]any{
		"prompt":     "review HEAD for regressions",
		"recipe_id":  "code-review",
		"target":     "HEAD~1",
		"project_id": "proj-replay-target",
	})
	if second.IsError {
		t.Fatalf("second call unexpected error: %s", taskToolResultText(t, second))
	}
	secondPayload := decodeTaskToolResult(t, second)
	secondFingerprint := metadataStringValue(t, taskToolPayloadMetadata(t, secondPayload), "recipe_replay_fingerprint")
	if secondPayload["task_id"] == firstPayload["task_id"] {
		t.Fatalf("changed target reused task_id %v; want fresh task", secondPayload["task_id"])
	}
	if firstFingerprint == secondFingerprint {
		t.Fatalf("fingerprint did not change for changed target: %s", firstFingerprint)
	}
	if got := waitTaskCount(t, reviewWorker, 2); got != 2 {
		t.Fatalf("review task count = %d, want cache miss and fresh submit", got)
	}
	assertMetadataBool(t, taskToolPayloadMetadata(t, secondPayload), "recipe_replay_cache_hit", false)
}

func TestHandleTaskDirectReviewSkipsRecipeReplayMetadata(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	result := callTaskTool(t, srv, map[string]any{
		"prompt":     "review HEAD",
		"task_class": "review",
		"target":     "HEAD",
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}
	task := reviewWorker.onlyTask(t)
	if _, ok := task.Metadata["recipe_replay_fingerprint"]; ok {
		t.Fatalf("direct review grew recipe replay metadata: %v", task.Metadata)
	}
	if _, ok := task.Metadata["recipe_replay_cache_hit"]; ok {
		t.Fatalf("direct review grew recipe replay cache-hit metadata: %v", task.Metadata)
	}
}

func TestHandleTaskRecipeReplayDoesNotReuseFailedTask(t *testing.T) {
	t.Parallel()

	engine := newTaskToolEngine(t)
	reviewWorker := &flakyTaskWorker{workerType: review.WorkerTypeReview}
	engine.RegisterWorker(review.WorkerTypeReview, reviewWorker)
	registry := driver.NewRegistry(map[string]*config.CLIProfile{"codex": defaultRecipeProfile()})
	registry.SetAvailable("codex", true)
	srv := &Server{loom: engine, registry: registry}

	args := map[string]any{
		"prompt":     "review HEAD for regressions",
		"recipe_id":  "code-review",
		"target":     "HEAD",
		"project_id": "proj-replay-failed",
	}
	first := callTaskTool(t, srv, args)
	if first.IsError {
		t.Fatalf("first call unexpected immediate error: %s", taskToolResultText(t, first))
	}
	firstPayload := decodeTaskToolResult(t, first)
	waitTaskToolStatus(t, srv, firstPayload["task_id"].(string), loom.TaskStatusFailed)
	second := callTaskTool(t, srv, args)
	if second.IsError {
		t.Fatalf("second call unexpected error: %s", taskToolResultText(t, second))
	}
	if got := waitTaskCount(t, reviewWorker, 2); got != 2 {
		t.Fatalf("review task count = %d, want failed task ignored and fresh submit", got)
	}
	assertMetadataBool(t, taskToolPayloadMetadata(t, decodeTaskToolResult(t, second)), "recipe_replay_cache_hit", false)
}

func TestHandleTaskRecipePolicyMismatchCannotReplayCompletedTask(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServer(t)
	args := map[string]any{
		"prompt":     "review HEAD for regressions",
		"recipe_id":  "code-review",
		"target":     "HEAD",
		"project_id": "proj-replay-policy",
	}
	first := callTaskTool(t, srv, args)
	if first.IsError {
		t.Fatalf("first call unexpected error: %s", taskToolResultText(t, first))
	}
	firstPayload := decodeTaskToolResult(t, first)
	waitTaskToolStatus(t, srv, firstPayload["task_id"].(string), loom.TaskStatusCompleted)
	waitTaskCount(t, reviewWorker, 1)
	limited := driver.NewRegistry(map[string]*config.CLIProfile{"codex": limitedRecipeProfile()})
	limited.SetAvailable("codex", true)
	srv.registry = limited

	second := callTaskTool(t, srv, args)
	if !second.IsError {
		t.Fatalf("expected policy mismatch before replay, got %s", taskToolResultText(t, second))
	}
	payload := decodeTaskToolError(t, second)
	if payload.Code != extypes.CLIErrorCodeCapabilityMismatch.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeCapabilityMismatch)
	}
	if got := reviewWorker.taskCount(); got != 1 {
		t.Fatalf("review task count = %d, want no duplicate submit after policy mismatch", got)
	}
}

func TestHandleTaskUnsupportedRecipeFailsBeforeSubmit(t *testing.T) {
	t.Parallel()

	srv, codeWorker, reviewWorker := newTaskToolServer(t)
	result := callTaskTool(t, srv, map[string]any{
		"prompt":    "review HEAD",
		"recipe_id": "secaudit",
		"target":    "HEAD",
	})
	if !result.IsError {
		t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeUserInputError.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeUserInputError)
	}
	if !strings.Contains(payload.Message, "unsupported recipe_id") {
		t.Fatalf("message = %q, want unsupported recipe_id", payload.Message)
	}
	if !stringSlicesEqual(payload.AvailableRecipes, []string{"code-review", "second-opinion", "security-audit", "debug-investigation"}) {
		t.Fatalf("available_recipes = %#v, want deterministic public recipe IDs", payload.AvailableRecipes)
	}
	if got := codeWorker.taskCount(); got != 0 {
		t.Fatalf("code task count = %d, want 0", got)
	}
	if got := reviewWorker.taskCount(); got != 0 {
		t.Fatalf("review task count = %d, want 0", got)
	}
}

func TestHandleTaskRecipeCapabilityMismatchFailsBeforeSubmit(t *testing.T) {
	t.Parallel()

	srv, codeWorker, reviewWorker := newTaskToolServerWithProfile(t, limitedRecipeProfile())
	result := callTaskTool(t, srv, map[string]any{
		"prompt":    "review HEAD",
		"recipe_id": "code-review",
		"target":    "HEAD",
	})
	if !result.IsError {
		t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeCapabilityMismatch.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeCapabilityMismatch)
	}
	if payload.Retryable {
		t.Fatal("retryable = true, want false")
	}
	if payload.RecipeID != "code-review" {
		t.Fatalf("recipe_id = %q, want code-review", payload.RecipeID)
	}
	if payload.SelectedCLI != "codex" {
		t.Fatalf("selected_cli = %q, want codex", payload.SelectedCLI)
	}
	if !stringSliceContains(payload.RequestedPolicy, "read_only") {
		t.Fatalf("requested_policy = %#v, want read_only", payload.RequestedPolicy)
	}
	if !stringSliceContains(payload.MissingCapabilities, "read_only") {
		t.Fatalf("missing_capabilities = %#v, want read_only", payload.MissingCapabilities)
	}
	if !stringSliceContains(payload.SupportedCapabilities, "target_required") {
		t.Fatalf("supported_capabilities = %#v, want target_required evidence", payload.SupportedCapabilities)
	}
	if got := codeWorker.taskCount(); got != 0 {
		t.Fatalf("code task count = %d, want 0", got)
	}
	if got := reviewWorker.taskCount(); got != 0 {
		t.Fatalf("review task count = %d, want 0", got)
	}
}

func TestTaskDispatchFailFastWhenCLIUnavailable(t *testing.T) {
	t.Parallel()

	srv, _, _ := newTaskToolServer(t)
	srv.registry.SetAvailable("codex", false)
	_, err := srv.taskDispatch(context.Background(), "codex", picker.TaskSpec{Prompt: "Implement timeout propagation."})
	if err == nil {
		t.Fatal("taskDispatch error = nil, want CapabilityMismatch")
	}
	var cliErr *extypes.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("taskDispatch error = %T, want *CLIError", err)
	}
	if cliErr.Code != extypes.CLIErrorCodeCapabilityMismatch {
		t.Fatalf("CLIError code = %s, want %s", cliErr.Code, extypes.CLIErrorCodeCapabilityMismatch)
	}
	if !strings.Contains(cliErr.Message, "runtime-unavailable") {
		t.Fatalf("message = %q, want runtime-unavailable clue", cliErr.Message)
	}
}

func TestHandleTaskDirectReviewSkipsRecipeCapabilityPreflight(t *testing.T) {
	t.Parallel()

	srv, _, reviewWorker := newTaskToolServerWithProfile(t, limitedRecipeProfile())
	result := callTaskTool(t, srv, map[string]any{
		"prompt":     "review HEAD",
		"task_class": "review",
		"target":     "HEAD",
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}
	if got := waitTaskCount(t, reviewWorker, 1); got != 1 {
		t.Fatalf("review task count = %d, want 1", got)
	}
}

func TestHandleTaskCLIOverrideDoesNotBypassRouter(t *testing.T) {
	t.Parallel()

	srv, codeWorker, _ := newTaskToolServer(t)
	result := callTaskTool(t, srv, map[string]any{
		"prompt":     "Implement CLI override routing.",
		"task_class": "code",
		"cli":        "codex",
	})
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	task := codeWorker.onlyTask(t)
	if task.CLI != "" {
		t.Fatalf("task CLI = %q, want empty root task CLI", task.CLI)
	}
	assertMetadataString(t, task.Metadata, "driver_cli_override", "codex")
	if task.WorkerType != code.WorkerTypeCode {
		t.Fatalf("worker_type = %s, want code; override must not direct-dispatch pipe executor", task.WorkerType)
	}
}

func TestHandleTaskRecordsWorktreeSessionKey(t *testing.T) {
	t.Parallel()

	srv, codeWorker, _ := newTaskToolServer(t)
	ctx := contextWithMuxSessionID(context.Background(), "mux-session-a")
	result, err := srv.handleTask(ctx, makeRequest("task", map[string]any{
		"prompt":     "Implement session-scoped worktree cancellation.",
		"task_class": "code",
	}))
	if err != nil {
		t.Fatalf("handleTask returned Go error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	task := codeWorker.onlyTask(t)
	assertMetadataString(t, task.Metadata, worktreeSessionMetadataKey, "mux:mux-session-a")
}

func TestHandleTaskRejectsUnregisteredClassBeforeSubmit(t *testing.T) {
	t.Parallel()

	srv, codeWorker, reviewWorker := newTaskToolServer(t)
	result := callTaskTool(t, srv, map[string]any{
		"prompt":     "research official docs",
		"task_class": "research",
	})
	if !result.IsError {
		t.Fatalf("expected error result, got %s", taskToolResultText(t, result))
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeUserInputError.String() {
		t.Fatalf("code = %s, want %s", payload.Code, extypes.CLIErrorCodeUserInputError)
	}
	if got := codeWorker.taskCount(); got != 0 {
		t.Fatalf("code task count = %d, want 0", got)
	}
	if got := reviewWorker.taskCount(); got != 0 {
		t.Fatalf("review task count = %d, want 0", got)
	}
}

func TestHandleDeepresearchReturnsAcceptedLoomJob(t *testing.T) {
	t.Parallel()

	engine := newTaskToolEngine(t)
	worker := &recordingTaskWorker{workerType: deepResearchWorkerType}
	engine.RegisterWorker(deepResearchWorkerType, worker)
	srv := &Server{loom: engine}

	result, err := srv.handleDeepresearch(context.Background(), makeRequest("deepresearch", map[string]any{
		"topic":           "P26 async contract",
		"output_format":   "summary",
		"model":           "gemini-test",
		"force":           true,
		"timeout_seconds": float64(7),
		"project_id":      "proj-deepresearch",
	}))
	if err != nil {
		t.Fatalf("handleDeepresearch returned Go error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", taskToolResultText(t, result))
	}

	payload := decodeTaskToolResult(t, result)
	taskID, _ := payload["task_id"].(string)
	if taskID == "" || payload["job_id"] != taskID {
		t.Fatalf("task/job id missing or mismatched: %v", payload)
	}
	if payload["task_class"] != "deepresearch" {
		t.Fatalf("task_class = %v, want deepresearch; payload=%v", payload["task_class"], payload)
	}
	if payload["content"] != nil {
		t.Fatalf("content = %v, want omitted from accepted response", payload["content"])
	}
	if payload["status"] != string(loom.TaskStatusDispatched) {
		t.Fatalf("status = %v, want dispatched; payload=%v", payload["status"], payload)
	}
	metadata := taskToolPayloadMetadata(t, payload)
	assertMetadataString(t, metadata, "async_contract", "loom")
	assertMetadataBool(t, metadata, "accepted", true)

	task := worker.onlyTask(t)
	if task.WorkerType != deepResearchWorkerType {
		t.Fatalf("worker_type = %s, want %s", task.WorkerType, deepResearchWorkerType)
	}
	if task.ProjectID != "proj-deepresearch" {
		t.Fatalf("project_id = %q, want proj-deepresearch", task.ProjectID)
	}
	if task.Prompt != "P26 async contract" {
		t.Fatalf("prompt = %q, want topic", task.Prompt)
	}
	if task.Timeout != 7 {
		t.Fatalf("timeout = %d, want 7", task.Timeout)
	}
	assertMetadataString(t, task.Metadata, "topic", "P26 async contract")
	assertMetadataString(t, task.Metadata, "output_format", "summary")
	assertMetadataString(t, task.Metadata, "model", "gemini-test")
	assertMetadataBool(t, task.Metadata, "force", true)
	if _, ok := task.Metadata["accepted"]; ok {
		t.Fatalf("task metadata contains response-only accepted field: %v", task.Metadata)
	}
}

func TestProfileTaskWorkerProgressSinkForwardsLinesToLoom(t *testing.T) {
	t.Parallel()

	srv, _, _ := newTaskToolServer(t)
	worker := profileTaskWorker{
		server:     srv,
		workerType: code.WorkerTypeCodeDriver,
		taskClass:  "code",
		defaultCLI: "codex",
	}

	now := time.Now().UTC()
	taskID := "task-progress-sink"
	if err := srv.loom.Import(&loom.Task{
		ID:         taskID,
		Status:     loom.TaskStatusRunning,
		WorkerType: code.WorkerTypeCodeDriver,
		ProjectID:  "progress-sink-proj",
		Prompt:     "drive",
		CLI:        "codex",
		CreatedAt:  now,
	}); err != nil {
		t.Fatalf("import running task: %v", err)
	}

	sink := worker.progressSink(taskID, "")
	if sink == nil {
		t.Fatal("progressSink returned nil for a live engine + task")
	}

	sink("first line")
	sink("   ") // whitespace-only: must be skipped (no signal)
	sink("second line")

	task, err := srv.loom.Get(taskID)
	if err != nil {
		t.Fatalf("get task after progress: %v", err)
	}
	if task.LastOutputLine != "second line" {
		t.Fatalf("LastOutputLine = %q, want %q", task.LastOutputLine, "second line")
	}
	if task.ProgressLines != 2 {
		t.Fatalf("ProgressLines = %d, want 2 (whitespace line skipped)", task.ProgressLines)
	}
	if task.ProgressUpdatedAt == nil {
		t.Fatal("ProgressUpdatedAt is nil after progress — stall detector would still measure from dispatch time")
	}
	runtimePage, err := srv.loom.ListArtifacts(taskID, loom.TaskArtifactListOptions{Kinds: []loom.TaskArtifactKind{loom.TaskArtifactKindRuntime}})
	if err != nil {
		t.Fatalf("ListArtifacts runtime: %v", err)
	}
	if len(runtimePage.Items) != 2 {
		t.Fatalf("runtime artifacts = %#v; want two raw stdout progress lines", runtimePage.Items)
	}
	for i, item := range runtimePage.Items {
		if item.EventType != "raw" || item.Channel != "stdout" {
			t.Fatalf("runtime artifact %d = %#v; want raw/stdout truthful fallback", i, item)
		}
	}
}

func TestProfileTaskWorkerProgressSinkNilWhenNoSignalPossible(t *testing.T) {
	t.Parallel()

	srv, _, _ := newTaskToolServer(t)
	live := profileTaskWorker{server: srv, workerType: code.WorkerTypeCodeDriver, defaultCLI: "codex"}
	if live.progressSink("", "") != nil {
		t.Fatal("progressSink(empty taskID) = non-nil, want nil")
	}

	noEngine := profileTaskWorker{server: &Server{}, workerType: code.WorkerTypeCodeDriver}
	if noEngine.progressSink("task-x", "") != nil {
		t.Fatal("progressSink with nil loom = non-nil, want nil")
	}

	noServer := profileTaskWorker{workerType: code.WorkerTypeCodeDriver}
	if noServer.progressSink("task-x", "") != nil {
		t.Fatal("progressSink with nil server = non-nil, want nil")
	}
}

func TestProfileTaskWorkerRuntimeEventsFromStructuredEmulator(t *testing.T) {
	dir := t.TempDir()
	bin := fakeExecutableWithContents(t, dir, "codex-runtime-jsonl",
		"#!/bin/sh\nprintf '%s\n' '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"hello from emulator\"}}'\nprintf '%s\n' '{\"type\":\"unknown.future\",\"payload\":\"kept\"}'\n",
		"@echo off\r\necho {\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"hello from emulator\"}}\r\necho {\"type\":\"unknown.future\",\"payload\":\"kept\"}\r\nexit /b 0\r\n",
	)
	profile := &config.CLIProfile{
		Name:           "codex",
		Binary:         bin,
		ResolvedPath:   bin,
		OutputFormat:   "jsonl",
		PromptFlagType: "positional",
		TimeoutSeconds: 5,
		Features:       types.CLIFeatures{JSONL: true},
	}
	registry := driver.NewRegistry(map[string]*config.CLIProfile{"codex": profile})
	registry.SetAvailable("codex", true)
	engine := newTaskToolEngine(t)
	srv := &Server{loom: engine, registry: registry}
	engine.RegisterWorker(code.WorkerTypeCodeDriver, profileTaskWorker{
		server:     srv,
		workerType: code.WorkerTypeCodeDriver,
		taskClass:  "code",
		defaultCLI: "codex",
	})

	taskID, err := engine.Submit(context.Background(), loom.TaskRequest{
		WorkerType: code.WorkerTypeCodeDriver,
		ProjectID:  "structured-runtime-emulator",
		Prompt:     "emit structured runtime frames",
		CLI:        "codex",
		Timeout:    5,
	})
	if err != nil {
		t.Fatalf("Submit structured runtime emulator: %v", err)
	}
	waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusCompleted)

	page, err := engine.ListArtifacts(taskID, loom.TaskArtifactListOptions{Kinds: []loom.TaskArtifactKind{loom.TaskArtifactKindRuntime}})
	if err != nil {
		t.Fatalf("ListArtifacts runtime: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("runtime artifacts = %#v; want text_delta plus raw fallback", page.Items)
	}
	if page.Items[0].EventType != "text_delta" || page.Items[0].Channel != "stdout" || page.Items[0].Summary != "hello from emulator" {
		t.Fatalf("first runtime event = %#v; want text_delta/stdout emulator text", page.Items[0])
	}
	if page.Items[1].EventType != "raw" || page.Items[1].Channel != "stdout" {
		t.Fatalf("second runtime event = %#v; want raw/stdout fallback for unknown frame", page.Items[1])
	}
}

func TestBuildFallbackPickerWarmsHealthCachesAtConstruction(t *testing.T) {
	cliNames := []string{"codex", "claude", "gemini"}
	profiles := make(map[string]*config.CLIProfile, len(cliNames))
	for _, cli := range cliNames {
		bin := fakeExecutable(t, t.TempDir(), cli)
		profiles[cli] = &config.CLIProfile{
			Name:         cli,
			Binary:       bin,
			ResolvedPath: bin,
			Capabilities: []string{"code", "task", "review"},
		}
	}

	registry := driver.NewRegistry(profiles)
	for _, cli := range cliNames {
		registry.SetAvailable(cli, true)
	}
	pickerCfg := picker.DefaultPickerConfig()
	srv := &Server{
		cfg: &config.Config{
			Executor: config.ExecutorConfig{Picker: pickerCfg},
		},
		registry: registry,
	}

	fp := buildFallbackPicker(srv)
	if fp == nil {
		t.Fatal("buildFallbackPicker returned nil")
	}

	for _, profile := range profiles {
		if err := os.Remove(profile.ResolvedPath); err != nil {
			t.Fatalf("remove fake binary %s: %v", profile.ResolvedPath, err)
		}
	}

	if _, _, err := fp.PickPair(context.Background(), classifier.TaskClassCode); err != nil {
		t.Fatalf("PickPair after binary removal = %v, want warmed picker health cache", err)
	}

	result, err := fp.RunPrimary(
		context.Background(),
		"codex",
		picker.TaskSpec{TaskClass: classifier.TaskClassCode},
		fallback.RunOptions{},
		func(_ context.Context, cli string, _ picker.TaskSpec) (string, error) {
			if cli == "codex" {
				return "", extypes.NewRateLimit("codex temporarily rate limited", nil)
			}
			return "fallback via " + cli, nil
		},
	)
	if err != nil {
		t.Fatalf("RunPrimary after binary removal = %v, want warmed fallback health cache", err)
	}
	if result.SelectedCLI == "codex" || result.Content == "" {
		t.Fatalf("RunPrimary result = %#v, want successful fallback attempt", result)
	}
}

func fakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	filename := name
	contents := []byte("#!/bin/sh\nexit 0\n")
	if runtime.GOOS == "windows" {
		filename += ".cmd"
		contents = []byte("@echo off\r\nexit /b 0\r\n")
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, contents, 0o755); err != nil {
		t.Fatalf("write fake binary %s: %v", path, err)
	}
	return path
}

func fakeExecutableWithContents(t *testing.T, dir, name, unixContents, windowsContents string) string {
	t.Helper()
	filename := name
	contents := []byte(unixContents)
	if runtime.GOOS == "windows" {
		filename += ".cmd"
		contents = []byte(windowsContents)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, contents, 0o755); err != nil {
		t.Fatalf("write fake binary %s: %v", path, err)
	}
	return path
}

func newTaskToolServer(t *testing.T) (*Server, *recordingTaskWorker, *recordingTaskWorker) {
	return newTaskToolServerWithProfile(t, defaultRecipeProfile())
}

func newTaskToolServerWithProfile(t *testing.T, profile *config.CLIProfile) (*Server, *recordingTaskWorker, *recordingTaskWorker) {
	return newTaskToolServerWithWorkflowHooks(t, profile, defaultWorkflowRecipeDispatch, defaultWorkflowRecipePattern)
}

func newTaskToolServerWithWorkflowHooks(t *testing.T, profile *config.CLIProfile, dispatch workflowRecipeDispatchFunc, patternFn func(string, map[string]any) (map[string]any, error)) (*Server, *recordingTaskWorker, *recordingTaskWorker) {
	t.Helper()
	engine := newTaskToolEngine(t)
	registry := driver.NewRegistry(map[string]*config.CLIProfile{"codex": profile})
	registry.SetAvailable("codex", true)
	srv := &Server{loom: engine, registry: registry}
	codeWorker := &recordingTaskWorker{workerType: code.WorkerTypeCode}
	reviewWorker := &recordingTaskWorker{workerType: review.WorkerTypeReview}
	engine.RegisterWorker(code.WorkerTypeCode, codeWorker)
	engine.RegisterWorker(review.WorkerTypeReview, reviewWorker)
	engine.RegisterWorker(workflowRecipeWorkerType, workflowRecipeWorker{
		server:     srv,
		defaultCLI: "codex",
		dispatch:   dispatch,
		patternFn:  patternFn,
	})
	return srv, codeWorker, reviewWorker
}

func newSpecTaskToolServer(t *testing.T) (*Server, *recordingTaskWorker) {
	t.Helper()
	srv, _, _ := newTaskToolServer(t)
	specWorker := &recordingTaskWorker{workerType: specWorkerType}
	srv.loom.RegisterWorker(specWorkerType, specWorker)
	return srv, specWorker
}

func defaultWorkflowRecipeDispatch(_ context.Context, cli string, spec picker.TaskSpec, _ map[string]any) (string, string, error) {
	if strings.TrimSpace(spec.Prompt) == "" {
		return "", cli, errors.New("empty workflow step prompt")
	}
	content := "workflow step complete"
	if strings.Contains(spec.Prompt, "Gather evidence for and against each debugging hypothesis") {
		content = "Evidence gathered from workflow execution: root cause is deterministic debug evidence from the workflow step."
	}
	return fmt.Sprintf("{\"type\":\"agent_message\",\"content\":%q}\n", content), cli, nil
}

func defaultWorkflowRecipePattern(name string, input map[string]any) (map[string]any, error) {
	if name != "decision_framework" {
		return map[string]any{"pattern": name, "summary": fmt.Sprint(input)}, nil
	}
	if verdict := workflowRootCauseVerdictFromEvidence(input["workflow_root_cause_evidence"]); verdict != "" {
		return map[string]any{"pattern": name, "summary": verdict}, nil
	}
	return map[string]any{"pattern": name, "summary": "Root cause not identified: structured workflow evidence is required."}, nil
}

func defaultRecipeProfile() *config.CLIProfile {
	return &config.CLIProfile{
		Name:         "codex",
		Binary:       "codex",
		OutputFormat: "jsonl",
		Features: types.CLIFeatures{
			ReadOnly: true,
			JSONL:    true,
		},
		ReadOnlyFlags: []string{"--sandbox", "read-only"},
		Capabilities:  []string{"coding", "review"},
	}
}

func limitedRecipeProfile() *config.CLIProfile {
	return &config.CLIProfile{
		Name:         "codex",
		Binary:       "codex",
		OutputFormat: "text",
		Capabilities: []string{"review"},
	}
}

func newTaskToolEngine(t *testing.T) *loom.LoomEngine {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:task_tool_%d?cache=shared&mode=memory", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	engine, err := loom.NewEngine(db, "task-tool-test")
	if err != nil {
		t.Fatalf("loom.NewEngine: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = engine.Close(ctx)
	})
	return engine
}

func callTaskTool(t *testing.T, srv *Server, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := srv.handleTask(context.Background(), makeRequest("task", args))
	if err != nil {
		t.Fatalf("handleTask returned Go error: %v", err)
	}
	return result
}

func callReviewTool(t *testing.T, srv *Server, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := srv.handleReview(context.Background(), makeRequest("review", args))
	if err != nil {
		t.Fatalf("handleReview returned Go error: %v", err)
	}
	return result
}

func callSpecTool(t *testing.T, srv *Server, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := srv.handleSpec(context.Background(), makeRequest("spec", args))
	if err != nil {
		t.Fatalf("handleSpec returned Go error: %v", err)
	}
	return result
}

type recordingTaskWorker struct {
	mu         sync.Mutex
	workerType loom.WorkerType
	tasks      []*loom.Task
}

func (w *recordingTaskWorker) Type() loom.WorkerType {
	return w.workerType
}

func (w *recordingTaskWorker) Execute(_ context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	w.mu.Lock()
	cp := *task
	cp.Metadata = cloneTaskMetadata(task.Metadata)
	w.tasks = append(w.tasks, &cp)
	w.mu.Unlock()

	metadata := cloneTaskMetadata(task.Metadata)
	metadata["rounds"] = 1
	metadata["confidence_score"] = 0.91
	return &loom.WorkerResult{
		Content:  "handled " + string(w.workerType),
		Metadata: metadata,
	}, nil
}

func (w *recordingTaskWorker) onlyTask(t *testing.T) *loom.Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		w.mu.Lock()
		if len(w.tasks) == 1 {
			cp := *w.tasks[0]
			cp.Metadata = cloneTaskMetadata(w.tasks[0].Metadata)
			w.mu.Unlock()
			return &cp
		}
		count := len(w.tasks)
		w.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("task count = %d, want 1", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (w *recordingTaskWorker) taskCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.tasks)
}

type taskCounter interface {
	taskCount() int
}

func waitTaskCount(t *testing.T, counter taskCounter, want int) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := counter.taskCount()
		if got == want {
			return got
		}
		if time.Now().After(deadline) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type flakyTaskWorker struct {
	mu         sync.Mutex
	workerType loom.WorkerType
	calls      int
}

func (w *flakyTaskWorker) Type() loom.WorkerType {
	return w.workerType
}

func (w *flakyTaskWorker) Execute(_ context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	w.mu.Lock()
	w.calls++
	call := w.calls
	w.mu.Unlock()
	if call == 1 {
		return nil, errors.New("planned worker failure")
	}
	metadata := cloneTaskMetadata(task.Metadata)
	metadata["rounds"] = 1
	metadata["confidence_score"] = 0.91
	return &loom.WorkerResult{
		Content:  "handled " + string(w.workerType),
		Metadata: metadata,
	}, nil
}

func (w *flakyTaskWorker) taskCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

func taskToolPayloadMetadata(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	metadata, ok := payload["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata type = %T, want map; payload=%v", payload["metadata"], payload)
	}
	return metadata
}

func waitTaskToolStatus(t *testing.T, srv *Server, taskID string, want loom.TaskStatus) *loom.Task {
	t.Helper()
	if taskID == "" {
		t.Fatal("task_id is empty")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		task, err := srv.loom.Get(taskID)
		if err == nil && task.Status == want {
			return task
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("task %s did not reach %s: last error %v", taskID, want, err)
			}
			t.Fatalf("task %s status = %s, want %s", taskID, task.Status, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func metadataStringValue(t *testing.T, metadata map[string]any, key string) string {
	t.Helper()
	value, ok := metadata[key]
	if !ok {
		t.Fatalf("metadata[%q] missing", key)
	}
	got, ok := value.(string)
	if !ok {
		t.Fatalf("metadata[%q] = %#v, want string", key, value)
	}
	if got == "" {
		t.Fatalf("metadata[%q] empty", key)
	}
	return got
}

func assertTaskResourceMetadataBool(t *testing.T, metadata map[string]any, key string, want bool) {
	t.Helper()
	got, ok := metadata[key].(bool)
	if !ok || got != want {
		t.Fatalf("metadata[%s] = %#v, want %v", key, metadata[key], want)
	}
}

type taskToolErrorPayload struct {
	Code                  string                 `json:"code"`
	Message               string                 `json:"message"`
	Retryable             bool                   `json:"retryable"`
	Candidates            []classifier.Candidate `json:"candidates,omitempty"`
	AvailableRecipes      []string               `json:"available_recipes,omitempty"`
	RecipeID              string                 `json:"recipe_id,omitempty"`
	SelectedCLI           string                 `json:"selected_cli,omitempty"`
	RequestedPolicy       []string               `json:"requested_policy,omitempty"`
	MissingCapabilities   []string               `json:"missing_capabilities,omitempty"`
	SupportedCapabilities []string               `json:"supported_capabilities,omitempty"`
}

func decodeTaskToolResult(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(taskToolResultText(t, result)), &payload); err != nil {
		t.Fatalf("decode task result: %v", err)
	}
	return payload
}

func decodeTaskToolError(t *testing.T, result *mcp.CallToolResult) taskToolErrorPayload {
	t.Helper()
	var payload taskToolErrorPayload
	if err := json.Unmarshal([]byte(taskToolResultText(t, result)), &payload); err != nil {
		t.Fatalf("decode task error: %v", err)
	}
	return payload
}

func taskToolResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) == 0 {
		t.Fatal("empty task tool result")
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("result content = %T, want TextContent", result.Content[0])
	}
	return text.Text
}

func TestTaskToolErrorPayloadShape(t *testing.T) {
	t.Parallel()

	err := extypes.NewUserInputError("bad input", errors.New("sentinel"))
	result, goErr := taskToolError(TaskResult{}, err)
	if goErr != nil {
		t.Fatalf("taskToolError Go error = %v", goErr)
	}
	if !result.IsError {
		t.Fatal("taskToolError IsError = false")
	}
	payload := decodeTaskToolError(t, result)
	if payload.Code != extypes.CLIErrorCodeUserInputError.String() {
		t.Fatalf("code = %s, want UserInputError", payload.Code)
	}
	if payload.Retryable {
		t.Fatal("retryable = true, want false")
	}
}

func TestBuildTaskArgsDropsBinaryTokenFromCommandBase(t *testing.T) {
	t.Parallel()

	profile := &config.CLIProfile{
		Binary:         "testcli",
		PromptFlagType: "positional",
		Command: config.CommandConfig{
			Base: "testcli codex --json --full-auto",
		},
	}

	got := buildTaskArgs(profile, pickerTaskSpec("hello"))
	want := []string{"codex", "--json", "--full-auto", "hello"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestBuildTaskArgsKeepsSubcommandAfterRealBinary(t *testing.T) {
	t.Parallel()

	profile := &config.CLIProfile{
		Binary:         "codex",
		PromptFlagType: "positional",
		Command: config.CommandConfig{
			Base: "codex exec",
		},
	}

	got := buildTaskArgs(profile, pickerTaskSpec("hello"))
	want := []string{"exec", "hello"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestBuildTaskArgsPreservesQuotedCommandBaseFields(t *testing.T) {
	t.Parallel()

	profile := &config.CLIProfile{
		Binary:         "codex",
		PromptFlagType: "positional",
		Command: config.CommandConfig{
			Base: `codex exec --profile "review mode" --label 'pair navigator'`,
		},
	}

	got := buildTaskArgs(profile, pickerTaskSpec("hello"))
	want := []string{"exec", "--profile", "review mode", "--label", "pair navigator", "hello"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestBuildTaskArgsUsesCommandArgsTemplate(t *testing.T) {
	t.Parallel()

	prompt := `say "hello" from C:\Tools`
	profile := &config.CLIProfile{
		Binary:         "gemini",
		PromptFlag:     "-p",
		PromptFlagType: "short",
		Command: config.CommandConfig{
			Base:         "gemini",
			ArgsTemplate: `{{if .Model}}--model {{.Model}}{{end}} --output-format json -p "{{.Prompt}}"`,
		},
		Features:      types.CLIFeatures{Headless: true, JSON: true},
		HeadlessFlags: []string{"-y"},
		DefaultModel:  "gemini-2.5-pro",
		ModelFlag:     "--model",
	}

	got := buildTaskArgs(profile, pickerTaskSpec(prompt))
	want := []string{"-y", "--model", "gemini-2.5-pro", "--output-format", "json", "-p", prompt}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestBuildTaskArgsPassesResumeStateToCommandArgsTemplate(t *testing.T) {
	t.Parallel()

	profile := &config.CLIProfile{
		Binary:         "codex",
		PromptFlagType: "positional",
		Command: config.CommandConfig{
			Base:         "codex exec",
			ArgsTemplate: `{{if .SessionResume}}resume {{.SessionID}}{{end}} "{{.Prompt}}"`,
		},
	}

	got := buildTaskArgs(profile, picker.TaskSpec{
		Prompt:        "continue work",
		SessionID:     "thread-123",
		SessionResume: true,
	})
	want := []string{"exec", "resume", "thread-123", "continue work"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestSessionResumeFromTaskMetadataUsesThreadID(t *testing.T) {
	t.Parallel()

	metadata := map[string]any{
		code.MetadataThreadID: "thread-123",
		"resume_id":           "root-task-1",
	}
	if got := sessionIDFromTaskMetadata(metadata); got != "thread-123" {
		t.Fatalf("session id = %q, want thread-123", got)
	}
	if !sessionResumeFromTaskMetadata(metadata) {
		t.Fatal("session resume = false, want true")
	}
}

func TestSplitCommandLinePreservesWindowsBackslashesInQuotes(t *testing.T) {
	got, err := splitCommandLine(`codex exec "C:\Program Files\cli.exe" --flag`)
	if err != nil {
		t.Fatalf("splitCommandLine returned error: %v", err)
	}
	want := []string{"codex", "exec", `C:\Program Files\cli.exe`, "--flag"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("fields = %#v, want %#v", got, want)
	}
}

func TestSplitCommandLineEscapesQuoteAndBackslashInDoubleQuotes(t *testing.T) {
	got, err := splitCommandLine(`cmd "say \"hi\" from C:\\Tools"`)
	if err != nil {
		t.Fatalf("splitCommandLine returned error: %v", err)
	}
	want := []string{"cmd", `say "hi" from C:\Tools`}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("fields = %#v, want %#v", got, want)
	}
}

func TestBuildTaskArgsUsesProfileExecutionFlags(t *testing.T) {
	t.Parallel()

	profile := &config.CLIProfile{
		Binary:         "codex",
		PromptFlagType: "positional",
		Command: config.CommandConfig{
			Base: "codex exec",
		},
		Features:      types.CLIFeatures{Headless: true},
		HeadlessFlags: []string{"--full-auto", "--json"},
		ReadOnlyFlags: []string{"--sandbox", "read-only"},
		DefaultModel:  "gpt-5.5",
		ModelFlag:     "-m",
		Reasoning: &config.ReasoningConfig{
			Flag:              "-c",
			FlagValueTemplate: "model_reasoning_effort={{.Level}}",
		},
	}

	got := buildTaskArgs(profile, picker.TaskSpec{
		Prompt:  "hello",
		Effort:  "xhigh",
		Sandbox: "read-only",
	})
	want := []string{
		"exec",
		"--full-auto", "--json",
		"--sandbox", "read-only",
		"-m", "gpt-5.5",
		"-c", "model_reasoning_effort=xhigh",
		"hello",
	}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func pickerTaskSpec(prompt string) picker.TaskSpec {
	return picker.TaskSpec{Prompt: prompt}
}

// TestBuildTaskArgsInjectsMCPSuppressionTemplateBranch covers the codex path
// (args_template branch): the #359 MCP suppression flags must land in the
// spawned argv before the template output, so the child codex never loads MCP.
func TestBuildTaskArgsInjectsMCPSuppressionTemplateBranch(t *testing.T) {
	t.Parallel()

	profile := &config.CLIProfile{
		Binary:              "codex",
		PromptFlagType:      "stdin",
		StdinSentinel:       "-",
		MCPSuppressionFlags: []string{"-c", "mcp_servers={}"},
		Command: config.CommandConfig{
			Base:         "codex exec",
			ArgsTemplate: "--json -",
		},
	}

	got := buildTaskArgs(profile, pickerTaskSpec("hello"))
	want := []string{"exec", "-c", "mcp_servers={}", "--json", "-"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

// TestBuildTaskArgsInjectsMCPSuppressionDefaultBranch covers the claude path
// (default flag/positional branch, no args_template): suppression flags land
// after the command base, before headless/prompt.
func TestBuildTaskArgsInjectsMCPSuppressionDefaultBranch(t *testing.T) {
	t.Parallel()

	profile := &config.CLIProfile{
		Binary:              "claude",
		PromptFlagType:      "positional",
		MCPSuppressionFlags: []string{"--strict-mcp-config", "--mcp-config", "{}"},
		Features:            types.CLIFeatures{Headless: true},
		HeadlessFlags:       []string{"-p", "--output-format", "json"},
		Command:             config.CommandConfig{Base: "claude"},
	}

	got := buildTaskArgs(profile, pickerTaskSpec("review this"))
	want := []string{"--strict-mcp-config", "--mcp-config", "{}", "-p", "--output-format", "json", "review this"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestTaskDispatchCWDUsesTaskCWDBeforeEnvFallback(t *testing.T) {
	t.Setenv("AIMUX_CWD", "env-cwd")

	if got := taskDispatchCWD(" task-cwd "); got != "task-cwd" {
		t.Fatalf("taskDispatchCWD(task) = %q, want task-cwd", got)
	}
	if got := taskDispatchCWD(""); got != "env-cwd" {
		t.Fatalf("taskDispatchCWD(empty) = %q, want env-cwd", got)
	}
}

func TestTaskDispatchSpawnArgsCarriesTaskEnv(t *testing.T) {
	profile := &config.CLIProfile{
		TimeoutSeconds:    7,
		CompletionPattern: "done",
		Command:           config.CommandConfig{Base: "codex exec"},
		PromptFlagType:    "positional",
	}
	env := map[string]string{"OPENAI_API_KEY": "session-key"}
	args := taskDispatchSpawnArgs("codex", "codex.exe", profile, picker.TaskSpec{
		Prompt: "hello",
		CWD:    "project-cwd",
		Env:    env,
	})

	if args.Env["OPENAI_API_KEY"] != "session-key" {
		t.Fatalf("Env = %#v, want session key", args.Env)
	}
	env["OPENAI_API_KEY"] = "mutated"
	if args.Env["OPENAI_API_KEY"] != "session-key" {
		t.Fatalf("Env was not cloned: %#v", args.Env)
	}
}

func TestTaskDispatchSpawnArgsUsesRequestTimeoutOverride(t *testing.T) {
	profile := &config.CLIProfile{
		TimeoutSeconds:    7,
		CompletionPattern: "done",
		Command:           config.CommandConfig{Base: "codex exec"},
		PromptFlagType:    "positional",
	}
	args := taskDispatchSpawnArgs("codex", "codex.exe", profile, picker.TaskSpec{
		Prompt:         "hello",
		TimeoutSeconds: 42,
	})
	if args.TimeoutSeconds != 42 {
		t.Fatalf("TimeoutSeconds = %d, want 42", args.TimeoutSeconds)
	}

	fallback := taskDispatchSpawnArgs("codex", "codex.exe", profile, picker.TaskSpec{Prompt: "hello"})
	if fallback.TimeoutSeconds != 7 {
		t.Fatalf("TimeoutSeconds without override = %d, want profile timeout 7", fallback.TimeoutSeconds)
	}
}

func workflowStepStatusesFromSteps(steps []workflow.WorkflowStep) []string {
	out := make([]string, len(steps))
	for i, step := range steps {
		out[i] = step.Name + "=completed"
	}
	return out
}

func assertMetadataInt(t *testing.T, metadata map[string]any, key string, want int) {
	t.Helper()
	value, ok := metadata[key]
	if !ok {
		t.Fatalf("metadata[%q] missing", key)
	}
	switch typed := value.(type) {
	case int:
		if typed != want {
			t.Fatalf("metadata[%q] = %d, want %d", key, typed, want)
		}
	case int64:
		if typed != int64(want) {
			t.Fatalf("metadata[%q] = %d, want %d", key, typed, want)
		}
	case float64:
		if typed != float64(want) {
			t.Fatalf("metadata[%q] = %v, want %d", key, typed, want)
		}
	default:
		t.Fatalf("metadata[%q] = %#v, want int %d", key, value, want)
	}
}

func stringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertMetadataStringSlice(t *testing.T, metadata map[string]any, key string, want []string) {
	t.Helper()
	value, ok := metadata[key]
	if !ok {
		t.Fatalf("metadata[%q] missing", key)
	}
	got, ok := value.([]string)
	if !ok {
		items, ok := value.([]any)
		if !ok {
			t.Fatalf("metadata[%q] = %#v, want string slice %#v", key, value, want)
		}
		got = make([]string, len(items))
		for i, item := range items {
			text, ok := item.(string)
			if !ok {
				t.Fatalf("metadata[%q][%d] = %#v, want string", key, i, item)
			}
			got[i] = text
		}
	}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("metadata[%q] = %#v, want %#v", key, got, want)
	}
}
