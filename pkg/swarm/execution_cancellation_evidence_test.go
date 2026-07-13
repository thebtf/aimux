package swarm_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

type treeEvidenceExecutor struct {
	started, returned chan struct{}
	evidenceGate      <-chan struct{}
	stopped           bool
}

func (*treeEvidenceExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*treeEvidenceExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*treeEvidenceExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*treeEvidenceExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*treeEvidenceExecutor) Close() error                { return nil }
func (e *treeEvidenceExecutor) SendEvents(ctx context.Context, _ types.ExecutionID, _ types.Message, _ types.ExecutorEventSink) (*types.Response, error) {
	close(e.started)
	<-ctx.Done()
	close(e.returned)
	return &types.Response{ExitCode: 130, Error: types.NewExecutorError("cancelled", ctx.Err(), "")}, nil
}
func (e *treeEvidenceExecutor) ProcessTreeEvidence(context.Context, types.ExecutionID) (types.ProcessTreeEvidence, error) {
	if e.evidenceGate != nil {
		<-e.evidenceGate
	}
	return types.ProcessTreeEvidence{Process: types.ProcessIdentity{PID: 17, StartFingerprint: "generation", TreeID: "tree"}, Stopped: e.stopped}, nil
}

func TestSwarmCancelWaitsForFinalTreeEvidenceAndInspectSharesIt(t *testing.T) {
	gate := make(chan struct{})
	exec := &treeEvidenceExecutor{started: make(chan struct{}), returned: make(chan struct{}), evidenceGate: gate, stopped: true}
	s := swarm.New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "tree", swarm.Stateful, swarm.WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	var events []types.ExecutorEvent
	var eventsMu sync.Mutex
	go func() {
		_, err := s.Execute(context.Background(), h, "scope", "tree-proof", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
			return true
		}))
		runDone <- err
	}()
	<-exec.started
	cancelDone := make(chan struct {
		evidence types.CancellationEvidence
		err      error
	}, 1)
	go func() {
		evidence, err := s.Cancel(context.Background(), h, "scope", "tree-proof", "test")
		cancelDone <- struct {
			evidence types.CancellationEvidence
			err      error
		}{evidence, err}
	}()
	<-exec.returned
	repeated := make(chan struct {
		evidence types.CancellationEvidence
		err      error
	}, 5)
	for range 5 {
		go func() {
			evidence, err := s.Cancel(context.Background(), h, "scope", "tree-proof", "repeat")
			repeated <- struct {
				evidence types.CancellationEvidence
				err      error
			}{evidence, err}
		}()
	}
	inspectDone := make(chan struct {
		inspection swarm.ExecutionInspection
		err        error
	}, 1)
	go func() {
		inspection, err := s.Inspect(context.Background(), h, "scope", "tree-proof")
		inspectDone <- struct {
			inspection swarm.ExecutionInspection
			err        error
		}{inspection, err}
	}()
	select {
	case got := <-inspectDone:
		t.Fatalf("Inspect returned provisional state: %#v, %v", got.inspection, got.err)
	case <-time.After(25 * time.Millisecond):
	}
	close(gate)
	cancel := <-cancelDone
	if cancel.err != nil || cancel.evidence.NativeAcknowledged {
		t.Fatalf("Cancel = %#v, %v, want native false", cancel.evidence, cancel.err)
	}
	for range 5 {
		got := <-repeated
		if got.err != nil || got.evidence != cancel.evidence {
			t.Fatalf("repeated Cancel = %#v, %v; want %#v", got.evidence, got.err, cancel.evidence)
		}
	}
	inspection := <-inspectDone
	if inspection.err != nil || !inspection.inspection.Cancelled || !inspection.inspection.ProcessTreeEvidence.Stopped || inspection.inspection.ProcessTreeEvidence.Process.Validate() != nil {
		t.Fatalf("Inspect = %#v, %v", inspection.inspection, inspection.err)
	}
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	if len(events) != 1 || !events[0].Terminal || events[0].Type != "cancelled" {
		t.Fatalf("events = %#v", events)
	}
}

func TestSwarmCancellationWithoutAcknowledgementOrStopProofFailsClosed(t *testing.T) {
	gate := make(chan struct{})
	close(gate)
	exec := &treeEvidenceExecutor{started: make(chan struct{}), returned: make(chan struct{}), evidenceGate: gate}
	s := swarm.New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "tree", swarm.Stateful, swarm.WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	terminal := make(chan types.ExecutorEvent, 1)
	runDone := make(chan error, 1)
	go func() {
		_, err := s.Execute(context.Background(), h, "scope", "no-proof", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			if event.Terminal {
				terminal <- event
			}
			return true
		}))
		runDone <- err
	}()
	<-exec.started
	evidence, err := s.Cancel(context.Background(), h, "scope", "no-proof", "test")
	if err != nil || evidence.NativeAcknowledged {
		t.Fatalf("Cancel = %#v, %v", evidence, err)
	}
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if got := <-terminal; got.Type == "cancelled" {
		t.Fatalf("terminal = %#v, want fail closed", got)
	}
	inspection, err := s.Inspect(context.Background(), h, "scope", "no-proof")
	if err != nil || inspection.Cancelled || inspection.ProcessTreeEvidence.Stopped {
		t.Fatalf("Inspect = %#v, %v", inspection, err)
	}
}

type completionExecutor struct {
	started chan struct{}
	release <-chan struct{}
}

func (*completionExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*completionExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*completionExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*completionExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*completionExecutor) Close() error                { return nil }
func (e *completionExecutor) SendEvents(_ context.Context, _ types.ExecutionID, _ types.Message, _ types.ExecutorEventSink) (*types.Response, error) {
	close(e.started)
	<-e.release
	return &types.Response{}, nil
}

func TestSwarmLateCancelCannotReclassifyCompletedExecution(t *testing.T) {
	release := make(chan struct{})
	exec := &completionExecutor{started: make(chan struct{}), release: release}
	s := swarm.New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "complete", swarm.Stateful, swarm.WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	terminal := make(chan types.ExecutorEvent, 1)
	done := make(chan error, 1)
	go func() {
		_, err := s.Execute(context.Background(), h, "scope", "complete", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			if event.Terminal {
				terminal <- event
			}
			return true
		}))
		done <- err
	}()
	<-exec.started
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	evidence, err := s.Cancel(context.Background(), h, "scope", "complete", "late")
	if err != nil || evidence.NativeAcknowledged {
		t.Fatalf("late Cancel = %#v, %v", evidence, err)
	}
	if got := <-terminal; got.Type != "completed" {
		t.Fatalf("terminal = %#v, want completed", got)
	}
	inspection, err := s.Inspect(context.Background(), h, "scope", "complete")
	if err != nil || inspection.Cancelled {
		t.Fatalf("Inspect = %#v, %v", inspection, err)
	}
}
