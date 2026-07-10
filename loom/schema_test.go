package loom

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
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

func loomIndexExists(t *testing.T, db *sql.DB, index string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA index_list(tasks)`)
	if err != nil {
		t.Fatalf("PRAGMA index_list(tasks): %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index info: %v", err)
		}
		if name == index {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index info: %v", err)
	}
	return false
}

func loomTableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("query sqlite_master table %s: %v", table, err)
	}
	return name == table
}

func loomTableColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
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
			t.Fatalf("scan %s column info: %v", table, err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s column info: %v", table, err)
	}
	return false
}

func loomTableIndexExists(t *testing.T, db *sql.DB, table, index string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA index_list(` + table + `)`)
	if err != nil {
		t.Fatalf("PRAGMA index_list(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan %s index info: %v", table, err)
		}
		if name == index {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s index info: %v", table, err)
	}
	return false
}

// TestTaskStore_MigrateV7_FreshDB verifies that NewTaskStore on a fresh DB
// creates the task artifact projection table and its task/cursor index.
func TestTaskStore_MigrateV7_FreshDB(t *testing.T) {
	store := newTestStore(t)

	if !loomTableExists(t, store.db, "task_artifacts") {
		t.Fatal("task_artifacts table missing after NewTaskStore (v7 migration)")
	}
	for _, col := range []string{
		"seq",
		"task_id",
		"kind",
		"event_type",
		"summary",
		"payload_json",
		"content_length",
		"redacted",
		"truncated",
		"created_at",
	} {
		if !loomTableColumnExists(t, store.db, "task_artifacts", col) {
			t.Errorf("task_artifacts.%s column missing after NewTaskStore (v7 migration)", col)
		}
	}
	if !loomTableIndexExists(t, store.db, "task_artifacts", "idx_task_artifacts_task_seq") {
		t.Error("idx_task_artifacts_task_seq missing after NewTaskStore (v7 migration)")
	}
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

// TestTaskStore_MigrateV4_FreshDB verifies that NewTaskStore on a fresh DB
// creates the tasks table with the tenant_id column and composite index.
func TestTaskStore_MigrateV4_FreshDB(t *testing.T) {
	store := newTestStore(t)
	if !loomColumnExists(t, store.db, "tenant_id") {
		t.Error("tasks.tenant_id column missing after NewTaskStore (v4 migration)")
	}
}

// TestTaskStore_TenantID_RoundTrip verifies that tenant_id is persisted on
// Create and returned by Get with the correct value.
func TestTaskStore_TenantID_RoundTrip(t *testing.T) {
	db := newTestDB(t)
	store, err := NewTaskStore(db, "test-rt")
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}

	task := &Task{
		ID:         "task-tenant-rt",
		Status:     TaskStatusPending,
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-tenant-rt",
		TenantID:   "acme",
		Prompt:     "tenant round-trip",
		CreatedAt:  time.Now().UTC(),
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get("task-tenant-rt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TenantID != "acme" {
		t.Errorf("TenantID = %q; want %q", got.TenantID, "acme")
	}
}

// TestTaskStore_TenantID_LegacyDefault verifies that tasks inserted without an
// explicit tenant_id receive the LegacyTenantID sentinel via the SQL column default.
func TestTaskStore_TenantID_LegacyDefault(t *testing.T) {
	db := newTestDB(t)
	store, err := NewTaskStore(db, "test-legacy")
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}

	// Insert with empty TenantID — LegacyTenantID must be used by Submit.
	task := &Task{
		ID:         "task-legacy-tid",
		Status:     TaskStatusPending,
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-legacy",
		TenantID:   LegacyTenantID,
		Prompt:     "legacy tenant",
		CreatedAt:  time.Now().UTC(),
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get("task-legacy-tid")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TenantID != LegacyTenantID {
		t.Errorf("TenantID = %q; want %q", got.TenantID, LegacyTenantID)
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

func TestTaskStore_MigrateV6_FreshDB(t *testing.T) {
	store := newTestStore(t)
	if !loomColumnExists(t, store.db, "parent_task_id") {
		t.Fatal("tasks.parent_task_id column missing after NewTaskStore (v6 migration)")
	}
	if !loomIndexExists(t, store.db, "idx_tasks_parent_task_id") {
		t.Fatal("idx_tasks_parent_task_id missing after NewTaskStore (v6 migration)")
	}
}

func TestTaskStore_MigrateV6_Idempotent(t *testing.T) {
	db := newTestDB(t)
	if _, err := NewTaskStore(db, "v6-idempotent"); err != nil {
		t.Fatalf("first NewTaskStore: %v", err)
	}
	if _, err := NewTaskStore(db, "v6-idempotent"); err != nil {
		t.Fatalf("second NewTaskStore: %v", err)
	}
}

func TestTaskStore_MigrateV6_Down(t *testing.T) {
	store := newTestStore(t)

	if err := MigrateV6Down(store.db); err != nil {
		t.Fatalf("MigrateV6Down: %v", err)
	}
	if loomColumnExists(t, store.db, "parent_task_id") {
		t.Fatal("tasks.parent_task_id column still present after MigrateV6Down")
	}
	if loomIndexExists(t, store.db, "idx_tasks_parent_task_id") {
		t.Fatal("idx_tasks_parent_task_id still present after MigrateV6Down")
	}

	if err := MigrateV6Down(store.db); err != nil {
		t.Fatalf("MigrateV6Down second call: %v", err)
	}
}

func TestTaskStore_ParentTaskID_NullForRoot(t *testing.T) {
	store := newTestStore(t)
	task := makeTask("root-parent-null", "proj-parent-null", TaskStatusPending)
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var parent sql.NullString
	if err := store.db.QueryRow(`SELECT parent_task_id FROM tasks WHERE id = ?`, task.ID).Scan(&parent); err != nil {
		t.Fatalf("scan parent_task_id: %v", err)
	}
	if parent.Valid {
		t.Fatalf("parent_task_id = %q, want NULL", parent.String)
	}
}

const t004LiteralV8Schema = `
CREATE TABLE tasks (
 id TEXT PRIMARY KEY, status TEXT NOT NULL DEFAULT 'pending', worker_type TEXT NOT NULL,
 project_id TEXT NOT NULL, request_id TEXT NOT NULL DEFAULT '', parent_task_id TEXT REFERENCES tasks(id),
 prompt TEXT NOT NULL, cwd TEXT DEFAULT '', env TEXT DEFAULT '{}', cli TEXT DEFAULT '', role TEXT DEFAULT '',
 model TEXT DEFAULT '', effort TEXT DEFAULT '', timeout INTEGER DEFAULT 0, metadata TEXT DEFAULT '{}',
 result TEXT DEFAULT '', error TEXT DEFAULT '', retries INTEGER DEFAULT 0, created_at DATETIME NOT NULL,
 dispatched_at DATETIME, completed_at DATETIME, engine_name TEXT NOT NULL DEFAULT '',
 tenant_id TEXT NOT NULL DEFAULT '__legacy__', daemon_uuid TEXT, last_seen_at TEXT, aborted_at TEXT,
 last_output_line TEXT NOT NULL DEFAULT '', progress_lines INTEGER NOT NULL DEFAULT 0, progress_updated_at DATETIME
);
CREATE INDEX idx_tasks_project_id ON tasks(project_id); CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_engine_status ON tasks(engine_name,status); CREATE INDEX idx_tasks_tenant_id ON tasks(tenant_id,status);
CREATE INDEX idx_tasks_parent_task_id ON tasks(parent_task_id);
CREATE TABLE task_artifacts (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 kind TEXT NOT NULL, event_type TEXT NOT NULL DEFAULT '', channel TEXT NOT NULL DEFAULT '',
 summary TEXT NOT NULL DEFAULT '', payload_json TEXT NOT NULL DEFAULT '{}', content_length INTEGER NOT NULL DEFAULT 0,
 redacted INTEGER NOT NULL DEFAULT 0, truncated INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL
);
CREATE INDEX idx_task_artifacts_task_seq ON task_artifacts(task_id,seq);
CREATE INDEX idx_task_artifacts_runtime_slice ON task_artifacts(task_id,kind,event_type,channel,seq);`

const t004TaskDigest = `quote(id)||'|'||quote(status)||'|'||quote(worker_type)||'|'||quote(project_id)||'|'||
 quote(request_id)||'|'||quote(parent_task_id)||'|'||quote(prompt)||'|'||quote(cwd)||'|'||quote(env)||'|'||
 quote(cli)||'|'||quote(role)||'|'||quote(model)||'|'||quote(effort)||'|'||quote(timeout)||'|'||quote(metadata)||'|'||
 quote(result)||'|'||quote(error)||'|'||quote(retries)||'|'||quote(created_at)||'|'||quote(dispatched_at)||'|'||
 quote(completed_at)||'|'||quote(engine_name)||'|'||quote(tenant_id)||'|'||quote(daemon_uuid)||'|'||
 quote(last_seen_at)||'|'||quote(aborted_at)||'|'||quote(last_output_line)||'|'||quote(progress_lines)||'|'||quote(progress_updated_at)`

const t004ActionDigest = `quote(id)||'|'||quote(task_id)||'|'||quote(kind)||'|'||quote(status)||'|'||quote(provider_request_id)||'|'||quote(connection_generation)||'|'||quote(request_json)||'|'||quote(response_json)||'|'||quote(delivery_json)||'|'||quote(expires_at)||'|'||quote(created_at)||'|'||quote(responded_at)||'|'||quote(resolved_at)`

const t004ArtifactDigest = `quote(seq)||'|'||quote(task_id)||'|'||quote(kind)||'|'||quote(event_type)||'|'||quote(channel)||'|'||
 quote(summary)||'|'||quote(payload_json)||'|'||quote(content_length)||'|'||quote(redacted)||'|'||quote(truncated)||'|'||quote(created_at)`

func t004Must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func t004OpenDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	t004Must(t, err)
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA foreign_keys=ON`)
	t004Must(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func t004NewStore(t *testing.T, db *sql.DB, name string) *TaskStore {
	t.Helper()
	store, err := NewTaskStore(db, name)
	if err != nil {
		t.Fatalf("NewTaskStore(%s): %v", name, err)
	}
	return store
}

func t004Scalar(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var got string
	t004Must(t, db.QueryRow(query, args...).Scan(&got))
	return got
}

func t004Digest(t *testing.T, db *sql.DB, expr, table, id string) string {
	t.Helper()
	return t004Scalar(t, db, `SELECT `+expr+` FROM `+table+` WHERE id=?`, id)
}

func t004LegacyDigest(t *testing.T, db *sql.DB, hasCancel bool) string {
	expr := t004TaskDigest + `||'|'||'NULL'`
	if hasCancel {
		expr = t004TaskDigest + `||'|'||quote(cancel_requested_at)`
	}
	return t004Digest(t, db, expr, "tasks", "v9-legacy")
}

func t004AssertV9(t *testing.T, db *sql.DB) {
	t.Helper()
	var columns, idPK, taskNotNull int
	t004Must(t, db.QueryRow(`SELECT count(*),coalesce(max(CASE WHEN name='id' THEN pk ELSE 0 END),0),coalesce(max(CASE WHEN name='task_id' THEN "notnull" ELSE 0 END),0)
FROM pragma_table_info('pending_actions')
WHERE name IN ('id','task_id','kind','status','provider_request_id','connection_generation','request_json','response_json','delivery_json','expires_at','created_at','responded_at','resolved_at')`).Scan(&columns, &idPK, &taskNotNull))
	if columns != 13 || idPK == 0 || taskNotNull == 0 || !loomColumnExists(t, db, "cancel_requested_at") {
		t.Fatalf("v9 columns/pk/not-null incomplete: columns=%d id_pk=%d task_not_null=%d", columns, idPK, taskNotNull)
	}
	var taskFK int
	t004Must(t, db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_list('pending_actions') WHERE "table"='tasks' AND "from"='task_id' AND "to"='id'`).Scan(&taskFK))
	if taskFK == 0 {
		t.Fatal("pending_actions.task_id foreign key missing")
	}
	rows, err := db.Query(`PRAGMA index_info(idx_pending_actions_provider_generation)`)
	t004Must(t, err)
	var indexColumns []string
	for rows.Next() {
		var seq, cid int
		var name string
		t004Must(t, rows.Scan(&seq, &cid, &name))
		indexColumns = append(indexColumns, name)
	}
	t004Must(t, rows.Err())
	_ = rows.Close()
	wantIndexColumns := []string{"task_id", "provider_request_id", "connection_generation"}
	if !reflect.DeepEqual(indexColumns, wantIndexColumns) {
		t.Fatalf("idx_pending_actions_provider_generation columns=%v want=%v", indexColumns, wantIndexColumns)
	}
	for _, forbidden := range []string{"worker_session_id", "run_binding_id", "lease_id", "principal_id", "provider_session_id"} {
		if loomTableColumnExists(t, db, "pending_actions", forbidden) {
			t.Errorf("v9 introduced CR-003 column pending_actions.%s", forbidden)
		}
	}
	if integrity := t004Scalar(t, db, `PRAGMA integrity_check`); integrity != "ok" {
		t.Fatalf("integrity_check=%q", integrity)
	}
	rows, err = db.Query(`PRAGMA foreign_key_check`)
	t004Must(t, err)
	if rows.Next() {
		t.Fatal("foreign_key_check returned a row")
	}
	t004Must(t, rows.Err())
	_ = rows.Close()
}

func t004AssertProviderFence(t *testing.T, db *sql.DB, stage string) {
	t.Helper()
	for _, id := range []string{stage + "-a", stage + "-b"} {
		_, err := db.Exec(`INSERT INTO tasks(id,status,worker_type,project_id,prompt,created_at) VALUES(?,'running','cli','p','p','2030-01-01T00:00:00Z')`, id)
		t004Must(t, err)
	}
	insert := func(id, task string, generation int64) error {
		_, err := db.Exec(`INSERT INTO pending_actions(id,task_id,kind,status,provider_request_id,connection_generation,request_json,expires_at,created_at) VALUES(?,?,'approval','pending',?,?, '{}','2030-01-02T00:00:00Z','2030-01-01T00:00:00Z')`, id, task, "provider-"+stage, generation)
		return err
	}
	if err := insert(stage+"-one", stage+"-a", 7); err != nil {
		t.Fatalf("%s first provider insert: %v", stage, err)
	}
	if err := insert(stage+"-cross-task", stage+"-b", 7); err != nil {
		t.Fatalf("%s rejected identical provider correlation on another task: %v", stage, err)
	}
	if err := insert(stage+"-same-task-duplicate", stage+"-b", 7); err == nil {
		t.Fatalf("%s accepted same-task duplicate provider correlation", stage)
	}
	if err := insert(stage+"-next", stage+"-b", 8); err != nil {
		t.Fatalf("%s rejected different generation: %v", stage, err)
	}
}

func t004AssertV9Stage(t *testing.T, db *sql.DB, stage string) {
	t.Helper()
	t004AssertV9(t, db)
	t004AssertProviderFence(t, db, stage)
}

func TestTaskStore_MigrateV9_FreshRepeatReopenAndConstraints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh-v9.db")
	db := t004OpenDB(t, path)
	_ = t004NewStore(t, db, "fresh-v9")
	t004AssertV9Stage(t, db, "fresh-first")
	_ = t004NewStore(t, db, "fresh-v9")
	t004AssertV9Stage(t, db, "fresh-repeat")
	t004Must(t, db.Close())
	reopened := t004OpenDB(t, path)
	_ = t004NewStore(t, reopened, "fresh-v9")
	t004AssertV9Stage(t, reopened, "fresh-reopen")
}

func TestTaskStore_MigrateV9_RepairsLiteralFixturesWithoutDataLoss(t *testing.T) {
	fixtures := []struct {
		name, setup          string
		hasCancel, hasAction bool
	}{
		{"raw-v8", "", false, false},
		{"column-only", `ALTER TABLE tasks ADD COLUMN cancel_requested_at DATETIME; UPDATE tasks SET cancel_requested_at='2030-01-02T03:09:05Z'`, true, false},
		{"table-without-unique", `CREATE TABLE pending_actions(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id),kind TEXT NOT NULL,status TEXT NOT NULL,provider_request_id TEXT NOT NULL,connection_generation INTEGER NOT NULL,request_json TEXT NOT NULL,response_json TEXT,delivery_json TEXT,expires_at DATETIME NOT NULL,created_at DATETIME NOT NULL,responded_at DATETIME,resolved_at DATETIME); INSERT INTO pending_actions VALUES('legacy-action','v9-legacy','approval','pending','legacy-provider',4,'{"q":1}',NULL,NULL,'2030-02-01T00:00:00Z','2030-01-01T00:00:00Z',NULL,NULL)`, false, true},
		{"legacy-two-column-index", `CREATE TABLE pending_actions(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id),kind TEXT NOT NULL,status TEXT NOT NULL,provider_request_id TEXT NOT NULL,connection_generation INTEGER NOT NULL,request_json TEXT NOT NULL,response_json TEXT,delivery_json TEXT,expires_at DATETIME NOT NULL,created_at DATETIME NOT NULL,responded_at DATETIME,resolved_at DATETIME); CREATE UNIQUE INDEX idx_pending_actions_provider_generation ON pending_actions(provider_request_id,connection_generation); INSERT INTO pending_actions VALUES('legacy-action','v9-legacy','approval','pending','legacy-provider',4,'{"q":1}',NULL,NULL,'2030-02-01T00:00:00Z','2030-01-01T00:00:00Z',NULL,NULL)`, false, true},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), fixture.name+".db")
			db := t004OpenDB(t, path)
			_, err := db.Exec(t004LiteralV8Schema + `
INSERT INTO tasks(id,status,worker_type,project_id,request_id,prompt,cwd,env,cli,role,model,effort,timeout,metadata,result,error,retries,created_at,dispatched_at,engine_name,tenant_id,daemon_uuid,last_seen_at,aborted_at,last_output_line,progress_lines,progress_updated_at)
VALUES('v9-legacy','running','cli','legacy-project','legacy-request','legacy prompt','D:/legacy','{"LEGACY":"yes"}','legacy-cli','legacy-role','legacy-model','high',77,'{"legacy":true}','legacy-result','legacy-error',3,'2030-01-02T03:04:05Z','2030-01-02T03:05:05Z','legacy-engine','legacy-tenant','legacy-daemon','2030-01-02T03:06:05Z','2030-01-02T03:07:05Z','legacy-tail',123,'2030-01-02T03:08:05Z');`)
			t004Must(t, err)
			if fixture.setup != "" {
				_, err = db.Exec(fixture.setup)
				t004Must(t, err)
			}
			beforeTask := t004LegacyDigest(t, db, fixture.hasCancel)
			beforeAction := ""
			if fixture.hasAction {
				beforeAction = t004Digest(t, db, t004ActionDigest, "pending_actions", "legacy-action")
			}
			assertStage := func(stage string, db *sql.DB) {
				t.Helper()
				label := fixture.name + "-" + stage
				t004AssertV9Stage(t, db, label)
				if got := t004LegacyDigest(t, db, true); got != beforeTask {
					t.Fatalf("%s changed legacy task", label)
				}
				if fixture.hasAction && t004Digest(t, db, t004ActionDigest, "pending_actions", "legacy-action") != beforeAction {
					t.Fatalf("%s changed legacy action", label)
				}
			}
			_ = t004NewStore(t, db, fixture.name)
			assertStage("first", db)
			_ = t004NewStore(t, db, fixture.name)
			assertStage("repeat", db)
			t004Must(t, db.Close())
			reopened := t004OpenDB(t, path)
			_ = t004NewStore(t, reopened, fixture.name)
			assertStage("reopen", reopened)
		})
	}
}

func TestTaskStore_MigrateV9_AuthorityDDLRemainsCompatibleWithFutureV10(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v9-with-v10.db")
	db := t004OpenDB(t, path)
	_ = t004NewStore(t, db, "v9-before-v10")
	// This sentinel models an additive event-ledger migration. The authority
	// oracle intentionally does not assert that 9 is the global latest version.
	_, err := db.Exec(`CREATE TABLE t004_v10_event_ledger_sentinel (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		created_at DATETIME NOT NULL
	)`)
	t004Must(t, err)
	_ = t004NewStore(t, db, "v9-after-v10")
	t004AssertV9Stage(t, db, "future-v10")
	if !loomTableExists(t, db, "t004_v10_event_ledger_sentinel") {
		t.Fatal("additive v10 event-ledger sentinel was removed")
	}
}

const t010MigrationLedgerDDL = `
CREATE TABLE loom_migrations (
	version INTEGER PRIMARY KEY,
	state TEXT NOT NULL CHECK (state IN ('running','complete')),
	checkpoint_seq INTEGER NOT NULL,
	source_rows INTEGER NOT NULL,
	processed_rows INTEGER NOT NULL,
	batch_count INTEGER NOT NULL,
	started_at DATETIME NOT NULL,
	completed_at DATETIME
);`

type t010MigrationLedgerSnapshot struct {
	Version       int
	State         string
	CheckpointSeq int64
	SourceRows    int64
	ProcessedRows int64
	BatchCount    int64
	StartedAt     string
	CompletedAt   sql.NullString
}

type t010V10Snapshot struct {
	Ledger         t010MigrationLedgerSnapshot
	SchemaDigest   [32]byte
	EventSeqDigest string
	ArtifactRows   int64
	NullEventSeq   int64
	DuplicatePairs int64
}

func t010AssertMigrationLedgerShape(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info('loom_migrations')`)
	t004Must(t, err)
	var columns []string
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue sql.NullString
		t004Must(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk))
		columns = append(columns, name)
	}
	t004Must(t, rows.Err())
	t004Must(t, rows.Close())
	want := []string{"version", "state", "checkpoint_seq", "source_rows", "processed_rows", "batch_count", "started_at", "completed_at"}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("loom_migrations columns=%v want=%v", columns, want)
	}
	if loomTableColumnExists(t, db, "loom_migrations", "integrity_result") {
		t.Fatal("loom_migrations must not persist stale integrity_result")
	}
}

func t010ReadV10Ledger(t *testing.T, db *sql.DB) t010MigrationLedgerSnapshot {
	t.Helper()
	var rows int
	t004Must(t, db.QueryRow(`SELECT count(*) FROM loom_migrations WHERE version=10`).Scan(&rows))
	if rows != 1 {
		t.Fatalf("v10 ledger row count=%d want=1", rows)
	}
	var got t010MigrationLedgerSnapshot
	t004Must(t, db.QueryRow(`
SELECT version,state,checkpoint_seq,source_rows,processed_rows,batch_count,started_at,completed_at
FROM loom_migrations WHERE version=10`).Scan(
		&got.Version,
		&got.State,
		&got.CheckpointSeq,
		&got.SourceRows,
		&got.ProcessedRows,
		&got.BatchCount,
		&got.StartedAt,
		&got.CompletedAt,
	))
	return got
}

func t010HasUniqueTaskEventSeq(t *testing.T, db *sql.DB) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA index_list('task_artifacts')`)
	t004Must(t, err)
	var candidates []string
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		t004Must(t, rows.Scan(&seq, &name, &unique, &origin, &partial))
		if unique == 1 && partial == 0 {
			candidates = append(candidates, name)
		}
	}
	t004Must(t, rows.Err())
	t004Must(t, rows.Close())
	for _, name := range candidates {
		escaped := strings.ReplaceAll(name, "'", "''")
		indexRows, err := db.Query(`PRAGMA index_info('` + escaped + `')`)
		t004Must(t, err)
		var columns []string
		for indexRows.Next() {
			var seq, cid int
			var column string
			t004Must(t, indexRows.Scan(&seq, &cid, &column))
			columns = append(columns, column)
		}
		t004Must(t, indexRows.Err())
		t004Must(t, indexRows.Close())
		if reflect.DeepEqual(columns, []string{"task_id", "event_seq"}) {
			return true
		}
	}
	return false
}

func t010SchemaDigest(t *testing.T, db *sql.DB) [32]byte {
	t.Helper()
	rows, err := db.Query(`
SELECT type,name,sql
FROM sqlite_master
WHERE tbl_name IN ('task_artifacts','loom_migrations')
ORDER BY type,name`)
	t004Must(t, err)
	var builder strings.Builder
	for rows.Next() {
		var objectType, name string
		var ddl sql.NullString
		t004Must(t, rows.Scan(&objectType, &name, &ddl))
		builder.WriteString(objectType)
		builder.WriteByte(0)
		builder.WriteString(name)
		builder.WriteByte(0)
		builder.WriteString(ddl.String)
		builder.WriteByte(0)
	}
	t004Must(t, rows.Err())
	t004Must(t, rows.Close())
	return sha256.Sum256([]byte(builder.String()))
}

func t010EventSeqDigest(t *testing.T, db *sql.DB) string {
	t.Helper()
	var digest sql.NullString
	t004Must(t, db.QueryRow(`
SELECT group_concat(task_id||':'||event_seq,'|')
FROM (SELECT task_id,event_seq FROM task_artifacts ORDER BY seq)`).Scan(&digest))
	return digest.String
}

func t010AssertDatabaseChecks(t *testing.T, db *sql.DB) {
	t.Helper()
	if got := t004Scalar(t, db, `PRAGMA integrity_check`); got != "ok" {
		t.Fatalf("integrity_check=%q want ok", got)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	t004Must(t, err)
	if rows.Next() {
		t.Fatal("foreign_key_check returned a row")
	}
	t004Must(t, rows.Err())
	t004Must(t, rows.Close())
}

func t010AssertV10Stage(t *testing.T, db *sql.DB, want t010MigrationLedgerSnapshot) t010V10Snapshot {
	t.Helper()
	if !loomTableColumnExists(t, db, "task_artifacts", "event_seq") {
		t.Fatal("task_artifacts.event_seq is missing")
	}
	t010AssertMigrationLedgerShape(t, db)
	if !t010HasUniqueTaskEventSeq(t, db) {
		t.Fatal("unique (task_id,event_seq) constraint/index is missing")
	}
	got := t010ReadV10Ledger(t, db)
	if got.Version != want.Version || got.State != want.State ||
		got.CheckpointSeq != want.CheckpointSeq || got.SourceRows != want.SourceRows ||
		got.ProcessedRows != want.ProcessedRows || got.BatchCount != want.BatchCount {
		t.Fatalf("v10 ledger=%+v want version/state/checkpoint/source/processed/batches=%d/%s/%d/%d/%d/%d",
			got, want.Version, want.State, want.CheckpointSeq, want.SourceRows, want.ProcessedRows, want.BatchCount)
	}
	if got.StartedAt == "" {
		t.Fatal("v10 ledger started_at is empty")
	}
	if want.StartedAt != "" && got.StartedAt != want.StartedAt {
		t.Fatalf("v10 ledger started_at=%q want preserved %q", got.StartedAt, want.StartedAt)
	}
	if got.State == "complete" && !got.CompletedAt.Valid {
		t.Fatal("complete v10 ledger row has NULL completed_at")
	}
	var artifactRows, nullRows, duplicates int64
	t004Must(t, db.QueryRow(`SELECT count(*) FROM task_artifacts`).Scan(&artifactRows))
	t004Must(t, db.QueryRow(`SELECT count(*) FROM task_artifacts WHERE event_seq IS NULL`).Scan(&nullRows))
	t004Must(t, db.QueryRow(`
SELECT count(*) FROM (
	SELECT task_id,event_seq FROM task_artifacts
	GROUP BY task_id,event_seq HAVING count(*)>1
)`).Scan(&duplicates))
	if artifactRows != want.SourceRows {
		t.Fatalf("artifact row count=%d want ledger source_rows=%d", artifactRows, want.SourceRows)
	}
	if nullRows != 0 || duplicates != 0 {
		t.Fatalf("event_seq null/duplicate counts=%d/%d want=0/0", nullRows, duplicates)
	}
	t010AssertDatabaseChecks(t, db)
	return t010V10Snapshot{
		Ledger:         got,
		SchemaDigest:   t010SchemaDigest(t, db),
		EventSeqDigest: t010EventSeqDigest(t, db),
		ArtifactRows:   artifactRows,
		NullEventSeq:   nullRows,
		DuplicatePairs: duplicates,
	}
}

func t010ArtifactDigestAll(t *testing.T, db *sql.DB) [32]byte {
	t.Helper()
	rows, err := db.Query(`SELECT ` + t004ArtifactDigest + ` FROM task_artifacts ORDER BY seq`)
	t004Must(t, err)
	var builder strings.Builder
	for rows.Next() {
		var row string
		t004Must(t, rows.Scan(&row))
		builder.WriteString(row)
		builder.WriteByte(0)
	}
	t004Must(t, rows.Err())
	t004Must(t, rows.Close())
	return sha256.Sum256([]byte(builder.String()))
}

func t010ArtifactGlobalSeqs(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	rows, err := db.Query(`SELECT seq FROM task_artifacts ORDER BY seq`)
	t004Must(t, err)
	var seqs []int64
	for rows.Next() {
		var seq int64
		t004Must(t, rows.Scan(&seq))
		seqs = append(seqs, seq)
	}
	t004Must(t, rows.Err())
	t004Must(t, rows.Close())
	return seqs
}

func t010AssertTaskEventSeq(t *testing.T, db *sql.DB, taskID string, want []int64) {
	t.Helper()
	rows, err := db.Query(`SELECT event_seq FROM task_artifacts WHERE task_id=? ORDER BY seq`, taskID)
	t004Must(t, err)
	var got []int64
	for rows.Next() {
		var eventSeq int64
		t004Must(t, rows.Scan(&eventSeq))
		got = append(got, eventSeq)
	}
	t004Must(t, rows.Err())
	t004Must(t, rows.Close())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("task %s event_seq=%v want=%v", taskID, got, want)
	}
}

func TestTaskStore_MigrateV10_FreshRepeatReopenAndConstraints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh-v10.db")
	db := t004OpenDB(t, path)
	_ = t004NewStore(t, db, "fresh-v10")
	want := t010MigrationLedgerSnapshot{
		Version: 10, State: "complete", CheckpointSeq: 0,
		SourceRows: 0, ProcessedRows: 0, BatchCount: 0,
	}
	first := t010AssertV10Stage(t, db, want)
	_ = t004NewStore(t, db, "fresh-v10")
	repeat := t010AssertV10Stage(t, db, want)
	if repeat != first {
		t.Fatalf("repeat changed v10 completion evidence: first=%+v repeat=%+v", first, repeat)
	}
	t004Must(t, db.Close())
	reopened := t004OpenDB(t, path)
	_ = t004NewStore(t, reopened, "fresh-v10")
	reopen := t010AssertV10Stage(t, reopened, want)
	if reopen != first {
		t.Fatalf("reopen changed v10 completion evidence: first=%+v reopen=%+v", first, reopen)
	}
}

func TestTaskStore_MigrateV10_ResumesInterruptedBackfillWithoutDataLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "interrupted-v10.db")
	fixture := t004OpenDB(t, path)
	_, err := fixture.Exec(t004LiteralV8Schema)
	t004Must(t, err)
	t004Must(t, migrateV9(fixture))
	_, err = fixture.Exec(`
INSERT INTO tasks(id,status,worker_type,project_id,prompt,created_at,engine_name,tenant_id)
VALUES
 ('t010-resume-a','running','cli','t010-resume','A','2030-01-01T00:00:00Z','resume','tenant'),
 ('t010-resume-b','running','cli','t010-resume','B','2030-01-01T00:00:01Z','resume','tenant');
INSERT INTO task_artifacts(seq,task_id,kind,event_type,channel,summary,payload_json,content_length,redacted,truncated,created_at)
VALUES
 (10,'t010-resume-a','runtime','text_delta','stdout','A1','{"text":"A1"}',2,0,0,'2030-01-01T00:01:00Z'),
 (20,'t010-resume-b','runtime','status','stderr','B1','{"state":"running"}',2,0,0,'2030-01-01T00:02:00Z'),
 (30,'t010-resume-a','runtime','output_truncated','system','A2 gap','{"reason":"admission_quota","dropped_events":7,"dropped_bytes":257}',257,0,1,'2030-01-01T00:03:00Z');`)
	t004Must(t, err)

	beforeDigest := t010ArtifactDigestAll(t, fixture)
	beforeSeqs := t010ArtifactGlobalSeqs(t, fixture)
	var beforeCount int64
	t004Must(t, fixture.QueryRow(`SELECT count(*) FROM task_artifacts`).Scan(&beforeCount))

	_, err = fixture.Exec(`ALTER TABLE task_artifacts ADD COLUMN event_seq INTEGER;` + t010MigrationLedgerDDL + `
INSERT INTO loom_migrations(version,state,checkpoint_seq,source_rows,processed_rows,batch_count,started_at,completed_at)
VALUES(10,'running',20,3,2,1,'2030-01-01T00:10:00Z',NULL);
UPDATE task_artifacts SET event_seq=1 WHERE seq IN (10,20);
CREATE TRIGGER t010_reject_backfill_prefix_rewrite
BEFORE UPDATE OF event_seq ON task_artifacts
WHEN OLD.seq <= 20
BEGIN
	SELECT RAISE(ABORT,'t010 processed prefix was rewritten');
END;`)
	t004Must(t, err)
	t004Must(t, fixture.Close())

	reopened := t004OpenDB(t, path)
	_ = t004NewStore(t, reopened, "resume-v10")
	want := t010MigrationLedgerSnapshot{
		Version: 10, State: "complete", CheckpointSeq: 30,
		SourceRows: 3, ProcessedRows: 3, BatchCount: 2,
		StartedAt: "2030-01-01T00:10:00Z",
	}
	first := t010AssertV10Stage(t, reopened, want)
	t010AssertTaskEventSeq(t, reopened, "t010-resume-a", []int64{1, 2})
	t010AssertTaskEventSeq(t, reopened, "t010-resume-b", []int64{1})
	if got := t010ArtifactGlobalSeqs(t, reopened); !reflect.DeepEqual(got, beforeSeqs) {
		t.Fatalf("global seq changed: got=%v want=%v", got, beforeSeqs)
	}
	var afterCount int64
	t004Must(t, reopened.QueryRow(`SELECT count(*) FROM task_artifacts`).Scan(&afterCount))
	if afterCount != beforeCount {
		t.Fatalf("artifact row count changed: got=%d want=%d", afterCount, beforeCount)
	}
	if got := t010ArtifactDigestAll(t, reopened); got != beforeDigest {
		t.Fatalf("pre-v10 artifact digest changed: got=%x want=%x", got, beforeDigest)
	}

	_ = t004NewStore(t, reopened, "resume-v10")
	repeat := t010AssertV10Stage(t, reopened, want)
	if repeat != first {
		t.Fatalf("repeat changed resumed v10 evidence: first=%+v repeat=%+v", first, repeat)
	}
	if got := t010ArtifactDigestAll(t, reopened); got != beforeDigest {
		t.Fatalf("repeat changed pre-v10 artifact digest: got=%x want=%x", got, beforeDigest)
	}
	t004Must(t, reopened.Close())

	reopenedAgain := t004OpenDB(t, path)
	_ = t004NewStore(t, reopenedAgain, "resume-v10")
	reopen := t010AssertV10Stage(t, reopenedAgain, want)
	if reopen != first {
		t.Fatalf("second reopen changed resumed v10 evidence: first=%+v reopen=%+v", first, reopen)
	}
	t010AssertTaskEventSeq(t, reopenedAgain, "t010-resume-a", []int64{1, 2})
	t010AssertTaskEventSeq(t, reopenedAgain, "t010-resume-b", []int64{1})
	if got := t010ArtifactGlobalSeqs(t, reopenedAgain); !reflect.DeepEqual(got, beforeSeqs) {
		t.Fatalf("second reopen changed global seq: got=%v want=%v", got, beforeSeqs)
	}
	if got := t010ArtifactDigestAll(t, reopenedAgain); got != beforeDigest {
		t.Fatalf("second reopen changed pre-v10 artifact digest: got=%x want=%x", got, beforeDigest)
	}
}
