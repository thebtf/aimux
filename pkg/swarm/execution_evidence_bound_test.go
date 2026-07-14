package swarm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aimexecutor "github.com/thebtf/aimux/pkg/executor"
	pipeexecutor "github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/types"
)

type lateNativeCancellationExecutor struct {
	started, cancelStarted, cancelReturned chan struct{}
	releaseCancel                          <-chan struct{}
	lateErr                                error
	closeCalls                             atomic.Int32
}

func (*lateNativeCancellationExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*lateNativeCancellationExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}

func (*lateNativeCancellationExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*lateNativeCancellationExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (e *lateNativeCancellationExecutor) Close() error {
	e.closeCalls.Add(1)
	return nil
}

func (e *lateNativeCancellationExecutor) SendEvents(ctx context.Context, _ types.ExecutionID, _ types.Message, _ types.ExecutorEventSink) (*types.Response, error) {
	close(e.started)
	<-ctx.Done()
	return &types.Response{ExitCode: 130, Error: types.NewExecutorError("cancelled", ctx.Err(), "")}, nil
}

func (e *lateNativeCancellationExecutor) CancelExecution(_ context.Context, id types.ExecutionID, _ string) (types.CancellationEvidence, error) {
	defer close(e.cancelReturned)
	close(e.cancelStarted)
	<-e.releaseCancel
	return types.CancellationEvidence{ExecutionID: id, NativeAcknowledged: true}, e.lateErr
}

func TestSwarmCancellationResolutionDiscardsLateNativeResult(t *testing.T) {
	previousTimeout := cancellationResolutionTimeout
	cancellationResolutionTimeout = 50 * time.Millisecond
	t.Cleanup(func() { cancellationResolutionTimeout = previousTimeout })
	releaseCancel := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseCancel:
		default:
			close(releaseCancel)
		}
	})
	lateErr := errors.New("late native cancellation result")
	exec := &lateNativeCancellationExecutor{
		started: make(chan struct{}), cancelStarted: make(chan struct{}), cancelReturned: make(chan struct{}), releaseCancel: releaseCancel, lateErr: lateErr,
	}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	s.nativeCancellationGate = make(chan struct{}, 1)
	h, err := s.Get(context.Background(), "late-native", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	const id = types.ExecutionID("late-native")
	terminal := make(chan types.ExecutorEvent, 1)
	runDone := make(chan error, 1)
	go func() {
		_, runErr := s.Execute(context.Background(), h, "scope", id, types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			if event.Terminal {
				terminal <- event
			}
			return true
		}))
		runDone <- runErr
	}()
	<-exec.started
	type cancelResult struct {
		evidence types.CancellationEvidence
		err      error
	}
	firstDone := make(chan cancelResult, 1)
	go func() {
		evidence, cancelErr := s.Cancel(context.Background(), h, "scope", id, "test")
		firstDone <- cancelResult{evidence: evidence, err: cancelErr}
	}()
	<-exec.cancelStarted

	var terminalEvent types.ExecutorEvent
	select {
	case terminalEvent = <-terminal:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal publication did not reach its bounded cancellation deadline")
	}
	beforeLateReturn, err := s.Inspect(context.Background(), h, "scope", id)
	if err != nil {
		t.Fatal(err)
	}
	repeatedEvidence, repeatedErr := s.Cancel(context.Background(), h, "scope", id, "repeat")
	close(releaseCancel)
	<-exec.cancelReturned
	waitForNativeCancellationSlot(t, s)
	var first cancelResult
	select {
	case first = <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first Cancel did not return its canonical result")
	}
	afterLateReturn, err := s.Inspect(context.Background(), h, "scope", id)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case runErr := <-runDone:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("execution did not finish after terminal publication")
	}

	wantEvidence := types.CancellationEvidence{ExecutionID: id}
	if terminalEvent.Type != "failed" || !beforeLateReturn.Terminal || beforeLateReturn.Cancelled || beforeLateReturn != afterLateReturn || beforeLateReturn.CancellationEvidence != wantEvidence || first.evidence != wantEvidence || repeatedEvidence != wantEvidence || afterLateReturn.CancellationEvidence != wantEvidence {
		t.Fatalf("terminal=%#v before=%#v first=%#v repeated=%#v after=%#v", terminalEvent, beforeLateReturn, first.evidence, repeatedEvidence, afterLateReturn)
	}
	if !errors.Is(first.err, context.DeadlineExceeded) || !errors.Is(repeatedErr, context.DeadlineExceeded) || errors.Is(first.err, lateErr) || errors.Is(repeatedErr, lateErr) {
		t.Fatalf("first error=%v repeated error=%v, want canonical deadline and no late error", first.err, repeatedErr)
	}
}

func waitForNativeCancellationSlot(t *testing.T, s *Swarm) {
	t.Helper()
	select {
	case s.nativeCancellationGate <- struct{}{}:
		<-s.nativeCancellationGate
	case <-time.After(2 * time.Second):
		t.Fatal("native cancellation slot was not released")
	}
}

func waitForTransferredOperationRelease(t *testing.T, h *Handle) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.acquireOperation(ctx); err != nil {
		t.Fatalf("transferred handle operation lease was not released: %v", err)
	}
	h.releaseOperation()
}

func TestSwarmNativeCancellationTransfersHandleLeaseUntilProviderReturn(t *testing.T) {
	previousTimeout := cancellationResolutionTimeout
	cancellationResolutionTimeout = 25 * time.Millisecond
	t.Cleanup(func() { cancellationResolutionTimeout = previousTimeout })

	for _, tt := range []struct {
		name string
		mode SpawnMode
	}{
		{name: "stateless", mode: Stateless},
		{name: "stateful", mode: Stateful},
		{name: "persistent", mode: Persistent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			releaseCancel := make(chan struct{})
			t.Cleanup(func() {
				select {
				case <-releaseCancel:
				default:
					close(releaseCancel)
				}
			})
			exec := &lateNativeCancellationExecutor{
				started: make(chan struct{}), cancelStarted: make(chan struct{}), cancelReturned: make(chan struct{}), releaseCancel: releaseCancel,
			}
			s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
			s.nativeCancellationGate = make(chan struct{}, 1)
			h, err := s.Get(context.Background(), "lease-transfer-"+tt.name, tt.mode, WithScope("scope"))
			if err != nil {
				t.Fatal(err)
			}
			id := types.ExecutionID("lease-transfer-" + tt.name)
			terminal := make(chan types.ExecutorEvent, 1)
			runDone := make(chan error, 1)
			go func() {
				_, runErr := s.Execute(context.Background(), h, "scope", id, types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
					if event.Terminal {
						terminal <- event
					}
					return true
				}))
				runDone <- runErr
			}()
			<-exec.started

			evidence, cancelErr := s.Cancel(context.Background(), h, "scope", id, "handoff")
			if !errors.Is(cancelErr, context.DeadlineExceeded) || evidence != (types.CancellationEvidence{ExecutionID: id}) {
				t.Fatalf("Cancel = %#v, %v; want bounded deadline result", evidence, cancelErr)
			}
			<-exec.cancelStarted
			select {
			case event := <-terminal:
				if event.Type != "failed" {
					t.Fatalf("terminal = %#v, want failed", event)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("terminal publication waited for provider return")
			}
			select {
			case runErr := <-runDone:
				if runErr != nil {
					t.Fatal(runErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Execute waited for provider return")
			}
			if got := exec.closeCalls.Load(); got != 0 {
				t.Fatalf("Close calls before provider return = %d, want 0", got)
			}
			if h.tryAcquireOperation() {
				h.releaseOperation()
				t.Fatal("handle operation lease was released before provider return")
			}

			close(releaseCancel)
			<-exec.cancelReturned
			waitForNativeCancellationSlot(t, s)
			waitForTransferredOperationRelease(t, h)
			wantCloseCalls := int32(0)
			if tt.mode == Stateless {
				wantCloseCalls = 1
			}
			if got := exec.closeCalls.Load(); got != wantCloseCalls {
				t.Fatalf("Close calls after provider return = %d, want %d", got, wantCloseCalls)
			}
		})
	}
}

func TestSwarmNativeCancellationReturnedBeforeExecuteEndUsesImmediateStatelessClose(t *testing.T) {
	previousTimeout := cancellationResolutionTimeout
	cancellationResolutionTimeout = 200 * time.Millisecond
	t.Cleanup(func() { cancellationResolutionTimeout = previousTimeout })

	releaseCancel := make(chan struct{})
	close(releaseCancel)
	exec := &lateNativeCancellationExecutor{
		started: make(chan struct{}), cancelStarted: make(chan struct{}), cancelReturned: make(chan struct{}), releaseCancel: releaseCancel,
	}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	s.nativeCancellationGate = make(chan struct{}, 1)
	h, err := s.Get(context.Background(), "immediate-native", Stateless, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	const id = types.ExecutionID("immediate-native")
	runDone := make(chan error, 1)
	go func() {
		_, runErr := s.Execute(context.Background(), h, "scope", id, types.Message{}, types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true }))
		runDone <- runErr
	}()
	<-exec.started
	evidence, cancelErr := s.Cancel(context.Background(), h, "scope", id, "immediate")
	if cancelErr != nil || evidence.ExecutionID != id || !evidence.NativeAcknowledged {
		t.Fatalf("Cancel = %#v, %v; want immediate native acknowledgement", evidence, cancelErr)
	}
	<-exec.cancelReturned
	select {
	case runErr := <-runDone:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not finish")
	}
	waitForNativeCancellationSlot(t, s)
	waitForTransferredOperationRelease(t, h)
	if got := exec.closeCalls.Load(); got != 1 {
		t.Fatalf("Close calls = %d, want exactly 1", got)
	}
}

type deadlineNativeCancellationExecutor struct {
	started chan struct{}
}

func (*deadlineNativeCancellationExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*deadlineNativeCancellationExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}

func (*deadlineNativeCancellationExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*deadlineNativeCancellationExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*deadlineNativeCancellationExecutor) Close() error                { return nil }
func (e *deadlineNativeCancellationExecutor) SendEvents(ctx context.Context, _ types.ExecutionID, _ types.Message, _ types.ExecutorEventSink) (*types.Response, error) {
	close(e.started)
	<-ctx.Done()
	return &types.Response{ExitCode: 130, Error: types.NewExecutorError("cancelled", ctx.Err(), "")}, nil
}

func (*deadlineNativeCancellationExecutor) CancelExecution(_ context.Context, id types.ExecutionID, _ string) (types.CancellationEvidence, error) {
	return types.CancellationEvidence{ExecutionID: id, NativeAcknowledged: true}, nil
}

func TestSwarmCancellationResolutionFinalReadAcceptsAtDeadline(t *testing.T) {
	previousTimeout := cancellationResolutionTimeout
	previousResultHook := beforeNativeCancellationResultSend
	previousDeadlineHook := beforeNativeCancellationDeadlineFinalRead
	cancellationResolutionTimeout = 100 * time.Millisecond
	resultCaptured := make(chan struct{})
	allowResultSend := make(chan struct{})
	var releaseResult sync.Once
	releaseSend := func() { releaseResult.Do(func() { close(allowResultSend) }) }
	beforeNativeCancellationResultSend = func() {
		close(resultCaptured)
		<-allowResultSend
	}
	t.Cleanup(func() {
		releaseSend()
		cancellationResolutionTimeout = previousTimeout
		beforeNativeCancellationResultSend = previousResultHook
		beforeNativeCancellationDeadlineFinalRead = previousDeadlineHook
	})

	exec := &deadlineNativeCancellationExecutor{started: make(chan struct{})}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	s.nativeCancellationGate = make(chan struct{}, 1)
	hookErr := make(chan error, 1)
	beforeNativeCancellationDeadlineFinalRead = func() {
		releaseSend()
		select {
		case s.nativeCancellationGate <- struct{}{}:
			<-s.nativeCancellationGate
		case <-time.After(time.Second):
			hookErr <- errors.New("native result was not sent and its slot released")
		}
	}
	h, err := s.Get(context.Background(), "deadline-native", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	const id = types.ExecutionID("deadline-native")
	terminal := make(chan types.ExecutorEvent, 1)
	runDone := make(chan error, 1)
	go func() {
		_, runErr := s.Execute(context.Background(), h, "scope", id, types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			if event.Terminal {
				terminal <- event
			}
			return true
		}))
		runDone <- runErr
	}()
	<-exec.started
	type cancelResult struct {
		evidence types.CancellationEvidence
		err      error
	}
	cancelDone := make(chan cancelResult, 1)
	go func() {
		evidence, cancelErr := s.Cancel(context.Background(), h, "scope", id, "deadline")
		cancelDone <- cancelResult{evidence: evidence, err: cancelErr}
	}()
	select {
	case <-resultCaptured:
	case <-time.After(2 * time.Second):
		t.Fatal("native cancellation did not return before the deadline")
	}
	result := <-cancelDone
	select {
	case err := <-hookErr:
		t.Fatal(err)
	default:
	}
	if result.err != nil || !result.evidence.NativeAcknowledged || result.evidence.ExecutionID != id {
		t.Fatalf("Cancel = %#v, %v; want acknowledged result captured before deadline", result.evidence, result.err)
	}
	if runErr := <-runDone; runErr != nil {
		t.Fatal(runErr)
	}
	if event := <-terminal; event.Type != "cancelled" {
		t.Fatalf("terminal = %#v, want cancelled", event)
	}
	inspection, err := s.Inspect(context.Background(), h, "scope", id)
	if err != nil || !inspection.Terminal || !inspection.Cancelled || !inspection.CancellationEvidence.NativeAcknowledged {
		t.Fatalf("Inspect = %#v, %v", inspection, err)
	}
}

type cancellationGateExecutor struct {
	executionStarted chan struct{}
	nativeStarted    chan struct{}
	nativeReturned   chan struct{}
	releaseNative    <-chan struct{}
	nativeCalls      atomic.Int32
}

func (*cancellationGateExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*cancellationGateExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}

func (*cancellationGateExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*cancellationGateExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*cancellationGateExecutor) Close() error                { return nil }
func (e *cancellationGateExecutor) SendEvents(ctx context.Context, _ types.ExecutionID, _ types.Message, _ types.ExecutorEventSink) (*types.Response, error) {
	close(e.executionStarted)
	<-ctx.Done()
	return &types.Response{ExitCode: 130, Error: types.NewExecutorError("cancelled", ctx.Err(), "")}, nil
}

func (e *cancellationGateExecutor) CancelExecution(_ context.Context, id types.ExecutionID, _ string) (types.CancellationEvidence, error) {
	e.nativeCalls.Add(1)
	close(e.nativeStarted)
	if e.releaseNative != nil {
		<-e.releaseNative
	}
	close(e.nativeReturned)
	return types.CancellationEvidence{ExecutionID: id, NativeAcknowledged: true}, nil
}

func TestSwarmNativeCancellationSaturationFailsClosedAndRecovers(t *testing.T) {
	previousTimeout := cancellationResolutionTimeout
	cancellationResolutionTimeout = 20 * time.Millisecond
	t.Cleanup(func() { cancellationResolutionTimeout = previousTimeout })
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	releaseBlocked := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	t.Cleanup(releaseBlocked)
	first := &cancellationGateExecutor{
		executionStarted: make(chan struct{}), nativeStarted: make(chan struct{}), nativeReturned: make(chan struct{}), releaseNative: releaseFirst,
	}
	second := &cancellationGateExecutor{
		executionStarted: make(chan struct{}), nativeStarted: make(chan struct{}), nativeReturned: make(chan struct{}),
	}
	s := New(func(name string) (types.ExecutorV2, error) {
		switch name {
		case "blocked-native":
			return first, nil
		case "saturated-native":
			return second, nil
		default:
			return nil, errors.New("unexpected executor")
		}
	}, nil)
	s.nativeCancellationGate = make(chan struct{}, 1)
	firstHandle, err := s.Get(context.Background(), "blocked-native", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	secondHandle, err := s.Get(context.Background(), "saturated-native", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}

	type executionResult struct {
		terminal types.ExecutorEvent
		err      error
	}
	startExecution := func(h *Handle, id types.ExecutionID) <-chan executionResult {
		done := make(chan executionResult, 1)
		terminal := make(chan types.ExecutorEvent, 1)
		go func() {
			_, runErr := s.Execute(context.Background(), h, "scope", id, types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
				if event.Terminal {
					terminal <- event
				}
				return true
			}))
			done <- executionResult{terminal: <-terminal, err: runErr}
		}()
		return done
	}

	const firstID = types.ExecutionID("blocked-native")
	firstDone := startExecution(firstHandle, firstID)
	<-first.executionStarted
	firstEvidence, firstErr := s.Cancel(context.Background(), firstHandle, "scope", firstID, "occupy slot")
	if !errors.Is(firstErr, context.DeadlineExceeded) || firstEvidence != (types.CancellationEvidence{ExecutionID: firstID}) {
		t.Fatalf("first Cancel = %#v, %v; want bounded deadline without acknowledgement", firstEvidence, firstErr)
	}
	<-first.nativeStarted
	firstRun := <-firstDone
	if firstRun.err != nil || firstRun.terminal.Type != "failed" {
		t.Fatalf("first execution = %#v, %v; want bounded failed terminal", firstRun.terminal, firstRun.err)
	}
	firstBeforeRelease, err := s.Inspect(context.Background(), firstHandle, "scope", firstID)
	if err != nil {
		t.Fatal(err)
	}

	const secondID = types.ExecutionID("saturated-native")
	secondDone := startExecution(secondHandle, secondID)
	<-second.executionStarted
	secondEvidence, secondErr := s.Cancel(context.Background(), secondHandle, "scope", secondID, "saturated")
	if secondErr != nil || secondEvidence != (types.CancellationEvidence{ExecutionID: secondID}) {
		t.Fatalf("second Cancel = %#v, %v; want fail-closed result without native call", secondEvidence, secondErr)
	}
	secondRun := <-secondDone
	if secondRun.err != nil || secondRun.terminal.Type != "failed" {
		t.Fatalf("second execution = %#v, %v; want bounded failed terminal", secondRun.terminal, secondRun.err)
	}
	if calls := second.nativeCalls.Load(); calls != 0 {
		t.Fatalf("saturated native calls = %d, want 0", calls)
	}
	select {
	case <-second.nativeStarted:
		t.Fatal("saturated cancellation started a provider goroutine")
	default:
	}

	releaseBlocked()
	select {
	case <-first.nativeReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked native cancellation did not return during cleanup")
	}
	waitForNativeCancellationSlot(t, s)
	firstAfterRelease, err := s.Inspect(context.Background(), firstHandle, "scope", firstID)
	if err != nil || firstAfterRelease != firstBeforeRelease {
		t.Fatalf("first Inspect changed after late native cleanup: before=%#v after=%#v err=%v", firstBeforeRelease, firstAfterRelease, err)
	}
}

type blockingExactEvidenceExecutor struct {
	ready chan types.ProcessTreeEvidence
	holds atomic.Int32
}

func (*blockingExactEvidenceExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*blockingExactEvidenceExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{ExitCode: 0}, nil
}

func (*blockingExactEvidenceExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{ExitCode: 0}, nil
}
func (*blockingExactEvidenceExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*blockingExactEvidenceExecutor) Close() error                { return nil }
func (*blockingExactEvidenceExecutor) SendEvents(context.Context, types.ExecutionID, types.Message, types.ExecutorEventSink) (*types.Response, error) {
	return &types.Response{ExitCode: 0}, nil
}

func (e *blockingExactEvidenceExecutor) HoldProcessEvidence(types.ExecutionID) bool {
	e.holds.Add(1)
	return true
}

func (e *blockingExactEvidenceExecutor) ProcessEvidenceReady(types.ExecutionID) <-chan types.ProcessTreeEvidence {
	return e.ready
}
func (e *blockingExactEvidenceExecutor) ReleaseProcessEvidence(types.ExecutionID) { e.holds.Add(-1) }

func TestSwarmBoundsNeverReadyExactLeaseWithoutCaptureGoroutine(t *testing.T) {
	previous := processEvidenceCaptureTimeout
	processEvidenceCaptureTimeout = 20 * time.Millisecond
	t.Cleanup(func() { processEvidenceCaptureTimeout = previous })

	exec := &blockingExactEvidenceExecutor{ready: make(chan types.ProcessTreeEvidence)}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "blocking-evidence", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	terminal := make(chan types.ExecutorEvent, 2)
	for i := 0; i < 3; i++ {
		id := types.ExecutionID("bounded-evidence-" + string(rune('a'+i)))
		if _, err := s.Execute(context.Background(), h, "scope", id, types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			if event.Terminal {
				terminal <- event
			}
			return true
		})); err != nil {
			t.Fatal(err)
		}
		if event := <-terminal; event.Type == "completed" {
			t.Fatalf("terminal = %#v, want fail-closed bounded evidence result", event)
		}
		if got := exec.holds.Load(); got != 0 {
			t.Fatalf("evidence holds after %d executions = %d, want released", i+1, got)
		}
	}
}

type noLeaseExecutor struct{ releases atomic.Int32 }

func (*noLeaseExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*noLeaseExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{ExitCode: 0}, nil
}

func (*noLeaseExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{ExitCode: 0}, nil
}
func (*noLeaseExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*noLeaseExecutor) Close() error                { return nil }
func (*noLeaseExecutor) SendEvents(context.Context, types.ExecutionID, types.Message, types.ExecutorEventSink) (*types.Response, error) {
	return &types.Response{ExitCode: 0}, nil
}
func (*noLeaseExecutor) HoldProcessEvidence(types.ExecutionID) bool { return false }
func (*noLeaseExecutor) ProcessEvidenceReady(types.ExecutionID) <-chan types.ProcessTreeEvidence {
	return nil
}
func (e *noLeaseExecutor) ReleaseProcessEvidence(types.ExecutionID) { e.releases.Add(1) }

func TestSwarmDoesNotReleaseUnacquiredExactLease(t *testing.T) {
	exec := &noLeaseExecutor{}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "no-lease", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(context.Background(), h, "scope", "no-lease", types.Message{}, types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true })); err != nil {
		t.Fatal(err)
	}
	if got := exec.releases.Load(); got != 0 {
		t.Fatalf("release calls = %d, want 0 for a refused lease", got)
	}
}

func TestSwarmReadyExactLeaseWinsDeadline(t *testing.T) {
	previous := processEvidenceCaptureTimeout
	processEvidenceCaptureTimeout = time.Nanosecond
	t.Cleanup(func() { processEvidenceCaptureTimeout = previous })
	exec := &blockingExactEvidenceExecutor{ready: make(chan types.ProcessTreeEvidence, 1)}
	exec.ready <- types.ProcessTreeEvidence{Process: types.ProcessIdentity{PID: 17, StartFingerprint: "start", TreeID: "tree"}, OwnershipBoundary: types.ProcessOwnershipBoundaryProcessGroup, Stopped: true}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "ready-evidence", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	terminal := make(chan types.ExecutorEvent, 1)
	if _, err := s.Execute(context.Background(), h, "scope", "ready-evidence", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		if event.Terminal {
			terminal <- event
		}
		return true
	})); err != nil {
		t.Fatal(err)
	}
	if event := <-terminal; event.Type != "completed" {
		t.Fatalf("terminal = %#v, want ready exact evidence to win the deadline", event)
	}
}

func TestSwarmTimerFinalReadAdmitsEvidencePublishedAtDeadline(t *testing.T) {
	previousTimeout := processEvidenceCaptureTimeout
	previousHook := beforeProcessEvidenceDeadlineFinalRead
	processEvidenceCaptureTimeout = time.Millisecond
	entered, release := make(chan struct{}), make(chan struct{})
	beforeProcessEvidenceDeadlineFinalRead = func() {
		close(entered)
		<-release
	}
	t.Cleanup(func() {
		processEvidenceCaptureTimeout = previousTimeout
		beforeProcessEvidenceDeadlineFinalRead = previousHook
	})

	exec := &blockingExactEvidenceExecutor{ready: make(chan types.ProcessTreeEvidence, 1)}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "deadline-final-read", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	terminal := make(chan types.ExecutorEvent, 1)
	done := make(chan error, 1)
	go func() {
		_, runErr := s.Execute(context.Background(), h, "scope", "deadline-final-read", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			if event.Terminal {
				terminal <- event
			}
			return true
		}))
		done <- runErr
	}()
	<-entered // timer branch won; the final receive is deliberately still blocked.
	exec.ready <- types.ProcessTreeEvidence{Process: types.ProcessIdentity{PID: 18, StartFingerprint: "deadline", TreeID: "tree"}, OwnershipBoundary: types.ProcessOwnershipBoundaryProcessGroup, Stopped: true}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if event := <-terminal; event.Type != "completed" {
		t.Fatalf("terminal = %#v, want completed from final timer-branch read", event)
	}
}

func TestSwarmPipeAbortReturnsBeforeEvidenceDeadline(t *testing.T) {
	previous := processEvidenceCaptureTimeout
	processEvidenceCaptureTimeout = time.Second
	t.Cleanup(func() { processEvidenceCaptureTimeout = previous })
	s := New(func(string) (types.ExecutorV2, error) {
		return aimexecutor.NewCLIPipeAdapter(pipeexecutor.New()), nil
	}, nil)
	h, err := s.Get(context.Background(), "pipe-abort", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = s.Execute(context.Background(), h, "scope", "pipe-abort", types.Message{Spawn: &types.SpawnArgs{Command: "aimux-command-that-does-not-exist"}}, types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true }))
	if err == nil {
		t.Fatal("invalid executable unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed >= processEvidenceCaptureTimeout/2 {
		t.Fatalf("startup error waited %s for evidence deadline", elapsed)
	}
}

type completedSession struct{}

func (*completedSession) ID() string { return "session" }
func (*completedSession) Send(context.Context, string) (*types.Result, error) {
	return &types.Result{Content: "ok", ExitCode: 0}, nil
}

func (*completedSession) Stream(context.Context, string) (<-chan types.Event, error) {
	stream := make(chan types.Event)
	close(stream)
	return stream, nil
}
func (*completedSession) Close() error { return nil }
func (*completedSession) Alive() bool  { return true }
func (*completedSession) PID() int     { return 17 }

func TestSwarmSessionBoundCLIPipeAdapterNaturalSuccessCompletes(t *testing.T) {
	s := New(func(string) (types.ExecutorV2, error) {
		return aimexecutor.NewCLIPipeAdapterWithSession(pipeexecutor.New(), &completedSession{}), nil
	}, nil)
	h, err := s.Get(context.Background(), "session-pipe", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	terminal := make(chan types.ExecutorEvent, 1)
	if _, err := s.Execute(context.Background(), h, "scope", "session-pipe", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		if event.Terminal {
			terminal <- event
		}
		return true
	})); err != nil {
		t.Fatal(err)
	}
	if event := <-terminal; event.Type != "completed" {
		t.Fatalf("terminal = %#v, want completed session-bound adapter result", event)
	}
}
