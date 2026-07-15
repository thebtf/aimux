package swarm

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

// These tests cover an accepted T018 CR-002 review finding: when exact
// process evidence is required to confirm finality and that evidence is
// missing or reports Stopped=false, Execute must produce a caller-visible,
// non-retryable failure instead of a clean success or a retryable timeout.
// Pre-spawn cancellation and start failures are out of scope here — they
// already retain their own truthful classification through a different code
// path (record.outcome != executionOutcomeCompleted).

// exitZeroUnstoppedExecutor simulates a provider that reports a clean exit
// (ExitCode 0, no error) while the exact process-evidence channel proves the
// owned tree was never confirmed stopped.
type exitZeroUnstoppedExecutor struct {
	ready chan types.ProcessTreeEvidence
}

func (*exitZeroUnstoppedExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*exitZeroUnstoppedExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{ExitCode: 0}, nil
}
func (*exitZeroUnstoppedExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{ExitCode: 0}, nil
}
func (*exitZeroUnstoppedExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*exitZeroUnstoppedExecutor) Close() error                { return nil }
func (*exitZeroUnstoppedExecutor) SendEvents(context.Context, types.ExecutionID, types.Message, types.ExecutorEventSink) (*types.Response, error) {
	return &types.Response{ExitCode: 0}, nil
}
func (e *exitZeroUnstoppedExecutor) HoldProcessEvidence(types.ExecutionID) bool { return true }
func (e *exitZeroUnstoppedExecutor) ProcessEvidenceReady(types.ExecutionID) <-chan types.ProcessTreeEvidence {
	return e.ready
}
func (e *exitZeroUnstoppedExecutor) ReleaseProcessEvidence(types.ExecutionID) {}

func unstoppedEvidence() types.ProcessTreeEvidence {
	return types.ProcessTreeEvidence{
		Process: types.ProcessIdentity{
			PID:              4242,
			StartFingerprint: "1700000000",
			TreeID:           "pipe:4242:1700000000",
		},
		OwnershipBoundary: types.ProcessOwnershipBoundaryProcessGroup,
		Stopped:           false,
	}
}

func TestSwarmExecuteExitZeroWithUnconfirmedStopIsCallerVisibleFailure(t *testing.T) {
	ready := make(chan types.ProcessTreeEvidence, 1)
	ready <- unstoppedEvidence()
	exec := &exitZeroUnstoppedExecutor{ready: ready}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "unstopped-exit0", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}

	var terminal types.ExecutorEvent
	resp, execErr := s.Execute(context.Background(), h, "scope", "exit0-unconfirmed", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		if event.Terminal {
			terminal = event
		}
		return true
	}))

	if execErr == nil && (resp == nil || resp.Error == nil) {
		t.Fatalf("Execute() with exit-0 + unconfirmed Stopped=false returned clean success: resp=%#v err=%v; want a caller-visible non-retryable failure", resp, execErr)
	}
	if resp != nil && resp.Error != nil && resp.Error.Type == types.ErrorTypeTimeout {
		t.Fatalf("resp.Error.Type = timeout for an unconfirmed exit-0 evidence case; want a non-retryable classification, not a retryable timeout")
	}
	if terminal.Type == "completed" {
		t.Fatalf("terminal event type = completed for unconfirmed Stopped=false evidence; want failed")
	}
}

// timeoutUnstoppedExecutor simulates a provider that reports a timeout
// (mirroring pipe.Executor's own timedOut branch) while the exact
// process-evidence channel proves the owned tree was never confirmed
// stopped, so the timeout cannot be trusted as safely retryable.
type timeoutUnstoppedExecutor struct {
	ready chan types.ProcessTreeEvidence
}

func (*timeoutUnstoppedExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*timeoutUnstoppedExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{ExitCode: 124}, nil
}
func (*timeoutUnstoppedExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{ExitCode: 124}, nil
}
func (*timeoutUnstoppedExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*timeoutUnstoppedExecutor) Close() error                { return nil }
func (*timeoutUnstoppedExecutor) SendEvents(context.Context, types.ExecutionID, types.Message, types.ExecutorEventSink) (*types.Response, error) {
	return &types.Response{
		ExitCode: 124,
		Partial:  true,
		Error:    types.NewTimeoutError("timed out after 5s", "partial output"),
	}, nil
}
func (e *timeoutUnstoppedExecutor) HoldProcessEvidence(types.ExecutionID) bool { return true }
func (e *timeoutUnstoppedExecutor) ProcessEvidenceReady(types.ExecutionID) <-chan types.ProcessTreeEvidence {
	return e.ready
}
func (e *timeoutUnstoppedExecutor) ReleaseProcessEvidence(types.ExecutionID) {}

func TestSwarmExecuteTimeoutWithUnconfirmedStopIsNotRetryable(t *testing.T) {
	ready := make(chan types.ProcessTreeEvidence, 1)
	ready <- unstoppedEvidence()
	exec := &timeoutUnstoppedExecutor{ready: ready}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "unstopped-timeout", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}

	var terminal types.ExecutorEvent
	resp, _ := s.Execute(context.Background(), h, "scope", "timeout-unconfirmed", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		if event.Terminal {
			terminal = event
		}
		return true
	}))

	if resp != nil && resp.Error != nil && resp.Error.Type == types.ErrorTypeTimeout {
		t.Fatalf("resp.Error.Type = timeout with unconfirmed Stopped=false; want a non-retryable classification because finality is unconfirmed (evidence never proved the tree stopped)")
	}
	if terminal.Type == "timeout" {
		t.Fatalf("terminal event type = timeout with unconfirmed Stopped=false; want failed — a retryable timeout requires stop proof")
	}
}
