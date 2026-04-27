package loom

import (
	"fmt"
	"testing"
	"time"
)

// benchmarkRecoverConst is the number of tasks seeded per engine for
// BenchmarkRecoverCrashed10K. Two engines × 5000 tasks = 10,000 total rows.
const benchmarkRecoverConst = 5000

// BenchmarkRecoverCrashed10K measures the wall-clock duration of
// TaskStore.MarkCrashed when 5,000 tasks in 'dispatched'/'running' state
// (belonging to the caller's engine) must be bulk-updated to 'failed_crash'.
//
// This benchmark records NFR-1 baseline performance for AIMUX-10. It is a
// measurement tool, not a CI gate: durations > 100 ms trigger a warning log
// entry but do NOT fail the benchmark (b.Fatal is not called).
//
// Two sub-benchmarks cover:
//   - WithIndex:    idx_tasks_engine_status present (normal production path).
//   - WithoutIndex: index dropped beforehand to capture unindexed baseline.
func BenchmarkRecoverCrashed10K(b *testing.B) {
	b.Run("WithIndex", func(b *testing.B) {
		benchmarkRecoverCrashed(b, true)
	})
	b.Run("WithoutIndex", func(b *testing.B) {
		benchmarkRecoverCrashed(b, false)
	})
}

// benchmarkRecoverCrashed seeds benchmarkRecoverConst tasks per engine (two
// engines = 10,000 total), optionally drops the composite index, calls
// MarkCrashed once on the "bench-prod" store, and reports wall-clock duration.
func benchmarkRecoverCrashed(b *testing.B, withIndex bool) {
	b.Helper()

	db := newTestDB(b)

	storeProd, err := NewTaskStore(db, "bench-prod")
	if err != nil {
		b.Fatalf("NewTaskStore bench-prod: %v", err)
	}
	storeDev, err := NewTaskStore(db, "bench-dev")
	if err != nil {
		b.Fatalf("NewTaskStore bench-dev: %v", err)
	}

	// Seed benchmarkRecoverConst tasks per engine in a single transaction
	// for fast setup (avoid 10k individual INSERT round-trips).
	tx, err := db.Begin()
	if err != nil {
		b.Fatalf("begin tx: %v", err)
	}
	now := time.Now().UTC()
	for i := 0; i < benchmarkRecoverConst; i++ {
		_, err := tx.Exec(
			`INSERT INTO tasks (id, status, worker_type, project_id, request_id, prompt,
			                    cwd, env, cli, role, model, effort, timeout, metadata,
			                    result, error, retries, created_at, engine_name)
			 VALUES (?, 'running', 'cli', 'proj-bench', '', 'bench prompt',
			         '', '{}', '', '', '', '', 0, '{}',
			         '', '', 0, ?, ?)`,
			fmt.Sprintf("prod-%d", i),
			now,
			storeProd.engineName,
		)
		if err != nil {
			_ = tx.Rollback()
			b.Fatalf("INSERT prod task %d: %v", i, err)
		}

		_, err = tx.Exec(
			`INSERT INTO tasks (id, status, worker_type, project_id, request_id, prompt,
			                    cwd, env, cli, role, model, effort, timeout, metadata,
			                    result, error, retries, created_at, engine_name)
			 VALUES (?, 'dispatched', 'cli', 'proj-bench', '', 'bench prompt',
			         '', '{}', '', '', '', '', 0, '{}',
			         '', '', 0, ?, ?)`,
			fmt.Sprintf("dev-%d", i),
			now,
			storeDev.engineName,
		)
		if err != nil {
			_ = tx.Rollback()
			b.Fatalf("INSERT dev task %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatalf("commit tx: %v", err)
	}

	if !withIndex {
		if _, err := db.Exec(`DROP INDEX IF EXISTS idx_tasks_engine_status`); err != nil {
			b.Fatalf("DROP INDEX: %v", err)
		}
	}

	// Reset benchmark timer to exclude setup time.
	b.ResetTimer()

	// b.N is 1 when called via -benchtime 1x (one iteration, measure absolute time).
	for range b.N {
		start := time.Now()

		n, err := storeProd.MarkCrashed()
		if err != nil {
			b.Fatalf("MarkCrashed: %v", err)
		}

		elapsed := time.Since(start)
		b.ReportMetric(float64(elapsed.Milliseconds()), "ms/op")
		b.ReportMetric(float64(n), "rows_crashed")

		const warnThresholdMs = 100
		if elapsed.Milliseconds() > warnThresholdMs {
			b.Logf("WARN: MarkCrashed took %v (> %d ms soft threshold); NFR-1 baseline exceeded", elapsed, warnThresholdMs)
		}
	}
}
