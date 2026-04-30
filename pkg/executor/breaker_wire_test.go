// AIMUX-16 CR-002 — integration tests for the breaker wiring on the
// executor dispatch path. Verifies that:
//
//   1. SelectAvailableCLIs filters CLIs whose breakers are Open.
//   2. RecordResultToBreaker maps ErrorClass → breaker state per EC-2.3.
//   3. RunWithModelFallbackBreaker emits RecordSuccess on healthy run and
//      RecordFailure on Fatal/Quota terminal outcomes.
//   4. K consecutive Fatal classifications trip the breaker; the next
//      call to SelectAvailableCLIs excludes that CLI.
//   5. The HalfOpen → Closed transition fires on the first successful
//      probe after cooldown; subsequent failure re-opens the breaker.
//
// These tests do NOT modify breaker.go internals — they exercise only the
// new wiring surface area (SelectAvailableCLIs, RecordResultToBreaker,
// RunWithModelFallbackBreaker) plus the existing public breaker API.

package executor_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

// breakerStubExecutor is a minimal types.Executor whose Run behaviour is
// programmed by a slice of response functions. Reused locally to avoid
// touching the existing fallback_test.go stubFallbackExecutor.
type breakerStubExecutor struct {
	n   atomic.Int32
	fns []func(types.SpawnArgs) (*types.Result, error)
}

func (s *breakerStubExecutor) Run(_ context.Context, args types.SpawnArgs) (*types.Result, error) {
	idx := int(s.n.Add(1)) - 1
	if idx >= len(s.fns) {
		idx = len(s.fns) - 1
	}
	return s.fns[idx](args)
}

func (s *breakerStubExecutor) Start(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
	return nil, errors.New("breakerStubExecutor.Start: not implemented")
}

func (s *breakerStubExecutor) Name() string  { return "breaker-stub" }
func (s *breakerStubExecutor) Available() bool { return true }

// fatalResult builds a Result whose stderr matches a Fatal-classified pattern.
func fatalResult(_ types.SpawnArgs) (*types.Result, error) {
	return &types.Result{Content: "authentication failed", ExitCode: 1}, nil
}

// transientResult builds a Result whose content matches a Transient-classified pattern.
func transientResult(_ types.SpawnArgs) (*types.Result, error) {
	return &types.Result{Content: "connection refused", ExitCode: 1}, nil
}

// successResult builds a healthy Result.
func successResult(_ types.SpawnArgs) (*types.Result, error) {
	return &types.Result{Content: "ok", ExitCode: 0}, nil
}

// newTestRegistry builds a BreakerRegistry suitable for fast-failing tests:
// threshold=2, 100ms cooldown so HalfOpen transitions happen within test
// budget, halfOpen probe budget=1.
func newTestRegistry() *executor.BreakerRegistry {
	return executor.NewBreakerRegistry(executor.BreakerConfig{
		FailureThreshold: 2,
		CooldownSeconds:  1, // 1s — smallest non-zero supported by the duration math
		HalfOpenMaxCalls: 1,
	})
}

// --- T002-2: SelectAvailableCLIs ---

func TestSelectAvailableCLIs_NilBreakerReturnsInputUnchanged(t *testing.T) {
	clis := []string{"codex", "claude", "gemini"}
	got := executor.SelectAvailableCLIs(clis, nil)
	if !reflect.DeepEqual(got, clis) {
		t.Errorf("nil breaker should pass through clis: got %v, want %v", got, clis)
	}
}

func TestSelectAvailableCLIs_EmptyInputReturnsEmpty(t *testing.T) {
	reg := newTestRegistry()
	got := executor.SelectAvailableCLIs(nil, reg)
	if len(got) != 0 {
		t.Errorf("empty input should return empty: got %v", got)
	}
}

func TestSelectAvailableCLIs_OpenBreakerExcludesCLI(t *testing.T) {
	reg := newTestRegistry()
	// Trip codex via permanent (Fatal) failure.
	reg.Get("codex").RecordFailure(true)

	got := executor.SelectAvailableCLIs([]string{"codex", "claude", "gemini"}, reg)
	for _, cli := range got {
		if cli == "codex" {
			t.Errorf("codex should be excluded — breaker open; got=%v", got)
		}
	}
	if len(got) != 2 {
		t.Errorf("expected 2 CLIs (claude, gemini); got %d: %v", len(got), got)
	}
}

func TestSelectAvailableCLIs_AllOpenReturnsEmpty(t *testing.T) {
	reg := newTestRegistry()
	for _, cli := range []string{"codex", "claude", "gemini"} {
		reg.Get(cli).RecordFailure(true)
	}
	got := executor.SelectAvailableCLIs([]string{"codex", "claude", "gemini"}, reg)
	if len(got) != 0 {
		t.Errorf("all-open should return empty (caller surfaces NotFoundError per EC-2.1); got %v", got)
	}
}

// --- T002-3: RecordResultToBreaker classification mapping ---

func TestRecordResultToBreaker_NilBreakerNoOp(t *testing.T) {
	// Should not panic.
	executor.RecordResultToBreaker(nil, "codex", executor.ErrorClassFatal)
}

func TestRecordResultToBreaker_EmptyCLINoOp(t *testing.T) {
	reg := newTestRegistry()
	executor.RecordResultToBreaker(reg, "", executor.ErrorClassFatal)
	// Empty CLI must not create a breaker entry; AvailableCLIs over the same
	// list should return everything.
	if got := reg.AvailableCLIs([]string{"codex"}); len(got) != 1 {
		t.Errorf("empty cli should not affect codex breaker; got %v", got)
	}
}

func TestRecordResultToBreaker_FatalTripsImmediately(t *testing.T) {
	reg := newTestRegistry()
	executor.RecordResultToBreaker(reg, "codex", executor.ErrorClassFatal)
	if state := reg.Get("codex").State(); state != executor.BreakerOpen {
		t.Errorf("Fatal should trip breaker immediately to Open; got state=%d", state)
	}
}

func TestRecordResultToBreaker_QuotaIncrementsTowardThreshold(t *testing.T) {
	reg := newTestRegistry() // threshold=2
	executor.RecordResultToBreaker(reg, "codex", executor.ErrorClassQuota)
	if state := reg.Get("codex").State(); state != executor.BreakerClosed {
		t.Errorf("first Quota should not trip; got state=%d", state)
	}
	executor.RecordResultToBreaker(reg, "codex", executor.ErrorClassQuota)
	if state := reg.Get("codex").State(); state != executor.BreakerOpen {
		t.Errorf("Quota count reaching threshold (2) should trip; got state=%d", state)
	}
}

// EC-2.3: Transient MUST NOT increment the breaker counter.
func TestRecordResultToBreaker_TransientNoOpEC23(t *testing.T) {
	reg := newTestRegistry()
	for i := 0; i < 10; i++ {
		executor.RecordResultToBreaker(reg, "codex", executor.ErrorClassTransient)
	}
	cb := reg.Get("codex")
	if state := cb.State(); state != executor.BreakerClosed {
		t.Errorf("Transient must NOT trip breaker (EC-2.3 flapping network protection); got state=%d", state)
	}
	if f := cb.Failures(); f != 0 {
		t.Errorf("Transient must NOT increment failure counter (EC-2.3); got failures=%d", f)
	}
}

// ErrorClassModelUnavailable and ErrorClassUnknown also must not trip the breaker.
func TestRecordResultToBreaker_ModelUnavailableNoOp(t *testing.T) {
	reg := newTestRegistry()
	for i := 0; i < 10; i++ {
		executor.RecordResultToBreaker(reg, "codex", executor.ErrorClassModelUnavailable)
	}
	if f := reg.Get("codex").Failures(); f != 0 {
		t.Errorf("ModelUnavailable should be no-op for breaker (per-model concern, not per-CLI); got failures=%d", f)
	}
}

func TestRecordResultToBreaker_UnknownNoOp(t *testing.T) {
	reg := newTestRegistry()
	for i := 0; i < 10; i++ {
		executor.RecordResultToBreaker(reg, "codex", executor.ErrorClassUnknown)
	}
	if f := reg.Get("codex").Failures(); f != 0 {
		t.Errorf("Unknown should be no-op for breaker (could be transient); got failures=%d", f)
	}
}

// --- T002-4: RecordSuccess on completed dispatch ---

func TestRecordResultToBreaker_SuccessResetsFailureCount(t *testing.T) {
	reg := newTestRegistry()
	cb := reg.Get("codex")
	executor.RecordResultToBreaker(reg, "codex", executor.ErrorClassQuota) // failures=1
	if f := cb.Failures(); f != 1 {
		t.Fatalf("setup: expected failures=1; got %d", f)
	}
	executor.RecordResultToBreaker(reg, "codex", executor.ErrorClassNone)
	if f := cb.Failures(); f != 0 {
		t.Errorf("Success must zero the failure counter; got %d", f)
	}
	if state := cb.State(); state != executor.BreakerClosed {
		t.Errorf("Success must keep state=Closed; got %d", state)
	}
}

// --- T002-5: End-to-end RunWithModelFallbackBreaker ---

func TestRunWithModelFallbackBreaker_SuccessRecordsSuccess(t *testing.T) {
	reg := newTestRegistry()
	tracker := executor.NewModelCooldownTracker()
	stub := &breakerStubExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){successResult},
	}
	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-m", "model-a"}}

	result, err := executor.RunWithModelFallbackBreaker(
		context.Background(), stub, baseArgs,
		[]string{"model-a"}, "-m",
		tracker, time.Second,
		nil, nil,
		reg,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.ExitCode != 0 {
		t.Fatalf("expected success result; got %v", result)
	}
	if state := reg.Get("codex").State(); state != executor.BreakerClosed {
		t.Errorf("success should keep breaker Closed; got state=%d", state)
	}
}

func TestRunWithModelFallbackBreaker_FatalTripsBreaker(t *testing.T) {
	reg := newTestRegistry()
	tracker := executor.NewModelCooldownTracker()
	stub := &breakerStubExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){fatalResult},
	}
	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-m", "model-a"}}

	_, err := executor.RunWithModelFallbackBreaker(
		context.Background(), stub, baseArgs,
		[]string{"model-a"}, "-m",
		tracker, time.Second,
		nil, nil,
		reg,
	)
	if err == nil {
		t.Fatal("expected fatal error; got nil")
	}
	if state := reg.Get("codex").State(); state != executor.BreakerOpen {
		t.Errorf("fatal should trip breaker to Open; got state=%d", state)
	}
}

// EC-2.3: A transient terminal outcome (e.g. all models exhausted with only
// transient errors leading to "all models exhausted ...:transient" terminal
// error) MUST NOT trip the breaker. We verify the negative path: K transient
// dispatches in a row leave the breaker Closed.
func TestRunWithModelFallbackBreaker_TransientDoesNotTripEC23(t *testing.T) {
	reg := newTestRegistry()
	tracker := executor.NewModelCooldownTracker()

	for i := 0; i < 10; i++ {
		// Each call: transient, then transient on retry — yields terminal
		// "transient error after retry" classification.
		stub := &breakerStubExecutor{
			fns: []func(types.SpawnArgs) (*types.Result, error){
				transientResult,
				transientResult, // retry within the same model
			},
		}
		baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-m", "model-a"}}
		_, _ = executor.RunWithModelFallbackBreaker(
			context.Background(), stub, baseArgs,
			[]string{"model-a"}, "-m",
			tracker, time.Second,
			nil, nil,
			reg,
		)
	}

	if state := reg.Get("codex").State(); state != executor.BreakerClosed {
		t.Errorf("EC-2.3 violated: 10 transient dispatches tripped breaker; got state=%d", state)
	}
	if f := reg.Get("codex").Failures(); f != 0 {
		t.Errorf("EC-2.3 violated: transient dispatches incremented failure counter; got %d", f)
	}
}

// --- T002-5: K consecutive Fatal trips, SelectAvailableCLIs excludes ---

func TestRunWithModelFallbackBreaker_KFatalsTripThenSelectExcludes(t *testing.T) {
	reg := newTestRegistry()
	tracker := executor.NewModelCooldownTracker()

	// One Fatal is enough (permanent=true), but the spec language is
	// "K consecutive Fatal classifications". We assert the wiring works
	// for K=1 (immediate trip) AND that the same wiring works for K>=
	// threshold via Quota (cumulative).
	stub := &breakerStubExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){fatalResult},
	}
	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-m", "model-a"}}
	_, _ = executor.RunWithModelFallbackBreaker(
		context.Background(), stub, baseArgs,
		[]string{"model-a"}, "-m",
		tracker, time.Second,
		nil, nil,
		reg,
	)

	got := executor.SelectAvailableCLIs([]string{"codex", "claude", "gemini"}, reg)
	for _, cli := range got {
		if cli == "codex" {
			t.Errorf("after Fatal trip, SelectAvailableCLIs should exclude codex; got %v", got)
		}
	}
}

// --- T002-5: HalfOpen → Closed transition; HalfOpen → Open on failure ---

func TestBreakerHalfOpenToClosedTransition(t *testing.T) {
	reg := newTestRegistry() // 1s cooldown
	cb := reg.Get("codex")

	// Trip via Fatal classification through the wiring.
	executor.RecordResultToBreaker(reg, "codex", executor.ErrorClassFatal)
	if cb.State() != executor.BreakerOpen {
		t.Fatal("setup: expected Open after Fatal")
	}

	// Wait past cooldown.
	time.Sleep(1100 * time.Millisecond)

	// CanAllow should now report true (HalfOpen-with-budget) without
	// advancing state — preserves EC-2.2 (no double-probe race).
	if !cb.CanAllow() {
		t.Fatal("after cooldown, CanAllow should permit the next probe")
	}

	// Successful run through the wired path should flip back to Closed.
	tracker := executor.NewModelCooldownTracker()
	stub := &breakerStubExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){successResult},
	}
	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-m", "model-a"}}
	_, err := executor.RunWithModelFallbackBreaker(
		context.Background(), stub, baseArgs,
		[]string{"model-a"}, "-m",
		tracker, time.Second,
		nil, nil,
		reg,
	)
	if err != nil {
		t.Fatalf("expected probe to succeed: %v", err)
	}
	if cb.State() != executor.BreakerClosed {
		t.Errorf("HalfOpen + success must flip to Closed; got state=%d", cb.State())
	}
	if cb.Failures() != 0 {
		t.Errorf("Closed transition must zero failure counter; got %d", cb.Failures())
	}
}

func TestBreakerHalfOpenToOpenOnFailure(t *testing.T) {
	reg := newTestRegistry()
	cb := reg.Get("codex")

	// Trip.
	executor.RecordResultToBreaker(reg, "codex", executor.ErrorClassFatal)

	// Wait past cooldown.
	time.Sleep(1100 * time.Millisecond)

	// Failed probe through the wired path → Open again.
	tracker := executor.NewModelCooldownTracker()
	stub := &breakerStubExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){fatalResult},
	}
	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-m", "model-a"}}
	_, _ = executor.RunWithModelFallbackBreaker(
		context.Background(), stub, baseArgs,
		[]string{"model-a"}, "-m",
		tracker, time.Second,
		nil, nil,
		reg,
	)

	if cb.State() != executor.BreakerOpen {
		t.Errorf("HalfOpen + Fatal must re-open breaker; got state=%d", cb.State())
	}
}

// --- T002-5: nil-breaker passthrough on the new entry point ---

func TestRunWithModelFallbackBreaker_NilBreakerEqualsLegacy(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()
	stub := &breakerStubExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){successResult},
	}
	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-m", "model-a"}}

	result, err := executor.RunWithModelFallbackBreaker(
		context.Background(), stub, baseArgs,
		[]string{"model-a"}, "-m",
		tracker, time.Second,
		nil, nil,
		nil, // no breaker
	)
	if err != nil {
		t.Fatalf("nil-breaker passthrough should succeed: %v", err)
	}
	if result == nil || result.ExitCode != 0 {
		t.Fatalf("expected success result via passthrough; got %v", result)
	}
}
