package workerruntime

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

type runtimeExecutor struct{}

func (*runtimeExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*runtimeExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*runtimeExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*runtimeExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*runtimeExecutor) Close() error                { return nil }
func (*runtimeExecutor) SendEvents(_ context.Context, _ types.ExecutionID, _ types.Message, emit func(types.ExecutorEvent)) (*types.Response, error) {
	emit(types.ExecutorEvent{Channel: "stdout", Type: "output", Content: []byte{0xff}})
	emit(types.ExecutorEvent{Channel: "terminal", Type: "terminal", Terminal: true})
	return &types.Response{}, nil
}

func TestWorkerRuntimeRoutesOnlyThroughSwarm(t *testing.T) {
	s := swarm.New(func(string) (types.ExecutorV2, error) { return &runtimeExecutor{}, nil }, nil)
	r, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Get(context.Background(), "runtime", swarm.Stateful)
	if err != nil {
		t.Fatal(err)
	}
	var events []ExecutionEnvelope
	if _, err := r.Execute(context.Background(), h, "run", types.Message{}, func(event ExecutionEnvelope) { events = append(events, event) }); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Event.Content[0] != 0xff || !events[1].Event.Terminal {
		t.Fatalf("events=%#v", events)
	}
}

func TestWorkerRuntimeSourceGuardHasNoDirectExecutorDependency(t *testing.T) {
	source, err := os.ReadFile("runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "pkg/executor") || strings.Contains(string(source), "pkg/loom") || strings.Contains(string(source), "pkg/server") {
		t.Fatal("runtime must access live execution through swarm only")
	}
}
