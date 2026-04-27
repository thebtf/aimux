package loom

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// loomColumnExists checks if a column exists in the tasks table.
func loomColumnExists(t *testing.T, db *sql.DB, column string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(tasks)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(tasks): %v", err)
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

// TestTaskStore_MigrateV2_FreshDB verifies that NewTaskStore on a fresh DB
// creates the tasks table with all v2 columns present.
func TestTaskStore_MigrateV2_FreshDB(t *testing.T) {
	store := newTestStore(t)

	for _, col := range []string{"daemon_uuid", "last_seen_at", "aborted_at"} {
		if !loomColumnExists(t, store.db, col) {
			t.Errorf("tasks.%s column missing after NewTaskStore (v2 migration)", col)
		}
	}
}

// TestTaskStore_MigrateV2_ExistingDB verifies that NewTaskStore on a DB that
// was created before the v2 migration (no daemon_uuid/last_seen_at/aborted_at)
// adds the columns without failing and without touching existing rows.
func TestTaskStore_MigrateV2_ExistingDB(t *testing.T) {
	// Build a pre-v2 database without the new columns.
	db := newTestDB(t)
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'pending',
			worker_type TEXT NOT NULL,
			project_id TEXT NOT NULL,
			request_id TEXT NOT NULL DEFAULT '',
			prompt TEXT NOT NULL,
			cwd TEXT DEFAULT '',
			env TEXT DEFAULT '{}',
			cli TEXT DEFAULT '',
			role TEXT DEFAULT '',
			model TEXT DEFAULT '',
			effort TEXT DEFAULT '',
			timeout INTEGER DEFAULT 0,
			metadata TEXT DEFAULT '{}',
			result TEXT DEFAULT '',
			error TEXT DEFAULT '',
			retries INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			dispatched_at DATETIME,
			completed_at DATETIME
		);
		INSERT INTO tasks
			(id, status, worker_type, project_id, request_id, prompt, created_at)
		VALUES
			('t-existing', 'running', 'cli', 'proj1', '', 'hello', '2026-01-01T00:00:00Z');
	`)
	if err != nil {
		t.Fatalf("create pre-v2 schema: %v", err)
	}

	// Now run NewTaskStore which applies the v2 migration.
	store, err := NewTaskStore(db, "test")
	if err != nil {
		t.Fatalf("NewTaskStore on pre-v2 db: %v", err)
	}

	// All new columns must exist.
	for _, col := range []string{"daemon_uuid", "last_seen_at", "aborted_at"} {
		if !loomColumnExists(t, store.db, col) {
			t.Errorf("tasks.%s column missing after v2 migration on existing db", col)
		}
	}

	// Existing row must have NULL in the new columns.
	var daemonUUID, lastSeenAt, abortedAt sql.NullString
	row := db.QueryRow(`SELECT daemon_uuid, last_seen_at, aborted_at FROM tasks WHERE id = 't-existing'`)
	if err := row.Scan(&daemonUUID, &lastSeenAt, &abortedAt); err != nil {
		t.Fatalf("scan existing task: %v", err)
	}
	if daemonUUID.Valid {
		t.Errorf("existing task daemon_uuid = %q, want NULL", daemonUUID.String)
	}
	if lastSeenAt.Valid {
		t.Errorf("existing task last_seen_at = %q, want NULL", lastSeenAt.String)
	}
	if abortedAt.Valid {
		t.Errorf("existing task aborted_at = %q, want NULL", abortedAt.String)
	}
}

// TestTaskStore_MigrateV2_Idempotent verifies that running NewTaskStore twice
// on the same DB does not fail when columns already exist.
func TestTaskStore_MigrateV2_Idempotent(t *testing.T) {
	db := newTestDB(t)

	_, err := NewTaskStore(db, "test")
	if err != nil {
		t.Fatalf("first NewTaskStore: %v", err)
	}
	// Second call: columns already present, must not error.
	_, err = NewTaskStore(db, "test")
	if err != nil {
		t.Fatalf("second NewTaskStore (idempotent check): %v", err)
	}
}

// TestTaskStore_EmptyEngineName verifies that NewTaskStore rejects an empty engineName.
func TestTaskStore_EmptyEngineName(t *testing.T) {
	db := newTestDB(t)
	_, err := NewTaskStore(db, "")
	if err == nil {
		t.Fatal("NewTaskStore with empty engineName: want error, got nil")
	}
}

// TestTaskStore_MigrateV3_FreshDB verifies that NewTaskStore on a fresh DB
// creates the tasks table with the engine_name column and composite index.
func TestTaskStore_MigrateV3_FreshDB(t *testing.T) {
	store := newTestStore(t)
	if !loomColumnExists(t, store.db, "engine_name") {
		t.Error("tasks.engine_name column missing after NewTaskStore (v3 migration)")
	}
}

// TestTaskStore_EngineName_RoundTrip verifies that engine_name is stamped on
// Create and returned by Get. The EngineName field on Task must match the
// engineName passed to NewTaskStore (anti-stub for T003/T004).
func TestTaskStore_EngineName_RoundTrip(t *testing.T) {
	db := newTestDB(t)
	const name = "test-daemon"
	store, err := NewTaskStore(db, name)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}

	task := &Task{
		ID:         "task-engine-rt",
		Status:     TaskStatusPending,
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-engine-rt",
		Prompt:     "engine name round-trip",
		CreatedAt:  time.Now().UTC(),
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get("task-engine-rt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.EngineName != name {
		t.Errorf("EngineName = %q; want %q", got.EngineName, name)
	}
}
