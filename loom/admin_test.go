package loom

import (
	"context"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom/deps"
)

type ignoreCancelWorker struct {
	started chan struct{}
	release chan struct{}
}

func (w *ignoreCancelWorker) Type() WorkerType {
	return WorkerTypeCLI
}

func (w *ignoreCancelWorker) Execute(_ context.Context, _ *Task) (*WorkerResult, error) {
	close(w.started)
	<-w.release
	return &WorkerResult{Content: "late success"}, nil
}

func TestTaskStore_FailActive_PendingToFailed(t *testing.T) {
	store := newTestStore(t)
	task := makeTask("admin-pending", "proj-admin", TaskStatusPending)
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE tasks SET result = ? WHERE id = ? AND engine_name = ?`, "some output", task.ID, store.engineName); err != nil {
		t.Fatalf("seed result: %v", err)
	}

	info, err := store.FailActive(task.ID, "operator cancelled")
	if err != nil {
		t.Fatalf("FailActive: %v", err)
	}
	if info == nil {
		t.Fatal("FailActive returned nil info for active task")
	}
	if info.FromStatus != TaskStatusPending {
		t.Fatalf("FromStatus = %s, want pending", info.FromStatus)
	}
	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != TaskStatusFailed {
		t.Fatalf("Status = %s, want failed", got.Status)
	}
	if got.Error != "operator cancelled" {
		t.Fatalf("Error = %q, want operator cancelled", got.Error)
	}
	if got.Result != "" {
		t.Fatalf("Result = %q, want empty after admin failure", got.Result)
	}
}

func TestTaskStore_FailActive_IgnoresTerminal(t *testing.T) {
	store := newTestStore(t)
	task := makeTask("admin-completed", "proj-admin", TaskStatusCompleted)
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	info, err := store.FailActive(task.ID, "should not apply")
	if err != nil {
		t.Fatalf("FailActive: %v", err)
	}
	if info != nil {
		t.Fatalf("FailActive returned info for terminal task: %+v", info)
	}
	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != TaskStatusCompleted {
		t.Fatalf("Status = %s, want completed", got.Status)
	}
}

func TestLoomEngine_FailStaleRunning(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	engine := New(store, WithClock(deps.NewFakeClock(now)))
	oldTask := makeTask("admin-stale", "proj-admin", TaskStatusRunning)
	oldTask.CreatedAt = now.Add(-30 * time.Minute)
	freshTask := makeTask("admin-fresh", "proj-admin", TaskStatusRunning)
	freshTask.CreatedAt = now
	if err := store.Create(oldTask); err != nil {
		t.Fatalf("Create oldTask: %v", err)
	}
	if err := store.Create(freshTask); err != nil {
		t.Fatalf("Create freshTask: %v", err)
	}

	failed, err := engine.FailStaleRunning(15*time.Minute, "stale")
	if err != nil {
		t.Fatalf("FailStaleRunning: %v", err)
	}
	if failed != 1 {
		t.Fatalf("failed = %d, want 1", failed)
	}
	gotOld, err := store.Get(oldTask.ID)
	if err != nil {
		t.Fatalf("Get oldTask: %v", err)
	}
	if gotOld.Status != TaskStatusFailed {
		t.Fatalf("old status = %s, want failed", gotOld.Status)
	}
	gotFresh, err := store.Get(freshTask.ID)
	if err != nil {
		t.Fatalf("Get freshTask: %v", err)
	}
	if gotFresh.Status != TaskStatusRunning {
		t.Fatalf("fresh status = %s, want running", gotFresh.Status)
	}
}

func TestLoomEngine_FailStaleRunningUsesDispatchedAtWithoutProgress(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	engine := New(store, WithClock(deps.NewFakeClock(now)))
	dispatchedAt := now
	task := makeTask("admin-fresh-dispatch", "proj-admin", TaskStatusRunning)
	task.CreatedAt = dispatchedAt.Add(-30 * time.Minute)
	task.DispatchedAt = &dispatchedAt
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	failed, err := engine.FailStaleRunning(15*time.Minute, "stale")
	if err != nil {
		t.Fatalf("FailStaleRunning: %v", err)
	}
	if failed != 0 {
		t.Fatalf("failed = %d, want 0", failed)
	}
	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != TaskStatusRunning {
		t.Fatalf("status = %s, want running", got.Status)
	}
}

func TestLoomEngine_FailStaleRunningUsesProgressUpdatedAtOverDispatchedAt(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	engine := New(store, WithClock(deps.NewFakeClock(now)))
	dispatchedAt := now.Add(-30 * time.Minute)
	task := makeTask("admin-fresh-progress", "proj-admin", TaskStatusRunning)
	task.CreatedAt = dispatchedAt.Add(-30 * time.Minute)
	task.DispatchedAt = &dispatchedAt
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if info, err := store.AppendProgress(task.ID, "still running"); err != nil {
		t.Fatalf("AppendProgress: %v", err)
	} else if !info.OK {
		t.Fatal("AppendProgress OK = false, want true")
	}

	failed, err := engine.FailStaleRunning(15*time.Minute, "stale")
	if err != nil {
		t.Fatalf("FailStaleRunning: %v", err)
	}
	if failed != 0 {
		t.Fatalf("failed = %d, want 0", failed)
	}
	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != TaskStatusRunning {
		t.Fatalf("status = %s, want running", got.Status)
	}
}

func TestLoomEngine_FailActiveDoesNotCancelWhenStoreUpdateFails(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	task := makeTask("admin-store-fail", "proj-admin", TaskStatusRunning)
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cancelled := make(chan struct{})
	engine.mu.Lock()
	engine.cancels[task.ID] = func() { close(cancelled) }
	engine.mu.Unlock()

	if err := store.db.Close(); err != nil {
		t.Fatalf("Close db: %v", err)
	}
	ok, err := engine.FailActive(task.ID, "store unavailable")
	if err == nil {
		t.Fatal("FailActive error = nil, want store error")
	}
	if ok {
		t.Fatal("FailActive ok = true, want false when store update fails")
	}
	select {
	case <-cancelled:
		t.Fatal("cancel called before persistent failure was recorded")
	default:
	}
}

func TestLoomEngine_FailActivePreventsLateWorkerResultOverwrite(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	worker := &ignoreCancelWorker{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	taskID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-admin",
		Prompt:     "ignore cancel",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case <-worker.started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start")
	}

	ok, err := engine.FailActive(taskID, "operator cancelled")
	if err != nil {
		t.Fatalf("FailActive: %v", err)
	}
	if !ok {
		t.Fatal("FailActive returned false for running task")
	}
	close(worker.release)

	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := engine.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := store.Get(taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != TaskStatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	if got.Error != "operator cancelled" {
		t.Fatalf("error = %q, want operator cancelled", got.Error)
	}
	if got.Result != "" {
		t.Fatalf("result = %q, want empty after admin failure", got.Result)
	}
}
