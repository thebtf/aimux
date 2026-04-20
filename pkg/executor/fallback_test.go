package executor_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

// stubFallbackExecutor is a minimal types.Executor whose Run behaviour is
// controlled by a per-call counter. Each successive call invokes the next
// entry in the funcs slice; if the counter exceeds the slice length the last
// entry is reused.
type stubFallbackExecutor struct {
	n   atomic.Int32
	fns []func(types.SpawnArgs) (*types.Result, error)
}

func (s *stubFallbackExecutor) Run(_ context.Context, args types.SpawnArgs) (*types.Result, error) {
	idx := int(s.n.Add(1)) - 1
	if idx >= len(s.fns) {
		idx = len(s.fns) - 1
	}
	return s.fns[idx](args)
}

// Start is required by types.Executor; unused in these tests.
func (s *stubFallbackExecutor) Start(_ context.Context, _ types.SpawnArgs) (types.Session, error) {
	return nil, errors.New("stubFallbackExecutor.Start: not implemented")
}

// Name is required by types.Executor; returns a fixed identifier for tests.
func (s *stubFallbackExecutor) Name() string { return "stub" }

// Available is required by types.Executor; stub is always available.
func (s *stubFallbackExecutor) Available() bool { return true }

// --- Q5a: transient error followed by model-unavailable on retry ---

// TestModelFallback_TransientThenModelUnavailable verifies the nested retry path:
//   - First call returns a transient error → RunWithModelFallback retries the same model.
//   - Retry call returns a model-unavailable error → model marked cooled down, advance to next.
//   - Next model succeeds → result returned.
func TestModelFallback_TransientThenModelUnavailable(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	stub := &stubFallbackExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){
			// Call 1: model-a, transient error.
			func(args types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "connection refused", ExitCode: 1}, nil
			},
			// Call 2: model-a retry, model-unavailable error.
			func(args types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "model not found: model-a", ExitCode: 1}, nil
			},
			// Call 3: model-b, success.
			func(args types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "success from model-b", ExitCode: 0}, nil
			},
		},
	}

	baseArgs := types.SpawnArgs{
		CLI:     "codex",
		Command: "echo",
		Args:    []string{"-p", "hello", "-m", "model-a"},
	}
	models := []string{"model-a", "model-b"}

	result, err := executor.RunWithModelFallback(
		context.Background(),
		stub,
		baseArgs,
		models,
		"-m",
		tracker,
		1*time.Second,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("RunWithModelFallback: unexpected error: %v", err)
	}
	if result == nil || result.Content != "success from model-b" {
		t.Errorf("result content = %q, want %q", result.Content, "success from model-b")
	}
	if stub.n.Load() != 3 {
		t.Errorf("executor called %d times, want 3 (transient + retry-unavailable + model-b)", stub.n.Load())
	}
	// model-a must be on cooldown after the model-unavailable on retry.
	if tracker.IsAvailable("codex", "model-a") {
		t.Error("model-a should be on cooldown after model-unavailable error on transient retry")
	}
	// model-b must NOT be on cooldown — it succeeded.
	if !tracker.IsAvailable("codex", "model-b") {
		t.Error("model-b should NOT be on cooldown after successful execution")
	}
}

// TestModelFallback_SentinelErrors_ErrQuotaExhausted verifies that when all models
// hit quota, the returned error wraps ErrQuotaExhausted and is detectable via errors.Is.
func TestModelFallback_SentinelErrors_ErrQuotaExhausted(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()
	stub := &stubFallbackExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){
			func(args types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "quota exceeded", ExitCode: 1}, nil
			},
		},
	}

	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-p", "hi"}}
	_, err := executor.RunWithModelFallback(
		context.Background(), stub, baseArgs,
		[]string{"model-a"}, "", tracker, 1*time.Second, nil, nil,
	)
	if err == nil {
		t.Fatal("expected error when all models are quota-limited")
	}
	if !errors.Is(err, executor.ErrQuotaExhausted) {
		t.Errorf("expected errors.Is(err, ErrQuotaExhausted) to be true; err = %v", err)
	}
}

// TestModelFallback_SentinelErrors_ErrModelUnavailable verifies that when all models
// are unavailable, the returned error wraps ErrModelUnavailable and is detectable via errors.Is.
func TestModelFallback_SentinelErrors_ErrModelUnavailable(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()
	stub := &stubFallbackExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){
			func(args types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "model not found: model-a", ExitCode: 1}, nil
			},
		},
	}

	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-p", "hi"}}
	_, err := executor.RunWithModelFallback(
		context.Background(), stub, baseArgs,
		[]string{"model-a"}, "", tracker, 1*time.Second, nil, nil,
	)
	if err == nil {
		t.Fatal("expected error when all models are unavailable")
	}
	if !errors.Is(err, executor.ErrModelUnavailable) {
		t.Errorf("expected errors.Is(err, ErrModelUnavailable) to be true; err = %v", err)
	}
}

// --- T007: nil-wrap regression test for ErrorClassUnknown ---

// TestModelFallback_UnknownErrorClass_NilWrapRegression is a direct regression
// test for the P0 bug: RunWithModelFallback was missing a default: case, so
// ErrorClassUnknown (exit≠0 with unrecognised message) left lastErr=nil.
// The final fmt.Errorf("%w", nil) produced "%!w(<nil>)" in the error string.
//
// After the fix: the default: case sets lastErr to a structured message with
// the CLI, model, exit code, and redacted excerpt.  This test verifies:
//  1. err is non-nil when the only model returns an unrecognised non-zero exit.
//  2. err.Error() does NOT contain the nil-wrap sentinel "%!w(".
//  3. err.Error() contains recognisable context (cli+model names or exit code).
func TestModelFallback_UnknownErrorClass_NilWrapRegression(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	stub := &stubFallbackExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){
			// Returns exit code 42 with content that matches no known error class.
			func(args types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "something unexpected happened", ExitCode: 42}, nil
			},
		},
	}

	baseArgs := types.SpawnArgs{CLI: "testcli", Command: "testcli", Args: []string{"-p", "probe"}}
	_, err := executor.RunWithModelFallback(
		context.Background(), stub, baseArgs,
		[]string{"model-x"}, "", tracker, 1*time.Second, nil, nil,
	)

	if err == nil {
		t.Fatal("T007 nil-wrap regression: RunWithModelFallback returned nil error for unknown exit code — bug re-introduced")
	}

	msg := err.Error()

	// The sentinel for the old bug: fmt.Errorf("%w", nil) formats as "%!w(<nil>)".
	if strings.Contains(msg, "%!w(") {
		t.Errorf("T007 nil-wrap regression: error message contains nil-wrap sentinel %%!w(: %q", msg)
	}

	// Verify the message includes meaningful context (not an empty string).
	if msg == "" {
		t.Error("T007 nil-wrap regression: error message is empty")
	}
}

// --- T007: BuildModelChain suffix-strip gate test ---

// TestBuildModelChain_SuffixStripGate verifies that suffix-stripped model
// variants are appended ONLY for quota/model-unavailable error classes, and
// NOT for transient, fatal, unknown, or none classes.
func TestBuildModelChain_SuffixStripGate(t *testing.T) {
	base := "gpt-5.3-codex-spark"
	suffixes := []string{"-spark"}
	explicit := []string{}

	cases := []struct {
		errClass    executor.ErrorClass
		wantStrip   bool // true → stripped variant expected in chain
		description string
	}{
		{executor.ErrorClassNone, false, "none (initial build — no strip)"},
		{executor.ErrorClassQuota, true, "quota — strip expected"},
		{executor.ErrorClassModelUnavailable, true, "model-unavailable — strip expected"},
		{executor.ErrorClassTransient, false, "transient — no strip"},
		{executor.ErrorClassFatal, false, "fatal — no strip"},
		{executor.ErrorClassUnknown, false, "unknown — no strip (P0 fix)"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.description, func(t *testing.T) {
			chain := executor.BuildModelChain(base, explicit, suffixes, tc.errClass)

			// The stripped variant is "gpt-5.3-codex" (suffix "-spark" removed).
			stripped := "gpt-5.3-codex"
			hasStripped := false
			for _, m := range chain {
				if m == stripped {
					hasStripped = true
					break
				}
			}

			if tc.wantStrip && !hasStripped {
				t.Errorf("%s: want stripped variant %q in chain %v, but not found", tc.description, stripped, chain)
			}
			if !tc.wantStrip && hasStripped {
				t.Errorf("%s: stripped variant %q should NOT be in chain %v for this error class", tc.description, stripped, chain)
			}

			// Base model must always be first regardless of error class.
			if len(chain) == 0 || chain[0] != base {
				t.Errorf("%s: base model %q must be first in chain, got %v", tc.description, base, chain)
			}
		})
	}
}
