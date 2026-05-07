package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/loom"
)

// fakeLoom is a minimal in-memory loom implementation for handler tests.
type fakeLoom struct {
	tasks      map[string]*loom.Task
	submitErr  error
	nextTaskID string
}

func newFakeLoom() *fakeLoom {
	return &fakeLoom{
		tasks:      make(map[string]*loom.Task),
		nextTaskID: "task-001",
	}
}

func (f *fakeLoom) Submit(_ context.Context, req loom.TaskRequest) (string, error) {
	if f.submitErr != nil {
		return "", f.submitErr
	}
	id := f.nextTaskID
	now := time.Now()
	task := &loom.Task{
		ID:         id,
		WorkerType: req.WorkerType,
		ProjectID:  req.ProjectID,
		Prompt:     req.Prompt,
		Model:      req.Model,
		Metadata:   req.Metadata,
		Status:     loom.TaskStatusPending,
		CreatedAt:  now,
	}
	f.tasks[id] = task
	return id, nil
}

func (f *fakeLoom) Get(taskID string) (*loom.Task, error) {
	task, ok := f.tasks[taskID]
	if !ok {
		return nil, loom.ErrTaskNotFound
	}
	return task, nil
}

func (f *fakeLoom) Cancel(taskID string) error {
	task, ok := f.tasks[taskID]
	if !ok {
		return loom.ErrTaskNotFound
	}
	task.Status = loom.TaskStatusFailed
	return nil
}

// setTerminal marks a fake task as completed with the given result content.
func (f *fakeLoom) setTerminal(taskID, result string) {
	if task, ok := f.tasks[taskID]; ok {
		task.Status = loom.TaskStatusCompleted
		task.Result = result
		now := time.Now()
		task.CompletedAt = &now
	}
}

// newHandlers returns a CodexHandlers wired to the fake loom and a fake pool.
// The pool is not exercised by handler tests (submit goes to loom only).
func newHandlers(t *testing.T, l *fakeLoom) *CodexHandlers {
	t.Helper()
	pool := newTestPool(t, nil)
	h, err := NewCodexHandlers(pool.CodexPool, l)
	if err != nil {
		t.Fatalf("NewCodexHandlers: %v", err)
	}
	return h
}

// buildReq constructs a minimal mcp.CallToolRequest for testing.
// Arguments are passed as map[string]any so GetArguments() returns them directly.
func buildReq(t *testing.T, args map[string]any) mcp.CallToolRequest {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

// decodeResult unmarshals a JSON tool result body into m.
func decodeResult(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatal("result has no content")
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(text.Text), &m); err != nil {
		t.Fatalf("decode result JSON: %v (raw: %s)", err, text.Text)
	}
	return m
}

// errResult extracts the error message from an error tool result.
func errResult(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("expected error result, got: %+v", result)
	}
	if len(result.Content) == 0 {
		return ""
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return ""
	}
	return text.Text
}

// --- Tests ---

func TestHandleCodexTask_Submit(t *testing.T) {
	l := newFakeLoom()
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{
		"prompt":     "write a unit test",
		"project_id": "proj-1",
	})
	result, err := h.HandleCodexTask(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeResult(t, result)

	if m["task_id"] != "task-001" {
		t.Errorf("task_id: got %v, want task-001", m["task_id"])
	}
	if m["project_id"] != "proj-1" {
		t.Errorf("project_id: got %v, want proj-1", m["project_id"])
	}
	if m["status"] != string(loom.TaskStatusPending) {
		t.Errorf("status: got %v, want %s", m["status"], loom.TaskStatusPending)
	}
	if _, ok := m["invoked_at"]; !ok {
		t.Error("invoked_at missing")
	}
}

func TestHandleCodexTask_DefaultSandboxClass(t *testing.T) {
	l := newFakeLoom()
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{"prompt": "do something"})
	result, err := h.HandleCodexTask(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decodeResult(t, result) // should not be an error result

	task := l.tasks["task-001"]
	if task == nil {
		t.Fatal("task not found in fake loom")
	}
	if got, _ := task.Metadata["job_class"].(string); got != JobClassTask {
		t.Errorf("job_class: got %q, want %q", got, JobClassTask)
	}
}

func TestHandleCodexTask_InvalidSandboxClass(t *testing.T) {
	l := newFakeLoom()
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{
		"prompt":       "do something",
		"sandbox_class": "invalid-class",
	})
	result, err := h.HandleCodexTask(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for invalid sandbox_class")
	}
}

func TestHandleCodexTask_MissingPrompt(t *testing.T) {
	l := newFakeLoom()
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{})
	result, err := h.HandleCodexTask(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when prompt is missing")
	}
}

func TestHandleCodexTask_SubmitError(t *testing.T) {
	l := newFakeLoom()
	l.submitErr = errors.New("loom full")
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{"prompt": "task"})
	result, err := h.HandleCodexTask(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result on submit failure")
	}
}

func TestHandleCodexTask_ResumeTaskID(t *testing.T) {
	l := newFakeLoom()
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{
		"prompt":         "continue the work",
		"resume_task_id": "prior-task",
	})
	result, err := h.HandleCodexTask(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decodeResult(t, result)

	task := l.tasks["task-001"]
	if task == nil {
		t.Fatal("task not created")
	}
	if got, _ := task.Metadata["resume_task_id"].(string); got != "prior-task" {
		t.Errorf("resume_task_id in metadata: got %q, want %q", got, "prior-task")
	}
}

func TestHandleCodexReview_Submit(t *testing.T) {
	l := newFakeLoom()
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{
		"target":     "HEAD~1..HEAD",
		"project_id": "proj-review",
	})
	result, err := h.HandleCodexReview(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeResult(t, result)

	if m["task_id"] != "task-001" {
		t.Errorf("task_id: got %v, want task-001", m["task_id"])
	}

	task := l.tasks["task-001"]
	if task == nil {
		t.Fatal("task not found")
	}
	if got, _ := task.Metadata["job_class"].(string); got != JobClassReview {
		t.Errorf("job_class: got %q, want %q", got, JobClassReview)
	}
	if got, _ := task.Metadata["review_target"].(string); got != "HEAD~1..HEAD" {
		t.Errorf("review_target: got %q, want %q", got, "HEAD~1..HEAD")
	}
}

func TestHandleCodexReview_MissingTarget(t *testing.T) {
	l := newFakeLoom()
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{})
	result, err := h.HandleCodexReview(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when target is missing")
	}
}

func TestHandleCodexStatus_Found(t *testing.T) {
	l := newFakeLoom()
	// Pre-seed a running task.
	now := time.Now()
	l.tasks["task-42"] = &loom.Task{
		ID:             "task-42",
		WorkerType:     WorkerTypeCodex,
		Status:         loom.TaskStatusRunning,
		CreatedAt:      now,
		LastOutputLine: "processing...",
		ProgressLines:  5,
	}
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{"task_id": "task-42"})
	result, err := h.HandleCodexStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeResult(t, result)

	if m["task_id"] != "task-42" {
		t.Errorf("task_id: got %v, want task-42", m["task_id"])
	}
	if m["status"] != string(loom.TaskStatusRunning) {
		t.Errorf("status: got %v, want running", m["status"])
	}
	if m["progress_tail"] != "processing..." {
		t.Errorf("progress_tail: got %v", m["progress_tail"])
	}
}

func TestHandleCodexStatus_NotFound(t *testing.T) {
	l := newFakeLoom()
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{"task_id": "nonexistent"})
	result, err := h.HandleCodexStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for nonexistent task")
	}
}

func TestHandleCodexStatus_CompletedWithContent(t *testing.T) {
	l := newFakeLoom()
	l.tasks["task-done"] = &loom.Task{
		ID:         "task-done",
		WorkerType: WorkerTypeCodex,
		Status:     loom.TaskStatusCompleted,
		Result:     "all done",
		CreatedAt:  time.Now(),
	}
	now := time.Now()
	l.tasks["task-done"].CompletedAt = &now
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{
		"task_id":         "task-done",
		"include_content": true,
	})
	result, err := h.HandleCodexStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeResult(t, result)
	if m["content"] != "all done" {
		t.Errorf("content: got %v, want 'all done'", m["content"])
	}
	if _, ok := m["completed_at"]; !ok {
		t.Error("completed_at missing")
	}
}

func TestHandleCodexStatus_TailParam(t *testing.T) {
	l := newFakeLoom()
	l.tasks["task-tail"] = &loom.Task{
		ID:         "task-tail",
		WorkerType: WorkerTypeCodex,
		Status:     loom.TaskStatusCompleted,
		Result:     "ABCDEFGHIJ",
		CreatedAt:  time.Now(),
	}
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{
		"task_id": "task-tail",
		"tail":    5,
	})
	result, err := h.HandleCodexStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeResult(t, result)
	if m["content_tail"] != "FGHIJ" {
		t.Errorf("content_tail: got %v, want FGHIJ", m["content_tail"])
	}
}

func TestHandleCodexCancel_ActiveTask(t *testing.T) {
	l := newFakeLoom()
	l.tasks["task-run"] = &loom.Task{
		ID:         "task-run",
		WorkerType: WorkerTypeCodex,
		Status:     loom.TaskStatusRunning,
		CreatedAt:  time.Now(),
	}
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{"task_id": "task-run"})
	result, err := h.HandleCodexCancel(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeResult(t, result)

	if m["previous_status"] != string(loom.TaskStatusRunning) {
		t.Errorf("previous_status: got %v, want running", m["previous_status"])
	}
	if _, ok := m["cancelled_at"]; !ok {
		t.Error("cancelled_at missing")
	}
}

func TestHandleCodexCancel_AlreadyTerminal(t *testing.T) {
	l := newFakeLoom()
	l.tasks["task-done"] = &loom.Task{
		ID:         "task-done",
		WorkerType: WorkerTypeCodex,
		Status:     loom.TaskStatusCompleted,
		CreatedAt:  time.Now(),
	}
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{"task_id": "task-done"})
	result, err := h.HandleCodexCancel(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeResult(t, result)

	if m["current_status"] != string(loom.TaskStatusCompleted) {
		t.Errorf("current_status: got %v, want completed", m["current_status"])
	}
	// Cancel must be idempotent — no error for already-terminal task.
	if result.IsError {
		t.Error("expected successful response for already-terminal task")
	}
}

func TestHandleCodexCancel_NotFound(t *testing.T) {
	l := newFakeLoom()
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{"task_id": "missing"})
	result, err := h.HandleCodexCancel(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for nonexistent task")
	}
}

func TestHandleCodexReviewGate_AllowOnTimeout(t *testing.T) {
	l := newFakeLoom()
	h := newHandlers(t, l)

	// Set timeout to 1 second; task stays pending → timeout triggers fail-open.
	req := buildReq(t, map[string]any{
		"target":          "HEAD",
		"timeout_seconds": 1,
	})

	ctx := context.Background()
	result, err := h.HandleCodexReviewGate(ctx, req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	m := decodeResult(t, result)

	if m["decision"] != "allow" {
		t.Errorf("decision: got %v, want allow (fail-open on timeout)", m["decision"])
	}
	if _, ok := m["task_id"]; !ok {
		t.Error("task_id missing")
	}
}

func TestHandleCodexReviewGate_BlockDecision(t *testing.T) {
	// Verify parseGateDecision: BLOCK embedded in JSON is extracted correctly.
	input := `{"findings":[],"summary":"critical issue","decision":"BLOCK","reason":"critical security issue found"}`
	decision, reason := parseGateDecision(input)
	if decision != "block" {
		t.Errorf("decision: got %q, want block", decision)
	}
	if reason != "critical security issue found" {
		t.Errorf("reason: got %q", reason)
	}
}

func TestHandleCodexReviewGate_AllowDecision(t *testing.T) {
	input := `{"findings":[],"summary":"clean","decision":"ALLOW","reason":"no issues found"}`
	decision, reason := parseGateDecision(input)
	if decision != "allow" {
		t.Errorf("decision: got %q, want allow", decision)
	}
	if reason != "no issues found" {
		t.Errorf("reason: got %q", reason)
	}
}

func TestHandleCodexReviewGate_UnrecognisedOutput(t *testing.T) {
	// No JSON object → fail-open
	decision, reason := parseGateDecision("some random output without a decision")
	if decision != "allow" {
		t.Errorf("decision: got %q, want allow", decision)
	}
	if reason == "" {
		t.Error("expected non-empty reason for unrecognised output")
	}
}

func TestHandleCodexReviewGate_CaseInsensitive(t *testing.T) {
	// decision field is case-insensitive via ToUpper comparison.
	input := `{"findings":[],"summary":"issues","decision":"block","reason":"lowercase block"}`
	decision, _ := parseGateDecision(input)
	if decision != "block" {
		t.Errorf("decision: got %q, want block", decision)
	}
}

func TestHandleCodexReviewGate_MissingTarget(t *testing.T) {
	l := newFakeLoom()
	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{})
	result, err := h.HandleCodexReviewGate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when target is missing")
	}
}

func TestNewCodexHandlers_NilPool(t *testing.T) {
	_, err := NewCodexHandlers(nil, newFakeLoom())
	if err == nil {
		t.Error("expected error for nil pool")
	}
}

func TestNewCodexHandlers_NilLoom(t *testing.T) {
	pool := newTestPool(t, nil)
	_, err := NewCodexHandlers(pool.CodexPool, nil)
	if err == nil {
		t.Error("expected error for nil loom")
	}
}

// TestBuildCodexStatusResult_NoContent verifies that terminal tasks without
// include_content return content_length rather than the content body.
func TestBuildCodexStatusResult_NoContent(t *testing.T) {
	now := time.Now()
	task := &loom.Task{
		ID:        "t1",
		Status:    loom.TaskStatusCompleted,
		Result:    "hello world",
		CreatedAt: now,
	}
	m := buildCodexStatusResult(task, false, 0)
	if _, hasContent := m["content"]; hasContent {
		t.Error("content should not be present when include_content=false")
	}
	if m["content_length"] != 11 {
		t.Errorf("content_length: got %v, want 11", m["content_length"])
	}
}

// TestBuildCodexStatusResult_CompactionFields verifies that last_input_tokens and
// compaction_count are returned in the status result when include_content=true and
// the task metadata contains non-zero values for those fields (FR-12).
func TestBuildCodexStatusResult_CompactionFields_IncludeContent(t *testing.T) {
	meta := CodexTaskMeta{
		JobClass:        JobClassTask,
		ThreadID:        "thread-t1",
		LastInputTokens: 200_000,
		CompactionCount: 2,
	}
	metaMap, err := codeTaskMetaToMap(meta)
	if err != nil {
		t.Fatalf("codeTaskMetaToMap: %v", err)
	}

	now := time.Now()
	task := &loom.Task{
		ID:        "task-compact",
		Status:    loom.TaskStatusCompleted,
		Result:    "done",
		CreatedAt: now,
		Metadata:  metaMap,
	}
	task.CompletedAt = &now

	m := buildCodexStatusResult(task, true /* includeContent */, 0)

	if v, ok := m["last_input_tokens"]; !ok {
		t.Error("last_input_tokens missing from status result")
	} else {
		// buildCodexStatusResult stores the value directly (int64), not via JSON round-trip.
		switch tv := v.(type) {
		case int64:
			if tv != 200_000 {
				t.Errorf("last_input_tokens: got %d, want 200_000", tv)
			}
		case float64:
			if int64(tv) != 200_000 {
				t.Errorf("last_input_tokens: got %v, want 200_000", tv)
			}
		default:
			t.Errorf("last_input_tokens: unexpected type %T value %v", v, v)
		}
	}
	if v, ok := m["compaction_count"]; !ok {
		t.Error("compaction_count missing from status result")
	} else {
		switch tv := v.(type) {
		case int:
			if tv != 2 {
				t.Errorf("compaction_count: got %d, want 2", tv)
			}
		case float64:
			if int(tv) != 2 {
				t.Errorf("compaction_count: got %v, want 2", tv)
			}
		default:
			t.Errorf("compaction_count: unexpected type %T value %v", v, v)
		}
	}
}

// TestBuildCodexStatusResult_CompactionFields_NotReturnedWithoutIncludeContent verifies
// that compaction fields are NOT present when include_content=false (FR-12).
func TestBuildCodexStatusResult_CompactionFields_NotReturnedWithoutIncludeContent(t *testing.T) {
	meta := CodexTaskMeta{
		JobClass:        JobClassTask,
		ThreadID:        "thread-t2",
		LastInputTokens: 200_000,
		CompactionCount: 3,
	}
	metaMap, _ := codeTaskMetaToMap(meta)

	now := time.Now()
	task := &loom.Task{
		ID:        "task-compact-hidden",
		Status:    loom.TaskStatusCompleted,
		Result:    "done",
		CreatedAt: now,
		Metadata:  metaMap,
	}
	task.CompletedAt = &now

	m := buildCodexStatusResult(task, false /* includeContent */, 0)

	if _, ok := m["last_input_tokens"]; ok {
		t.Error("last_input_tokens must not be present when include_content=false")
	}
	if _, ok := m["compaction_count"]; ok {
		t.Error("compaction_count must not be present when include_content=false")
	}
}

// TestBuildCodexStatusResult_CompactionFields_ZeroValuesOmitted verifies that
// last_input_tokens and compaction_count are omitted when their values are zero
// (omitempty semantics — avoids cluttering status responses for non-compacted tasks).
func TestBuildCodexStatusResult_CompactionFields_ZeroValuesOmitted(t *testing.T) {
	meta := CodexTaskMeta{
		JobClass: JobClassTask,
		ThreadID: "thread-t3",
		// LastInputTokens and CompactionCount are zero (default).
	}
	metaMap, _ := codeTaskMetaToMap(meta)

	now := time.Now()
	task := &loom.Task{
		ID:        "task-compact-zero",
		Status:    loom.TaskStatusCompleted,
		Result:    "done",
		CreatedAt: now,
		Metadata:  metaMap,
	}
	task.CompletedAt = &now

	m := buildCodexStatusResult(task, true /* includeContent */, 0)

	if _, ok := m["last_input_tokens"]; ok {
		t.Error("last_input_tokens must be omitted when value is 0")
	}
	if _, ok := m["compaction_count"]; ok {
		t.Error("compaction_count must be omitted when value is 0")
	}
}

// TestHandleCodexStatus_CompactionFields_ViaHandler verifies that the MCP handler
// surfaces last_input_tokens and compaction_count when include_content=true (FR-12).
func TestHandleCodexStatus_CompactionFields_ViaHandler(t *testing.T) {
	meta := CodexTaskMeta{
		JobClass:        JobClassTask,
		ThreadID:        "thread-handler",
		LastInputTokens: 181_881,
		CompactionCount: 1,
	}
	metaMap, _ := codeTaskMetaToMap(meta)

	l := newFakeLoom()
	now := time.Now()
	l.tasks["task-handler-compact"] = &loom.Task{
		ID:         "task-handler-compact",
		WorkerType: WorkerTypeCodex,
		Status:     loom.TaskStatusCompleted,
		Result:     "compacted result",
		CreatedAt:  now,
		Metadata:   metaMap,
	}
	l.tasks["task-handler-compact"].CompletedAt = &now

	h := newHandlers(t, l)

	req := buildReq(t, map[string]any{
		"task_id":         "task-handler-compact",
		"include_content": true,
	})
	result, err := h.HandleCodexStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeResult(t, result)

	if v, ok := m["last_input_tokens"]; !ok {
		t.Error("last_input_tokens missing from codex_status response")
	} else if int64(v.(float64)) != 181_881 {
		t.Errorf("last_input_tokens: got %v, want 181_881", v)
	}

	if v, ok := m["compaction_count"]; !ok {
		t.Error("compaction_count missing from codex_status response")
	} else if int(v.(float64)) != 1 {
		t.Errorf("compaction_count: got %v, want 1", v)
	}
}
