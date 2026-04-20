package session_test

// reconcile_integration_test.go — T013 SIGKILL+restart simulation.
//
// Simulates the exact sequence that happens when the daemon is killed mid-run:
//  1. Daemon A: create a session + job, transition to running, snapshot state to SQLite.
//  2. SIGKILL: the daemon dies; the job remains "running" in SQLite (never completed).
//  3. Daemon B: open the same SQLite file with a NEW daemon UUID.
//  4. NewStore triggers ReconcileOnStartup automatically; the previously-running
//     job (owned by daemon A) must be transitioned to "aborted" with aborted_at set.
//
// This test does NOT require an actual process kill — it simulates the scenario
// by using two stores with different daemon UUIDs against the same database file.
// The daemon UUID rotation (ResetDaemonUUID) mimics "new process" semantics.

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

// TestReconcile_SIGKILLRestart verifies that a job snapshotted as "running"
// by daemon A is reconciled to "aborted" when daemon B opens the same DB.
func TestReconcile_SIGKILLRestart(t *testing.T) {
	// --- Daemon A: create and start a job, snapshot it, then "die". ---

	session.ResetDaemonUUID()
	daemonA := session.GetDaemonUUID()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sigkill_test.db")

	storeA, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore (daemon A): %v", err)
	}

	jmA := session.NewJobManager()
	jmA.SetStore(storeA)

	// Create a session row so the foreign key constraint is satisfied.
	regA := session.NewRegistry()
	sess := regA.Create("codex", types.SessionModeLive, "/tmp")
	if err := storeA.SnapshotSession(sess); err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}

	// Create and start a job (Created → Running).
	jobA := jmA.Create(sess.ID, "codex")
	jobAID := jobA.ID

	started := jmA.StartJob(jobAID, 42)
	if !started {
		t.Fatal("StartJob returned false — job not transitioned to running")
	}

	// Verify the running state is in SQLite (StartJob now calls SnapshotJob).
	db := storeA.DB()
	var statusInDB string
	if err := db.QueryRow(`SELECT status FROM jobs WHERE id = ?`, jobAID).Scan(&statusInDB); err != nil {
		t.Fatalf("query job status from DB: %v", err)
	}
	if statusInDB != "running" {
		t.Errorf("pre-reconcile DB status = %q, want running", statusInDB)
	}

	// "Kill" daemon A: close the store without completing the job.
	// In production this would be a SIGKILL — no cleanup, no status transition.
	storeA.Close()

	// --- Daemon B: open the same DB with a NEW daemon UUID. ---
	// NewStore calls ReconcileOnStartup automatically (this is the restart path).

	session.ResetDaemonUUID()
	daemonB := session.GetDaemonUUID()

	if daemonB == daemonA {
		t.Fatal("daemon UUID did not rotate — test precondition violated")
	}

	storeB, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore (daemon B): %v", err)
	}
	defer storeB.Close()

	// The job should now be "aborted" in the database.
	dbB := storeB.DB()

	var finalStatus string
	var abortedAtStr sql.NullString
	if err := dbB.QueryRow(`SELECT status, aborted_at FROM jobs WHERE id = ?`, jobAID).Scan(&finalStatus, &abortedAtStr); err != nil {
		t.Fatalf("query reconciled job: %v", err)
	}

	if finalStatus != "aborted" {
		t.Errorf("reconciled job status = %q, want aborted", finalStatus)
	}
	if !abortedAtStr.Valid || abortedAtStr.String == "" {
		t.Error("aborted_at is NULL or empty — reconcile did not stamp aborted_at")
	} else {
		abortedAt, parseErr := time.Parse(time.RFC3339, abortedAtStr.String)
		if parseErr != nil {
			t.Errorf("aborted_at %q is not valid RFC3339: %v", abortedAtStr.String, parseErr)
		} else if abortedAt.IsZero() {
			t.Error("aborted_at parsed to zero time")
		}
	}

	// The session that owned the job must also be marked aborted.
	var sessStatus sql.NullString
	if err := dbB.QueryRow(`SELECT status FROM sessions WHERE id = ?`, sess.ID).Scan(&sessStatus); err != nil {
		t.Fatalf("query reconciled session: %v", err)
	}
	if sessStatus.String != "aborted" {
		t.Errorf("reconciled session status = %q, want aborted", sessStatus.String)
	}
}

// BenchmarkReconcileIntegration10k seeds 10,000 running rows owned by an old
// daemon UUID and measures how long ReconcileOnStartup takes on a fresh daemon start.
// Must complete in < 5 seconds (NFR-1 from engram #111).
//
// Run: go test -bench=BenchmarkReconcileIntegration10k -benchtime=1x -run=^$ ./pkg/session/
func BenchmarkReconcileIntegration10k(b *testing.B) {
	session.ResetDaemonUUID()
	oldUUID := "old-uuid-integration-bench-10k"

	dir := b.TempDir()
	dbPath := filepath.Join(dir, "integration_bench.db")

	// Use daemon A UUID to seed data, close, then open with daemon B.
	// We need a raw DB connection to seed without triggering reconcile yet.
	// Open with the OLD UUID as the current daemon, seed, close.
	session.ResetDaemonUUID()
	// Override: we need to seed rows with a specific "old" UUID.
	// Use the insertJob/insertSession helpers from reconcile_test.go (same package).
	// Those helpers accept an explicit daemon UUID so we can set it directly.

	// Open a temporary store with the OLD uuid to seed data.
	// After seeding, reset daemon UUID to simulate a new daemon and call NewStore.
	seedStore, err := session.NewStore(dbPath)
	if err != nil {
		b.Fatalf("NewStore (seed): %v", err)
	}
	seedDB := seedStore.DB()

	// Insert a single parent session.
	if _, insertErr := seedDB.Exec(`
		INSERT INTO sessions (id, cli, mode, status, created_at, last_active_at, daemon_uuid)
		VALUES ('bench-integ-sess', 'codex', 'once_stateless', 'running', datetime('now'), datetime('now'), ?)`,
		oldUUID,
	); insertErr != nil {
		seedStore.Close()
		b.Fatalf("insert bench session: %v", insertErr)
	}

	// Batch-insert 10,000 running rows with old daemon UUID inside one transaction.
	tx, txErr := seedDB.Begin()
	if txErr != nil {
		seedStore.Close()
		b.Fatalf("begin seeding tx: %v", txErr)
	}
	stmt, stmtErr := tx.Prepare(`
		INSERT INTO jobs (id, session_id, cli, status, created_at, progress_updated_at, daemon_uuid)
		VALUES (?, 'bench-integ-sess', 'codex', 'running', datetime('now'), datetime('now'), ?)`)
	if stmtErr != nil {
		tx.Rollback()
		seedStore.Close()
		b.Fatalf("prepare seeding stmt: %v", stmtErr)
	}
	for i := 0; i < 10000; i++ {
		id := benchJobID(i)
		if _, execErr := stmt.Exec(id, oldUUID); execErr != nil {
			stmt.Close()
			tx.Rollback()
			seedStore.Close()
			b.Fatalf("insert bench job %d: %v", i, execErr)
		}
	}
	stmt.Close()
	if commitErr := tx.Commit(); commitErr != nil {
		seedStore.Close()
		b.Fatalf("commit seeding tx: %v", commitErr)
	}
	seedStore.Close()

	// Reset daemon UUID to simulate a new daemon restart.
	session.ResetDaemonUUID()

	b.ResetTimer()
	start := time.Now()

	for i := 0; i < b.N; i++ {
		// Opening a new store triggers ReconcileOnStartup.
		benchStore, openErr := session.NewStore(dbPath)
		if openErr != nil {
			b.Fatalf("NewStore (daemon B, iter %d): %v", i, openErr)
		}
		benchStore.Close()

		// After first iteration reset rows so subsequent runs have work to do.
		if i < b.N-1 {
			session.ResetDaemonUUID()
			resetStore, resetErr := session.NewStore(dbPath)
			if resetErr != nil {
				b.Fatalf("NewStore (reset, iter %d): %v", i, resetErr)
			}
			rDB := resetStore.DB()
			if _, resetJobErr := rDB.Exec(`UPDATE jobs SET status = 'running', aborted_at = NULL, daemon_uuid = ?`, oldUUID); resetJobErr != nil {
				resetStore.Close()
				b.Fatalf("reset bench jobs: %v", resetJobErr)
			}
			if _, resetSessErr := rDB.Exec(`UPDATE sessions SET status = 'running', aborted_at = NULL, aborted_job_ids = NULL, daemon_uuid = ?`, oldUUID); resetSessErr != nil {
				resetStore.Close()
				b.Fatalf("reset bench sessions: %v", resetSessErr)
			}
			resetStore.Close()
			session.ResetDaemonUUID()
		}
	}

	elapsed := time.Since(start)
	b.ReportMetric(float64(elapsed.Milliseconds()), "ms/total")

	if elapsed > 5*time.Second {
		b.Fatalf("BenchmarkReconcileIntegration10k: reconciliation took %v, want < 5s (NFR-1)", elapsed)
	}
}

// benchJobID returns a zero-padded bench job ID string.
func benchJobID(i int) string {
	return fmt.Sprintf("integ-bench-%05d", i)
}
