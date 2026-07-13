package swarm_test

import (
	"context"
	"sync"
	"testing"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

type eventExecutor struct {
	release <-chan struct{}
	started chan<- struct{}
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
func (e *eventExecutor) SendEvents(_ context.Context, _ types.ExecutionID, _ types.Message, emit func(types.ExecutorEvent)) (*types.Response, error) {
	emit(types.ExecutorEvent{Channel: "stdout", Type: "output", Content: []byte{0xff}})
	e.started <- struct{}{}
	<-e.release
	emit(types.ExecutorEvent{Channel: "terminal", Type: "terminal", Terminal: true})
	return &types.Response{Content: "ok"}, nil
}

func TestSwarmExecuteFencesOneActiveExecutionAndCancelWins(t *testing.T) {
	release, started := make(chan struct{}), make(chan struct{}, 1)
	s := swarm.New(func(string) (types.ExecutorV2, error) { return &eventExecutor{release: release, started: started}, nil }, nil)
	h, err := s.Get(context.Background(), "event", swarm.Stateful)
	if err != nil {
		t.Fatal(err)
	}
	var events []types.ExecutorEvent
	var mu sync.Mutex
	done := make(chan error, 1)
	go func() {
		_, err := s.Execute(context.Background(), h, "one", types.Message{}, func(event types.ExecutorEvent) { mu.Lock(); events = append(events, event); mu.Unlock() })
		done <- err
	}()
	<-started
	if _, err := s.Execute(context.Background(), h, "two", types.Message{}, func(types.ExecutorEvent) {}); err != swarm.ErrExecutionActive {
		t.Fatalf("second execute = %v, want active fence", err)
	}
	if evidence, err := s.Cancel(context.Background(), h, "one", "test"); err != nil || evidence.ExecutionID != "one" {
		t.Fatalf("cancel = %#v, %v", evidence, err)
	}
	inspection, err := s.Inspect(context.Background(), h, "one")
	if err != nil || !inspection.Terminal || !inspection.Cancelled {
		t.Fatalf("inspect = %#v, %v", inspection, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 || events[0].Content[0] != 0xff {
		t.Fatalf("events = %#v", events)
	}
}
