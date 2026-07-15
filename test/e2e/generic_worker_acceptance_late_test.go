package e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/workerruntime"
)

// t016LateNativeCancelSlack pushes the fake native cancellation return
// strictly past the real cancellation resolution deadline (currently 5s,
// package-private in pkg/swarm) without accessing any package-private
// timing seam: CancelExecution below waits for its ctx to expire and then
// sleeps this much longer before returning.
const t016LateNativeCancelSlack = time.Second

// t016LateBoundedWait bounds every blocking step in this scenario so it can
// never hang, while still comfortably exceeding the real resolution
// deadline plus t016LateNativeCancelSlack.
const t016LateBoundedWait = 15 * time.Second

const (
	t016LateExecutionID             types.ExecutionID = "t016-late"
	t016LateCleanupProbeExecutionID types.ExecutionID = "t016-late-cleanup-probe"
)

// errT016LateProvider is the deliberately non-context.Canceled error the
// fake provider below returns after observing cancellation, so the
// execution classifies as failed rather than cancelled.
var errT016LateProvider = errors.New("t016 late scenario: provider returned after cancellation")

// t016LateCancellationExecutor is a minimal controlled ExecutorV2 that also
// implements EventExecutor and ExecutionCanceller. SendEvents blocks until
// the execution context is cancelled and then returns errT016LateProvider;
// CancelExecution intentionally returns only after the real cancellation
// resolution deadline elapses, simulating a late native acknowledgement.
type t016LateCancellationExecutor struct {
	started        chan struct{}
	cancelStarted  chan struct{}
	nativeReturned chan struct{}
}

func (*t016LateCancellationExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*t016LateCancellationExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*t016LateCancellationExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*t016LateCancellationExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*t016LateCancellationExecutor) Close() error                { return nil }

func (e *t016LateCancellationExecutor) SendEvents(ctx context.Context, id types.ExecutionID, _ types.Message, _ types.ExecutorEventSink) (*types.Response, error) {
	if id == t016LateCleanupProbeExecutionID {
		return &types.Response{}, nil
	}
	close(e.started)
	<-ctx.Done()
	return nil, errT016LateProvider
}

func (e *t016LateCancellationExecutor) CancelExecution(ctx context.Context, id types.ExecutionID, _ string) (types.CancellationEvidence, error) {
	close(e.cancelStarted)
	<-ctx.Done()
	time.Sleep(t016LateNativeCancelSlack)
	evidence := types.CancellationEvidence{ExecutionID: id, NativeAcknowledged: true}
	close(e.nativeReturned)
	return evidence, nil
}

// runT016LateScenario proves that a native cancellation returned after the
// resolution deadline cannot rewrite an already-finalized terminal or
// inspection, that repeated Cancel/Inspect calls keep returning the same
// canonical evidence, and that execution/handle cleanup still completes
// once the late native call actually returns.
func runT016LateScenario(t *testing.T) string {
	t.Helper()

	exec := &t016LateCancellationExecutor{
		started:        make(chan struct{}),
		cancelStarted:  make(chan struct{}),
		nativeReturned: make(chan struct{}),
	}
	s := swarm.New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	runtime, err := workerruntime.New(s)
	if err != nil {
		t.Fatalf("create worker runtime: %v", err)
	}
	h, err := s.Get(context.Background(), "t016-late-worker", swarm.Stateful, swarm.WithScope("t016-late-scope"))
	if err != nil {
		t.Fatalf("get handle: %v", err)
	}

	terminalEvents := make(chan types.ExecutorEvent, 1)
	sink := types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		if event.Terminal {
			terminalEvents <- event
		}
		return true
	})

	execDone := make(chan error, 1)
	go func() {
		_, err := runtime.Execute(context.Background(), h, "t016-late-scope", t016LateExecutionID, types.Message{Content: "late"}, sink)
		execDone <- err
	}()

	select {
	case <-exec.started:
	case <-time.After(t016LateBoundedWait):
		t.Fatal("timed out waiting for execution to start")
	}

	cancelDone := make(chan struct {
		evidence types.CancellationEvidence
		err      error
	}, 1)
	go func() {
		evidence, err := runtime.Cancel(context.Background(), h, "t016-late-scope", t016LateExecutionID, "t016 late scenario cancel")
		cancelDone <- struct {
			evidence types.CancellationEvidence
			err      error
		}{evidence, err}
	}()

	select {
	case <-exec.cancelStarted:
	case <-time.After(t016LateBoundedWait):
		t.Fatal("timed out waiting for native cancellation to start")
	}

	var firstCancel struct {
		evidence types.CancellationEvidence
		err      error
	}
	select {
	case firstCancel = <-cancelDone:
	case <-time.After(t016LateBoundedWait):
		t.Fatal("timed out waiting for Cancel to resolve")
	}
	if !errors.Is(firstCancel.err, context.DeadlineExceeded) {
		t.Fatalf("Cancel error = %v, want context.DeadlineExceeded", firstCancel.err)
	}
	if firstCancel.evidence.ExecutionID != t016LateExecutionID || firstCancel.evidence.NativeAcknowledged {
		t.Fatalf("Cancel evidence = %#v, want ExecutionID=%q NativeAcknowledged=false before the late native return", firstCancel.evidence, t016LateExecutionID)
	}

	var execErr error
	select {
	case execErr = <-execDone:
	case <-time.After(t016LateBoundedWait):
		t.Fatal("timed out waiting for Execute to return")
	}
	if !errors.Is(execErr, errT016LateProvider) {
		t.Fatalf("Execute error = %v, want %v", execErr, errT016LateProvider)
	}

	var terminalEvent types.ExecutorEvent
	select {
	case terminalEvent = <-terminalEvents:
	case <-time.After(t016LateBoundedWait):
		t.Fatal("timed out waiting for the terminal event")
	}
	if terminalEvent.Type != "failed" {
		t.Fatalf("terminal event type = %q, want %q", terminalEvent.Type, "failed")
	}

	// Repeated cancellation and inspection must keep returning the same
	// canonical evidence once the execution is terminal.
	repeatEvidence, repeatErr := runtime.Cancel(context.Background(), h, "t016-late-scope", t016LateExecutionID, "t016 late scenario repeat cancel")
	if repeatEvidence != firstCancel.evidence || !errors.Is(repeatErr, context.DeadlineExceeded) {
		t.Fatalf("repeated Cancel = %#v, %v, want %#v, context.DeadlineExceeded", repeatEvidence, repeatErr, firstCancel.evidence)
	}

	firstInspection, err := runtime.Inspect(context.Background(), h, "t016-late-scope", t016LateExecutionID)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !firstInspection.Terminal || firstInspection.Cancelled || firstInspection.CancellationEvidence.NativeAcknowledged {
		t.Fatalf("Inspect = %#v, want Terminal=true Cancelled=false NativeAcknowledged=false", firstInspection)
	}

	// Wait for the intentionally late native cancellation to actually
	// return, then confirm it could not rewrite the already-finalized
	// terminal or inspection evidence.
	select {
	case <-exec.nativeReturned:
	case <-time.After(t016LateBoundedWait):
		t.Fatal("timed out waiting for the late native cancellation to return")
	}

	secondInspection, err := runtime.Inspect(context.Background(), h, "t016-late-scope", t016LateExecutionID)
	if err != nil {
		t.Fatalf("Inspect after late native return: %v", err)
	}
	if secondInspection != firstInspection {
		t.Fatalf("Inspect after late native return = %#v, want unchanged %#v", secondInspection, firstInspection)
	}

	lateEvidence, lateErr := runtime.Cancel(context.Background(), h, "t016-late-scope", t016LateExecutionID, "t016 late scenario cancel after native return")
	if lateEvidence != firstCancel.evidence || !errors.Is(lateErr, context.DeadlineExceeded) {
		t.Fatalf("Cancel after late native return = %#v, %v, want %#v, context.DeadlineExceeded", lateEvidence, lateErr, firstCancel.evidence)
	}

	// Execution and handle cleanup must still complete: the operation lease
	// held across the in-flight native cancellation only releases once the
	// late CancelExecution call actually returns, so a fresh execution on
	// the same handle eventually succeeds without ErrExecutionActive.
	cleanupSink := types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true })
	cleanupDeadline := time.Now().Add(t016LateBoundedWait)
	var cleanupErr error
	for {
		_, cleanupErr = runtime.Execute(context.Background(), h, "t016-late-scope", t016LateCleanupProbeExecutionID, types.Message{Content: "cleanup-probe"}, cleanupSink)
		if cleanupErr == nil || !errors.Is(cleanupErr, swarm.ErrExecutionActive) || time.Now().After(cleanupDeadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cleanupErr != nil {
		t.Fatalf("post-cleanup Execute = %v, want success once the handle operation lease is released", cleanupErr)
	}

	return terminalEvent.Type
}
