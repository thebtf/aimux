package loom

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom/deps"
	_ "modernc.org/sqlite"
)

// ---- test helpers ----

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// Use a per-test named in-memory database so connections within the same test
	// share the schema, but tests remain fully isolated from each other.
	// Plain ":memory:" gives each connection its own DB (breaks goroutines).
	dbName := "file:" + t.Name() + "?cache=shared&mode=memory"
	db, err := sql.Open("sqlite", dbName)
	if err != nil {
		t.Fatal(err)
	}
	// Single connection forces all pool connections to use the same in-memory DB.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func newTestStore(t *testing.T) *TaskStore {
	t.Helper()
	db := newTestDB(t)
	store, err := NewTaskStore(db, "test")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func makeTask(id, projectID string, status TaskStatus) *Task {
	return &Task{
		ID:         id,
		Status:     status,
		WorkerType: WorkerTypeCLI,
		ProjectID:  projectID,
		Prompt:     "test prompt",
		CreatedAt:  time.Now().UTC(),
	}
}

// ---- testWorker ----

type testWorker struct {
	wtype   WorkerType
	result  string
	err     error
	called  atomic.Int32
	delay   time.Duration
}

func (w *testWorker) Execute(_ context.Context, _ *Task) (*WorkerResult, error) {
	w.called.Add(1)
	if w.delay > 0 {
		time.Sleep(w.delay)
	}
	if w.err != nil {
		return nil, w.err
	}
	return &WorkerResult{Content: w.result}, nil
}

func (w *testWorker) Type() WorkerType { return w.wtype }

// ---- state machine tests ----

func TestTaskTransitions_Valid(t *testing.T) {
	cases := []struct {
		from TaskStatus
		to   TaskStatus
	}{
		{TaskStatusPending, TaskStatusDispatched},
		{TaskStatusDispatched, TaskStatusRunning},
		{TaskStatusDispatched, TaskStatusFailed},
		{TaskStatusDispatched, TaskStatusFailedCrash},
		{TaskStatusRunning, TaskStatusCompleted},
		{TaskStatusRunning, TaskStatusFailed},
		{TaskStatusRunning, TaskStatusRetrying},
		{TaskStatusRunning, TaskStatusFailedCrash},
		{TaskStatusRetrying, TaskStatusDispatched},
		// NEW-001 (v0.1.1 PRC #2): retrying → failed is valid — enables failTask
		// to correctly terminate tasks that hit IncrementRetries or
		// retrying→dispatched errors during the BUG-002 retry-path fix.
		{TaskStatusRetrying, TaskStatusFailed},
	}

	for _, tc := range cases {
		if !tc.from.CanTransitionTo(tc.to) {
			t.Errorf("expected %s → %s to be valid", tc.from, tc.to)
		}
	}
}

func TestTaskTransitions_Invalid(t *testing.T) {
	cases := []struct {
		from TaskStatus
		to   TaskStatus
	}{
		{TaskStatusPending, TaskStatusCompleted},
		{TaskStatusPending, TaskStatusRunning},
		{TaskStatusPending, TaskStatusFailed},
		{TaskStatusCompleted, TaskStatusPending},
		{TaskStatusCompleted, TaskStatusRunning},
		{TaskStatusFailed, TaskStatusRunning},
		{TaskStatusFailedCrash, TaskStatusDispatched},
		{TaskStatusDispatched, TaskStatusCompleted},
		{TaskStatusDispatched, TaskStatusRetrying},
		// Note: {TaskStatusDispatched, TaskStatusFailed} is VALID (added for pre-run rejection)
		// so it is intentionally absent from this invalid list.
	}

	for _, tc := range cases {
		if tc.from.CanTransitionTo(tc.to) {
			t.Errorf("expected %s → %s to be INVALID", tc.from, tc.to)
		}
	}
}

func TestTaskStatus_IsTerminal(t *testing.T) {
	terminal := []TaskStatus{TaskStatusCompleted, TaskStatusFailed, TaskStatusFailedCrash}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("expected %s to be terminal", s)
		}
	}

	nonTerminal := []TaskStatus{TaskStatusPending, TaskStatusDispatched, TaskStatusRunning, TaskStatusRetrying}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("expected %s to be non-terminal", s)
		}
	}
}

// ---- store tests ----

func TestTaskStore_Create_Get(t *testing.T) {
	store := newTestStore(t)

	env := map[string]string{"KEY": "val"}
	meta := map[string]any{"foo": "bar"}

	task := &Task{
		ID:         "task-1",
		Status:     TaskStatusPending,
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-1",
		Prompt:     "hello",
		CWD:        "/tmp",
		Env:        env,
		CLI:        "claude",
		Role:       "coder",
		Model:      "opus",
		Effort:     "high",
		Timeout:    30,
		Metadata:   meta,
		Retries:    0,
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
	}

	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get("task-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != task.ID {
		t.Errorf("ID: got %q want %q", got.ID, task.ID)
	}
	if got.Status != task.Status {
		t.Errorf("Status: got %q want %q", got.Status, task.Status)
	}
	if got.WorkerType != task.WorkerType {
		t.Errorf("WorkerType: got %q want %q", got.WorkerType, task.WorkerType)
	}
	if got.ProjectID != task.ProjectID {
		t.Errorf("ProjectID: got %q want %q", got.ProjectID, task.ProjectID)
	}
	if got.Prompt != task.Prompt {
		t.Errorf("Prompt: got %q want %q", got.Prompt, task.Prompt)
	}
	if got.CWD != task.CWD {
		t.Errorf("CWD: got %q want %q", got.CWD, task.CWD)
	}
	if got.CLI != task.CLI {
		t.Errorf("CLI: got %q want %q", got.CLI, task.CLI)
	}
	if got.Role != task.Role {
		t.Errorf("Role: got %q want %q", got.Role, task.Role)
	}
	if got.Model != task.Model {
		t.Errorf("Model: got %q want %q", got.Model, task.Model)
	}
	if got.Effort != task.Effort {
		t.Errorf("Effort: got %q want %q", got.Effort, task.Effort)
	}
	if got.Timeout != task.Timeout {
		t.Errorf("Timeout: got %d want %d", got.Timeout, task.Timeout)
	}
	if got.Retries != task.Retries {
		t.Errorf("Retries: got %d want %d", got.Retries, task.Retries)
	}
	if got.Env["KEY"] != env["KEY"] {
		t.Errorf("Env[KEY]: got %q want %q", got.Env["KEY"], env["KEY"])
	}
	if got.Metadata["foo"] != meta["foo"] {
		t.Errorf("Metadata[foo]: got %v want %v", got.Metadata["foo"], meta["foo"])
	}
}

func TestTaskStore_List_ByProject(t *testing.T) {
	store := newTestStore(t)

	_ = store.Create(makeTask("t1", "proj-A", TaskStatusPending))
	_ = store.Create(makeTask("t2", "proj-A", TaskStatusPending))
	_ = store.Create(makeTask("t3", "proj-B", TaskStatusPending))

	listA, err := store.List("proj-A")
	if err != nil {
		t.Fatalf("List proj-A: %v", err)
	}
	if len(listA) != 2 {
		t.Errorf("proj-A: got %d tasks, want 2", len(listA))
	}

	listB, err := store.List("proj-B")
	if err != nil {
		t.Fatalf("List proj-B: %v", err)
	}
	if len(listB) != 1 {
		t.Errorf("proj-B: got %d tasks, want 1", len(listB))
	}

	listC, err := store.List("proj-C")
	if err != nil {
		t.Fatalf("List proj-C: %v", err)
	}
	if len(listC) != 0 {
		t.Errorf("proj-C: got %d tasks, want 0", len(listC))
	}
}

func TestTaskStore_List_ByStatus(t *testing.T) {
	store := newTestStore(t)

	t1 := makeTask("t1", "proj", TaskStatusPending)
	t2 := makeTask("t2", "proj", TaskStatusPending)
	t3 := makeTask("t3", "proj", TaskStatusPending)
	_ = store.Create(t1)
	_ = store.Create(t2)
	_ = store.Create(t3)

	// Manually set statuses for t2 and t3 via DB.
	_, _ = store.db.Exec("UPDATE tasks SET status='running' WHERE id='t2'")
	_, _ = store.db.Exec("UPDATE tasks SET status='completed' WHERE id='t3'")

	pending, err := store.List("proj", TaskStatusPending)
	if err != nil {
		t.Fatalf("List pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "t1" {
		t.Errorf("pending: got %d tasks", len(pending))
	}

	running, err := store.List("proj", TaskStatusRunning)
	if err != nil {
		t.Fatalf("List running: %v", err)
	}
	if len(running) != 1 || running[0].ID != "t2" {
		t.Errorf("running: got %d tasks", len(running))
	}

	multi, err := store.List("proj", TaskStatusPending, TaskStatusRunning)
	if err != nil {
		t.Fatalf("List multi: %v", err)
	}
	if len(multi) != 2 {
		t.Errorf("multi: got %d tasks, want 2", len(multi))
	}
}

func TestTaskStore_UpdateStatus(t *testing.T) {
	store := newTestStore(t)

	_ = store.Create(makeTask("t1", "proj", TaskStatusPending))

	// Valid transition.
	if err := store.UpdateStatus("t1", TaskStatusPending, TaskStatusDispatched); err != nil {
		t.Fatalf("UpdateStatus pending→dispatched: %v", err)
	}

	task, _ := store.Get("t1")
	if task.Status != TaskStatusDispatched {
		t.Errorf("expected dispatched, got %q", task.Status)
	}
	if task.DispatchedAt == nil {
		t.Error("dispatched_at should be set after transition to dispatched")
	}

	// Invalid transition rejected.
	err := store.UpdateStatus("t1", TaskStatusPending, TaskStatusCompleted)
	if err == nil {
		t.Error("expected error for invalid transition, got nil")
	}

	// Wrong current status — row not updated.
	err = store.UpdateStatus("t1", TaskStatusPending, TaskStatusDispatched)
	if err == nil {
		t.Error("expected error when current status doesn't match 'from'")
	}
}

func TestTaskStore_SetResult(t *testing.T) {
	store := newTestStore(t)
	_ = store.Create(makeTask("t1", "proj", TaskStatusPending))

	if err := store.SetResult("t1", "output content", ""); err != nil {
		t.Fatalf("SetResult: %v", err)
	}

	task, err := store.Get("t1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Result != "output content" {
		t.Errorf("Result: got %q want %q", task.Result, "output content")
	}
	if task.CompletedAt == nil {
		t.Error("CompletedAt should be set after SetResult")
	}
}

func TestTaskStore_SetResult_WithError(t *testing.T) {
	store := newTestStore(t)
	_ = store.Create(makeTask("t1", "proj", TaskStatusPending))

	if err := store.SetResult("t1", "", "something went wrong"); err != nil {
		t.Fatalf("SetResult: %v", err)
	}

	task, _ := store.Get("t1")
	if task.Error != "something went wrong" {
		t.Errorf("Error: got %q", task.Error)
	}
}

func TestTaskStore_MarkCrashed(t *testing.T) {
	store := newTestStore(t)

	dispatched := makeTask("t1", "proj", TaskStatusPending)
	running := makeTask("t2", "proj", TaskStatusPending)
	completed := makeTask("t3", "proj", TaskStatusPending)
	_ = store.Create(dispatched)
	_ = store.Create(running)
	_ = store.Create(completed)

	_, _ = store.db.Exec("UPDATE tasks SET status='dispatched' WHERE id='t1'")
	_, _ = store.db.Exec("UPDATE tasks SET status='running' WHERE id='t2'")
	_, _ = store.db.Exec("UPDATE tasks SET status='completed' WHERE id='t3'")

	n, err := store.MarkCrashed()
	if err != nil {
		t.Fatalf("MarkCrashed: %v", err)
	}
	if n != 2 {
		t.Errorf("MarkCrashed: marked %d tasks, want 2", n)
	}

	t1, _ := store.Get("t1")
	t2, _ := store.Get("t2")
	t3, _ := store.Get("t3")

	if t1.Status != TaskStatusFailedCrash {
		t.Errorf("t1 status: got %q want failed_crash", t1.Status)
	}
	if t2.Status != TaskStatusFailedCrash {
		t.Errorf("t2 status: got %q want failed_crash", t2.Status)
	}
	if t3.Status != TaskStatusCompleted {
		t.Errorf("t3 status: got %q want completed (untouched)", t3.Status)
	}
}

// ---- LoomEngine tests ----

func TestLoomEngine_Submit(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	worker := &testWorker{wtype: WorkerTypeCLI, result: "hello"}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	taskID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-1",
		Prompt:     "do something",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if taskID == "" {
		t.Fatal("Submit returned empty taskID")
	}

	// Task should be persisted immediately.
	task, err := store.Get(taskID)
	if err != nil {
		t.Fatalf("Get after Submit: %v", err)
	}
	if task.ID != taskID {
		t.Errorf("task ID mismatch: got %q want %q", task.ID, taskID)
	}

	// Wait for background dispatch to complete.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, _ = store.Get(taskID)
		if task.Status == TaskStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if task.Status != TaskStatusCompleted {
		t.Errorf("task status: got %q want completed", task.Status)
	}
	if task.Result != "hello" {
		t.Errorf("task result: got %q want %q", task.Result, "hello")
	}
	if worker.called.Load() != 1 {
		t.Errorf("worker Execute called %d times, want 1", worker.called.Load())
	}
}

func TestLoomEngine_Submit_NoWorker(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	// No worker registered for WorkerTypeCLI.

	taskID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-1",
		Prompt:     "will fail",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for the task to reach a terminal state.
	deadline := time.Now().Add(3 * time.Second)
	var task *Task
	for time.Now().Before(deadline) {
		var getErr error
		task, getErr = store.Get(taskID)
		if getErr != nil {
			t.Fatalf("Get: %v", getErr)
		}
		if task.Status.IsTerminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if task == nil {
		t.Fatal("task not found")
	}
	if task.Status != TaskStatusFailed {
		t.Errorf("task status: got %q want failed", task.Status)
	}
}

func TestLoomEngine_Submit_WorkerError(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	worker := &testWorker{wtype: WorkerTypeCLI, err: errors.New("worker exploded")}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	taskID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-1",
		Prompt:     "will fail",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var task *Task
	for time.Now().Before(deadline) {
		task, _ = store.Get(taskID)
		if task.Status.IsTerminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if task.Status != TaskStatusFailed {
		t.Errorf("task status: got %q want failed", task.Status)
	}
	if task.Error != "worker exploded" {
		t.Errorf("task error: got %q want %q", task.Error, "worker exploded")
	}
}

func TestLoomEngine_RecoverCrashed(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	// Insert tasks directly into the store bypassing state machine for test setup.
	t1 := makeTask("t1", "proj", TaskStatusPending)
	t2 := makeTask("t2", "proj", TaskStatusPending)
	t3 := makeTask("t3", "proj", TaskStatusPending)
	_ = store.Create(t1)
	_ = store.Create(t2)
	_ = store.Create(t3)

	_, _ = store.db.Exec("UPDATE tasks SET status='dispatched' WHERE id='t1'")
	_, _ = store.db.Exec("UPDATE tasks SET status='running' WHERE id='t2'")
	// t3 stays pending — should NOT be marked crashed.

	n, err := engine.RecoverCrashed()
	if err != nil {
		t.Fatalf("RecoverCrashed: %v", err)
	}
	if n != 2 {
		t.Errorf("RecoverCrashed: got %d, want 2", n)
	}

	task1, _ := store.Get("t1")
	task2, _ := store.Get("t2")
	task3, _ := store.Get("t3")

	if task1.Status != TaskStatusFailedCrash {
		t.Errorf("t1: got %q want failed_crash", task1.Status)
	}
	if task2.Status != TaskStatusFailedCrash {
		t.Errorf("t2: got %q want failed_crash", task2.Status)
	}
	if task3.Status != TaskStatusPending {
		t.Errorf("t3: got %q want pending (untouched)", task3.Status)
	}
}

func TestLoomEngine_Cancel(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	// Worker that blocks until context is cancelled.
	cancelCh := make(chan struct{})
	worker := &blockingWorker{done: cancelCh}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	taskID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj",
		Prompt:     "block",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait until the worker is actually running.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, _ := store.Get(taskID)
		if task.Status == TaskStatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Cancel should succeed while task is running.
	if err := engine.Cancel(taskID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	// Signal the blocking worker to unblock so the goroutine can clean up.
	select {
	case cancelCh <- struct{}{}:
	case <-time.After(time.Second):
	}

	// Wait for task to reach terminal state after cancellation.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, _ := store.Get(taskID)
		if task != nil && task.Status.IsTerminal() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLoomEngine_Cancel_NotFound(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	err := engine.Cancel("nonexistent-task-id")
	if err == nil {
		t.Error("expected error cancelling non-existent task")
	}
}

func TestLoomEngine_Get(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	worker := &testWorker{wtype: WorkerTypeCLI, result: "output"}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	taskID, _ := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj",
		Prompt:     "query",
	})

	task, err := engine.Get(taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.ID != taskID {
		t.Errorf("ID mismatch: got %q want %q", task.ID, taskID)
	}
}

func TestLoomEngine_List(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	// Use a slow worker so tasks stay in running state long enough to observe.
	worker := &testWorker{wtype: WorkerTypeCLI, result: "done", delay: 500 * time.Millisecond}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	_, _ = engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "proj", Prompt: "1"})
	_, _ = engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "proj", Prompt: "2"})
	_, _ = engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "other", Prompt: "3"})

	// Wait for tasks to reach dispatched/running.
	time.Sleep(50 * time.Millisecond)

	tasks, err := engine.List("proj")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("List proj: got %d, want 2", len(tasks))
	}

	other, err := engine.List("other")
	if err != nil {
		t.Fatalf("List other: %v", err)
	}
	if len(other) != 1 {
		t.Errorf("List other: got %d, want 1", len(other))
	}
}

// blockingWorker blocks until either ctx is cancelled or done channel receives.
type blockingWorker struct {
	started chan struct{}
	done    chan struct{}
}

func (w *blockingWorker) Execute(ctx context.Context, _ *Task) (*WorkerResult, error) {
	if w.started != nil {
		select {
		case w.started <- struct{}{}:
		default:
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.done:
		return &WorkerResult{Content: "cancelled"}, nil
	}
}

func (w *blockingWorker) Type() WorkerType { return WorkerTypeCLI }

func TestLoomEngine_ExecSurvivesDisconnect(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	// Worker that takes 200ms to complete.
	worker := &testWorker{wtype: WorkerTypeCLI, result: "survived", delay: 200 * time.Millisecond}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	// Submit with a context we'll cancel (simulating session disconnect).
	ctx, cancel := context.WithCancel(context.Background())
	taskID, err := engine.Submit(ctx, TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-disconnect",
		Prompt:     "do work",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Cancel context immediately (simulate CC session disconnect).
	cancel()

	// Wait for task to complete despite context cancellation.
	// The key property: LoomEngine creates a task-scoped context (FR-4),
	// NOT derived from the caller's context.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, _ := store.Get(taskID)
		if task != nil && task.Status == TaskStatusCompleted {
			if task.Result != "survived" {
				t.Errorf("result: got %q want survived", task.Result)
			}
			return // SUCCESS
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("task did not complete after context cancellation (disconnect)")
}

func TestNewEngine_ConstructsFromSqlDB(t *testing.T) {
	// Verify NewEngine creates a usable *LoomEngine from a raw *sql.DB.
	db := newTestDB(t)
	engine, err := NewEngine(db, "test")
	if err != nil {
		t.Fatalf("NewEngine(db, \"test\"): %v", err)
	}
	if engine == nil {
		t.Fatal("NewEngine returned nil engine")
	}

	// Verify injected deps are observable: FakeClock + SequentialIDGenerator.
	frozen := time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC)
	fake := deps.NewFakeClock(frozen)
	seq := deps.NewSequentialIDGenerator()

	engine2, err := NewEngine(db, "test", WithClock(fake), WithIDGenerator(seq))
	if err != nil {
		t.Fatalf("NewEngine(db, \"test\", WithClock, WithIDGenerator): %v", err)
	}
	if engine2.clock != fake {
		t.Error("NewEngine: WithClock not applied")
	}
	if engine2.idGen != seq {
		t.Error("NewEngine: WithIDGenerator not applied")
	}

	// Behavioral: verify that Submit uses the injected idGen and clock.
	taskID, err := engine2.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-di",
		Prompt:     "check injected deps",
	})
	if err != nil {
		t.Fatalf("Submit with injected deps: %v", err)
	}
	if taskID != "id-0" {
		t.Fatalf("Submit ID = %q; want %q (from SequentialIDGenerator)", taskID, "id-0")
	}
	task, err := engine2.Get(taskID)
	if err != nil {
		t.Fatalf("Get after Submit: %v", err)
	}
	if !task.CreatedAt.Equal(frozen) {
		t.Fatalf("CreatedAt = %v; want %v (from FakeClock)", task.CreatedAt, frozen)
	}

	// NewEngine(nil) must return an error (db is nil).
	if _, err := NewEngine(nil, "test"); err == nil {
		t.Error("NewEngine(nil, \"test\") should return error; got nil")
	}
}

func TestLoomEngine_AgentSurvivesDisconnect(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	// Simulate agent worker (same interface, just different type).
	worker := &testWorker{wtype: WorkerTypeCLI, result: "agent output", delay: 200 * time.Millisecond}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	ctx, cancel := context.WithCancel(context.Background())
	taskID, err := engine.Submit(ctx, TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-agent-disconnect",
		Prompt:     "agent task",
		Metadata:   map[string]any{"agent": "test-agent"},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Disconnect immediately.
	cancel()

	// Task must still complete.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, _ := store.Get(taskID)
		if task != nil && task.Status == TaskStatusCompleted {
			if task.Result != "agent output" {
				t.Errorf("result: got %q want agent output", task.Result)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("agent task did not complete after disconnect")
}

// ---- CancelAllForProject tests ----

// TestLoomEngine_CancelAllForProject cancels running tasks in project A but not B.
// Uses a single engine with a single store; both project A and project B tasks are
// submitted to the same engine. CancelAllForProject("proj-A") should only signal
// the 3 proj-A cancel funcs, leaving the 2 proj-B goroutines running.
func TestLoomEngine_CancelAllForProject(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	// A shared blocking worker. The blockingWorker blocks on ctx.Done or done channel.
	// We'll send on doneAll to unblock all goroutines at cleanup.
	doneAll := make(chan struct{})
	worker := &blockingWorker{done: doneAll}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	// Submit 3 tasks to project A.
	var taskIDsA []string
	for i := 0; i < 3; i++ {
		id, err := engine.Submit(context.Background(), TaskRequest{
			WorkerType: WorkerTypeCLI,
			ProjectID:  "proj-A-cancel",
			Prompt:     "block",
		})
		if err != nil {
			t.Fatalf("Submit A[%d]: %v", i, err)
		}
		taskIDsA = append(taskIDsA, id)
	}

	// Submit 2 tasks to project B in the SAME engine/store.
	var taskIDsB []string
	for i := 0; i < 2; i++ {
		id, err := engine.Submit(context.Background(), TaskRequest{
			WorkerType: WorkerTypeCLI,
			ProjectID:  "proj-B-cancel",
			Prompt:     "block",
		})
		if err != nil {
			t.Fatalf("Submit B[%d]: %v", i, err)
		}
		taskIDsB = append(taskIDsB, id)
	}

	// Wait until all 5 tasks are running.
	allIDs := append(taskIDsA, taskIDsB...)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		running := 0
		for _, id := range allIDs {
			task, _ := store.Get(id)
			if task != nil && task.Status == TaskStatusRunning {
				running++
			}
		}
		if running == 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Verify all 5 are running before cancelling.
	for _, id := range allIDs {
		task, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		if task.Status != TaskStatusRunning {
			t.Fatalf("expected task %s to be running before cancel, got %s", id, task.Status)
		}
	}

	// CancelAllForProject("proj-A-cancel") must return 3.
	count, err := engine.CancelAllForProject("proj-A-cancel")
	if err != nil {
		t.Fatalf("CancelAllForProject: %v", err)
	}
	if count != 3 {
		t.Errorf("CancelAllForProject returned %d, want 3", count)
	}

	// Unblock all remaining goroutines so the test can clean up.
	// proj-A goroutines may already be unblocked via ctx cancellation;
	// these sends handle any that haven't exited yet and unblock proj-B goroutines.
	for i := 0; i < 5; i++ {
		select {
		case doneAll <- struct{}{}:
		case <-time.After(200 * time.Millisecond):
			// Goroutine already exited via context cancellation — that's fine.
		}
	}
}

// TestLoomEngine_CancelAllForProject_Empty returns 0 when no tasks are running.
func TestLoomEngine_CancelAllForProject_Empty(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	count, err := engine.CancelAllForProject("proj-nonexistent")
	if err != nil {
		t.Fatalf("CancelAllForProject: %v", err)
	}
	if count != 0 {
		t.Errorf("CancelAllForProject on empty project: got %d, want 0", count)
	}
}

// TestLoomEngine_Submit_WithRequestID verifies RequestID round-trips through Submit→Get.
func TestLoomEngine_Submit_WithRequestID(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	worker := &testWorker{wtype: WorkerTypeCLI, result: "ok"}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	ctx := WithRequestID(context.Background(), "trace-req-42")
	taskID, err := engine.Submit(ctx, TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-trace",
		Prompt:     "test",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	task, err := store.Get(taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.RequestID != "trace-req-42" {
		t.Errorf("task.RequestID = %q, want \"trace-req-42\"", task.RequestID)
	}
}

// TestLoomEngine_Events_Subscribe verifies that the EventBus returned by Events()
// delivers lifecycle events to a registered subscriber.
func TestLoomEngine_Events_Subscribe(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	worker := &testWorker{wtype: WorkerTypeCLI, result: "output"}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	var received []EventType
	var mu sync.Mutex

	engine.Events().Subscribe(func(e TaskEvent) {
		mu.Lock()
		received = append(received, e.Type)
		mu.Unlock()
	})

	taskID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-events",
		Prompt:     "test",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for completion.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, _ := store.Get(taskID)
		if task != nil && task.Status == TaskStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Allow a short window for the final event to propagate.
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Minimum expected sequence: Created, Dispatched, Running, Completed.
	wantContains := []EventType{EventTaskCreated, EventTaskDispatched, EventTaskRunning, EventTaskCompleted}
	receivedSet := make(map[EventType]bool, len(received))
	for _, et := range received {
		receivedSet[et] = true
	}
	for _, want := range wantContains {
		if !receivedSet[want] {
			t.Errorf("expected event %q not received; got %v", want, received)
		}
	}
}
