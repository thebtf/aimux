package loom

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t001WorkerSessionColumns = []string{
	"id", "tenant_id", "engine_name", "project_id", "canonical_worktree_root",
	"profile_fingerprint", "capability_fingerprint", "requested_mode", "provider_name",
	"provider_session_id", "provider_session_generation", "state", "active_task_id",
	"lease_owner", "lease_generation", "lease_expires_at", "parent_worker_session_id",
	"created_at", "updated_at", "closed_at",
}

var t001WorkerRunBindingColumns = []string{
	"id", "task_id", "worker_session_id", "tenant_id", "engine_name", "project_id",
	"requested_mode", "executor_name", "provider_name", "provider_session_id",
	"provider_session_generation", "provider_connection_generation", "swarm_scope",
	"swarm_handle_id", "swarm_handle_generation", "swarm_registry_generation", "execution_id",
	"process_pid", "process_start_fingerprint", "process_tree_id", "state", "lease_owner",
	"lease_generation", "lease_expires_at", "reconciliation_classification", "terminal_reason",
	"created_at", "started_at", "returned_at", "terminal_at", "updated_at",
}

var t001IntegerColumns = map[string]bool{
	"provider_session_generation":    true,
	"provider_connection_generation": true,
	"swarm_handle_generation":        true,
	"swarm_registry_generation":      true,
	"lease_generation":               true,
	"process_pid":                    true,
}

type t001Column struct {
	name       string
	typeName   string
	notNull    int
	defaultSQL sql.NullString
	primaryKey int
}

func t001TableColumns(t *testing.T, db *sql.DB, table string) []t001Column {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()

	var columns []t001Column
	for rows.Next() {
		var cid int
		var column t001Column
		if err := rows.Scan(&cid, &column.name, &column.typeName, &column.notNull, &column.defaultSQL, &column.primaryKey); err != nil {
			t.Fatalf("scan %s column: %v", table, err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s columns: %v", table, err)
	}
	return columns
}

func t001RequireColumns(t *testing.T, db *sql.DB, table string, want []string) {
	t.Helper()
	gotColumns := t001TableColumns(t, db, table)
	got := make([]string, len(gotColumns))
	for i, column := range gotColumns {
		got[i] = column.name
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s columns = %v, want %v", table, got, want)
	}

	for i, column := range gotColumns {
		wantType := "TEXT"
		if t001IntegerColumns[column.name] {
			wantType = "INTEGER"
		}
		if strings.ToUpper(column.typeName) != wantType {
			t.Errorf("%s.%s type = %q, want %s", table, column.name, column.typeName, wantType)
		}
		if column.primaryKey == 1 && (column.name != "id" || i != 0) {
			t.Errorf("%s primary key = %s, want id", table, column.name)
		}
	}
	if len(gotColumns) == 0 {
		t.Fatalf("%s has no columns", table)
	}
	if gotColumns[0].name != "id" || gotColumns[0].primaryKey != 1 || gotColumns[0].notNull != 1 || gotColumns[0].defaultSQL.Valid {
		t.Errorf("%s.id must be a NOT NULL TEXT primary key without a default; got %+v", table, gotColumns[0])
	}
}

func t001TableSQL(t *testing.T, db *sql.DB, objectType, name string) string {
	t.Helper()
	var statement string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type=? AND name=?`, objectType, name).Scan(&statement); err != nil {
		t.Fatalf("lookup %s %s: %v", objectType, name, err)
	}
	return statement
}

func t001NormalizedSQL(sql string) string {
	return strings.Join(strings.Fields(strings.ToLower(sql)), " ")
}

func t001RequireSQL(t *testing.T, db *sql.DB, table string, fragments ...string) {
	t.Helper()
	statement := t001NormalizedSQL(t001TableSQL(t, db, "table", table))
	for _, fragment := range fragments {
		if !strings.Contains(statement, t001NormalizedSQL(fragment)) {
			t.Errorf("%s schema missing %q in %s", table, fragment, statement)
		}
	}
}

func t001IndexColumns(t *testing.T, db *sql.DB, index string) []string {
	t.Helper()
	rows, err := db.Query(`PRAGMA index_info(` + index + `)`)
	if err != nil {
		t.Fatalf("PRAGMA index_info(%s): %v", index, err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var sequence, cid int
		var name string
		if err := rows.Scan(&sequence, &cid, &name); err != nil {
			t.Fatalf("scan index %s: %v", index, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index %s: %v", index, err)
	}
	return columns
}

func t001RequireIndex(t *testing.T, db *sql.DB, table string, want []string, unique bool) error {
	t.Helper()
	type index struct {
		name   string
		unique bool
	}
	rows, err := db.Query(`PRAGMA index_list(` + table + `)`)
	if err != nil {
		return fmt.Errorf("PRAGMA index_list(%s): %w", table, err)
	}
	var indexes []index
	for rows.Next() {
		var sequence, isUnique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &isUnique, &origin, &partial); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan index list %s: %w", table, err)
		}
		indexes = append(indexes, index{name: name, unique: isUnique == 1})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate index list %s: %w", table, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close index list %s: %w", table, err)
	}
	for _, index := range indexes {
		if index.unique == unique && strings.Join(t001IndexColumns(t, db, index.name), ",") == strings.Join(want, ",") {
			return nil
		}
	}
	return fmt.Errorf("%s lacks %s index on (%s)", table, map[bool]string{true: "unique", false: "non-unique"}[unique], strings.Join(want, ","))
}

func t001RequireForeignKey(t *testing.T, db *sql.DB, table, from, toTable, to string) error {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_list(` + table + `)`)
	if err != nil {
		return fmt.Errorf("PRAGMA foreign_key_list(%s): %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, sequence int
		var target, source, destination, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &sequence, &target, &source, &destination, &onUpdate, &onDelete, &match); err != nil {
			return fmt.Errorf("scan foreign key %s: %w", table, err)
		}
		if source == from && target == toTable && destination == to {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate foreign keys %s: %w", table, err)
	}
	return fmt.Errorf("%s.%s lacks foreign key to %s.%s", table, from, toTable, to)
}

func t001RequireNoSecretColumns(t *testing.T, db *sql.DB, table string) error {
	t.Helper()
	for _, column := range t001TableColumns(t, db, table) {
		for _, forbidden := range []string{"prompt", "raw", "frame", "credential", "auth", "token", "cookie", "secret", "env", "request_json", "response_json", "delivery_json"} {
			if strings.Contains(strings.ToLower(column.name), forbidden) {
				return fmt.Errorf("%s contains forbidden column %q", table, column.name)
			}
		}
	}
	return nil
}

func t001AssertForeignKeys(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, fk := range []struct{ table, from, target, to string }{
		{"worker_sessions", "active_task_id", "tasks", "id"},
		{"worker_sessions", "parent_worker_session_id", "worker_sessions", "id"},
		{"worker_run_bindings", "task_id", "tasks", "id"},
		{"worker_run_bindings", "worker_session_id", "worker_sessions", "id"},
	} {
		if err := t001RequireForeignKey(t, db, fk.table, fk.from, fk.target, fk.to); err != nil {
			t.Error(err)
		}
	}

	var foreignKeys int
	t004Must(t, db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys))
	if foreignKeys != 1 {
		t.Errorf("PRAGMA foreign_keys = %d, want 1", foreignKeys)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	t004Must(t, err)
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent string
		var fkIndex int
		t004Must(t, rows.Scan(&table, &rowID, &parent, &fkIndex))
		t.Errorf("foreign_key_check violation: table=%s rowid=%v parent=%s fk=%d", table, rowID, parent, fkIndex)
	}
	t004Must(t, rows.Err())
}

func t001AssertRuntimeBindingsSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	t001RequireColumns(t, db, "worker_sessions", t001WorkerSessionColumns)
	t001RequireColumns(t, db, "worker_run_bindings", t001WorkerRunBindingColumns)

	t001RequireSQL(t, db, "worker_sessions",
		"tenant_id text not null", "engine_name text not null", "project_id text not null",
		"canonical_worktree_root text not null", "profile_fingerprint text not null",
		"capability_fingerprint text not null", "requested_mode text not null",
		"state text not null default 'available'", "lease_generation integer not null default 0 check (lease_generation >= 0)",
		"check ((provider_name is null and provider_session_id is null and provider_session_generation is null) or (provider_name is not null and provider_session_id is not null and provider_session_generation > 0))",
		"check ((requested_mode = 'fork' and parent_worker_session_id is not null) or (requested_mode <> 'fork' and parent_worker_session_id is null))",
		"check ((state = 'leased' and active_task_id is not null) or (state <> 'leased' and active_task_id is null))",
		"check ((state = 'leased' and lease_owner is not null and lease_expires_at is not null) or (state <> 'leased' and lease_owner is null and lease_expires_at is null))",
		"check ((state = 'closed' and closed_at is not null) or (state <> 'closed' and closed_at is null))",
		"check (requested_mode in ('new','exact_resume','fork'))",
		"check (state in ('available','leased','closed','unavailable'))",
	)
	t001RequireSQL(t, db, "worker_run_bindings",
		"task_id text not null", "tenant_id text not null", "engine_name text not null", "project_id text not null",
		"requested_mode text not null", "executor_name text not null", "swarm_scope text not null", "state text not null default 'reserved'",
		"lease_generation integer not null default 0 check (lease_generation >= 0)",
		"check ((provider_name is null and provider_session_id is null and provider_session_generation is null) or (provider_name is not null and provider_session_id is not null and provider_session_generation > 0))",
		"check (provider_connection_generation is null or (provider_connection_generation > 0 and provider_name is not null))",
		"check ((swarm_handle_id is null and swarm_handle_generation is null and swarm_registry_generation is null) or (swarm_handle_id is not null and swarm_handle_generation > 0 and swarm_registry_generation > 0))",
		"check ((process_pid is null and process_start_fingerprint is null and process_tree_id is null) or (process_pid > 0 and process_start_fingerprint is not null and process_tree_id is not null))",
		"check ((requested_mode = 'stateless' and worker_session_id is null) or (requested_mode <> 'stateless' and worker_session_id is not null))",
		"check ((state in ('reserved','running','returned','cancelling') and lease_owner is not null and lease_expires_at is not null) or (state = 'terminal' and lease_owner is null and lease_expires_at is null))",
		"check ((state = 'terminal' and terminal_at is not null) or (state <> 'terminal' and terminal_at is null))",
		"check (requested_mode in ('stateless','new','exact_resume','fork'))",
		"check (state in ('reserved','running','returned','cancelling','terminal'))",
	)

	t001AssertForeignKeys(t, db)
	if err := t001RequireIndex(t, db, "worker_sessions", []string{"tenant_id", "engine_name", "project_id", "canonical_worktree_root", "profile_fingerprint", "capability_fingerprint", "state"}, false); err != nil {
		t.Error(err)
	}
	if err := t001RequireIndex(t, db, "worker_run_bindings", []string{"tenant_id", "engine_name", "project_id", "state", "updated_at"}, false); err != nil {
		t.Error(err)
	}
	if err := t001RequireNoSecretColumns(t, db, "worker_sessions"); err != nil {
		t.Error(err)
	}
	if err := t001RequireNoSecretColumns(t, db, "worker_run_bindings"); err != nil {
		t.Error(err)
	}

	const predicate = "worker_session_id is not null and state in ('reserved','running','returned','cancelling')"
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type='index' AND tbl_name='worker_run_bindings' AND sql IS NOT NULL`)
	t004Must(t, err)
	type indexSQL struct{ name, statement string }
	var indexes []indexSQL
	for rows.Next() {
		var index indexSQL
		t004Must(t, rows.Scan(&index.name, &index.statement))
		indexes = append(indexes, index)
	}
	t004Must(t, rows.Err())
	t004Must(t, rows.Close())
	found := false
	for _, index := range indexes {
		normalized := t001NormalizedSQL(index.statement)
		if strings.Contains(normalized, "create unique index") && strings.Contains(normalized, "where "+predicate) {
			if got := strings.Join(t001IndexColumns(t, db, index.name), ","); got != "worker_session_id" {
				t.Errorf("active-session unique index columns = (%s), want (worker_session_id)", got)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("worker_run_bindings lacks unique active-session index on (worker_session_id) with exact predicate %q", predicate)
	}
}

func TestTaskStore_MigrateV11_RuntimeBindingsSchema_Fresh(t *testing.T) {
	db := t004OpenDB(t, filepath.Join(t.TempDir(), "fresh.db"))
	t004NewStore(t, db, "runtime-bindings")
	t001AssertRuntimeBindingsSchema(t, db)
}

func TestTaskStore_MigrateV11_RuntimeBindingsSchema_ReopenIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	db := t004OpenDB(t, path)
	t004NewStore(t, db, "runtime-bindings")
	t004NewStore(t, db, "runtime-bindings")
	t001AssertRuntimeBindingsSchema(t, db)
	t004Must(t, db.Close())

	reopened := t004OpenDB(t, path)
	t004NewStore(t, reopened, "runtime-bindings")
	t001AssertRuntimeBindingsSchema(t, reopened)
}

func TestTaskStore_MigrateV11_RuntimeBindingsSchema_RejectsMalformedSameNameTable(t *testing.T) {
	db := t004OpenDB(t, filepath.Join(t.TempDir(), "malformed-table.db"))
	t004NewStore(t, db, "runtime-bindings")
	t004Must(t, func() error {
		_, err := db.Exec(`DROP TABLE worker_run_bindings; DROP TABLE worker_sessions; CREATE TABLE worker_sessions (id TEXT PRIMARY KEY)`)
		return err
	}())
	if _, err := NewTaskStore(db, "runtime-bindings"); err == nil {
		t.Fatal("NewTaskStore accepted malformed pre-existing worker_sessions table")
	}
}

func TestTaskStore_MigrateV11_RuntimeBindingsSchema_RejectsMalformedSameNameIndex(t *testing.T) {
	db := t004OpenDB(t, filepath.Join(t.TempDir(), "malformed-index.db"))
	t004NewStore(t, db, "runtime-bindings")
	t004Must(t, func() error {
		_, err := db.Exec(`DROP INDEX IF EXISTS idx_worker_run_bindings_active_session; CREATE INDEX idx_worker_run_bindings_active_session ON worker_run_bindings(id)`)
		return err
	}())
	if _, err := NewTaskStore(db, "runtime-bindings"); err == nil {
		t.Fatal("NewTaskStore accepted malformed pre-existing runtime-bindings index")
	}
}

func TestTaskStore_MigrateV11_RuntimeBindingsSchema_LegacyV10Compatibility(t *testing.T) {
	db := t004OpenDB(t, filepath.Join(t.TempDir(), "legacy-v10.db"))
	store := t004NewStore(t, db, "runtime-bindings")
	task := &Task{
		ID:         "legacy-v10-task",
		Status:     TaskStatusPending,
		WorkerType: WorkerTypeCLI,
		ProjectID:  "p",
		TenantID:   "t",
		Prompt:     "legacy",
		CreatedAt:  time.Now().UTC(),
	}
	t004Must(t, store.Create(task))
	t004Must(t, func() error {
		_, err := db.Exec(`DROP TABLE worker_run_bindings; DROP TABLE worker_sessions`)
		return err
	}())
	reopenedStore, err := NewTaskStore(db, "runtime-bindings")
	t004Must(t, err)
	got, err := reopenedStore.Get(task.ID)
	t004Must(t, err)
	if got.ID != task.ID {
		t.Errorf("legacy task ID = %q, want %q", got.ID, task.ID)
	}
	t001AssertRuntimeBindingsSchema(t, db)

}
func TestTaskStore_MigrateV11_RuntimeBindingsSchema_AuditSQL(t *testing.T) {
	migrationSQL, err := os.ReadFile("migrations/011_worker_session_bindings.sql")
	t004Must(t, err)

	db := t004OpenDB(t, filepath.Join(t.TempDir(), "audit.sql.db"))
	t004NewStore(t, db, "runtime-bindings")
	t004Must(t, func() error {
		_, err := db.Exec(`DROP TABLE worker_run_bindings; DROP TABLE worker_sessions`)
		return err
	}())
	t004Must(t, func() error { _, err := db.Exec(string(migrationSQL)); return err }())
	t001AssertRuntimeBindingsSchema(t, db)
}

func TestTaskStore_MigrateV11_RuntimeBindingsSchema_MutationTeeth(t *testing.T) {
	db := t004OpenDB(t, filepath.Join(t.TempDir(), "mutation-teeth.db"))
	t004Must(t, func() error {
		_, err := db.Exec(`
			CREATE TABLE worker_sessions (id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, prompt TEXT);
			CREATE TABLE worker_run_bindings (id TEXT PRIMARY KEY, worker_session_id TEXT, state TEXT);
			CREATE TABLE tasks (id TEXT PRIMARY KEY);
		`)
		return err
	}())
	if err := t001RequireIndex(t, db, "worker_run_bindings", []string{"worker_session_id"}, true); err == nil {
		t.Fatal("unique-index guard did not reject missing active-session uniqueness")
	}
	if err := t001RequireForeignKey(t, db, "worker_run_bindings", "worker_session_id", "worker_sessions", "id"); err == nil {
		t.Fatal("foreign-key guard did not reject missing worker-session foreign key")
	}
	if err := t001RequireNoSecretColumns(t, db, "worker_sessions"); err == nil {
		t.Fatal("secret-column guard did not reject prompt column")
	}
}

func t001OpenReopenDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	t004Must(t, err)
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA foreign_keys=ON`)
	t004Must(t, err)
	return db
}
