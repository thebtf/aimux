package session_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/session"
)

// openReconcileDB creates a fresh SQLite session store in a temp directory.
func openReconcileDB(t testing.TB) (*sql.DB, *session.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := session.NewStore(filepath.Join(dir, "reconcile_test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store.DB(), store
}

// insertJob inserts a job row directly into the DB for test seeding.
func insertJob(t testing.TB, db *sql.DB, id, sessID, status, daemonUUID string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO jobs (id, session_id, cli, status, created_at, progress_updated_at, daemon_uuid)
		VALUES (?, ?, 'codex', ?, datetime('now'), datetime('now'), ?)`,
		id, sessID, status, daemonUUID)
	if err != nil {
		t.Fatalf("insert job %s: %v", id, err)
	}
}

// insertSession inserts a session row directly into the DB for test seeding.
func insertSession(t testing.TB, db *sql.DB, id, daemonUUID string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO sessions (id, cli, mode, status, created_at, last_active_at, daemon_uuid)
		VALUES (?, 'codex', 'once_stateless', 'running', datetime('now'), datetime('now'), ?)`,
		id, daemonUUID)
	if err != nil {
		t.Fatalf("insert session %s: %v", id, err)
	}
}

// TestReconcileOnStartup_OrphanedRunningJobsAborted seeds 10 orphaned running
// jobs, 5 orphaned completed jobs, and 3 current-daemon running jobs, then
// verifies only the orphaned running rows are transitioned to "aborted".
func TestReconcileOnStartup_OrphanedRunningJobsAborted(t *testing.T) {
	session.ResetDaemonUUID()
	currentUUID := session.GetDaemonUUID()
	oldUUID := "old-daemon-uuid-test-t010"

	db, _ := openReconcileDB(t)

	insertSession(t, db, "sess-orphan-t010", oldUUID)
	insertSession(t, db, "sess-current-t010", currentUUID)

	// 10 orphaned running jobs.
	for i := 0; i < 10; i++ {
		insertJob(t, db, fmt.Sprintf("orphan-running-%02d", i), "sess-orphan-t010", "running", oldUUID)
	}
	// 5 orphaned completed jobs (must NOT be touched).
	for i := 0; i < 5; i++ {
		insertJob(t, db, fmt.Sprintf("orphan-completed-%02d", i), "sess-orphan-t010", "completed", oldUUID)
	}
	// 3 current-daemon running jobs (must NOT be touched).
	for i := 0; i < 3; i++ {
		insertJob(t, db, fmt.Sprintf("current-running-%02d", i), "sess-current-t010", "running", currentUUID)
	}

	orphanedJobs, orphanedSessions, _, err := session.ReconcileOnStartup(context.Background(), db, currentUUID)
	if err != nil {
		t.Fatalf("ReconcileOnStartup: %v", err)
	}

	// 15 orphaned job IDs returned (10 running + 5 completed — all with old daemon_uuid).
	if len(orphanedJobs) != 15 {
		t.Errorf("orphanedJobs = %d, want 15", len(orphanedJobs))
	}
	// 1 orphaned session.
	if len(orphanedSessions) != 1 {
		t.Errorf("orphanedSessions = %d, want 1", len(orphanedSessions))
	}

	// Exactly 10 rows must have status=aborted.
	var abortedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status = 'aborted'`).Scan(&abortedCount); err != nil {
		t.Fatalf("count aborted jobs: %v", err)
	}
	if abortedCount != 10 {
		t.Errorf("aborted job count = %d, want 10", abortedCount)
	}

	// 5 completed rows must remain completed.
	var completedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status = 'completed'`).Scan(&completedCount); err != nil {
		t.Fatalf("count completed jobs: %v", err)
	}
	if completedCount != 5 {
		t.Errorf("completed job count = %d, want 5", completedCount)
	}

	// 3 current-daemon running rows must remain running.
	var currentRunningCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status = 'running' AND daemon_uuid = ?`, currentUUID).Scan(&currentRunningCount); err != nil {
		t.Fatalf("count current running jobs: %v", err)
	}
	if currentRunningCount != 3 {
		t.Errorf("current-daemon running job count = %d, want 3", currentRunningCount)
	}
}

// TestReconcileOnStartup_SessionRollup verifies the session-level abort rollup.
//
// Scenario:
//   - Session A: 2 orphaned running → all aborted → session status="aborted", aborted_job_ids=[job-A1,job-A2]
//   - Session B: 1 orphaned running + 1 completed → mixed → session status="aborted"
//   - Session C: 3 current-daemon running → untouched
//   - Session D: 2 completed jobs (orphaned daemon) → completed rows unchanged, session has no aborted → no rollup
func TestReconcileOnStartup_SessionRollup(t *testing.T) {
	session.ResetDaemonUUID()
	currentUUID := session.GetDaemonUUID()
	oldUUID := "old-daemon-uuid-rollup-t011"

	db, _ := openReconcileDB(t)

	// Session A: 2 orphaned running.
	insertSession(t, db, "sess-A", oldUUID)
	insertJob(t, db, "job-A1", "sess-A", "running", oldUUID)
	insertJob(t, db, "job-A2", "sess-A", "running", oldUUID)

	// Session B: 1 orphaned running + 1 completed.
	insertSession(t, db, "sess-B", oldUUID)
	insertJob(t, db, "job-B1", "sess-B", "running", oldUUID)
	insertJob(t, db, "job-B2", "sess-B", "completed", oldUUID)

	// Session C: 3 current-daemon running (untouched).
	insertSession(t, db, "sess-C", currentUUID)
	insertJob(t, db, "job-C1", "sess-C", "running", currentUUID)
	insertJob(t, db, "job-C2", "sess-C", "running", currentUUID)
	insertJob(t, db, "job-C3", "sess-C", "running", currentUUID)

	// Session D: 2 completed jobs (old daemon — orphaned session but no running jobs).
	insertSession(t, db, "sess-D", oldUUID)
	insertJob(t, db, "job-D1", "sess-D", "completed", oldUUID)
	insertJob(t, db, "job-D2", "sess-D", "completed", oldUUID)

	_, _, _, err := session.ReconcileOnStartup(context.Background(), db, currentUUID)
	if err != nil {
		t.Fatalf("ReconcileOnStartup: %v", err)
	}

	// Session A: must be aborted with 2 aborted_job_ids.
	var sessAStatus, sessAAbortedIDs sql.NullString
	if err := db.QueryRow(`SELECT status, aborted_job_ids FROM sessions WHERE id = 'sess-A'`).Scan(&sessAStatus, &sessAAbortedIDs); err != nil {
		t.Fatalf("scan sess-A: %v", err)
	}
	if sessAStatus.String != "aborted" {
		t.Errorf("sess-A status = %q, want aborted", sessAStatus.String)
	}
	if !sessAAbortedIDs.Valid || sessAAbortedIDs.String == "" {
		t.Error("sess-A aborted_job_ids is NULL or empty")
	} else {
		var ids []string
		if jsonErr := json.Unmarshal([]byte(sessAAbortedIDs.String), &ids); jsonErr != nil {
			t.Errorf("sess-A aborted_job_ids parse error: %v", jsonErr)
		} else if len(ids) != 2 {
			t.Errorf("sess-A aborted_job_ids = %v (len=%d), want 2 entries", ids, len(ids))
		}
	}

	// Session B: must be aborted (1 aborted + 1 completed = mixed).
	var sessBStatus sql.NullString
	if err := db.QueryRow(`SELECT status FROM sessions WHERE id = 'sess-B'`).Scan(&sessBStatus); err != nil {
		t.Fatalf("scan sess-B: %v", err)
	}
	if sessBStatus.String != "aborted" {
		t.Errorf("sess-B status = %q, want aborted (mixed: 1 aborted + 1 completed)", sessBStatus.String)
	}

	// Session C: must NOT be aborted (current daemon, running jobs).
	var sessCStatus sql.NullString
	if err := db.QueryRow(`SELECT status FROM sessions WHERE id = 'sess-C'`).Scan(&sessCStatus); err != nil {
		t.Fatalf("scan sess-C: %v", err)
	}
	if sessCStatus.String == "aborted" {
		t.Errorf("sess-C status = aborted, want original status (current daemon sessions untouched)")
	}

	// Session D: must NOT be aborted (all jobs are completed, no aborted jobs).
	var sessDStatus, sessDAbortedIDs sql.NullString
	if err := db.QueryRow(`SELECT status, aborted_job_ids FROM sessions WHERE id = 'sess-D'`).Scan(&sessDStatus, &sessDAbortedIDs); err != nil {
		t.Fatalf("scan sess-D: %v", err)
	}
	if sessDStatus.String == "aborted" {
		t.Errorf("sess-D status = aborted, want unchanged (all jobs completed)")
	}
	if sessDAbortedIDs.Valid && sessDAbortedIDs.String != "" {
		t.Errorf("sess-D aborted_job_ids = %q, want NULL (no aborted jobs)", sessDAbortedIDs.String)
	}
}

// BenchmarkReconcile10k seeds 10,000 running rows and measures reconciliation latency.
// Must complete in < 5 seconds on dev hardware (NFR-1).
// Run: go test -bench=BenchmarkReconcile10k -benchtime=1x -run=^$ ./pkg/session/
func BenchmarkReconcile10k(b *testing.B) {
	session.ResetDaemonUUID()
	newUUID := session.GetDaemonUUID()
	oldUUID := "old-uuid-bench-10k"

	dir := b.TempDir()
	store, err := session.NewStore(filepath.Join(dir, "bench.db"))
	if err != nil {
		b.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	db := store.DB()

	// Insert a single session to hold all bench jobs.
	if _, insertErr := db.Exec(`
		INSERT INTO sessions (id, cli, mode, status, created_at, last_active_at, daemon_uuid)
		VALUES ('bench-sess-10k', 'codex', 'once_stateless', 'running', datetime('now'), datetime('now'), ?)`,
		oldUUID); insertErr != nil {
		b.Fatalf("insert bench session: %v", insertErr)
	}

	// Batch-insert 10,000 running rows inside one transaction for speed.
	tx, txErr := db.Begin()
	if txErr != nil {
		b.Fatalf("begin seeding tx: %v", txErr)
	}
	stmt, stmtErr := tx.Prepare(`
		INSERT INTO jobs (id, session_id, cli, status, created_at, progress_updated_at, daemon_uuid)
		VALUES (?, 'bench-sess-10k', 'codex', 'running', datetime('now'), datetime('now'), ?)`)
	if stmtErr != nil {
		tx.Rollback()
		b.Fatalf("prepare seeding stmt: %v", stmtErr)
	}
	for i := 0; i < 10000; i++ {
		if _, execErr := stmt.Exec(fmt.Sprintf("bench-job-%05d", i), oldUUID); execErr != nil {
			stmt.Close()
			tx.Rollback()
			b.Fatalf("insert bench job %d: %v", i, execErr)
		}
	}
	stmt.Close()
	if commitErr := tx.Commit(); commitErr != nil {
		b.Fatalf("commit seeding tx: %v", commitErr)
	}

	b.ResetTimer()
	start := time.Now()

	for i := 0; i < b.N; i++ {
		if _, _, _, reconcileErr := session.ReconcileOnStartup(context.Background(), db, newUUID); reconcileErr != nil {
			b.Fatalf("ReconcileOnStartup: %v", reconcileErr)
		}
		// Reset after first run so subsequent runs have rows to process.
		// For benchtime=1x we only run once, but reset keeps the bench stable.
		if i < b.N-1 {
			if _, resetErr := db.Exec(`UPDATE jobs SET status = 'running', aborted_at = NULL`); resetErr != nil {
				b.Fatalf("reset bench jobs: %v", resetErr)
			}
			if _, resetErr := db.Exec(`UPDATE sessions SET status = 'running', aborted_at = NULL, aborted_job_ids = NULL`); resetErr != nil {
				b.Fatalf("reset bench sessions: %v", resetErr)
			}
		}
	}

	elapsed := time.Since(start)
	b.ReportMetric(float64(elapsed.Milliseconds()), "ms/total")

	if elapsed > 5*time.Second {
		b.Fatalf("BenchmarkReconcile10k: reconciliation took %v, want < 5s (NFR-1)", elapsed)
	}
}
