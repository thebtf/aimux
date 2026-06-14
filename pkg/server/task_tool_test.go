package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/thebtf/aimux/pkg/executor/picker"
	"github.com/thebtf/aimux/pkg/executor/review"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/server/classifier"
	"github.com/thebtf/aimux/pkg/types"
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
	if payload["content"] != "handled code" {
		t.Fatalf("content = %v, want handled code", payload["content"])
	}

	task := codeWorker.onlyTask(t)
	if task.WorkerType != code.WorkerTypeCode {
		t.Fatalf("worker_type = %s, want %s", task.WorkerType, code.WorkerTypeCode)
	}
	assertMetadataString(t, task.Metadata, "task_class", classifier.TaskClassCode)
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
	if got := reviewWorker.taskCount(); got != 2 {
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
	if !first.IsError {
		t.Fatalf("first call expected worker failure, got %s", taskToolResultText(t, first))
	}
	second := callTaskTool(t, srv, args)
	if second.IsError {
		t.Fatalf("second call unexpected error: %s", taskToolResultText(t, second))
	}
	if got := reviewWorker.taskCount(); got != 2 {
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
		"recipe_id": "missing",
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
	if !stringSlicesEqual(payload.AvailableRecipes, []string{"code-review", "second-opinion"}) {
		t.Fatalf("available_recipes = %#v, want deterministic recipe IDs", payload.AvailableRecipes)
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
	if got := reviewWorker.taskCount(); got != 1 {
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

func newTaskToolServer(t *testing.T) (*Server, *recordingTaskWorker, *recordingTaskWorker) {
	return newTaskToolServerWithProfile(t, defaultRecipeProfile())
}

func newTaskToolServerWithProfile(t *testing.T, profile *config.CLIProfile) (*Server, *recordingTaskWorker, *recordingTaskWorker) {
	t.Helper()
	engine := newTaskToolEngine(t)
	codeWorker := &recordingTaskWorker{workerType: code.WorkerTypeCode}
	reviewWorker := &recordingTaskWorker{workerType: review.WorkerTypeReview}
	engine.RegisterWorker(code.WorkerTypeCode, codeWorker)
	engine.RegisterWorker(review.WorkerTypeReview, reviewWorker)
	registry := driver.NewRegistry(map[string]*config.CLIProfile{"codex": profile})
	registry.SetAvailable("codex", true)
	return &Server{loom: engine, registry: registry}, codeWorker, reviewWorker
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
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(w.tasks))
	}
	cp := *w.tasks[0]
	cp.Metadata = cloneTaskMetadata(w.tasks[0].Metadata)
	return &cp
}

func (w *recordingTaskWorker) taskCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.tasks)
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
