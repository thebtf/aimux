package swarm_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
)

type eventExecutor struct {
	release     <-chan struct{}
	started     chan<- struct{}
	nilResponse bool
}

func (*eventExecutor) CancelExecution(_ context.Context, id types.ExecutionID, _ string) (types.CancellationEvidence, error) {
	return types.CancellationEvidence{ExecutionID: id, NativeAcknowledged: true}, nil
}

func (e *eventExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (e *eventExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}
func (e *eventExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{}, nil
}
func (e *eventExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (e *eventExecutor) Close() error                { return nil }
func (e *eventExecutor) SendEvents(ctx context.Context, _ types.ExecutionID, _ types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	sink.TryAdmit(types.ExecutorEvent{Channel: "stdout", Type: "output", Content: []byte{0xff}})
	e.started <- struct{}{}
	<-e.release
	if e.nilResponse {
		return nil, nil
	}
	if ctx.Err() != nil {
		return &types.Response{ExitCode: 130, Error: types.NewExecutorError("cancelled", ctx.Err(), "")}, nil
	}
	return &types.Response{Content: "ok"}, nil
}

func TestSwarmNilResponseEventExecutorSuccessCompletes(t *testing.T) {
	release, started := make(chan struct{}), make(chan struct{}, 1)
	close(release)
	exec := &eventExecutor{release: release, started: started, nilResponse: true}
	s := swarm.New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "nil-response", swarm.Stateful, swarm.WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	var events []types.ExecutorEvent
	response, err := s.Execute(context.Background(), h, "scope", "nil-response", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		events = append(events, event)
		return true
	}))
	if err != nil || response != nil {
		t.Fatalf("Execute = %#v, %v; want nil, nil", response, err)
	}
	var terminals []types.ExecutorEvent
	for _, event := range events {
		if event.Terminal {
			terminals = append(terminals, event)
		}
	}
	if len(terminals) != 1 || terminals[0].Type != "completed" {
		t.Fatalf("events = %#v, want one completed terminal", events)
	}
	first, err := s.Inspect(context.Background(), h, "scope", "nil-response")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Inspect(context.Background(), h, "scope", "nil-response")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !first.Terminal || first.Cancelled || first.CancellationEvidence != (types.CancellationEvidence{}) {
		t.Fatalf("inspections = %#v then %#v, want stable completed state without cancellation", first, second)
	}
}

func TestSwarmExecuteFencesOneActiveExecutionAndCancelWins(t *testing.T) {
	release, started := make(chan struct{}), make(chan struct{}, 1)
	s := swarm.New(func(string) (types.ExecutorV2, error) { return &eventExecutor{release: release, started: started}, nil }, nil)
	const scope = "scope-a"
	h, err := s.Get(context.Background(), "event", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	var events []types.ExecutorEvent
	var mu sync.Mutex
	done := make(chan error, 1)
	go func() {
		_, err := s.Execute(context.Background(), h, scope, "one", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
			return true
		}))
		done <- err
	}()
	<-started
	if _, err := s.Execute(context.Background(), h, scope, "two", types.Message{}, types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true })); err != swarm.ErrExecutionActive {
		t.Fatalf("second execute = %v, want active fence", err)
	}
	if evidence, err := s.Cancel(context.Background(), h, scope, "one", "test"); err != nil || evidence.ExecutionID != "one" {
		t.Fatalf("cancel = %#v, %v", evidence, err)
	}
	inspectionDone := make(chan struct {
		inspection swarm.ExecutionInspection
		err        error
	}, 1)
	go func() {
		inspection, inspectErr := s.Inspect(context.Background(), h, scope, "one")
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
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	inspectionResult := <-inspectionDone
	inspection, err := inspectionResult.inspection, inspectionResult.err
	if err != nil || !inspection.Terminal || !inspection.Cancelled {
		t.Fatalf("terminal inspect = %#v, %v", inspection, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[0].Content[0] != 0xff || !events[1].Terminal || events[1].Type != "cancelled" {
		t.Fatalf("events = %#v", events)
	}
}

func TestSwarmCancelCancelsContextAndDrainsLateEventsBeforeTerminal(t *testing.T) {
	started := make(chan struct{}, 1)
	late := make(chan struct{})
	s := swarm.New(func(string) (types.ExecutorV2, error) {
		return &contextEventExecutor{started: started, late: late}, nil
	}, nil)
	const scope = "scope-a"
	h, err := s.Get(context.Background(), "event", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var events []types.ExecutorEvent
	done := make(chan error, 1)
	go func() {
		_, err := s.Execute(context.Background(), h, scope, "one", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
			return true
		}))
		done <- err
	}()
	<-started
	if _, err := s.Cancel(context.Background(), h, scope, "one", "test"); err != nil {
		t.Fatal(err)
	}
	close(late)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 3 || string(events[0].Content) != "before" || string(events[1].Content) != "late" || !events[2].Terminal || events[2].Type != "cancelled" {
		t.Fatalf("cancellation tail was not drained before terminal: %#v", events)
	}
}

func TestSwarmExecutionRejectsForgedHandleWrongScopeAndWrongExecutionID(t *testing.T) {
	s := swarm.New(func(string) (types.ExecutorV2, error) {
		return &eventExecutor{release: make(chan struct{}), started: make(chan struct{}, 1)}, nil
	}, nil)
	const scope = "scope-a"
	h, err := s.Get(context.Background(), "event", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	forged := &swarm.Handle{ID: h.ID, TenantID: h.TenantID, Name: h.Name, Mode: h.Mode}
	if _, err := s.Inspect(context.Background(), forged, scope, "missing"); err != swarm.ErrExecutionNotFound {
		t.Fatalf("forged handle inspect = %v, want not found", err)
	}
	if _, err := s.Cancel(context.Background(), h, "scope-b", "missing", "test"); err != swarm.ErrExecutionNotFound {
		t.Fatalf("wrong scope cancel = %v, want not found", err)
	}
	if _, err := s.Cancel(context.Background(), h, scope, "missing", "test"); err != swarm.ErrExecutionNotFound {
		t.Fatalf("wrong execution cancel = %v, want not found", err)
	}
}

func TestSwarmExecutionRejectsCrossTenantInspectionAndCancellationAsNotFound(t *testing.T) {
	release := make(chan struct{})
	close(release)
	s := swarm.New(func(string) (types.ExecutorV2, error) {
		return &eventExecutor{release: release, started: make(chan struct{}, 1)}, nil
	}, nil)
	ctxA := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-a"})
	ctxB := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-b"})
	const scope = "scope-a"
	h, err := s.Get(ctxA, "event", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(ctxA, h, scope, "tenant-run", types.Message{}, types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true })); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Inspect(ctxB, h, scope, "tenant-run"); err != swarm.ErrExecutionNotFound {
		t.Fatalf("cross-tenant inspect = %v, want not found", err)
	}
	if _, err := s.Cancel(ctxB, h, scope, "tenant-run", "forged tenant"); err != swarm.ErrExecutionNotFound {
		t.Fatalf("cross-tenant cancel = %v, want not found", err)
	}
}

func TestSwarmExecutionRejectsDuplicateIDWhileTerminalRecordIsRetained(t *testing.T) {
	release := make(chan struct{})
	close(release)
	s := swarm.New(func(string) (types.ExecutorV2, error) {
		return &eventExecutor{release: release, started: make(chan struct{}, 2)}, nil
	}, nil)
	const scope = "scope-a"
	h, err := s.Get(context.Background(), "event", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	sink := types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true })
	if _, err := s.Execute(context.Background(), h, scope, "same", types.Message{}, sink); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(context.Background(), h, scope, "same", types.Message{}, sink); err != swarm.ErrExecutionExists {
		t.Fatalf("duplicate execution = %v, want ErrExecutionExists", err)
	}
}

type contextEventExecutor struct {
	started chan<- struct{}
	late    <-chan struct{}
}

func (*contextEventExecutor) CancelExecution(_ context.Context, id types.ExecutionID, _ string) (types.CancellationEvidence, error) {
	return types.CancellationEvidence{ExecutionID: id, NativeAcknowledged: true}, nil
}

func (*contextEventExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*contextEventExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*contextEventExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*contextEventExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*contextEventExecutor) Close() error                { return nil }
func (e *contextEventExecutor) SendEvents(ctx context.Context, _ types.ExecutionID, _ types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	sink.TryAdmit(types.ExecutorEvent{Channel: "stdout", Type: "output", Content: []byte("before")})
	e.started <- struct{}{}
	<-ctx.Done()
	<-e.late
	sink.TryAdmit(types.ExecutorEvent{Channel: "stdout", Type: "output", Content: []byte("late")})
	return &types.Response{ExitCode: 130, Error: types.NewExecutorError("cancelled", ctx.Err(), "")}, nil
}
