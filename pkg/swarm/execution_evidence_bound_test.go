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
	exec.ready <- types.ProcessTreeEvidence{Process: types.ProcessIdentity{PID: 17, StartFingerprint: "start", TreeID: "tree"}, Stopped: true}
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
	exec.ready <- types.ProcessTreeEvidence{Process: types.ProcessIdentity{PID: 18, StartFingerprint: "deadline", TreeID: "tree"}, Stopped: true}
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
