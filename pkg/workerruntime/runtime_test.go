package workerruntime

import (
	"context"
	"os"
	"strings"
	"sync"
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
func (*runtimeExecutor) SendEvents(_ context.Context, _ types.ExecutionID, _ types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	sink.TryAdmit(types.ExecutorEvent{Channel: "stdout", Type: "output", Content: []byte("before\n")})
	sink.TryAdmit(types.ExecutorEvent{Channel: "stderr", Type: "output", Content: []byte{0xff}})
	return &types.Response{}, nil
}

type runtimeBatchSink struct {
	mu     sync.Mutex
	events []RuntimeEvent
}

func (sink *runtimeBatchSink) AppendRuntimeEvents(_ context.Context, batch []RuntimeEvent) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, batch...)
	return nil
}

func (*runtimeBatchSink) Checkpoint(context.Context) error { return nil }

func TestWorkerRuntimeRoutesOnlyThroughSwarmAndEventWriter(t *testing.T) {
	s := swarm.New(func(string) (types.ExecutorV2, error) { return &runtimeExecutor{}, nil }, nil)
	r, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	const scope = "scope-a"
	h, err := s.Get(context.Background(), "runtime", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	batchSink := &runtimeBatchSink{}
	writer, err := NewEventWriter(DefaultEventWriterConfig(batchSink))
	if err != nil {
		t.Fatal(err)
	}
	var progress []string
	sink := NewExecutorEventSink(writer, "generic", "text", func(line string) { progress = append(progress, line) })
	if _, err := r.Execute(context.Background(), h, scope, "run", types.Message{}, sink); err != nil {
		t.Fatal(err)
	}
	if err := writer.CloseAndFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	batchSink.mu.Lock()
	defer batchSink.mu.Unlock()
	if len(batchSink.events) < 3 || batchSink.events[len(batchSink.events)-1].Terminal != true {
		t.Fatalf("events=%#v", batchSink.events)
	}
	if len(progress) != 1 || progress[0] != "before" {
		t.Fatalf("progress=%#v", progress)
	}
}

func TestWorkerRuntimeSourceGuardHasNoDirectExecutorDependency(t *testing.T) {
	source, err := os.ReadFile("runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "pkg/executor") || strings.Contains(string(source), "pkg/loom") || strings.Contains(string(source), "pkg/server") || strings.Contains(string(source), "LegacyRun") {
		t.Fatal("runtime must access live execution through swarm only")
	}
}

func TestExecutorEventSinkWithoutWriterRejectsWithoutPanic(t *testing.T) {
	sink := NewExecutorEventSink(nil, "generic", "text", nil)
	if sink.TryAdmit(types.ExecutorEvent{Channel: "stdout", Type: "output", Content: []byte("x")}) {
		t.Fatal("nil-writer sink admitted output")
	}
	source, ok := sink.(interface{ Err() error })
	if !ok || source.Err() == nil {
		t.Fatal("nil-writer sink must expose an explicit error")
	}
}
