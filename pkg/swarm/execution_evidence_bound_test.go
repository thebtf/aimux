package swarm

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	aimexecutor "github.com/thebtf/aimux/pkg/executor"
	pipeexecutor "github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/types"
)

type blockingExactEvidenceExecutor struct {
	evidenceStarted  chan struct{}
	evidenceReleased chan struct{}
	evidenceReturned chan struct{}
	block            atomic.Bool
	calls            atomic.Int32
	holds            atomic.Int32
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
func (e *blockingExactEvidenceExecutor) ReleaseProcessEvidence(types.ExecutionID) { e.holds.Add(-1) }
func (e *blockingExactEvidenceExecutor) ProcessTreeEvidence(context.Context, types.ExecutionID) (types.ProcessTreeEvidence, error) {
	call := e.calls.Add(1)
	if call == 1 {
		close(e.evidenceStarted)
	}
	if e.block.Load() {
		<-e.evidenceReleased // Deliberately non-cooperative: Swarm must still bound its terminal.
	}
	if call == 1 {
		close(e.evidenceReturned)
	}
	return types.ProcessTreeEvidence{Process: types.ProcessIdentity{PID: 17, StartFingerprint: "start", TreeID: "tree"}, Stopped: true}, nil
}

func TestSwarmBoundsBlockingExactProcessEvidenceAndReleasesLease(t *testing.T) {
	previous := processEvidenceCaptureTimeout
	processEvidenceCaptureTimeout = 20 * time.Millisecond
	t.Cleanup(func() { processEvidenceCaptureTimeout = previous })

	exec := &blockingExactEvidenceExecutor{
		evidenceStarted: make(chan struct{}), evidenceReleased: make(chan struct{}), evidenceReturned: make(chan struct{}),
	}
	exec.block.Store(true)
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "blocking-evidence", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	terminal := make(chan types.ExecutorEvent, 2)
	runDone := make(chan error, 1)
	go func() {
		_, err := s.Execute(context.Background(), h, "scope", "bounded-evidence", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			if event.Terminal {
				terminal <- event
			}
			return true
		}))
		runDone <- err
	}()
	<-exec.evidenceStarted

	joinCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cancelDone := make(chan error, 1)
	inspectDone := make(chan struct {
		inspection ExecutionInspection
		err        error
	}, 1)
	go func() { _, err := s.Cancel(joinCtx, h, "scope", "bounded-evidence", "join"); cancelDone <- err }()
	go func() {
		inspection, err := s.Inspect(joinCtx, h, "scope", "bounded-evidence")
		inspectDone <- struct {
			inspection ExecutionInspection
			err        error
		}{inspection, err}
	}()

	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if err := <-cancelDone; err != nil {
		t.Fatalf("Cancel = %v", err)
	}
	if inspection := <-inspectDone; inspection.err != nil || !inspection.inspection.Terminal {
		t.Fatalf("Inspect = %#v, %v", inspection.inspection, inspection.err)
	}
	if event := <-terminal; event.Type == "completed" {
		t.Fatalf("terminal = %#v, want fail-closed bounded evidence result", event)
	}
	if got := exec.holds.Load(); got != 0 {
		t.Fatalf("evidence holds = %d, want released", got)
	}

	close(exec.evidenceReleased) // Let the deliberately non-cooperative oracle exit.
	select {
	case <-exec.evidenceReturned:
	case <-time.After(time.Second):
		t.Fatal("blocked evidence provider goroutine did not exit after release")
	}

	exec.block.Store(false)
	if _, err := s.Execute(context.Background(), h, "scope", "bounded-evidence-next", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		if event.Terminal {
			terminal <- event
		}
		return true
	})); err != nil {
		t.Fatalf("next Execute = %v", err)
	}
	if event := <-terminal; event.Type != "completed" {
		t.Fatalf("next terminal = %#v, want completed after released capacity", event)
	}
	if got := exec.holds.Load(); got != 0 {
		t.Fatalf("evidence holds after reuse = %d, want released", got)
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
