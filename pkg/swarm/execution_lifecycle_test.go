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
