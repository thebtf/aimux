package swarm_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

type lifecycleEventExecutor struct {
	started       chan struct{}
	release       chan struct{}
	stopped       chan struct{}
	closed        chan struct{}
	startedOnce   sync.Once
	stoppedOnce   sync.Once
	closedOnce    sync.Once
	sendCalls     atomic.Int32
	eventCalls    atomic.Int32
	ignoreRefusal bool
}

type cancellationTailExecutor struct {
	started     chan struct{}
	releaseTail chan struct{}
	tailTried   chan bool
}

func (*cancellationTailExecutor) CancelExecution(_ context.Context, id types.ExecutionID, _ string) (types.CancellationEvidence, error) {
	return types.CancellationEvidence{ExecutionID: id, NativeAcknowledged: true}, nil
}

func newCancellationTailExecutor() *cancellationTailExecutor {
	return &cancellationTailExecutor{
		started:     make(chan struct{}),
		releaseTail: make(chan struct{}),
		tailTried:   make(chan bool, 1),
	}
}

func (*cancellationTailExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }

func (*cancellationTailExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}

func (e *cancellationTailExecutor) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	return e.Send(ctx, msg)
}

func (*cancellationTailExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*cancellationTailExecutor) Close() error                { return nil }

func (e *cancellationTailExecutor) SendEvents(ctx context.Context, _ types.ExecutionID, _ types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	sink.TryAdmit(types.ExecutorEvent{Channel: "stdout", Type: "output", Content: []byte("before")})
	close(e.started)
	<-ctx.Done()
	<-e.releaseTail
	accepted := sink.TryAdmit(types.ExecutorEvent{Channel: "stdout", Type: "output", Content: []byte("cancellation-tail")})
	e.tailTried <- accepted
	return &types.Response{Content: "cancellation-tail", ExitCode: 130, Error: types.NewExecutorError("cancelled", ctx.Err(), "")}, nil
}

func newLifecycleEventExecutor() *lifecycleEventExecutor {
	return &lifecycleEventExecutor{
		started: make(chan struct{}),
		release: make(chan struct{}),
		stopped: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (*lifecycleEventExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }

func (e *lifecycleEventExecutor) Send(ctx context.Context, _ types.Message) (*types.Response, error) {
	e.sendCalls.Add(1)
	return &types.Response{}, ctx.Err()
}

func (e *lifecycleEventExecutor) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	return e.Send(ctx, msg)
}

func (*lifecycleEventExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }

func (e *lifecycleEventExecutor) Close() error {
	e.closedOnce.Do(func() { close(e.closed) })
	return nil
}

func (e *lifecycleEventExecutor) SendEvents(ctx context.Context, _ types.ExecutionID, _ types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	e.eventCalls.Add(1)
	e.startedOnce.Do(func() { close(e.started) })
	accepted := sink.TryAdmit(types.ExecutorEvent{Channel: "stdout", Type: "output", Content: []byte("payload")})
	if e.ignoreRefusal {
		return &types.Response{Content: "ok"}, nil
	}
	select {
	case <-ctx.Done():
		e.stoppedOnce.Do(func() { close(e.stopped) })
		return &types.Response{}, nil
	case <-e.release:
		e.stoppedOnce.Do(func() { close(e.stopped) })
		return &types.Response{Content: "ok", Partial: !accepted}, nil
	}
}

func TestSwarmPromotesGenericEventAdmissionLossToPartialResponse(t *testing.T) {
	exec := newLifecycleEventExecutor()
	exec.ignoreRefusal = true
	s := swarm.New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil, swarm.WithStatefulTTL(0))
	h, err := s.Get(context.Background(), "generic", swarm.Stateful, swarm.WithScope("scope-a"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := s.Execute(context.Background(), h, "scope-a", "loss", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		return event.Terminal
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || !response.Partial {
		t.Fatalf("response = %#v, want Partial after rejected generic output", response)
	}
}

func TestSwarmCancelFinalizesOnlyAfterTailAdmissionDecision(t *testing.T) {
	exec := newCancellationTailExecutor()
	s := swarm.New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil, swarm.WithStatefulTTL(0))
	const scope = "scope-a"
	h, err := s.Get(context.Background(), "generic", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var events []types.ExecutorEvent
	terminalSeen := make(chan struct{}, 1)
	result := make(chan struct {
		response *types.Response
		err      error
	}, 1)
	go func() {
		response, runErr := s.Execute(context.Background(), h, scope, "cancel-tail", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
			if event.Terminal {
				terminalSeen <- struct{}{}
				return true
			}
			return string(event.Content) != "cancellation-tail"
		}))
		result <- struct {
			response *types.Response
			err      error
		}{response: response, err: runErr}
	}()

	<-exec.started
	if evidence, cancelErr := s.Cancel(context.Background(), h, scope, "cancel-tail", "test"); cancelErr != nil || evidence.ExecutionID != "cancel-tail" {
		t.Fatalf("Cancel = %#v, %v", evidence, cancelErr)
	}
	inspectionDone := make(chan struct {
		inspection swarm.ExecutionInspection
		err        error
	}, 1)
	go func() {
		inspection, inspectErr := s.Inspect(context.Background(), h, scope, "cancel-tail")
		inspectionDone <- struct {
			inspection swarm.ExecutionInspection
			err        error
		}{inspection, inspectErr}
	}()
	select {
	case result := <-inspectionDone:
		t.Fatalf("Inspect returned provisional cancellation state: %#v, %v", result.inspection, result.err)
	case <-time.After(25 * time.Millisecond):
	}
	select {
	case <-terminalSeen:
		t.Fatal("terminal was published before cancellation-tail admission was decided")
	default:
	}

	close(exec.releaseTail)
	if accepted := <-exec.tailTried; accepted {
		t.Fatal("cancellation tail unexpectedly admitted")
	}
	run := <-result
	if run.err != nil {
		t.Fatal(run.err)
	}
	if run.response == nil || !run.response.Partial {
		t.Fatalf("response = %#v, want truthful Partial after rejected cancellation tail", run.response)
	}
	inspectionResult := <-inspectionDone
	if inspectionResult.err != nil || !inspectionResult.inspection.Terminal || !inspectionResult.inspection.Cancelled {
		t.Fatalf("terminal Inspect = %#v, %v", inspectionResult.inspection, inspectionResult.err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 3 {
		t.Fatalf("events = %#v, want before, rejected tail decision, terminal", events)
	}
	terminal := events[len(events)-1]
	if !terminal.Terminal || terminal.Type != "cancelled" || !terminal.Truncated {
		t.Fatalf("terminal = %#v, want cancelled truncated terminal after tail decision", terminal)
	}
}

func TestSwarmShutdownCancelsAndClosesActiveStatelessExecution(t *testing.T) {
	exec := newLifecycleEventExecutor()
	s := swarm.New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil, swarm.WithStatefulTTL(0))
	h, err := s.Get(context.Background(), "generic", swarm.Stateless, swarm.WithScope("scope-a"))
	if err != nil {
		t.Fatal(err)
	}
	executeDone := make(chan error, 1)
	go func() {
		_, runErr := s.Execute(context.Background(), h, "scope-a", "active", types.Message{}, types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true }))
		executeDone <- runErr
	}()
	<-exec.started
	defer func() {
		select {
		case <-exec.stopped:
		default:
			close(exec.release)
			<-executeDone
		}
	}()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case <-exec.stopped:
	case <-shutdownCtx.Done():
		t.Fatal("active stateless execution was not cancelled by Shutdown")
	}
	if err := <-executeDone; err != nil {
		t.Fatalf("Execute after shutdown cancellation: %v", err)
	}
	select {
	case <-exec.closed:
	default:
		t.Fatal("active stateless executor was not closed by Shutdown")
	}
	if _, err := s.Get(context.Background(), "generic", swarm.Stateless); err == nil {
		t.Fatal("Get succeeded after Shutdown")
	}
}

func TestSwarmRejectsExecutionAdmittedAfterShutdown(t *testing.T) {
	exec := newLifecycleEventExecutor()
	exec.ignoreRefusal = true
	s := swarm.New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil, swarm.WithStatefulTTL(0))
	h, err := s.Get(context.Background(), "generic", swarm.Stateless, swarm.WithScope("scope-a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(context.Background(), h, "scope-a", "late", types.Message{}, types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true })); err == nil {
		t.Fatal("Execute succeeded on a handle admitted before Shutdown")
	}
	if got := exec.eventCalls.Load(); got != 0 {
		t.Fatalf("SendEvents calls = %d, want zero after Shutdown", got)
	}
}

func TestSwarmFactoryCompletionCannotRegisterAfterShutdown(t *testing.T) {
	exec := newLifecycleEventExecutor()
	entered := make(chan struct{})
	releaseFactory := make(chan struct{})
	s := swarm.New(func(string) (types.ExecutorV2, error) {
		close(entered)
		<-releaseFactory
		return exec, nil
	}, nil, swarm.WithStatefulTTL(0))
	getDone := make(chan error, 1)
	go func() {
		_, getErr := s.Get(context.Background(), "generic", swarm.Stateless)
		getDone <- getErr
	}()
	<-entered
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(releaseFactory)
	if err := <-getDone; err == nil {
		t.Fatal("factory result registered after Shutdown")
	}
	select {
	case <-exec.closed:
	default:
		t.Fatal("executor created during shutdown race was not closed")
	}
}

func TestSwarmLegacySendCannotBypassActiveExecutionFence(t *testing.T) {
	exec := newLifecycleEventExecutor()
	s := swarm.New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil, swarm.WithStatefulTTL(0))
	const scope = "scope-a"
	h, err := s.Get(context.Background(), "generic", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	executeDone := make(chan error, 1)
	go func() {
		_, runErr := s.Execute(context.Background(), h, scope, "active", types.Message{}, types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true }))
		executeDone <- runErr
	}()
	<-exec.started

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Send(cancelled, h, types.Message{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send while Execute owns handle = %v, want context.Canceled", err)
	}
	if got := exec.sendCalls.Load(); got != 0 {
		t.Fatalf("legacy Send reached executor %d times during active Execute", got)
	}
	if _, err := s.Cancel(context.Background(), h, scope, "active", "test cleanup"); err != nil {
		t.Fatal(err)
	}
	if err := <-executeDone; err != nil {
		t.Fatal(err)
	}
}
