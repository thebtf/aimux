package loom

import (
	"context"
	"database/sql"
	"sort"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestLoomEngine_SubmitP99Bounds verifies NFR-10: Submit p99 < 100ms under
// a hot loop of 1000 submits against an in-memory SQLite store.
// Skipped under -short to avoid CI slowdown on quick loops.
func TestLoomEngine_SubmitP99Bounds(t *testing.T) {
	if testing.Short() {
		t.Skip("NFR-10 bounds test skipped in -short mode")
	}

	// Use a named in-memory database so all pool connections share one schema.
	// Plain ":memory:" gives each connection its own DB (breaks pool connections).
	db, err := sql.Open("sqlite", "file:bounds_test?cache=shared&mode=memory")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	// Single connection prevents pool exhaustion race on in-memory DB.
	db.SetMaxOpenConns(1)
	defer db.Close()

	engine, err := NewEngine(db)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	// Register a no-op worker that completes immediately.
	engine.RegisterWorker(WorkerTypeCLI, &noopBoundsWorker{})

	const N = 1000
	durations := make([]time.Duration, 0, N)
	ctx := context.Background()
	start := time.Now()

	for i := 0; i < N; i++ {
		t0 := time.Now()
		_, err := engine.Submit(ctx, TaskRequest{
			WorkerType: WorkerTypeCLI,
			ProjectID:  "bounds-test",
			Prompt:     "noop",
		})
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		durations = append(durations, time.Since(t0))
	}
	total := time.Since(start)
	t.Logf("submitted %d tasks in %v (avg %v/op)", N, total, total/N)
	if total > 5*time.Second {
		t.Errorf("NFR-10 wall-clock bound: 1000 submits took %v, expected < 5s", total)
	}

	// Compute p99.
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p99idx := int(float64(N) * 0.99)
	p99 := durations[p99idx]

	t.Logf("Submit p50=%v p95=%v p99=%v p100=%v",
		durations[N/2], durations[int(float64(N)*0.95)], p99, durations[N-1])

	const bound = 100 * time.Millisecond
	if p99 > bound {
		t.Errorf("NFR-10 violation: Submit p99 = %v, expected < %v", p99, bound)
	}
}

// noopBoundsWorker returns immediately with non-empty content so the quality
// gate always accepts (empty output would be rejected and retried).
type noopBoundsWorker struct{}

func (noopBoundsWorker) Type() WorkerType { return WorkerTypeCLI }
func (noopBoundsWorker) Execute(_ context.Context, _ *Task) (*WorkerResult, error) {
	return &WorkerResult{Content: "ok"}, nil
}
