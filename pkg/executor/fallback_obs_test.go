package executor_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/metrics"
	"github.com/thebtf/aimux/pkg/types"
)

// testLogger captures structured log lines.
type testLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLogger) log(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *testLogger) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := make([]string, len(l.lines))
	copy(cp, l.lines)
	return cp
}

// TestFallbackObs_LogLinesPerAttempt verifies structured log emission:
// 3 models: first=quota, second=unavailable, third=success -> 3 log lines.
func TestFallbackObs_LogLinesPerAttempt(t *testing.T) {
	old := executor.SetFallbackVerboseForTest(true)
	t.Cleanup(func() { executor.SetFallbackVerboseForTest(old) })

	tracker := executor.NewModelCooldownTracker()
	logger := &testLogger{}
	counter := metrics.NewFallbackCounter()

	stub := &stubFallbackExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){
			func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "quota exceeded", ExitCode: 1}, nil
			},
			func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "model not found: model-b", ExitCode: 1}, nil
			},
			func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "ok", ExitCode: 0}, nil
			},
		},
	}

	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-p", "hi", "-m", "model-a"}}
	models := []string{"model-a", "model-b", "model-c"}

	_, err := executor.RunWithModelFallback(
		context.Background(), stub, baseArgs, models, "-m",
		tracker, 1*time.Second, logger.log, counter,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := logger.Lines()
	if len(lines) != 3 {
		t.Fatalf("want 3 log lines, got %d: %v", len(lines), lines)
	}

	// Line 0: attempt=1, result=rate_limit
	if !strings.Contains(lines[0], "attempt=1") {
		t.Errorf("line 0 missing attempt=1: %s", lines[0])
	}
	if !strings.Contains(lines[0], "result=rate_limit") {
		t.Errorf("line 0 missing result=rate_limit: %s", lines[0])
	}
	if !strings.Contains(lines[0], "model=model-a") {
		t.Errorf("line 0 missing model=model-a: %s", lines[0])
	}

	// Line 1: attempt=2, result=unavailable
	if !strings.Contains(lines[1], "attempt=2") {
		t.Errorf("line 1 missing attempt=2: %s", lines[1])
	}
	if !strings.Contains(lines[1], "result=unavailable") {
		t.Errorf("line 1 missing result=unavailable: %s", lines[1])
	}

	// Line 2: attempt=3, result=success
	if !strings.Contains(lines[2], "attempt=3") {
		t.Errorf("line 2 missing attempt=3: %s", lines[2])
	}
	if !strings.Contains(lines[2], "result=success") {
		t.Errorf("line 2 missing result=success: %s", lines[2])
	}
}

// TestFallbackObs_CounterIncrements verifies counter values after 3 attempts.
func TestFallbackObs_CounterIncrements(t *testing.T) {
	old := executor.SetFallbackVerboseForTest(true)
	t.Cleanup(func() { executor.SetFallbackVerboseForTest(old) })

	tracker := executor.NewModelCooldownTracker()
	counter := metrics.NewFallbackCounter()

	stub := &stubFallbackExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){
			func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "quota exceeded", ExitCode: 1}, nil
			},
			func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "model not found: model-b", ExitCode: 1}, nil
			},
			func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "ok", ExitCode: 0}, nil
			},
		},
	}

	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-p", "hi"}}
	models := []string{"model-a", "model-b", "model-c"}

	_, err := executor.RunWithModelFallback(
		context.Background(), stub, baseArgs, models, "",
		tracker, 1*time.Second, nil, counter,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if total := counter.Total(); total != 3 {
		t.Errorf("counter.Total() = %d, want 3", total)
	}
	if v := counter.Get("codex", "model-a", metrics.FallbackResultRateLimit); v != 1 {
		t.Errorf("model-a rate_limit count = %d, want 1", v)
	}
	if v := counter.Get("codex", "model-b", metrics.FallbackResultUnavailable); v != 1 {
		t.Errorf("model-b unavailable count = %d, want 1", v)
	}
	if v := counter.Get("codex", "model-c", metrics.FallbackResultSuccess); v != 1 {
		t.Errorf("model-c success count = %d, want 1", v)
	}
}

// TestFallbackObs_VerboseFalse verifies that AIMUX_FALLBACK_VERBOSE=false suppresses logs but counter increments.
func TestFallbackObs_VerboseFalse(t *testing.T) {
	old := executor.SetFallbackVerboseForTest(false)
	t.Cleanup(func() { executor.SetFallbackVerboseForTest(old) })

	tracker := executor.NewModelCooldownTracker()
	logger := &testLogger{}
	counter := metrics.NewFallbackCounter()

	stub := &stubFallbackExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){
			func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "quota exceeded", ExitCode: 1}, nil
			},
			func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "ok", ExitCode: 0}, nil
			},
		},
	}

	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-p", "hi"}}
	models := []string{"model-a", "model-b"}

	_, err := executor.RunWithModelFallback(
		context.Background(), stub, baseArgs, models, "",
		tracker, 1*time.Second, logger.log, counter,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if lines := logger.Lines(); len(lines) != 0 {
		t.Errorf("want 0 log lines when AIMUX_FALLBACK_VERBOSE=false, got %d: %v", len(lines), lines)
	}
	if total := counter.Total(); total != 2 {
		t.Errorf("counter.Total() = %d, want 2 (counter always increments)", total)
	}
}

// TestFallbackObs_LogLine_Transient verifies that a transient error produces TWO log lines (attempt + retry).
func TestFallbackObs_LogLine_Transient(t *testing.T) {
	old := executor.SetFallbackVerboseForTest(true)
	t.Cleanup(func() { executor.SetFallbackVerboseForTest(old) })

	tracker := executor.NewModelCooldownTracker()
	logger := &testLogger{}
	counter := metrics.NewFallbackCounter()

	stub := &stubFallbackExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){
			// First call: transient error
			func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "connection refused", ExitCode: 1}, nil
			},
			// Second call (retry): success
			func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "ok", ExitCode: 0}, nil
			},
		},
	}

	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-p", "hi"}}
	models := []string{"model-a"}

	_, err := executor.RunWithModelFallback(
		context.Background(), stub, baseArgs, models, "",
		tracker, 1*time.Second, logger.log, counter,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := logger.Lines()
	if len(lines) != 2 {
		t.Fatalf("want 2 log lines for transient+retry, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "result=transient") {
		t.Errorf("line 0 should be transient: %s", lines[0])
	}
	if !strings.Contains(lines[1], "result=success") {
		t.Errorf("line 1 should be success: %s", lines[1])
	}
	if total := counter.Total(); total != 2 {
		t.Errorf("counter.Total() = %d, want 2", total)
	}
}

// TestFallbackObs_CounterNilSafe verifies that passing nil counter does not panic.
func TestFallbackObs_CounterNilSafe(t *testing.T) {
	old := executor.SetFallbackVerboseForTest(true)
	t.Cleanup(func() { executor.SetFallbackVerboseForTest(old) })

	tracker := executor.NewModelCooldownTracker()
	stub := &stubFallbackExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){
			func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "ok", ExitCode: 0}, nil
			},
		},
	}

	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-p", "hi"}}
	_, err := executor.RunWithModelFallback(
		context.Background(), stub, baseArgs, []string{"model-a"}, "",
		tracker, 1*time.Second, nil, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error with nil counter: %v", err)
	}
}
