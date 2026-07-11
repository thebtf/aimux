package loom

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom/deps"
)

type t014BlockingClock struct {
	mu      sync.Mutex
	now     time.Time
	entered *t013Gate
	release *t013Gate
}

func (c *t014BlockingClock) blockNext(entered, release *t013Gate) {
	c.mu.Lock()
	c.entered = entered
	c.release = release
	c.mu.Unlock()
}

func (c *t014BlockingClock) Now() time.Time {
	c.mu.Lock()
	now := c.now
	entered := c.entered
	release := c.release
	c.entered = nil
	c.release = nil
	c.mu.Unlock()
	if entered != nil {
		entered.open()
		<-release.ch
	}
	return now
}

func (c *t014BlockingClock) Sleep(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

var _ deps.Clock = (*t014BlockingClock)(nil)

type t014ProjectionOrderingWorker struct {
	started          *t013Gate
	release          *t013Gate
	returned         *t013Gate
	contextSignalled *t013Gate
}

func (w *t014ProjectionOrderingWorker) Execute(ctx context.Context, _ *Task) (*WorkerResult, error) {
	go func() {
		<-ctx.Done()
		w.contextSignalled.open()
	}()
	w.started.open()
	<-w.release.ch
	w.returned.open()
	return &WorkerResult{Content: "late result"}, nil
}

func (*t014ProjectionOrderingWorker) Type() WorkerType { return WorkerTypeCLI }

func TestCancelProjectionPrecedesConcurrentWorkerTerminalization(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(*LoomEngine, string) (int, error)
		count  int
	}{
		{
			name: "Cancel",
			cancel: func(engine *LoomEngine, taskID string) (int, error) {
				return 0, engine.Cancel(taskID)
			},
		},
		{
			name: "CancelAllForProject",
			cancel: func(engine *LoomEngine, _ string) (int, error) {
				return engine.CancelAllForProject("projection-order")
			},
			count: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := t013NewFixture(t)
			started := t013NewGate()
			workerRelease := t013NewGate()
			workerReturned := t013NewGate()
			contextSignalled := t013NewGate()
			projectionEntered := t013NewGate()
			projectionRelease := t013NewGate()
			terminalClockEntered := t013NewGate()
			terminalClockRelease := t013NewGate()
			explicitSignal := t013NewGate()
			t.Cleanup(workerRelease.open)
			t.Cleanup(projectionRelease.open)
			t.Cleanup(terminalClockRelease.open)

			worker := &t014ProjectionOrderingWorker{
				started: started, release: workerRelease, returned: workerReturned, contextSignalled: contextSignalled,
			}
			clock := &t014BlockingClock{now: t013At}
			engine := fixture.engine(t, worker, WithClock(clock))
			events := &t013Events{}
			engine.Events().Subscribe(events.record)
			engine.Events().Subscribe(func(event TaskEvent) {
				if event.Type != EventTaskCancelRequested {
					return
				}
				projectionEntered.open()
				<-projectionRelease.ch
			})

			taskID, err := engine.Submit(context.Background(), TaskRequest{
				WorkerType: WorkerTypeCLI,
				ProjectID:  "projection-order",
				RequestID:  "projection-order-request",
				Prompt:     "return while cancellation projection is blocked",
			})
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			t013AwaitGate(t, "worker start", started)

			var projectedAtSignal atomic.Int32
			engine.mu.Lock()
			originalCancel := engine.cancels[taskID]
			if originalCancel == nil {
				engine.mu.Unlock()
				t.Fatalf("missing live cancel func for %s", taskID)
			}
			engine.cancels[taskID] = func() {
				projectedAtSignal.Store(int32(events.count(taskID, EventTaskCancelRequested)))
				originalCancel()
				explicitSignal.open()
			}
			engine.mu.Unlock()

			type cancelResult struct {
				count int
				err   error
			}
			cancelDone := make(chan cancelResult, 1)
			go func() {
				count, cancelErr := tc.cancel(engine, taskID)
				cancelDone <- cancelResult{count: count, err: cancelErr}
			}()

			t013AwaitGate(t, "blocked cancel_requested projection", projectionEntered)
			clock.blockNext(terminalClockEntered, terminalClockRelease)
			workerRelease.open()
			t013AwaitGate(t, "concurrent worker return", workerReturned)
			t013AwaitGate(t, "worker terminalization clock", terminalClockEntered)

			beforeRelease, getErr := fixture.view.Get(taskID)
			if getErr != nil || beforeRelease.Status != TaskStatusCancelling {
				t.Fatalf("task before projection release=%#v err=%v, want cancelling", beforeRelease, getErr)
			}
			if got := t013ArtifactCountByEvent(t, fixture.view, taskID, "task.failed_crash"); got != 0 {
				t.Fatalf("failed_crash facts before projection release=%d, want 0", got)
			}
			if got := events.count(taskID, EventTaskFailedCrash); got != 0 {
				t.Fatalf("failed_crash events before projection release=%d, want 0", got)
			}
			if t013Signalled(explicitSignal.ch) || t013Signalled(contextSignalled.ch) {
				t.Fatal("worker was signalled before cancel_requested projection completed")
			}

			terminalClockRelease.open()
			for attempts := 0; attempts < 65536 && !t013Signalled(contextSignalled.ch); attempts++ {
				runtime.Gosched()
			}
			beforeRelease, getErr = fixture.view.Get(taskID)
			if getErr != nil || beforeRelease.Status != TaskStatusCancelling {
				t.Fatalf("task after concurrent terminalization=%#v err=%v, want cancelling until projection release", beforeRelease, getErr)
			}
			if got := t013ArtifactCountByEvent(t, fixture.view, taskID, "task.failed_crash"); got != 0 {
				t.Fatalf("failed_crash facts after concurrent terminalization=%d, want 0 before projection release", got)
			}
			if got := events.count(taskID, EventTaskFailedCrash); got != 0 {
				t.Fatalf("failed_crash events after concurrent terminalization=%d, want 0 before projection release", got)
			}
			if t013Signalled(explicitSignal.ch) || t013Signalled(contextSignalled.ch) {
				t.Fatal("worker was signalled before cancel_requested projection completed")
			}

			projectionRelease.open()
			select {
			case result := <-cancelDone:
				if result.err != nil || result.count != tc.count {
					t.Fatalf("cancel result=%d/%v, want %d/nil", result.count, result.err, tc.count)
				}
			case <-time.After(t013Wait):
				t.Fatal("cancel did not complete after projection release")
			}
			t013AwaitGate(t, "explicit worker signal", explicitSignal)
			t013AwaitGate(t, "worker context cancellation", contextSignalled)
			if got := projectedAtSignal.Load(); got != 1 {
				t.Fatalf("cancel_requested events visible at signal=%d, want 1", got)
			}

			t013Close(t, engine)
			finalTask, getErr := fixture.view.Get(taskID)
			if getErr != nil || finalTask.Status != TaskStatusFailedCrash {
				t.Fatalf("final task=%#v err=%v, want failed_crash", finalTask, getErr)
			}
			t013AssertTypes(t, t013EventTypes(events.snapshot(t), taskID),
				EventTaskCreated,
				EventTaskDispatched,
				EventTaskRunning,
				EventTaskCancelRequested,
				EventTaskFailedCrash,
			)
			artifacts := t013Artifacts(t, fixture.view, taskID)
			artifactEvents := make([]EventType, 0, len(artifacts))
			for _, artifact := range artifacts {
				artifactEvents = append(artifactEvents, EventType(artifact.EventType))
			}
			t013AssertTypes(t, artifactEvents,
				EventTaskCreated,
				EventTaskDispatched,
				EventTaskRunning,
				EventTaskCancelRequested,
				EventTaskFailedCrash,
			)
		})
	}
}
