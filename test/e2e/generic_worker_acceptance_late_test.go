package e2e

import (
	"context"
	"errors"
	"sync"
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

	// t016LateProof and t016LateFixture bind this runner to the exact
	// manifest row it implements; a manifest drift fails loudly here
	// instead of silently exercising the wrong fixture.
	t016LateProof   = "late_output"
	t016LateFixture = "in-process-late-output"

	// t016LateStdoutContent is the known late output the fake native
	// canceller attempts to admit after the resolution deadline.
	t016LateStdoutContent = "t016-late-stdout-after-deadline"
)

// errT016LateProvider is the deliberately non-context.Canceled error the
// fake provider below returns after observing cancellation, so the
// execution classifies as failed rather than cancelled.
var errT016LateProvider = errors.New("t016 late scenario: provider returned after cancellation")

// t016LateCancellationExecutor is a minimal controlled ExecutorV2 that also
// implements EventExecutor and ExecutionCanceller. SendEvents blocks until
// the execution context is cancelled and then returns errT016LateProvider;
// CancelExecution intentionally returns only after the real cancellation
// resolution deadline elapses, simulating a late native acknowledgement,
// and attempts one known late stdout admission through the same admission
// sink SendEvents received just before signalling it has returned.
type t016LateCancellationExecutor struct {
	started        chan struct{}
	cancelStarted  chan struct{}
	nativeReturned chan struct{}
	lateAdmission  chan bool

	mu   sync.Mutex
	sink types.ExecutorEventSink
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

func (e *t016LateCancellationExecutor) SendEvents(ctx context.Context, id types.ExecutionID, _ types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	if id == t016LateCleanupProbeExecutionID {
		return &types.Response{}, nil
	}
	e.mu.Lock()
	e.sink = sink
	e.mu.Unlock()
	close(e.started)
	<-ctx.Done()
	return nil, errT016LateProvider
}

func (e *t016LateCancellationExecutor) CancelExecution(ctx context.Context, id types.ExecutionID, _ string) (types.CancellationEvidence, error) {
	close(e.cancelStarted)
	<-ctx.Done()
	time.Sleep(t016LateNativeCancelSlack)

	e.mu.Lock()
	sink := e.sink
	e.mu.Unlock()
	if sink != nil {
		accepted := sink.TryAdmit(types.ExecutorEvent{Channel: "stdout", Type: "text-only", Content: []byte(t016LateStdoutContent)})
		e.lateAdmission <- accepted
	}

	evidence := types.CancellationEvidence{ExecutionID: id, NativeAcknowledged: true}
	close(e.nativeReturned)
	return evidence, nil
}

// t016LateSink is the outer, test-level ExecutorEventSink passed to
// WorkerRuntime.Execute. It keeps a mutex-guarded copy of every delivered
// event and publishes only the first terminal event on a buffered,
// non-blocking channel, so a hypothetical duplicate terminal admission can
// never block the sender.
type t016LateSink struct {
	mu       sync.Mutex
	events   []types.ExecutorEvent
	once     sync.Once
	terminal chan types.ExecutorEvent
}

func newT016LateSink() *t016LateSink {
	return &t016LateSink{terminal: make(chan types.ExecutorEvent, 1)}
}

func (s *t016LateSink) TryAdmit(event types.ExecutorEvent) bool {
	copied := event
	if event.Content != nil {
		copied.Content = append([]byte(nil), event.Content...)
	}
	s.mu.Lock()
	s.events = append(s.events, copied)
	s.mu.Unlock()
	if event.Terminal {
		s.once.Do(func() {
			select {
			case s.terminal <- copied:
			default:
			}
		})
	}
	return true
}

func (s *t016LateSink) snapshot() []types.ExecutorEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.ExecutorEvent, len(s.events))
	copy(out, s.events)
	return out
}

// runT016LateScenario proves that a native cancellation returned after the
// resolution deadline cannot rewrite an already-finalized terminal or
// inspection, that a late output admission attempt through the same
// admission sink is rejected and never reaches the outer sink, that
// repeated Cancel/Inspect calls keep returning the same canonical
// evidence, and that execution/handle cleanup still completes once the
// late native call actually returns.
func runT016LateScenario(t *testing.T, spec t016ScenarioSpec) string {
	t.Helper()

	if spec.Proof != t016LateProof {
		t.Fatalf("late scenario proof = %q, want %q", spec.Proof, t016LateProof)
	}
	if spec.Input.Fixture != t016LateFixture {
		t.Fatalf("late scenario fixture = %q, want %q", spec.Input.Fixture, t016LateFixture)
	}

	exec := &t016LateCancellationExecutor{
		started:        make(chan struct{}),
		cancelStarted:  make(chan struct{}),
		nativeReturned: make(chan struct{}),
		lateAdmission:  make(chan bool, 1),
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

	sink := newT016LateSink()

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
	case terminalEvent = <-sink.terminal:
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
	// return. Just before it does, it attempts a known stdout event
	// through the same admission sink SendEvents received; that attempt
	// must be rejected because the execution is already terminal, and must
	// not rewrite the already-finalized terminal or inspection evidence.
	select {
	case <-exec.nativeReturned:
	case <-time.After(t016LateBoundedWait):
		t.Fatal("timed out waiting for the late native cancellation to return")
	}

	select {
	case accepted := <-exec.lateAdmission:
		if accepted {
			t.Fatal("late stdout admission = true, want false")
		}
	case <-time.After(t016LateBoundedWait):
		t.Fatal("timed out waiting for the late stdout admission result")
	}

	// Execution and handle cleanup must still complete: the operation lease
	// held across the in-flight native cancellation only releases once the
	// late CancelExecution call actually returns, so a fresh execution on
	// the same handle eventually succeeds without ErrExecutionActive. This
	// probe is bounded by an explicit context deadline; the retry loop runs
	// in its own goroutine and the caller only ever blocks in a select
	// against that same deadline.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), t016LateBoundedWait)
	defer cleanupCancel()
	cleanupSink := types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true })
	cleanupDone := make(chan error, 1)
	go func() {
		for {
			_, probeErr := runtime.Execute(cleanupCtx, h, "t016-late-scope", t016LateCleanupProbeExecutionID, types.Message{Content: "cleanup-probe"}, cleanupSink)
			if probeErr == nil || !errors.Is(probeErr, swarm.ErrExecutionActive) {
				cleanupDone <- probeErr
				return
			}
			select {
			case <-cleanupCtx.Done():
				cleanupDone <- probeErr
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	var cleanupErr error
	select {
	case cleanupErr = <-cleanupDone:
	case <-cleanupCtx.Done():
		t.Fatal("timed out waiting for post-cleanup Execute")
	}
	if cleanupErr != nil {
		t.Fatalf("post-cleanup Execute = %v, want success once the handle operation lease is released", cleanupErr)
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

	// Exactly one event (the terminal failed event) must ever have reached
	// the outer sink; the late stdout admission attempt must not have
	// leaked through.
	delivered := sink.snapshot()
	if len(delivered) != 1 {
		t.Fatalf("delivered events = %#v, want exactly 1", delivered)
	}
	if !delivered[0].Terminal || delivered[0].Type != "failed" {
		t.Fatalf("delivered[0] = %#v, want the single terminal failed event", delivered[0])
	}
	for _, event := range delivered {
		if event.Channel == "stdout" {
			t.Fatalf("late stdout leaked into the outer sink: %#v", event)
		}
	}

	return terminalEvent.Type
}
