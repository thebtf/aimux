package executor_test

import (
	"context"
	"errors"
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
	n    atomic.Int32
	fns  []func(types.SpawnArgs) (*types.Result, error)
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
		[]string{"model-a"}, "", tracker, 1*time.Second, nil,
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
		[]string{"model-a"}, "", tracker, 1*time.Second, nil,
	)
	if err == nil {
		t.Fatal("expected error when all models are unavailable")
	}
	if !errors.Is(err, executor.ErrModelUnavailable) {
		t.Errorf("expected errors.Is(err, ErrModelUnavailable) to be true; err = %v", err)
	}
}
