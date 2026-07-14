package workerruntime

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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

type terminalFlushQuotaExecutor struct{}

func (*terminalFlushQuotaExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*terminalFlushQuotaExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}

func (*terminalFlushQuotaExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*terminalFlushQuotaExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*terminalFlushQuotaExecutor) Close() error                { return nil }
func (*terminalFlushQuotaExecutor) SendEvents(_ context.Context, _ types.ExecutionID, _ types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	sink.TryAdmit(types.ExecutorEvent{Channel: "stderr", Type: "output", Content: []byte("prefix\n")})
	sink.TryAdmit(types.ExecutorEvent{Channel: "stdout", Type: "output", Content: []byte("unterminated-tail")})
	return &types.Response{Content: "unterminated-tail", Stderr: "prefix\n"}, nil
}

type runtimeNeverTimer struct {
	ch chan time.Time
}

func (timer runtimeNeverTimer) C() <-chan time.Time { return timer.ch }
func (runtimeNeverTimer) Stop() bool                { return true }

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

func TestWorkerRuntimeFailsClosedWhenTerminalFlushExceedsOutputQuota(t *testing.T) {
	s := swarm.New(func(string) (types.ExecutorV2, error) { return &terminalFlushQuotaExecutor{}, nil }, nil)
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
	config := DefaultEventWriterConfig(batchSink)
	config.Pump = eventPumpConfig{
		MaxEvents:            3,
		MaxBytes:             8192,
		ControlReserveEvents: 2,
		ControlReserveBytes:  4096,
	}
	config.NewTimer = func(time.Duration) EventWriterTimer {
		return runtimeNeverTimer{ch: make(chan time.Time)}
	}
	writer, err := NewEventWriter(config)
	if err != nil {
		t.Fatal(err)
	}
	sink := NewExecutorEventSink(writer, "generic", "text", nil)
	response, runErr := r.Execute(context.Background(), h, scope, "terminal-flush-quota", types.Message{}, sink)
	if err := writer.CloseAndFlush(context.Background()); err != nil {
		t.Fatal(err)
	}

	batchSink.mu.Lock()
	if len(batchSink.events) == 0 {
		batchSink.mu.Unlock()
		t.Fatal("no runtime events persisted")
	}
	terminal := batchSink.events[len(batchSink.events)-1]
	batchSink.mu.Unlock()
	if !terminal.Terminal || !terminal.Truncated {
		t.Fatalf("terminal = %#v, want durable truncated terminal after rejected buffered tail", terminal)
	}
	if response == nil || !response.Partial {
		t.Fatalf("terminal is truncated but response = %#v, want matching Partial=true", response)
	}
	if !errors.Is(runErr, swarm.ErrEventAdmissionRejected) {
		t.Fatalf("run error = %v, want ErrEventAdmissionRejected so task dispatch cannot report success", runErr)
	}
}

func TestWorkerRuntimeSourceGuardHasNoDirectExecutorDependency(t *testing.T) {
	source, err := os.ReadFile("runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"pkg/executor", "pkg/loom", "pkg/server", "LegacyRun", "swarm.New(", ".Get(", ".Send(", ".SendStream(", "MaybeStartSession", "SessionFactory"} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("runtime must access live execution through supplied Swarm handles; found %q", forbidden)
		}
	}
}

func TestWorkerRuntimeInspectSuppliedEvidenceDelegatesDirectly(t *testing.T) {
	source, err := os.ReadFile("runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	if !workerRuntimeInspectSuppliedEvidenceDelegatesDirectly(source) {
		t.Fatal("InspectSuppliedEvidence must remain one direct delegation through the supplied Swarm")
	}

	mutations := []string{
		`package workerruntime
type WorkerRuntime struct{ swarm any }
func (r *WorkerRuntime) InspectSuppliedEvidence(evidence any) any {
	constructor := swarm.New
	_ = constructor
	return r.swarm.InspectSuppliedEvidence(evidence)
}`,
		`package workerruntime
type WorkerRuntime struct{ swarm any }
func (r *WorkerRuntime) InspectSuppliedEvidence(evidence any) any {
	r.swarm.Get()
	return r.swarm.InspectSuppliedEvidence(evidence)
}`,
	}
	for _, mutation := range mutations {
		if workerRuntimeInspectSuppliedEvidenceDelegatesDirectly([]byte(mutation)) {
			t.Fatal("delegation guard accepted an extra constructor or Swarm call")
		}
	}
}

func workerRuntimeInspectSuppliedEvidenceDelegatesDirectly(source []byte) bool {
	parsed, err := parser.ParseFile(token.NewFileSet(), "runtime.go", source, 0)
	if err != nil {
		return false
	}
	var target *ast.FuncDecl
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "InspectSuppliedEvidence" {
			target = function
			break
		}
	}
	if target == nil || target.Recv == nil || len(target.Recv.List) != 1 || len(target.Recv.List[0].Names) != 1 || target.Recv.List[0].Names[0].Name != "r" || target.Body == nil || len(target.Body.List) != 1 {
		return false
	}
	returned, ok := target.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		return false
	}
	call, ok := returned.Results[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	argument, ok := call.Args[0].(*ast.Ident)
	if !ok || argument.Name != "evidence" {
		return false
	}
	method, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || method.Sel.Name != "InspectSuppliedEvidence" {
		return false
	}
	field, ok := method.X.(*ast.SelectorExpr)
	if !ok || field.Sel.Name != "swarm" {
		return false
	}
	receiver, ok := field.X.(*ast.Ident)
	return ok && receiver.Name == "r"
}

func TestWorkerRuntimeInspectSuppliedEvidenceIsPure(t *testing.T) {
	factoryCalls := 0
	s := swarm.New(func(string) (types.ExecutorV2, error) {
		factoryCalls++
		return &runtimeExecutor{}, nil
	}, nil)
	r, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	expected := types.ProcessIdentity{PID: 42, StartFingerprint: "start-a", TreeID: "tree-a"}
	observed := expected
	stale := expected
	stale.StartFingerprint = "start-b"
	cleanExit, crashExit := 0, 23
	tests := []struct {
		name     string
		evidence *swarm.SuppliedProcessEvidence
		want     swarm.SuppliedEvidenceClassification
	}{
		{name: "missing", want: swarm.SuppliedEvidenceUnknown},
		{name: "live", evidence: &swarm.SuppliedProcessEvidence{ExpectedProcess: expected, ObservedProcess: &observed}, want: swarm.SuppliedEvidenceLive},
		{name: "clean exit", evidence: &swarm.SuppliedProcessEvidence{ExpectedProcess: expected, ExitCode: &cleanExit}, want: swarm.SuppliedEvidenceCleanExit},
		{name: "failed crash", evidence: &swarm.SuppliedProcessEvidence{ExpectedProcess: expected, ExitCode: &crashExit}, want: swarm.SuppliedEvidenceFailedCrash},
		{name: "stale identity", evidence: &swarm.SuppliedProcessEvidence{ExpectedProcess: expected, ObservedProcess: &stale}, want: swarm.SuppliedEvidenceStaleIdentity},
		{name: "stale after root absence", evidence: &swarm.SuppliedProcessEvidence{ExpectedProcess: expected, ObservedProcess: &stale, RootAbsent: true}, want: swarm.SuppliedEvidenceStaleIdentity},
		{name: "orphan after PID reuse", evidence: &swarm.SuppliedProcessEvidence{ExpectedProcess: expected, ObservedProcess: &stale, RootAbsent: true, DescendantsSurvived: true}, want: swarm.SuppliedEvidenceOrphanedTree},
		{name: "orphaned tree", evidence: &swarm.SuppliedProcessEvidence{ExpectedProcess: expected, RootAbsent: true, DescendantsSurvived: true}, want: swarm.SuppliedEvidenceOrphanedTree},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := r.InspectSuppliedEvidence(test.evidence)
			second := r.InspectSuppliedEvidence(test.evidence)
			if first != second || first.Classification != test.want {
				t.Fatalf("inspections = %#v then %#v, want frozen %q classification", first, second, test.want)
			}
		})
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls = %d, want zero", factoryCalls)
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
