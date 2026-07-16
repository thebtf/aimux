package swarm_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

func newSessionBindingExecutionSwarm(t *testing.T, exec types.ExecutorV2) *swarm.Swarm {
	t.Helper()

	s := swarm.New(singleFactory(exec), nil, swarm.WithStatefulTTL(0))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return s
}

// TestExecuteSessionBindingReportsTrueNativeReturn proves that once every
// pre-provider admission gate passes, ExecuteSessionBinding reports
// attempted=true regardless of the executor's own outcome, and returns the
// executor's response (CR-003 causal-return classification).
func TestExecuteSessionBindingReportsTrueNativeReturn(t *testing.T) {
	exec := &mockExecutorV2{alive: types.HealthAlive}
	s := newSessionBindingExecutionSwarm(t, exec)
	binding, err := s.AcquireSessionBinding(context.Background(), "codex", types.SessionBindingRequest{Mode: types.SessionBindingModeStateless}, swarm.WithScope("scope-a"))
	if err != nil {
		t.Fatalf("AcquireSessionBinding: %v", err)
	}
	response, attempted, err := s.ExecuteSessionBinding(context.Background(), binding, types.ExecutionID("exec-1"), types.Message{Content: "hi"}, nil)
	if err != nil {
		t.Fatalf("ExecuteSessionBinding: %v", err)
	}
	if !attempted {
		t.Fatal("attempted = false, want true for a run that reached the executor")
	}
	if response == nil || response.Content != "ok" {
		t.Fatalf("response = %#v, want executor content preserved", response)
	}
}

// TestExecuteSessionBindingReportsFalseOnPreProviderRejection proves that a
// rejection before the provider is ever invoked (here: a blank ExecutionID)
// is classified as attempted=false — the coordinator must be able to tell
// this apart from a true native return without guessing from error text.
func TestExecuteSessionBindingReportsFalseOnPreProviderRejection(t *testing.T) {
	exec := &mockExecutorV2{alive: types.HealthAlive}
	s := newSessionBindingExecutionSwarm(t, exec)
	binding, err := s.AcquireSessionBinding(context.Background(), "codex", types.SessionBindingRequest{Mode: types.SessionBindingModeStateless}, swarm.WithScope("scope-b"))
	if err != nil {
		t.Fatalf("AcquireSessionBinding: %v", err)
	}
	response, attempted, err := s.ExecuteSessionBinding(context.Background(), binding, types.ExecutionID(""), types.Message{Content: "hi"}, nil)
	if err == nil {
		t.Fatal("ExecuteSessionBinding with blank execution ID error = nil, want validation error")
	}
	if attempted {
		t.Fatal("attempted = true, want false: execution never reached the provider")
	}
	if response != nil {
		t.Fatalf("response = %#v, want nil on pre-provider rejection", response)
	}
}

// TestExecuteSessionBindingRejectsAbsentHandle proves a zero-value binding
// (e.g. one a caller never actually acquired) fails closed without invoking
// the provider.
func TestExecuteSessionBindingRejectsAbsentHandle(t *testing.T) {
	exec := &mockExecutorV2{alive: types.HealthAlive}
	s := newSessionBindingExecutionSwarm(t, exec)
	_, attempted, err := s.ExecuteSessionBinding(context.Background(), swarm.LiveSessionBinding{}, types.ExecutionID("exec-2"), types.Message{Content: "hi"}, nil)
	if !errors.Is(err, swarm.ErrHandleNotFound) {
		t.Fatalf("err = %v, want ErrHandleNotFound", err)
	}
	if attempted {
		t.Fatal("attempted = true, want false for an absent handle")
	}
}

// TestReleaseSessionBindingClosesLiveHandle proves ReleaseSessionBinding
// actually tears the live handle down: a later ExecuteSessionBinding call
// against the same (now-released) binding must fail before reaching the
// provider, proving no live handle/executor leaked past the release.
func TestReleaseSessionBindingClosesLiveHandle(t *testing.T) {
	exec := &mockExecutorV2{alive: types.HealthAlive}
	s := newSessionBindingExecutionSwarm(t, exec)
	binding, err := s.AcquireSessionBinding(context.Background(), "codex", types.SessionBindingRequest{Mode: types.SessionBindingModeStateless}, swarm.WithScope("scope-c"))
	if err != nil {
		t.Fatalf("AcquireSessionBinding: %v", err)
	}
	if err := s.ReleaseSessionBinding(context.Background(), binding, "test-release"); err != nil {
		t.Fatalf("ReleaseSessionBinding: %v", err)
	}
	if !exec.isClosed() {
		t.Fatal("executor Close was never called by ReleaseSessionBinding")
	}
	_, attempted, err := s.ExecuteSessionBinding(context.Background(), binding, types.ExecutionID("exec-3"), types.Message{Content: "hi"}, nil)
	if err == nil {
		t.Fatal("ExecuteSessionBinding after release error = nil, want the handle to be gone")
	}
	if attempted {
		t.Fatal("attempted = true, want false: the released handle must never reach the provider again")
	}
}

// TestReleaseSessionBindingRejectsAbsentHandle mirrors the execute-side
// zero-value guard for the release seam.
func TestReleaseSessionBindingRejectsAbsentHandle(t *testing.T) {
	exec := &mockExecutorV2{alive: types.HealthAlive}
	s := newSessionBindingExecutionSwarm(t, exec)
	if err := s.ReleaseSessionBinding(context.Background(), swarm.LiveSessionBinding{}, "reason"); !errors.Is(err, swarm.ErrHandleNotFound) {
		t.Fatalf("ReleaseSessionBinding(absent) = %v, want ErrHandleNotFound", err)
	}
}

// ownedLeaseSessionBindingExecutor is a minimal EventExecutor that also
// satisfies Swarm's private owned-process-evidence-lease capability, so a
// session-binding test can deterministically drive the owned-process
// pre-spawn cancellation gate (see pkg/swarm.executeAdmitted) using only
// the exported ExecutorV2/EventExecutor surface — no internal package hook
// or sleep is required.
type ownedLeaseSessionBindingExecutor struct{ mockExecutorV2 }

func (e *ownedLeaseSessionBindingExecutor) SendEvents(context.Context, types.ExecutionID, types.Message, types.ExecutorEventSink) (*types.Response, error) {
	return &types.Response{Content: "ok"}, nil
}

func (e *ownedLeaseSessionBindingExecutor) AcquireProcessEvidenceLease(types.ExecutionID) (any, <-chan types.ProcessTreeEvidence, bool) {
	return struct{}{}, nil, true
}

func (e *ownedLeaseSessionBindingExecutor) SendEventsWithProcessEvidenceLease(context.Context, types.ExecutionID, any, types.Message, types.ExecutorEventSink) (*types.Response, error) {
	panic("owned-process provider call must never run once the context is already cancelled before spawn")
}

func (e *ownedLeaseSessionBindingExecutor) ReleaseProcessEvidenceLease(types.ExecutionID, any) {}

// TestExecuteSessionBindingReportsFalseWhenContextCancelledBeforeOwnedProviderSpawn
// proves the owned-process pre-spawn cancellation gate is classified as
// never-attempted: the admission marker fires immediately before the actual
// provider call, never before Swarm's own pre-spawn cancellation check. A
// context already cancelled before ExecuteSessionBinding runs deterministically
// hits that gate — no sleeps or internal hooks needed.
func TestExecuteSessionBindingReportsFalseWhenContextCancelledBeforeOwnedProviderSpawn(t *testing.T) {
	exec := &ownedLeaseSessionBindingExecutor{mockExecutorV2: mockExecutorV2{alive: types.HealthAlive}}
	s := newSessionBindingExecutionSwarm(t, exec)
	binding, err := s.AcquireSessionBinding(context.Background(), "codex", types.SessionBindingRequest{Mode: types.SessionBindingModeStateless}, swarm.WithScope("scope-owned-lease-precancel"))
	if err != nil {
		t.Fatalf("AcquireSessionBinding: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response, attempted, err := s.ExecuteSessionBinding(ctx, binding, types.ExecutionID("exec-owned-lease-precancel"), types.Message{Content: "hi"}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if attempted {
		t.Fatal("attempted = true, want false: the owned provider was never spawned before the context was already cancelled")
	}
	if response != nil && response.Content == "ok" {
		t.Fatalf("response = %#v, want no successful provider content on a never-attempted run", response)
	}
}

// TestExecuteSessionBindingRejectsMutatedHandleGeneration proves a caller
// that mutates the captured HandleGeneration fails closed before the
// provider runs (CR-003 exact capability token).
func TestExecuteSessionBindingRejectsMutatedHandleGeneration(t *testing.T) {
	exec := &mockExecutorV2{alive: types.HealthAlive}
	s := newSessionBindingExecutionSwarm(t, exec)
	binding, err := s.AcquireSessionBinding(context.Background(), "codex", types.SessionBindingRequest{Mode: types.SessionBindingModeStateless}, swarm.WithScope("scope-mutated-generation"))
	if err != nil {
		t.Fatalf("AcquireSessionBinding: %v", err)
	}
	binding.HandleGeneration++
	_, attempted, err := s.ExecuteSessionBinding(context.Background(), binding, types.ExecutionID("exec-mutated-generation"), types.Message{Content: "hi"}, nil)
	if !errors.Is(err, swarm.ErrHandleNotFound) {
		t.Fatalf("err = %v, want ErrHandleNotFound", err)
	}
	if attempted {
		t.Fatal("attempted = true, want false: a mutated handle generation must never reach the provider")
	}
}

// TestExecuteSessionBindingRejectsMutatedRegistryGeneration proves a caller
// that mutates the captured RegistryGeneration fails closed before the
// provider runs.
func TestExecuteSessionBindingRejectsMutatedRegistryGeneration(t *testing.T) {
	exec := &mockExecutorV2{alive: types.HealthAlive}
	s := newSessionBindingExecutionSwarm(t, exec)
	binding, err := s.AcquireSessionBinding(context.Background(), "codex", types.SessionBindingRequest{Mode: types.SessionBindingModeStateless}, swarm.WithScope("scope-mutated-registry-generation"))
	if err != nil {
		t.Fatalf("AcquireSessionBinding: %v", err)
	}
	binding.RegistryGeneration++
	_, attempted, err := s.ExecuteSessionBinding(context.Background(), binding, types.ExecutionID("exec-mutated-registry-generation"), types.Message{Content: "hi"}, nil)
	if !errors.Is(err, swarm.ErrHandleNotFound) {
		t.Fatalf("err = %v, want ErrHandleNotFound", err)
	}
	if attempted {
		t.Fatal("attempted = true, want false: a mutated registry generation must never reach the provider")
	}
}

// TestExecuteSessionBindingRejectsMutatedHandleID proves a caller that
// mutates the captured HandleID fails closed before the provider runs.
func TestExecuteSessionBindingRejectsMutatedHandleID(t *testing.T) {
	exec := &mockExecutorV2{alive: types.HealthAlive}
	s := newSessionBindingExecutionSwarm(t, exec)
	binding, err := s.AcquireSessionBinding(context.Background(), "codex", types.SessionBindingRequest{Mode: types.SessionBindingModeStateless}, swarm.WithScope("scope-mutated-handle-id"))
	if err != nil {
		t.Fatalf("AcquireSessionBinding: %v", err)
	}
	binding.HandleID += "-mutated"
	_, attempted, err := s.ExecuteSessionBinding(context.Background(), binding, types.ExecutionID("exec-mutated-handle-id"), types.Message{Content: "hi"}, nil)
	if !errors.Is(err, swarm.ErrHandleNotFound) {
		t.Fatalf("err = %v, want ErrHandleNotFound", err)
	}
	if attempted {
		t.Fatal("attempted = true, want false: a mutated handle ID must never reach the provider")
	}
}

// TestExecuteSessionBindingRejectsInjectedProviderSessionOnStatelessBinding
// proves a caller that injects a ProviderSession into a stateless binding
// (whose executor exposes no identity provider) fails closed rather than
// being silently ignored.
func TestExecuteSessionBindingRejectsInjectedProviderSessionOnStatelessBinding(t *testing.T) {
	exec := &mockExecutorV2{alive: types.HealthAlive}
	s := newSessionBindingExecutionSwarm(t, exec)
	binding, err := s.AcquireSessionBinding(context.Background(), "codex", types.SessionBindingRequest{Mode: types.SessionBindingModeStateless}, swarm.WithScope("scope-injected-provider"))
	if err != nil {
		t.Fatalf("AcquireSessionBinding: %v", err)
	}
	if binding.ProviderSession != nil {
		t.Fatal("stateless binding unexpectedly captured a ProviderSession")
	}
	injected := types.SessionIdentity{Provider: "neutral", ID: "injected", Generation: 1}
	binding.ProviderSession = &injected
	_, attempted, err := s.ExecuteSessionBinding(context.Background(), binding, types.ExecutionID("exec-injected-provider"), types.Message{Content: "hi"}, nil)
	if !errors.Is(err, swarm.ErrHandleNotFound) {
		t.Fatalf("err = %v, want ErrHandleNotFound", err)
	}
	if attempted {
		t.Fatal("attempted = true, want false: an injected provider session must never reach the provider")
	}
}

// TestExecuteSessionBindingRejectsMismatchedProviderSessionIdentity proves a
// caller that mutates the captured ProviderSession identity fails closed
// even though the executor genuinely exposes a live identity.
func TestExecuteSessionBindingRejectsMismatchedProviderSessionIdentity(t *testing.T) {
	identity := types.SessionIdentity{Provider: "neutral", ID: "session-mismatch", Generation: 1}
	exec := newLiveBindingExecutor(identity, true)
	s := newSessionBindingExecutionSwarm(t, exec)
	binding, err := s.AcquireSessionBinding(context.Background(), "codex", types.SessionBindingRequest{Mode: types.SessionBindingModeNew}, swarm.WithScope("scope-provider-mismatch"))
	if err != nil {
		t.Fatalf("AcquireSessionBinding: %v", err)
	}
	if binding.ProviderSession == nil {
		t.Fatal("new binding missing ProviderSession")
	}
	mutated := *binding.ProviderSession
	mutated.Generation++
	binding.ProviderSession = &mutated
	_, attempted, err := s.ExecuteSessionBinding(context.Background(), binding, types.ExecutionID("exec-provider-mismatch"), types.Message{Content: "hi"}, nil)
	if !errors.Is(err, swarm.ErrHandleNotFound) {
		t.Fatalf("err = %v, want ErrHandleNotFound", err)
	}
	if attempted {
		t.Fatal("attempted = true, want false: a mutated provider session identity must never reach the provider")
	}
}

// TestExecuteSessionBindingRejectsNilProviderSessionWhenExecutorHasIdentity
// proves a caller-nilled ProviderSession never downgrades an identified
// executor's attempt into an unauthenticated one.
func TestExecuteSessionBindingRejectsNilProviderSessionWhenExecutorHasIdentity(t *testing.T) {
	identity := types.SessionIdentity{Provider: "neutral", ID: "session-nil-downgrade", Generation: 1}
	exec := newLiveBindingExecutor(identity, true)
	s := newSessionBindingExecutionSwarm(t, exec)
	binding, err := s.AcquireSessionBinding(context.Background(), "codex", types.SessionBindingRequest{Mode: types.SessionBindingModeNew}, swarm.WithScope("scope-provider-nil-downgrade"))
	if err != nil {
		t.Fatalf("AcquireSessionBinding: %v", err)
	}
	binding.ProviderSession = nil
	_, attempted, err := s.ExecuteSessionBinding(context.Background(), binding, types.ExecutionID("exec-provider-nil-downgrade"), types.Message{Content: "hi"}, nil)
	if !errors.Is(err, swarm.ErrHandleNotFound) {
		t.Fatalf("err = %v, want ErrHandleNotFound", err)
	}
	if attempted {
		t.Fatal("attempted = true, want false: a nilled provider session must never downgrade to an unauthenticated attempt")
	}
}

// TestReleaseSessionBindingRejectsMutatedHandleGeneration mirrors the
// execute-side stale-binding fence for the release seam: a mutated binding
// must fail before the live handle is closed.
func TestReleaseSessionBindingRejectsMutatedHandleGeneration(t *testing.T) {
	exec := &mockExecutorV2{alive: types.HealthAlive}
	s := newSessionBindingExecutionSwarm(t, exec)
	binding, err := s.AcquireSessionBinding(context.Background(), "codex", types.SessionBindingRequest{Mode: types.SessionBindingModeStateless}, swarm.WithScope("scope-release-mutated-generation"))
	if err != nil {
		t.Fatalf("AcquireSessionBinding: %v", err)
	}
	binding.HandleGeneration++
	if err := s.ReleaseSessionBinding(context.Background(), binding, "test-mutated-release"); !errors.Is(err, swarm.ErrHandleNotFound) {
		t.Fatalf("ReleaseSessionBinding err = %v, want ErrHandleNotFound", err)
	}
	if exec.isClosed() {
		t.Fatal("executor Close was called despite a mutated (stale) binding — release must fail before close")
	}
}
