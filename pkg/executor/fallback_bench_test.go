package executor_test

import (
	"context"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/metrics"
	"github.com/thebtf/aimux/pkg/types"
)

// BenchmarkRunWithModelFallback measures the overhead of observability instrumentation.
// The difference between WithObs and NoObs should be <1ms at p99 on realistic workloads.
func BenchmarkRunWithModelFallback(b *testing.B) {
	tracker := executor.NewModelCooldownTracker()
	baseArgs := types.SpawnArgs{CLI: "codex", Command: "echo", Args: []string{"-p", "hi"}}
	models := []string{"model-a"}
	stub := &stubFallbackExecutor{
		fns: []func(types.SpawnArgs) (*types.Result, error){
			func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: "ok", ExitCode: 0}, nil
			},
		},
	}

	b.Run("NoObs", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			stub.n.Store(0)
			_, _ = executor.RunWithModelFallback(
				context.Background(), stub, baseArgs, models, "",
				tracker, 1*time.Second, nil, nil,
			)
		}
	})

	b.Run("WithObs", func(b *testing.B) {
		counter := metrics.NewFallbackCounter()
		logFn := func(format string, args ...any) {}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			stub.n.Store(0)
			_, _ = executor.RunWithModelFallback(
				context.Background(), stub, baseArgs, models, "",
				tracker, 1*time.Second, logFn, counter,
			)
		}
	})
}
