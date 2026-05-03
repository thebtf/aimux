package loom

import (
	"context"
	"testing"
	"time"
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
	engine := New(store)
	oldTask := makeTask("admin-stale", "proj-admin", TaskStatusRunning)
	oldTask.CreatedAt = time.Now().UTC().Add(-30 * time.Minute)
	freshTask := makeTask("admin-fresh", "proj-admin", TaskStatusRunning)
	freshTask.CreatedAt = time.Now().UTC()
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
	engine := New(store)
	dispatchedAt := time.Now().UTC()
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
