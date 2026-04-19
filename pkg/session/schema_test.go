package session_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/thebtf/aimux/pkg/session"
	_ "modernc.org/sqlite"
)

// columnExists returns true if the named column exists in the table.
func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan column info: %v", err)
		}
		if name == column {
			return true
		}
	}
	return false
}

// schemaVersion returns the current schema version from schema_version table.
func schemaVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	row := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`)
	if err := row.Scan(&v); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	return v
}

// openRawDB opens a SQLite database without running any migrations.
func openRawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// TestMigrateV2_FreshDB verifies that opening a new Store applies v2 migration:
// all new columns must exist on sessions and jobs tables.
func TestMigrateV2_FreshDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fresh.db")

	store, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// Close the store before opening a second connection so SQLite WAL does
	// not contend between the two handles (store uses MaxOpenConns(1)).
	store.Close()

	db := openRawDB(t, dbPath)

	// sessions: daemon_uuid, aborted_at
	for _, col := range []string{"daemon_uuid", "aborted_at"} {
		if !columnExists(t, db, "sessions", col) {
			t.Errorf("sessions.%s column missing after v2 migration", col)
		}
	}

	// jobs: daemon_uuid, last_seen_at, aborted_at
	for _, col := range []string{"daemon_uuid", "last_seen_at", "aborted_at"} {
		if !columnExists(t, db, "jobs", col) {
			t.Errorf("jobs.%s column missing after v2 migration", col)
		}
	}

	// schema version must be 3 (v2 + v3 aborted_job_ids migration)
	if v := schemaVersion(t, db); v != 3 {
		t.Errorf("schema_version = %d, want 3", v)
	}
}

// TestMigrateV2_V1DB verifies that a v1-schema database (without the new columns)
// is correctly upgraded to v2: columns are added and existing rows now have NULL values.
func TestMigrateV2_V1DB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v1.db")

	// Build a v1 schema manually (mirrors what the old migrate() did through version 1).
	// Use sql.Open directly (not openRawDB) so we can close at a specific point
	// without conflicting with t.Cleanup.
	rawDB, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	rawDB.SetMaxOpenConns(1)
	_, err = rawDB.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			cli TEXT NOT NULL,
			mode TEXT NOT NULL,
			cli_session_id TEXT,
			pid INTEGER DEFAULT 0,
			status TEXT NOT NULL,
			turns INTEGER DEFAULT 0,
			cwd TEXT,
			metadata_json TEXT,
			created_at TEXT NOT NULL,
			last_active_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			cli TEXT NOT NULL,
			status TEXT NOT NULL,
			progress TEXT,
			content TEXT,
			exit_code INTEGER DEFAULT 0,
			error_json TEXT,
			poll_count INTEGER DEFAULT 0,
			pheromones_json TEXT,
			pipeline_json TEXT,
			pid INTEGER DEFAULT 0,
			created_at TEXT NOT NULL,
			progress_updated_at TEXT NOT NULL,
			completed_at TEXT,
			FOREIGN KEY (session_id) REFERENCES sessions(id)
		);
		CREATE TABLE IF NOT EXISTS schema_version (version INT NOT NULL);
		INSERT INTO schema_version (version) VALUES (1);
	`)
	if err != nil {
		t.Fatalf("build v1 schema: %v", err)
	}

	// Insert a pre-existing row to verify existing rows survive the migration
	// with NULL in the new columns.
	// NOTE: job status is 'completed' (not 'running') so that ReconcileOnStartup
	// called inside NewStore does not mark it as aborted — which would set aborted_at
	// and break the NULL assertion below.
	_, err = rawDB.Exec(`
		INSERT INTO sessions (id, cli, mode, status, created_at, last_active_at)
		VALUES ('sess-1', 'codex', 'live', 'created', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("insert test session: %v", err)
	}
	_, err = rawDB.Exec(`
		INSERT INTO jobs (id, session_id, cli, status, created_at, progress_updated_at)
		VALUES ('job-1', 'sess-1', 'codex', 'completed', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("insert test job: %v", err)
	}
	rawDB.Close()

	// Now open via NewStore which runs migrations.
	store, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore on v1 db: %v", err)
	}
	defer store.Close()

	// Use the store's own DB handle — avoids opening a second connection to
	// the same WAL file while MaxOpenConns=1 is in effect.
	db := store.DB()

	// All new columns must exist.
	for _, col := range []string{"daemon_uuid", "aborted_at"} {
		if !columnExists(t, db, "sessions", col) {
			t.Errorf("sessions.%s column missing after v1→v2 migration", col)
		}
	}
	for _, col := range []string{"daemon_uuid", "last_seen_at", "aborted_at"} {
		if !columnExists(t, db, "jobs", col) {
			t.Errorf("jobs.%s column missing after v1→v2 migration", col)
		}
	}

	// schema version must be 3 (v2 + v3 aborted_job_ids migration)
	if v := schemaVersion(t, db); v != 3 {
		t.Errorf("schema_version = %d, want 3", v)
	}

	// Existing rows must have NULL in the new columns (not an error, just NULL).
	var daemonUUID, abortedAt sql.NullString
	row := db.QueryRow(`SELECT daemon_uuid, aborted_at FROM sessions WHERE id = 'sess-1'`)
	if err := row.Scan(&daemonUUID, &abortedAt); err != nil {
		t.Fatalf("scan existing session: %v", err)
	}
	if daemonUUID.Valid {
		t.Errorf("existing session daemon_uuid = %q, want NULL", daemonUUID.String)
	}
	if abortedAt.Valid {
		t.Errorf("existing session aborted_at = %q, want NULL", abortedAt.String)
	}

	var jobDaemonUUID, lastSeenAt, jobAbortedAt sql.NullString
	row = db.QueryRow(`SELECT daemon_uuid, last_seen_at, aborted_at FROM jobs WHERE id = 'job-1'`)
	if err := row.Scan(&jobDaemonUUID, &lastSeenAt, &jobAbortedAt); err != nil {
		t.Fatalf("scan existing job: %v", err)
	}
	if jobDaemonUUID.Valid {
		t.Errorf("existing job daemon_uuid = %q, want NULL", jobDaemonUUID.String)
	}
	if lastSeenAt.Valid {
		t.Errorf("existing job last_seen_at = %q, want NULL", lastSeenAt.String)
	}
	if jobAbortedAt.Valid {
		t.Errorf("existing job aborted_at = %q, want NULL", jobAbortedAt.String)
	}
}

// TestMigrateV2_Idempotent verifies that running NewStore twice on the same DB
// does not fail (second open encounters existing columns and skips gracefully).
func TestMigrateV2_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "idem.db")

	store1, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("first NewStore: %v", err)
	}
	store1.Close()

	store2, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("second NewStore (idempotent check): %v", err)
	}
	store2.Close()
}
