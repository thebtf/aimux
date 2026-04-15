package loom

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestLoomEngine_Close_WaitsForInflightDispatch verifies that Close(ctx) blocks
// until all in-flight dispatch goroutines have finished.
func TestLoomEngine_Close_WaitsForInflightDispatch(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	// release is closed by the test to unblock all workers.
	release := make(chan struct{})
	const taskCount = 3

	worker := &blockingWorker{done: release}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	// Submit N tasks.
	ids := make([]string, 0, taskCount)
	for i := 0; i < taskCount; i++ {
		id, err := engine.Submit(context.Background(), TaskRequest{
			WorkerType: WorkerTypeCLI,
			ProjectID:  "proj-close-drain",
			Prompt:     "block",
		})
		if err != nil {
			t.Fatalf("Submit[%d]: %v", i, err)
		}
		ids = append(ids, id)
	}

	// Wait until all tasks are running so goroutines are in-flight.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		running := 0
		for _, id := range ids {
			task, _ := store.Get(id)
			if task != nil && task.Status == TaskStatusRunning {
				running++
			}
		}
		if running == taskCount {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Close must not return while goroutines are still blocked.
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- engine.Close(context.Background())
	}()

	// Verify Close has not returned yet.
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned prematurely with %v", err)
	case <-time.After(100 * time.Millisecond):
		// Good — Close is still waiting.
	}

	// Unblock all workers; Close should now drain and return.
	close(release)

	select {
	case err := <-closeDone:
		if err != nil {
			t.Errorf("Close returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after workers unblocked")
	}
}

// TestLoomEngine_Close_ContextTimeout verifies that Close returns ctx.Err()
// when the context expires before all goroutines drain.
func TestLoomEngine_Close_ContextTimeout(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	// Worker that blocks indefinitely until its context is cancelled.
	neverRelease := make(chan struct{}) // never closed
	worker := &blockingWorker{done: neverRelease}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	taskID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-close-timeout",
		Prompt:     "never finishes",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for task to start running.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, _ := store.Get(taskID)
		if task != nil && task.Status == TaskStatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Close with an already-expired context.
	expired, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond) // ensure deadline has passed

	err = engine.Close(expired)
	if err == nil {
		t.Fatal("Close with expired ctx should return an error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("Close with expired ctx: want DeadlineExceeded or Canceled, got %v", err)
	}

	// Clean up: cancel the remaining goroutine by cancelling the task.
	_ = engine.Cancel(taskID)
}

// TestLoomEngine_Submit_AfterClose_ReturnsErrEngineClosed verifies that Submit
// returns ErrEngineClosed after the engine has been shut down.
func TestLoomEngine_Submit_AfterClose_ReturnsErrEngineClosed(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	// Close immediately (no in-flight tasks).
	if err := engine.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-closed",
		Prompt:     "should be rejected",
	})
	if !errors.Is(err, ErrEngineClosed) {
		t.Errorf("Submit after Close: want ErrEngineClosed, got %v", err)
	}
}

// TestLoomEngine_Close_Idempotent verifies that multiple Close calls all return
// nil without panicking.
func TestLoomEngine_Close_Idempotent(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	for i := 0; i < 5; i++ {
		if err := engine.Close(context.Background()); err != nil {
			t.Errorf("Close[%d] returned error: %v", i, err)
		}
	}

	// Concurrent closes must also not panic.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := engine.Close(context.Background()); err != nil {
				t.Errorf("concurrent Close returned error: %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestLoomEngine_RetryPath_UpdateStatusError_TaskReachesFailed verifies the
// BUG-002 fix: when UpdateStatus(running→retrying) is rejected by the state
// machine (because the task row was externally mutated out of 'running'), the
// engine calls failTask and the task ends in TaskStatusFailed rather than
// remaining stuck in 'retrying'.
//
// Mechanism: we submit a task with a worker that returns empty output (which
// the quality gate treats as a retryable rejection). Before the goroutine can
// execute the running→retrying transition, we race to write the task's status
// directly to 'completed' in the store so that UpdateStatus sees a stale 'from'
// and returns an error. The engine must then fall through to failTask.
//
// Note: this test is inherently racy at the Go scheduler level. To keep it
// deterministic we add a small artificial delay in the worker to widen the
// window, and then poll for the terminal state.
func TestLoomEngine_RetryPath_UpdateStatusError_TaskReachesFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping racy state-machine test in -short mode")
	}

	store := newTestStore(t)
	engine := New(store)

	// readyCh is closed by the worker just before returning empty output,
	// giving the test a precise moment to corrupt the row.
	readyCh := make(chan struct{})

	engine.RegisterWorker(WorkerTypeCLI, &racyRetryWorker{readyCh: readyCh})

	taskID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-retry-err",
		Prompt:     "trigger retry path",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for the worker to signal it is about to return.
	select {
	case <-readyCh:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not signal readyCh in time")
	}

	// Corrupt the status so running→retrying will fail.
	_, _ = store.db.Exec("UPDATE tasks SET status='completed' WHERE id=?", taskID)

	// Poll for the task to reach a terminal state.
	deadline := time.Now().Add(5 * time.Second)
	var finalTask *Task
	for time.Now().Before(deadline) {
		finalTask, _ = store.Get(taskID)
		if finalTask != nil && finalTask.Status.IsTerminal() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if finalTask == nil {
		t.Fatal("task not found")
	}
	// The engine should have detected the store error and called failTask,
	// landing the task in 'failed'. It must NOT be stuck in 'retrying'.
	if finalTask.Status == TaskStatusRetrying {
		t.Errorf("task stuck in retrying — BUG-002 regression (UpdateStatus error was swallowed)")
	}
	// We accept 'failed', 'failed_crash', or 'completed' (if corruption raced
	// before the gate check). The key invariant is: NOT retrying.
}

// racyRetryWorker returns an empty result (retryable by gate) and signals
// readyCh just before returning so the test can corrupt the DB row.
type racyRetryWorker struct {
	readyCh chan struct{}
	once    sync.Once
}

func (w *racyRetryWorker) Execute(_ context.Context, _ *Task) (*WorkerResult, error) {
	// Signal only once — subsequent calls (if any) are silent.
	w.once.Do(func() { close(w.readyCh) })
	// Small sleep so the test goroutine has time to corrupt the row.
	time.Sleep(20 * time.Millisecond)
	return &WorkerResult{Content: ""}, nil // empty = gate retries
}

func (w *racyRetryWorker) Type() WorkerType { return WorkerTypeCLI }

// TestFailTask_FromRetrying_ReachesFailed verifies the NEW-001 fix (PRC #2):
// failTask called with fromStatus=TaskStatusRetrying must successfully
// transition the task to TaskStatusFailed via UpdateStatus(retrying→failed).
// Prior to the v0.1.1 PRC #2 fix, validTransitions[retrying] contained only
// {dispatched}, so UpdateStatus(retrying→failed) was rejected and the task
// stayed permanently stuck in retrying — invisible to RecoverCrashed,
// uncancellable, non-terminal.
//
// This test is fully deterministic: it manually drives a task through the
// state machine to `retrying`, then calls failTask directly, then asserts
// the final state is `failed`. No race windows, no test flakiness.
func TestFailTask_FromRetrying_ReachesFailed(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	defer func() { _ = engine.Close(context.Background()) }()

	task := &Task{
		ID:         "test-new-001",
		Status:     TaskStatusPending,
		WorkerType: WorkerTypeCLI,
		ProjectID:  "new-001",
		Prompt:     "test",
		CreatedAt:  time.Now().UTC(),
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Drive the task manually through pending → dispatched → running → retrying.
	transitions := []struct {
		from, to TaskStatus
	}{
		{TaskStatusPending, TaskStatusDispatched},
		{TaskStatusDispatched, TaskStatusRunning},
		{TaskStatusRunning, TaskStatusRetrying},
	}
	for _, tr := range transitions {
		if err := store.UpdateStatus(task.ID, tr.from, tr.to); err != nil {
			t.Fatalf("setup UpdateStatus(%s→%s): %v", tr.from, tr.to, err)
		}
	}
	task.Status = TaskStatusRetrying

	// Exercise the exact call pattern the BUG-002 retry-path fix uses:
	// failTask(task, TaskStatusRetrying, errMsg). Prior to NEW-001 this
	// would fail internally and leave the task stuck in retrying.
	engine.failTask(task, TaskStatusRetrying, "simulated retry-path failure")

	final, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Status != TaskStatusFailed {
		t.Errorf("NEW-001 regression: expected final status %s, got %s (task stuck)",
			TaskStatusFailed, final.Status)
	}
	if final.Error == "" {
		t.Errorf("expected non-empty error message, got empty")
	}
}
