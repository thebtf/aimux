package loom

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RuntimeBindingMode declares whether a run is independent or session-backed.
type RuntimeBindingMode string

const (
	RuntimeBindingModeStateless   RuntimeBindingMode = "stateless"
	RuntimeBindingModeNew         RuntimeBindingMode = "new"
	RuntimeBindingModeExactResume RuntimeBindingMode = "exact_resume"
	RuntimeBindingModeFork        RuntimeBindingMode = "fork"
)

// WorkerSessionState is the durable availability state of a provider-neutral worker session.
type WorkerSessionState string

const (
	WorkerSessionStateAvailable   WorkerSessionState = "available"
	WorkerSessionStateLeased      WorkerSessionState = "leased"
	WorkerSessionStateClosed      WorkerSessionState = "closed"
	WorkerSessionStateUnavailable WorkerSessionState = "unavailable"
)

// WorkerRunBindingState is the durable lifecycle state of a worker run binding.
type WorkerRunBindingState string

const (
	WorkerRunBindingStateReserved   WorkerRunBindingState = "reserved"
	WorkerRunBindingStateRunning    WorkerRunBindingState = "running"
	WorkerRunBindingStateReturned   WorkerRunBindingState = "returned"
	WorkerRunBindingStateCancelling WorkerRunBindingState = "cancelling"
	WorkerRunBindingStateTerminal   WorkerRunBindingState = "terminal"
)

// ProviderSessionIdentity identifies a provider session without retaining provider payloads.
type ProviderSessionIdentity struct {
	ProviderName string
	SessionID    string
	Generation   int64
}

// LiveHandleIdentity identifies the live Swarm handle that owns a binding.
type LiveHandleIdentity struct {
	Scope              string
	HandleID           string
	HandleGeneration   int64
	RegistryGeneration int64
}

// ProcessIdentity identifies a process tree without retaining process output.
type ProcessIdentity struct {
	PID              int64
	StartFingerprint string
	TreeID           string
}

// WorkerSessionRecord is the typed durable representation of worker_sessions.
type WorkerSessionRecord struct {
	ID                    string
	TenantID              string
	EngineName            string
	ProjectID             string
	CanonicalWorktreeRoot string
	ProfileFingerprint    string
	CapabilityFingerprint string
	RequestedMode         RuntimeBindingMode
	ProviderSession       *ProviderSessionIdentity
	State                 WorkerSessionState
	ActiveTaskID          *string
	LeaseOwner            *string
	LeaseGeneration       int64
	LeaseExpiresAt        *time.Time
	ParentWorkerSessionID *string
	CreatedAt             time.Time
	UpdatedAt             time.Time
	ClosedAt              *time.Time
}

// WorkerRunBindingRecord is the typed durable representation of worker_run_bindings.
type WorkerRunBindingRecord struct {
	ID                           string
	TaskID                       string
	WorkerSessionID              *string
	TenantID                     string
	EngineName                   string
	ProjectID                    string
	RequestedMode                RuntimeBindingMode
	ExecutorName                 string
	SwarmScope                   string
	ProviderSession              *ProviderSessionIdentity
	ProviderConnectionGeneration *int64
	LiveHandle                   *LiveHandleIdentity
	ExecutionID                  *string
	Process                      *ProcessIdentity
	State                        WorkerRunBindingState
	LeaseOwner                   *string
	LeaseGeneration              int64
	LeaseExpiresAt               *time.Time
	ReconciliationClassification *string
	TerminalReason               *string
	CreatedAt                    time.Time
	StartedAt                    *time.Time
	ReturnedAt                   *time.Time
	TerminalAt                   *time.Time
	UpdatedAt                    time.Time
}

const createWorkerSessionsTable = `
CREATE TABLE IF NOT EXISTS worker_sessions (
	id TEXT NOT NULL PRIMARY KEY,
	tenant_id TEXT NOT NULL,
	engine_name TEXT NOT NULL,
	project_id TEXT NOT NULL,
	canonical_worktree_root TEXT NOT NULL,
	profile_fingerprint TEXT NOT NULL,
	capability_fingerprint TEXT NOT NULL,
	requested_mode TEXT NOT NULL,
	provider_name TEXT,
	provider_session_id TEXT,
	provider_session_generation INTEGER,
	state TEXT NOT NULL DEFAULT 'available',
	active_task_id TEXT REFERENCES tasks(id),
	lease_owner TEXT,
	lease_generation INTEGER NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
	lease_expires_at TEXT,
	parent_worker_session_id TEXT REFERENCES worker_sessions(id),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	closed_at TEXT,
	CHECK ((provider_name IS NULL AND provider_session_id IS NULL AND provider_session_generation IS NULL) OR (provider_name IS NOT NULL AND provider_session_id IS NOT NULL AND provider_session_generation > 0)),
	CHECK ((requested_mode = 'fork' AND parent_worker_session_id IS NOT NULL) OR (requested_mode <> 'fork' AND parent_worker_session_id IS NULL)),
	CHECK ((state = 'leased' AND active_task_id IS NOT NULL) OR (state <> 'leased' AND active_task_id IS NULL)),
	CHECK ((state = 'leased' AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL) OR (state <> 'leased' AND lease_owner IS NULL AND lease_expires_at IS NULL)),
	CHECK ((state = 'closed' AND closed_at IS NOT NULL) OR (state <> 'closed' AND closed_at IS NULL)),
	CHECK (requested_mode IN ('new','exact_resume','fork')),
	CHECK (state IN ('available','leased','closed','unavailable'))
)`

const createWorkerRunBindingsTable = `
CREATE TABLE IF NOT EXISTS worker_run_bindings (
	id TEXT NOT NULL PRIMARY KEY,
	task_id TEXT NOT NULL REFERENCES tasks(id),
	worker_session_id TEXT REFERENCES worker_sessions(id),
	tenant_id TEXT NOT NULL,
	engine_name TEXT NOT NULL,
	project_id TEXT NOT NULL,
	requested_mode TEXT NOT NULL,
	executor_name TEXT NOT NULL,
	provider_name TEXT,
	provider_session_id TEXT,
	provider_session_generation INTEGER,
	provider_connection_generation INTEGER,
	swarm_scope TEXT NOT NULL,
	swarm_handle_id TEXT,
	swarm_handle_generation INTEGER,
	swarm_registry_generation INTEGER,
	execution_id TEXT,
	process_pid INTEGER,
	process_start_fingerprint TEXT,
	process_tree_id TEXT,
	state TEXT NOT NULL DEFAULT 'reserved',
	lease_owner TEXT,
	lease_generation INTEGER NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
	lease_expires_at TEXT,
	reconciliation_classification TEXT,
	terminal_reason TEXT,
	created_at TEXT NOT NULL,
	started_at TEXT,
	returned_at TEXT,
	terminal_at TEXT,
	updated_at TEXT NOT NULL,
	CHECK ((provider_name IS NULL AND provider_session_id IS NULL AND provider_session_generation IS NULL) OR (provider_name IS NOT NULL AND provider_session_id IS NOT NULL AND provider_session_generation > 0)),
	CHECK (provider_connection_generation IS NULL OR (provider_connection_generation > 0 AND provider_name IS NOT NULL)),
	CHECK ((swarm_handle_id IS NULL AND swarm_handle_generation IS NULL AND swarm_registry_generation IS NULL) OR (swarm_handle_id IS NOT NULL AND swarm_handle_generation > 0 AND swarm_registry_generation > 0)),
	CHECK ((process_pid IS NULL AND process_start_fingerprint IS NULL AND process_tree_id IS NULL) OR (process_pid > 0 AND process_start_fingerprint IS NOT NULL AND process_tree_id IS NOT NULL)),
	CHECK ((requested_mode = 'stateless' AND worker_session_id IS NULL) OR (requested_mode <> 'stateless' AND worker_session_id IS NOT NULL)),
	CHECK ((state IN ('reserved','running','returned','cancelling') AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL) OR (state = 'terminal' AND lease_owner IS NULL AND lease_expires_at IS NULL)),
	CHECK ((state = 'terminal' AND terminal_at IS NOT NULL) OR (state <> 'terminal' AND terminal_at IS NULL)),
	CHECK (requested_mode IN ('stateless','new','exact_resume','fork')),
	CHECK (state IN ('reserved','running','returned','cancelling','terminal'))
)`

const (
	createWorkerSessionsLookupIndex           = `CREATE INDEX IF NOT EXISTS idx_worker_sessions_lookup ON worker_sessions(tenant_id, engine_name, project_id, canonical_worktree_root, profile_fingerprint, capability_fingerprint, state)`
	createWorkerRunBindingsReconcileIndex     = `CREATE INDEX IF NOT EXISTS idx_worker_run_bindings_reconcile ON worker_run_bindings(tenant_id, engine_name, project_id, state, updated_at)`
	createWorkerRunBindingsActiveSessionIndex = `CREATE UNIQUE INDEX IF NOT EXISTS idx_worker_run_bindings_active_session ON worker_run_bindings(worker_session_id) WHERE worker_session_id IS NOT NULL AND state IN ('reserved','running','returned','cancelling')`
)

// migrateV11 creates the additive v11 runtime-binding schema. Existing objects
// are never repaired or replaced: every object is validated before commit.
func migrateV11(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("loom runtime bindings: nil database")
	}

	tx, err := beginAuthorityTransaction(context.Background(), db)
	if err != nil {
		return fmt.Errorf("loom runtime bindings begin: %w", err)
	}
	for _, statement := range []string{
		createWorkerSessionsTable,
		createWorkerRunBindingsTable,
		createWorkerSessionsLookupIndex,
		createWorkerRunBindingsReconcileIndex,
		createWorkerRunBindingsActiveSessionIndex,
	} {
		if _, err := tx.conn.ExecContext(tx.ctx, statement); err != nil {
			return migrateV11Rollback(tx, fmt.Errorf("loom runtime bindings create object: %w", err))
		}
	}
	if err := validateV11RuntimeBindings(tx.conn, tx.ctx); err != nil {
		return migrateV11Rollback(tx, err)
	}
	if err := tx.commit(); err != nil {
		return fmt.Errorf("loom runtime bindings commit: %w", err)
	}
	return nil
}

func migrateV11Rollback(tx *authorityTransaction, cause error) error {
	if rollbackErr := tx.rollback(); rollbackErr != nil {
		return fmt.Errorf("%v; rollback: %w", cause, rollbackErr)
	}
	return cause
}

type runtimeBindingsColumn struct {
	name       string
	typeName   string
	notNull    int
	defaultSQL sql.NullString
	primaryKey int
}

type runtimeBindingsColumnExpectation struct {
	name       string
	typeName   string
	notNull    int
	defaultSQL *string
	primaryKey int
}

func runtimeBindingsDefault(value string) *string { return &value }

var workerSessionsColumns = []runtimeBindingsColumnExpectation{
	{"id", "TEXT", 1, nil, 1},
	{"tenant_id", "TEXT", 1, nil, 0},
	{"engine_name", "TEXT", 1, nil, 0},
	{"project_id", "TEXT", 1, nil, 0},
	{"canonical_worktree_root", "TEXT", 1, nil, 0},
	{"profile_fingerprint", "TEXT", 1, nil, 0},
	{"capability_fingerprint", "TEXT", 1, nil, 0},
	{"requested_mode", "TEXT", 1, nil, 0},
	{"provider_name", "TEXT", 0, nil, 0},
	{"provider_session_id", "TEXT", 0, nil, 0},
	{"provider_session_generation", "INTEGER", 0, nil, 0},
	{"state", "TEXT", 1, runtimeBindingsDefault("'available'"), 0},
	{"active_task_id", "TEXT", 0, nil, 0},
	{"lease_owner", "TEXT", 0, nil, 0},
	{"lease_generation", "INTEGER", 1, runtimeBindingsDefault("0"), 0},
	{"lease_expires_at", "TEXT", 0, nil, 0},
	{"parent_worker_session_id", "TEXT", 0, nil, 0},
	{"created_at", "TEXT", 1, nil, 0},
	{"updated_at", "TEXT", 1, nil, 0},
	{"closed_at", "TEXT", 0, nil, 0},
}

var workerRunBindingsColumns = []runtimeBindingsColumnExpectation{
	{"id", "TEXT", 1, nil, 1},
	{"task_id", "TEXT", 1, nil, 0},
	{"worker_session_id", "TEXT", 0, nil, 0},
	{"tenant_id", "TEXT", 1, nil, 0},
	{"engine_name", "TEXT", 1, nil, 0},
	{"project_id", "TEXT", 1, nil, 0},
	{"requested_mode", "TEXT", 1, nil, 0},
	{"executor_name", "TEXT", 1, nil, 0},
	{"provider_name", "TEXT", 0, nil, 0},
	{"provider_session_id", "TEXT", 0, nil, 0},
	{"provider_session_generation", "INTEGER", 0, nil, 0},
	{"provider_connection_generation", "INTEGER", 0, nil, 0},
	{"swarm_scope", "TEXT", 1, nil, 0},
	{"swarm_handle_id", "TEXT", 0, nil, 0},
	{"swarm_handle_generation", "INTEGER", 0, nil, 0},
	{"swarm_registry_generation", "INTEGER", 0, nil, 0},
	{"execution_id", "TEXT", 0, nil, 0},
	{"process_pid", "INTEGER", 0, nil, 0},
	{"process_start_fingerprint", "TEXT", 0, nil, 0},
	{"process_tree_id", "TEXT", 0, nil, 0},
	{"state", "TEXT", 1, runtimeBindingsDefault("'reserved'"), 0},
	{"lease_owner", "TEXT", 0, nil, 0},
	{"lease_generation", "INTEGER", 1, runtimeBindingsDefault("0"), 0},
	{"lease_expires_at", "TEXT", 0, nil, 0},
	{"reconciliation_classification", "TEXT", 0, nil, 0},
	{"terminal_reason", "TEXT", 0, nil, 0},
	{"created_at", "TEXT", 1, nil, 0},
	{"started_at", "TEXT", 0, nil, 0},
	{"returned_at", "TEXT", 0, nil, 0},
	{"terminal_at", "TEXT", 0, nil, 0},
	{"updated_at", "TEXT", 1, nil, 0},
}

func validateV11RuntimeBindings(conn *sql.Conn, ctx context.Context) error {
	for _, table := range []struct {
		name    string
		columns []runtimeBindingsColumnExpectation
		checks  []string
	}{
		{"worker_sessions", workerSessionsColumns, []string{
			"check ((provider_name is null and provider_session_id is null and provider_session_generation is null) or (provider_name is not null and provider_session_id is not null and provider_session_generation > 0))",
			"check ((requested_mode = 'fork' and parent_worker_session_id is not null) or (requested_mode <> 'fork' and parent_worker_session_id is null))",
			"check ((state = 'leased' and active_task_id is not null) or (state <> 'leased' and active_task_id is null))",
			"check ((state = 'leased' and lease_owner is not null and lease_expires_at is not null) or (state <> 'leased' and lease_owner is null and lease_expires_at is null))",
			"check ((state = 'closed' and closed_at is not null) or (state <> 'closed' and closed_at is null))",
			"check (requested_mode in ('new','exact_resume','fork'))", "check (state in ('available','leased','closed','unavailable'))",
		}},
		{"worker_run_bindings", workerRunBindingsColumns, []string{
			"check ((provider_name is null and provider_session_id is null and provider_session_generation is null) or (provider_name is not null and provider_session_id is not null and provider_session_generation > 0))",
			"check (provider_connection_generation is null or (provider_connection_generation > 0 and provider_name is not null))",
			"check ((swarm_handle_id is null and swarm_handle_generation is null and swarm_registry_generation is null) or (swarm_handle_id is not null and swarm_handle_generation > 0 and swarm_registry_generation > 0))",
			"check ((process_pid is null and process_start_fingerprint is null and process_tree_id is null) or (process_pid > 0 and process_start_fingerprint is not null and process_tree_id is not null))",
			"check ((requested_mode = 'stateless' and worker_session_id is null) or (requested_mode <> 'stateless' and worker_session_id is not null))",
			"check ((state in ('reserved','running','returned','cancelling') and lease_owner is not null and lease_expires_at is not null) or (state = 'terminal' and lease_owner is null and lease_expires_at is null))",
			"check ((state = 'terminal' and terminal_at is not null) or (state <> 'terminal' and terminal_at is null))",
			"check (requested_mode in ('stateless','new','exact_resume','fork'))", "check (state in ('reserved','running','returned','cancelling','terminal'))",
		}},
	} {
		if err := validateV11Columns(conn, ctx, table.name, table.columns); err != nil {
			return err
		}
		if err := validateV11TableSQL(conn, ctx, table.name, table.checks); err != nil {
			return err
		}
		if err := validateV11ForbiddenColumns(conn, ctx, table.name); err != nil {
			return err
		}
	}
	if err := validateV11ForeignKeys(conn, ctx); err != nil {
		return err
	}
	for _, index := range []struct {
		table, name string
		columns     []string
		unique      bool
		partial     bool
		predicate   string
	}{
		{"worker_sessions", "idx_worker_sessions_lookup", []string{"tenant_id", "engine_name", "project_id", "canonical_worktree_root", "profile_fingerprint", "capability_fingerprint", "state"}, false, false, ""},
		{"worker_run_bindings", "idx_worker_run_bindings_reconcile", []string{"tenant_id", "engine_name", "project_id", "state", "updated_at"}, false, false, ""},
		{"worker_run_bindings", "idx_worker_run_bindings_active_session", []string{"worker_session_id"}, true, true, "worker_session_id is not null and state in ('reserved','running','returned','cancelling')"},
	} {
		if err := validateV11Index(conn, ctx, index.table, index.name, index.columns, index.unique, index.partial, index.predicate); err != nil {
			return err
		}
	}
	return validateV11ForeignKeyCheck(conn, ctx)
}

func validateV11Columns(conn *sql.Conn, ctx context.Context, table string, want []runtimeBindingsColumnExpectation) error {
	rows, err := conn.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return fmt.Errorf("loom runtime bindings table info %s: %w", table, err)
	}
	defer rows.Close()
	var got []runtimeBindingsColumn
	for rows.Next() {
		var cid int
		var column runtimeBindingsColumn
		if err := rows.Scan(&cid, &column.name, &column.typeName, &column.notNull, &column.defaultSQL, &column.primaryKey); err != nil {
			return fmt.Errorf("loom runtime bindings scan table info %s: %w", table, err)
		}
		got = append(got, column)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("loom runtime bindings iterate table info %s: %w", table, err)
	}
	if len(got) != len(want) {
		return fmt.Errorf("loom runtime bindings %s column count = %d, want %d", table, len(got), len(want))
	}
	for i, expected := range want {
		column := got[i]
		if column.name != expected.name || !strings.EqualFold(column.typeName, expected.typeName) || column.notNull != expected.notNull || column.primaryKey != expected.primaryKey || !sameV11Default(column.defaultSQL, expected.defaultSQL) {
			return fmt.Errorf("loom runtime bindings malformed %s column %d: got name=%q type=%q notnull=%d default=%q pk=%d", table, i, column.name, column.typeName, column.notNull, column.defaultSQL.String, column.primaryKey)
		}
	}
	return nil
}

func sameV11Default(got sql.NullString, want *string) bool {
	if want == nil {
		return !got.Valid
	}
	return got.Valid && got.String == *want
}

func validateV11TableSQL(conn *sql.Conn, ctx context.Context, table string, checks []string) error {
	var statement string
	if err := conn.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&statement); err != nil {
		return fmt.Errorf("loom runtime bindings lookup table %s: %w", table, err)
	}
	normalized := normalizeV11SQL(statement)
	for _, check := range checks {
		if !strings.Contains(normalized, normalizeV11SQL(check)) {
			return fmt.Errorf("loom runtime bindings %s missing required check %q", table, check)
		}
	}
	return nil
}

func validateV11ForbiddenColumns(conn *sql.Conn, ctx context.Context, table string) error {
	rows, err := conn.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return fmt.Errorf("loom runtime bindings table info %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, typeName string
		var defaultSQL sql.NullString
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultSQL, &primaryKey); err != nil {
			return fmt.Errorf("loom runtime bindings scan forbidden columns %s: %w", table, err)
		}
		for _, forbidden := range []string{"prompt", "raw", "frame", "credential", "auth", "token", "cookie", "secret", "env", "request_json", "response_json", "delivery_json"} {
			if strings.Contains(strings.ToLower(name), forbidden) {
				return fmt.Errorf("loom runtime bindings %s has forbidden column %q", table, name)
			}
		}
	}
	return rows.Err()
}

func validateV11ForeignKeys(conn *sql.Conn, ctx context.Context) error {
	want := map[string][]string{
		"worker_sessions":     {"active_task_id:tasks:id", "parent_worker_session_id:worker_sessions:id"},
		"worker_run_bindings": {"task_id:tasks:id", "worker_session_id:worker_sessions:id"},
	}
	for table, expected := range want {
		rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_list(`+table+`)`)
		if err != nil {
			return fmt.Errorf("loom runtime bindings foreign keys %s: %w", table, err)
		}
		var got []string
		for rows.Next() {
			var id, sequence int
			var target, source, destination, onUpdate, onDelete, match string
			if err := rows.Scan(&id, &sequence, &target, &source, &destination, &onUpdate, &onDelete, &match); err != nil {
				_ = rows.Close()
				return fmt.Errorf("loom runtime bindings scan foreign keys %s: %w", table, err)
			}
			got = append(got, source+":"+target+":"+destination)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("loom runtime bindings iterate foreign keys %s: %w", table, err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("loom runtime bindings close foreign keys %s: %w", table, err)
		}
		sort.Strings(got)
		sort.Strings(expected)
		if strings.Join(got, ",") != strings.Join(expected, ",") {
			return fmt.Errorf("loom runtime bindings %s foreign keys = %v, want %v", table, got, expected)
		}
	}
	return nil
}

func validateV11Index(conn *sql.Conn, ctx context.Context, table, name string, wantColumns []string, wantUnique, wantPartial bool, predicate string) error {
	rows, err := conn.QueryContext(ctx, `PRAGMA index_list(`+table+`)`)
	if err != nil {
		return fmt.Errorf("loom runtime bindings index list %s: %w", table, err)
	}
	var found bool
	for rows.Next() {
		var sequence, unique, partial int
		var indexName, origin string
		if err := rows.Scan(&sequence, &indexName, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			return fmt.Errorf("loom runtime bindings scan index list %s: %w", table, err)
		}
		if indexName != name {
			continue
		}
		found = true
		if (unique == 1) != wantUnique || (partial == 1) != wantPartial {
			_ = rows.Close()
			return fmt.Errorf("loom runtime bindings malformed index %s", name)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("loom runtime bindings iterate index list %s: %w", table, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("loom runtime bindings close index list %s: %w", table, err)
	}
	if !found {
		return fmt.Errorf("loom runtime bindings missing index %s", name)
	}

	infoRows, err := conn.QueryContext(ctx, `PRAGMA index_info(`+name+`)`)
	if err != nil {
		return fmt.Errorf("loom runtime bindings index info %s: %w", name, err)
	}
	var columns []string
	for infoRows.Next() {
		var sequence, cid int
		var column string
		if err := infoRows.Scan(&sequence, &cid, &column); err != nil {
			_ = infoRows.Close()
			return fmt.Errorf("loom runtime bindings scan index info %s: %w", name, err)
		}
		columns = append(columns, column)
	}
	if err := infoRows.Err(); err != nil {
		_ = infoRows.Close()
		return fmt.Errorf("loom runtime bindings iterate index info %s: %w", name, err)
	}
	if err := infoRows.Close(); err != nil {
		return fmt.Errorf("loom runtime bindings close index info %s: %w", name, err)
	}
	if strings.Join(columns, ",") != strings.Join(wantColumns, ",") {
		return fmt.Errorf("loom runtime bindings index %s columns = %v, want %v", name, columns, wantColumns)
	}
	var statement string
	if err := conn.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&statement); err != nil {
		return fmt.Errorf("loom runtime bindings lookup index %s: %w", name, err)
	}
	if predicate != "" {
		normalized := normalizeV11SQL(statement)
		where := strings.Index(normalized, " where ")
		if where < 0 || normalized[where+len(" where "):] != normalizeV11SQL(predicate) {
			return fmt.Errorf("loom runtime bindings index %s has wrong predicate", name)
		}
	}
	return nil
}

func validateV11ForeignKeyCheck(conn *sql.Conn, ctx context.Context) error {
	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("loom runtime bindings foreign key check: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var fkIndex int
		if err := rows.Scan(&table, &rowID, &parent, &fkIndex); err != nil {
			return fmt.Errorf("loom runtime bindings scan foreign key check: %w", err)
		}
		return fmt.Errorf("loom runtime bindings foreign key violation: table=%s rowid=%v parent=%s index=%d", table, rowID, parent, fkIndex)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("loom runtime bindings iterate foreign key check: %w", err)
	}
	return nil
}

func normalizeV11SQL(statement string) string {
	return strings.Join(strings.Fields(strings.ToLower(statement)), " ")
}

// WorkerRunBindingAuthority is the exact lease fence for one Run Binding.
type WorkerRunBindingAuthority struct {
	BindingID       string
	LeaseOwner      string
	LeaseGeneration int64
}

// ReserveWorkerRunBindingRequest atomically reserves one execution attempt.
type ReserveWorkerRunBindingRequest struct {
	BindingID             string
	TaskID                string
	WorkerSessionID       string
	TenantID              string
	ProjectID             string
	CanonicalWorktreeRoot string
	ProfileFingerprint    string
	CapabilityFingerprint string
	RequestedMode         RuntimeBindingMode
	ExecutorName          string
	SwarmScope            string
	LeaseOwner            string
	LeaseTTL              time.Duration
	ParentWorkerSessionID string
}

// RenewWorkerRunBindingLeaseRequest renews an exact unexpired lease.
type RenewWorkerRunBindingLeaseRequest struct {
	Authority WorkerRunBindingAuthority
	LeaseTTL  time.Duration
}

// TakeoverWorkerRunBindingLeaseRequest takes over one expired lease generation.
type TakeoverWorkerRunBindingLeaseRequest struct {
	BindingID               string
	NewLeaseOwner           string
	ExpectedLeaseGeneration int64
	LeaseTTL                time.Duration
}

// FinalizeWorkerRunBindingRequest terminalizes one exact active binding.
type FinalizeWorkerRunBindingRequest struct {
	Authority      WorkerRunBindingAuthority
	TerminalReason string
}

// StartWorkerRunBindingRequest attaches exact live authority before execution.
type StartWorkerRunBindingRequest struct {
	Authority                    WorkerRunBindingAuthority
	ProviderSession              *ProviderSessionIdentity
	ProviderConnectionGeneration *int64
	LiveHandle                   LiveHandleIdentity
	ExecutionID                  string
}

// ReturnWorkerRunBindingRequest records native return evidence without releasing the lease.
type ReturnWorkerRunBindingRequest struct {
	Authority WorkerRunBindingAuthority
	Process   *ProcessIdentity
}

type runtimeBindingScanner interface {
	Scan(...any) error
}

const workerSessionSelectColumns = `
	id, tenant_id, engine_name, project_id, canonical_worktree_root,
	profile_fingerprint, capability_fingerprint, requested_mode,
	provider_name, provider_session_id, provider_session_generation,
	state, active_task_id, lease_owner, lease_generation, lease_expires_at,
	parent_worker_session_id, created_at, updated_at, closed_at`

const workerRunBindingSelectColumns = `
	id, task_id, worker_session_id, tenant_id, engine_name, project_id,
	requested_mode, executor_name, provider_name, provider_session_id,
	provider_session_generation, provider_connection_generation, swarm_scope,
	swarm_handle_id, swarm_handle_generation, swarm_registry_generation,
	execution_id, process_pid, process_start_fingerprint, process_tree_id,
	state, lease_owner, lease_generation, lease_expires_at,
	reconciliation_classification, terminal_reason, created_at, started_at,
	returned_at, terminal_at, updated_at`

func validateRuntimeBindingText(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("loom runtime bindings: %s must be nonblank and canonical", name)
	}
	return nil
}

func validateCanonicalWorktreeRoot(root string) error {
	if err := validateRuntimeBindingText("canonical worktree root", root); err != nil {
		return err
	}
	if strings.Contains(root, `\`) {
		return fmt.Errorf("loom runtime bindings: canonical worktree root must use forward slashes")
	}

	var tail string
	switch {
	case root == "/":
		return nil
	case len(root) == 3 && root[0] >= 'A' && root[0] <= 'Z' && root[1:] == ":/":
		return nil
	case strings.HasPrefix(root, "//"):
		tail = root[2:]
		parts := strings.Split(tail, "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("loom runtime bindings: canonical UNC root requires server and share")
		}
	case len(root) >= 3 && root[0] >= 'A' && root[0] <= 'Z' && root[1] == ':' && root[2] == '/':
		tail = root[3:]
	case strings.HasPrefix(root, "/"):
		tail = root[1:]
	default:
		return fmt.Errorf("loom runtime bindings: canonical worktree root must be absolute")
	}
	if tail == "" || strings.HasSuffix(root, "/") {
		return fmt.Errorf("loom runtime bindings: canonical worktree root has a trailing separator")
	}
	for _, segment := range strings.Split(tail, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("loom runtime bindings: canonical worktree root is not lexically clean")
		}
	}
	return nil
}

func validateRuntimeBindingMode(mode RuntimeBindingMode) error {
	switch mode {
	case RuntimeBindingModeStateless, RuntimeBindingModeNew, RuntimeBindingModeExactResume, RuntimeBindingModeFork:
		return nil
	default:
		return fmt.Errorf("loom runtime bindings: unknown requested mode %q", mode)
	}
}

func validateWorkerSessionState(state WorkerSessionState) error {
	switch state {
	case WorkerSessionStateAvailable, WorkerSessionStateLeased, WorkerSessionStateClosed, WorkerSessionStateUnavailable:
		return nil
	default:
		return fmt.Errorf("loom runtime bindings: unknown worker session state %q", state)
	}
}

func validateWorkerRunBindingState(state WorkerRunBindingState) error {
	switch state {
	case WorkerRunBindingStateReserved, WorkerRunBindingStateRunning, WorkerRunBindingStateReturned, WorkerRunBindingStateCancelling, WorkerRunBindingStateTerminal:
		return nil
	default:
		return fmt.Errorf("loom runtime bindings: unknown run binding state %q", state)
	}
}

func validateProviderSessionIdentity(identity ProviderSessionIdentity) error {
	if err := validateRuntimeBindingText("provider name", identity.ProviderName); err != nil {
		return err
	}
	if err := validateRuntimeBindingText("provider session ID", identity.SessionID); err != nil {
		return err
	}
	if identity.Generation <= 0 {
		return fmt.Errorf("loom runtime bindings: provider session generation must be positive")
	}
	return nil
}

func validateLiveHandleIdentity(identity LiveHandleIdentity) error {
	if err := validateRuntimeBindingText("Swarm scope", identity.Scope); err != nil {
		return err
	}
	if err := validateRuntimeBindingText("Swarm handle ID", identity.HandleID); err != nil {
		return err
	}
	if identity.HandleGeneration <= 0 || identity.RegistryGeneration <= 0 {
		return fmt.Errorf("loom runtime bindings: live handle generations must be positive")
	}
	return nil
}

func validateProcessIdentity(identity ProcessIdentity) error {
	if identity.PID <= 0 {
		return fmt.Errorf("loom runtime bindings: process PID must be positive")
	}
	if err := validateRuntimeBindingText("process start fingerprint", identity.StartFingerprint); err != nil {
		return err
	}
	return validateRuntimeBindingText("process tree ID", identity.TreeID)
}

func validateWorkerRunBindingAuthority(authority WorkerRunBindingAuthority) error {
	if err := validateRuntimeBindingText("binding ID", authority.BindingID); err != nil {
		return err
	}
	if err := validateRuntimeBindingText("lease owner", authority.LeaseOwner); err != nil {
		return err
	}
	if authority.LeaseGeneration <= 0 {
		return fmt.Errorf("loom runtime bindings: lease generation must be positive")
	}
	return nil
}

func validateReserveWorkerRunBindingRequest(request ReserveWorkerRunBindingRequest) error {
	for _, field := range []struct {
		name, value string
	}{
		{"binding ID", request.BindingID},
		{"task ID", request.TaskID},
		{"tenant ID", request.TenantID},
		{"project ID", request.ProjectID},
		{"profile fingerprint", request.ProfileFingerprint},
		{"capability fingerprint", request.CapabilityFingerprint},
		{"executor name", request.ExecutorName},
		{"Swarm scope", request.SwarmScope},
		{"lease owner", request.LeaseOwner},
	} {
		if err := validateRuntimeBindingText(field.name, field.value); err != nil {
			return err
		}
	}
	if err := validateCanonicalWorktreeRoot(request.CanonicalWorktreeRoot); err != nil {
		return err
	}
	if err := validateRuntimeBindingMode(request.RequestedMode); err != nil {
		return err
	}
	if request.LeaseTTL <= 0 {
		return fmt.Errorf("loom runtime bindings: lease TTL must be positive")
	}
	switch request.RequestedMode {
	case RuntimeBindingModeStateless:
		if request.WorkerSessionID != "" || request.ParentWorkerSessionID != "" {
			return fmt.Errorf("loom runtime bindings: stateless reserve must not include a worker session")
		}
	case RuntimeBindingModeNew, RuntimeBindingModeExactResume:
		if err := validateRuntimeBindingText("worker session ID", request.WorkerSessionID); err != nil {
			return err
		}
		if request.ParentWorkerSessionID != "" {
			return fmt.Errorf("loom runtime bindings: %s reserve must not include a parent session", request.RequestedMode)
		}
	case RuntimeBindingModeFork:
		if err := validateRuntimeBindingText("worker session ID", request.WorkerSessionID); err != nil {
			return err
		}
		if err := validateRuntimeBindingText("parent worker session ID", request.ParentWorkerSessionID); err != nil {
			return err
		}
		if request.WorkerSessionID == request.ParentWorkerSessionID {
			return fmt.Errorf("loom runtime bindings: fork child must differ from parent")
		}
	}
	return nil
}

func (s *TaskStore) runtimeBindingNow() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func runtimeBindingTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseRuntimeBindingTimestamp(name, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("loom runtime bindings: parse %s: %w", name, err)
	}
	return parsed.UTC(), nil
}

func runtimeBindingOptionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func runtimeBindingOptionalInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func rollbackRuntimeBinding(tx *authorityTransaction, cause error) error {
	if tx == nil {
		return cause
	}
	if rollbackErr := tx.rollback(); rollbackErr != nil {
		return fmt.Errorf("%v; rollback: %w", cause, rollbackErr)
	}
	return cause
}

func runtimeBindingConflict(tx *authorityTransaction, detail string) error {
	return rollbackRuntimeBinding(tx, fmt.Errorf("%w: %s", ErrAuthorityConflict, detail))
}

func runtimeBindingWriteError(tx *authorityTransaction, operation string, err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "constraint") || strings.Contains(lower, "unique") {
		return runtimeBindingConflict(tx, operation)
	}
	return rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings %s: %w", operation, err))
}

func scanWorkerSession(scanner runtimeBindingScanner) (*WorkerSessionRecord, error) {
	var record WorkerSessionRecord
	var mode, state string
	var providerName, providerSessionID sql.NullString
	var providerGeneration sql.NullInt64
	var activeTaskID, leaseOwner, leaseExpiresAt, parentID sql.NullString
	var createdAt, updatedAt string
	var closedAt sql.NullString
	if err := scanner.Scan(
		&record.ID, &record.TenantID, &record.EngineName, &record.ProjectID,
		&record.CanonicalWorktreeRoot, &record.ProfileFingerprint, &record.CapabilityFingerprint,
		&mode, &providerName, &providerSessionID, &providerGeneration, &state,
		&activeTaskID, &leaseOwner, &record.LeaseGeneration, &leaseExpiresAt,
		&parentID, &createdAt, &updatedAt, &closedAt,
	); err != nil {
		return nil, err
	}
	record.RequestedMode = RuntimeBindingMode(mode)
	record.State = WorkerSessionState(state)
	if err := validateRuntimeBindingMode(record.RequestedMode); err != nil || record.RequestedMode == RuntimeBindingModeStateless {
		if err == nil {
			err = fmt.Errorf("loom runtime bindings: worker session cannot be stateless")
		}
		return nil, err
	}
	if err := validateWorkerSessionState(record.State); err != nil {
		return nil, err
	}
	providerFields := 0
	for _, present := range []bool{providerName.Valid, providerSessionID.Valid, providerGeneration.Valid} {
		if present {
			providerFields++
		}
	}
	if providerFields != 0 && providerFields != 3 {
		return nil, fmt.Errorf("loom runtime bindings: partial provider session identity")
	}
	if providerFields == 3 {
		identity := ProviderSessionIdentity{ProviderName: providerName.String, SessionID: providerSessionID.String, Generation: providerGeneration.Int64}
		if err := validateProviderSessionIdentity(identity); err != nil {
			return nil, err
		}
		record.ProviderSession = &identity
	}
	record.ActiveTaskID = runtimeBindingOptionalString(activeTaskID)
	record.LeaseOwner = runtimeBindingOptionalString(leaseOwner)
	if leaseExpiresAt.Valid {
		parsed, err := parseRuntimeBindingTimestamp("worker session lease expiry", leaseExpiresAt.String)
		if err != nil {
			return nil, err
		}
		record.LeaseExpiresAt = &parsed
	}
	record.ParentWorkerSessionID = runtimeBindingOptionalString(parentID)
	created, err := parseRuntimeBindingTimestamp("worker session created_at", createdAt)
	if err != nil {
		return nil, err
	}
	updated, err := parseRuntimeBindingTimestamp("worker session updated_at", updatedAt)
	if err != nil {
		return nil, err
	}
	record.CreatedAt, record.UpdatedAt = created, updated
	if closedAt.Valid {
		parsed, err := parseRuntimeBindingTimestamp("worker session closed_at", closedAt.String)
		if err != nil {
			return nil, err
		}
		record.ClosedAt = &parsed
	}
	if record.RequestedMode == RuntimeBindingModeFork {
		if record.ParentWorkerSessionID == nil {
			return nil, fmt.Errorf("loom runtime bindings: fork session missing parent")
		}
	} else if record.ParentWorkerSessionID != nil {
		return nil, fmt.Errorf("loom runtime bindings: non-fork session has parent")
	}
	if record.State == WorkerSessionStateLeased {
		if record.ActiveTaskID == nil || record.LeaseOwner == nil || record.LeaseExpiresAt == nil {
			return nil, fmt.Errorf("loom runtime bindings: leased worker session has incomplete authority")
		}
	} else if record.ActiveTaskID != nil || record.LeaseOwner != nil || record.LeaseExpiresAt != nil {
		return nil, fmt.Errorf("loom runtime bindings: inactive worker session retains lease authority")
	}
	if record.State == WorkerSessionStateClosed {
		if record.ClosedAt == nil {
			return nil, fmt.Errorf("loom runtime bindings: closed worker session missing closed_at")
		}
	} else if record.ClosedAt != nil {
		return nil, fmt.Errorf("loom runtime bindings: open worker session has closed_at")
	}
	return &record, nil
}

func scanWorkerRunBinding(scanner runtimeBindingScanner) (*WorkerRunBindingRecord, error) {
	var record WorkerRunBindingRecord
	var mode, state string
	var workerSessionID sql.NullString
	var providerName, providerSessionID sql.NullString
	var providerGeneration, connectionGeneration sql.NullInt64
	var handleID sql.NullString
	var handleGeneration, registryGeneration sql.NullInt64
	var executionID sql.NullString
	var processPID sql.NullInt64
	var processStart, processTree sql.NullString
	var leaseOwner, leaseExpiresAt sql.NullString
	var reconciliation, terminalReason sql.NullString
	var createdAt, updatedAt string
	var startedAt, returnedAt, terminalAt sql.NullString
	if err := scanner.Scan(
		&record.ID, &record.TaskID, &workerSessionID, &record.TenantID, &record.EngineName,
		&record.ProjectID, &mode, &record.ExecutorName, &providerName, &providerSessionID,
		&providerGeneration, &connectionGeneration, &record.SwarmScope, &handleID,
		&handleGeneration, &registryGeneration, &executionID, &processPID, &processStart,
		&processTree, &state, &leaseOwner, &record.LeaseGeneration, &leaseExpiresAt,
		&reconciliation, &terminalReason, &createdAt, &startedAt, &returnedAt, &terminalAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	record.RequestedMode = RuntimeBindingMode(mode)
	record.State = WorkerRunBindingState(state)
	if err := validateRuntimeBindingMode(record.RequestedMode); err != nil {
		return nil, err
	}
	if err := validateWorkerRunBindingState(record.State); err != nil {
		return nil, err
	}
	record.WorkerSessionID = runtimeBindingOptionalString(workerSessionID)
	providerFields := 0
	for _, present := range []bool{providerName.Valid, providerSessionID.Valid, providerGeneration.Valid} {
		if present {
			providerFields++
		}
	}
	if providerFields != 0 && providerFields != 3 {
		return nil, fmt.Errorf("loom runtime bindings: partial run provider session identity")
	}
	if providerFields == 3 {
		identity := ProviderSessionIdentity{ProviderName: providerName.String, SessionID: providerSessionID.String, Generation: providerGeneration.Int64}
		if err := validateProviderSessionIdentity(identity); err != nil {
			return nil, err
		}
		record.ProviderSession = &identity
	}
	if connectionGeneration.Valid {
		if connectionGeneration.Int64 <= 0 || record.ProviderSession == nil {
			return nil, fmt.Errorf("loom runtime bindings: invalid provider connection generation")
		}
		record.ProviderConnectionGeneration = runtimeBindingOptionalInt64(connectionGeneration)
	}
	liveFields := 0
	for _, present := range []bool{handleID.Valid, handleGeneration.Valid, registryGeneration.Valid} {
		if present {
			liveFields++
		}
	}
	if liveFields != 0 && liveFields != 3 {
		return nil, fmt.Errorf("loom runtime bindings: partial live handle identity")
	}
	if liveFields == 3 {
		identity := LiveHandleIdentity{Scope: record.SwarmScope, HandleID: handleID.String, HandleGeneration: handleGeneration.Int64, RegistryGeneration: registryGeneration.Int64}
		if err := validateLiveHandleIdentity(identity); err != nil {
			return nil, err
		}
		record.LiveHandle = &identity
	}
	record.ExecutionID = runtimeBindingOptionalString(executionID)
	processFields := 0
	for _, present := range []bool{processPID.Valid, processStart.Valid, processTree.Valid} {
		if present {
			processFields++
		}
	}
	if processFields != 0 && processFields != 3 {
		return nil, fmt.Errorf("loom runtime bindings: partial process identity")
	}
	if processFields == 3 {
		identity := ProcessIdentity{PID: processPID.Int64, StartFingerprint: processStart.String, TreeID: processTree.String}
		if err := validateProcessIdentity(identity); err != nil {
			return nil, err
		}
		record.Process = &identity
	}
	record.LeaseOwner = runtimeBindingOptionalString(leaseOwner)
	if leaseExpiresAt.Valid {
		parsed, err := parseRuntimeBindingTimestamp("run binding lease expiry", leaseExpiresAt.String)
		if err != nil {
			return nil, err
		}
		record.LeaseExpiresAt = &parsed
	}
	record.ReconciliationClassification = runtimeBindingOptionalString(reconciliation)
	record.TerminalReason = runtimeBindingOptionalString(terminalReason)
	created, err := parseRuntimeBindingTimestamp("run binding created_at", createdAt)
	if err != nil {
		return nil, err
	}
	updated, err := parseRuntimeBindingTimestamp("run binding updated_at", updatedAt)
	if err != nil {
		return nil, err
	}
	record.CreatedAt, record.UpdatedAt = created, updated
	for name, source := range map[string]sql.NullString{"started_at": startedAt, "returned_at": returnedAt, "terminal_at": terminalAt} {
		if !source.Valid {
			continue
		}
		parsed, parseErr := parseRuntimeBindingTimestamp("run binding "+name, source.String)
		if parseErr != nil {
			return nil, parseErr
		}
		switch name {
		case "started_at":
			record.StartedAt = &parsed
		case "returned_at":
			record.ReturnedAt = &parsed
		case "terminal_at":
			record.TerminalAt = &parsed
		}
	}
	if record.RequestedMode == RuntimeBindingModeStateless {
		if record.WorkerSessionID != nil {
			return nil, fmt.Errorf("loom runtime bindings: stateless binding has worker session")
		}
	} else if record.WorkerSessionID == nil {
		return nil, fmt.Errorf("loom runtime bindings: session binding lacks worker session")
	}
	if record.State == WorkerRunBindingStateTerminal {
		if record.LeaseOwner != nil || record.LeaseExpiresAt != nil || record.TerminalAt == nil {
			return nil, fmt.Errorf("loom runtime bindings: terminal binding has invalid authority")
		}
	} else if record.LeaseOwner == nil || record.LeaseExpiresAt == nil || record.TerminalAt != nil {
		return nil, fmt.Errorf("loom runtime bindings: active binding has incomplete authority")
	}
	return &record, nil
}

// GetWorkerSession returns one exact durable Worker Session record.
func (s *TaskStore) GetWorkerSession(ctx context.Context, id string) (*WorkerSessionRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("loom runtime bindings: store unavailable")
	}
	if err := validateRuntimeBindingText("worker session ID", id); err != nil {
		return nil, err
	}
	record, err := scanWorkerSession(s.db.QueryRowContext(ctx, `SELECT `+workerSessionSelectColumns+` FROM worker_sessions WHERE id=?`, id))
	if err != nil {
		return nil, fmt.Errorf("loom runtime bindings get worker session %q: %w", id, err)
	}
	return record, nil
}

// GetWorkerRunBinding returns one exact durable Run Binding record.
func (s *TaskStore) GetWorkerRunBinding(ctx context.Context, id string) (*WorkerRunBindingRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("loom runtime bindings: store unavailable")
	}
	if err := validateRuntimeBindingText("binding ID", id); err != nil {
		return nil, err
	}
	record, err := scanWorkerRunBinding(s.db.QueryRowContext(ctx, `SELECT `+workerRunBindingSelectColumns+` FROM worker_run_bindings WHERE id=?`, id))
	if err != nil {
		return nil, fmt.Errorf("loom runtime bindings get run binding %q: %w", id, err)
	}
	return record, nil
}

func loadWorkerSessionForUpdate(tx *authorityTransaction, id string) (*WorkerSessionRecord, error) {
	return scanWorkerSession(tx.conn.QueryRowContext(tx.ctx, `SELECT `+workerSessionSelectColumns+` FROM worker_sessions WHERE id=?`, id))
}

func loadWorkerRunBindingForUpdate(tx *authorityTransaction, id string) (*WorkerRunBindingRecord, error) {
	return scanWorkerRunBinding(tx.conn.QueryRowContext(tx.ctx, `SELECT `+workerRunBindingSelectColumns+` FROM worker_run_bindings WHERE id=?`, id))
}

// ReserveWorkerRunBinding atomically reserves one Worker Session turn and Run Binding.
func (s *TaskStore) ReserveWorkerRunBinding(ctx context.Context, request ReserveWorkerRunBindingRequest) (WorkerRunBindingAuthority, error) {
	var zero WorkerRunBindingAuthority
	if s == nil || s.db == nil {
		return zero, fmt.Errorf("loom runtime bindings: store unavailable")
	}
	if err := validateReserveWorkerRunBindingRequest(request); err != nil {
		return zero, err
	}
	if err := validateRuntimeBindingText("engine name", s.engineName); err != nil {
		return zero, err
	}
	now := s.runtimeBindingNow()
	expiresAt := now.Add(request.LeaseTTL)
	nowText, expiryText := runtimeBindingTimestamp(now), runtimeBindingTimestamp(expiresAt)
	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return zero, fmt.Errorf("loom runtime bindings reserve begin: %w", err)
	}

	var taskStatus, taskTenant, taskProject, taskEngine string
	err = tx.conn.QueryRowContext(tx.ctx, `SELECT status, tenant_id, project_id, engine_name FROM tasks WHERE id=?`, request.TaskID).Scan(&taskStatus, &taskTenant, &taskProject, &taskEngine)
	if err == sql.ErrNoRows {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings: task %q not found", request.TaskID))
	}
	if err != nil {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings load task: %w", err))
	}
	if taskTenant != request.TenantID || taskProject != request.ProjectID || taskEngine != s.engineName || TaskStatus(taskStatus).IsTerminal() {
		return zero, runtimeBindingConflict(tx, "task ownership or lifecycle changed")
	}

	var exists int
	err = tx.conn.QueryRowContext(tx.ctx, `SELECT 1 FROM worker_run_bindings WHERE id=?`, request.BindingID).Scan(&exists)
	if err == nil {
		return zero, runtimeBindingConflict(tx, "binding ID already exists")
	}
	if err != sql.ErrNoRows {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings inspect binding ID: %w", err))
	}

	leaseGeneration := int64(1)
	var workerSessionID any
	var providerName, providerSessionID any
	var providerGeneration any
	if request.RequestedMode != RuntimeBindingModeStateless {
		workerSessionID = request.WorkerSessionID
	}

	switch request.RequestedMode {
	case RuntimeBindingModeStateless:
		// The Run Binding itself owns the stateless attempt lease.
	case RuntimeBindingModeNew:
		err = tx.conn.QueryRowContext(tx.ctx, `SELECT 1 FROM worker_sessions WHERE id=?`, request.WorkerSessionID).Scan(&exists)
		if err == nil {
			return zero, runtimeBindingConflict(tx, "worker session ID already exists")
		}
		if err != sql.ErrNoRows {
			return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings inspect new session ID: %w", err))
		}
		_, err = tx.conn.ExecContext(tx.ctx, `
			INSERT INTO worker_sessions (
				id, tenant_id, engine_name, project_id, canonical_worktree_root,
				profile_fingerprint, capability_fingerprint, requested_mode, state,
				active_task_id, lease_owner, lease_generation, lease_expires_at,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, request.WorkerSessionID, request.TenantID, s.engineName, request.ProjectID,
			request.CanonicalWorktreeRoot, request.ProfileFingerprint, request.CapabilityFingerprint,
			RuntimeBindingModeNew, WorkerSessionStateLeased, request.TaskID, request.LeaseOwner,
			leaseGeneration, expiryText, nowText, nowText)
		if err != nil {
			return zero, runtimeBindingWriteError(tx, "insert new worker session", err)
		}
	case RuntimeBindingModeExactResume:
		session, loadErr := loadWorkerSessionForUpdate(tx, request.WorkerSessionID)
		if loadErr == sql.ErrNoRows {
			return zero, runtimeBindingConflict(tx, "exact worker session is absent")
		}
		if loadErr != nil {
			return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings load exact worker session: %w", loadErr))
		}
		if session.State != WorkerSessionStateAvailable || session.ActiveTaskID != nil || session.LeaseOwner != nil || session.LeaseExpiresAt != nil ||
			session.TenantID != request.TenantID || session.EngineName != s.engineName || session.ProjectID != request.ProjectID ||
			session.CanonicalWorktreeRoot != request.CanonicalWorktreeRoot || session.ProfileFingerprint != request.ProfileFingerprint ||
			session.CapabilityFingerprint != request.CapabilityFingerprint {
			return zero, runtimeBindingConflict(tx, "exact worker session identity is unavailable or mismatched")
		}
		if session.LeaseGeneration == 1<<63-1 {
			return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings: worker session lease generation exhausted"))
		}
		leaseGeneration = session.LeaseGeneration + 1
		result, updateErr := tx.conn.ExecContext(tx.ctx, `
			UPDATE worker_sessions
			SET state=?, active_task_id=?, lease_owner=?, lease_generation=?, lease_expires_at=?, updated_at=?
			WHERE id=? AND state=? AND lease_generation=?
		`, WorkerSessionStateLeased, request.TaskID, request.LeaseOwner, leaseGeneration,
			expiryText, nowText, request.WorkerSessionID, WorkerSessionStateAvailable, session.LeaseGeneration)
		if updateErr != nil {
			return zero, runtimeBindingWriteError(tx, "lease exact worker session", updateErr)
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings count exact session update: %w", affectedErr))
		}
		if affected != 1 {
			return zero, runtimeBindingConflict(tx, "exact worker session lease lost")
		}
		if session.ProviderSession != nil {
			providerName = session.ProviderSession.ProviderName
			providerSessionID = session.ProviderSession.SessionID
			providerGeneration = session.ProviderSession.Generation
		}
	case RuntimeBindingModeFork:
		parent, loadErr := loadWorkerSessionForUpdate(tx, request.ParentWorkerSessionID)
		if loadErr == sql.ErrNoRows {
			return zero, runtimeBindingConflict(tx, "fork parent worker session is absent")
		}
		if loadErr != nil {
			return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings load fork parent: %w", loadErr))
		}
		if parent.State != WorkerSessionStateAvailable || parent.ActiveTaskID != nil || parent.LeaseOwner != nil || parent.LeaseExpiresAt != nil ||
			parent.TenantID != request.TenantID || parent.EngineName != s.engineName || parent.ProjectID != request.ProjectID ||
			parent.CanonicalWorktreeRoot != request.CanonicalWorktreeRoot || parent.ProfileFingerprint != request.ProfileFingerprint ||
			parent.CapabilityFingerprint != request.CapabilityFingerprint {
			return zero, runtimeBindingConflict(tx, "fork parent identity is unavailable or mismatched")
		}
		err = tx.conn.QueryRowContext(tx.ctx, `SELECT 1 FROM worker_sessions WHERE id=?`, request.WorkerSessionID).Scan(&exists)
		if err == nil {
			return zero, runtimeBindingConflict(tx, "fork child worker session already exists")
		}
		if err != sql.ErrNoRows {
			return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings inspect fork child: %w", err))
		}
		_, err = tx.conn.ExecContext(tx.ctx, `
			INSERT INTO worker_sessions (
				id, tenant_id, engine_name, project_id, canonical_worktree_root,
				profile_fingerprint, capability_fingerprint, requested_mode, state,
				active_task_id, lease_owner, lease_generation, lease_expires_at,
				parent_worker_session_id, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, request.WorkerSessionID, request.TenantID, s.engineName, request.ProjectID,
			request.CanonicalWorktreeRoot, request.ProfileFingerprint, request.CapabilityFingerprint,
			RuntimeBindingModeFork, WorkerSessionStateLeased, request.TaskID, request.LeaseOwner,
			leaseGeneration, expiryText, request.ParentWorkerSessionID, nowText, nowText)
		if err != nil {
			return zero, runtimeBindingWriteError(tx, "insert fork worker session", err)
		}
	}

	_, err = tx.conn.ExecContext(tx.ctx, `
		INSERT INTO worker_run_bindings (
			id, task_id, worker_session_id, tenant_id, engine_name, project_id,
			requested_mode, executor_name, provider_name, provider_session_id,
			provider_session_generation, swarm_scope, state, lease_owner,
			lease_generation, lease_expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, request.BindingID, request.TaskID, workerSessionID, request.TenantID, s.engineName,
		request.ProjectID, request.RequestedMode, request.ExecutorName, providerName,
		providerSessionID, providerGeneration, request.SwarmScope, WorkerRunBindingStateReserved,
		request.LeaseOwner, leaseGeneration, expiryText, nowText, nowText)
	if err != nil {
		return zero, runtimeBindingWriteError(tx, "insert run binding", err)
	}
	if err := tx.commit(); err != nil {
		return zero, fmt.Errorf("loom runtime bindings reserve commit: %w", err)
	}
	return WorkerRunBindingAuthority{BindingID: request.BindingID, LeaseOwner: request.LeaseOwner, LeaseGeneration: leaseGeneration}, nil
}

func exactActiveRuntimeBinding(binding *WorkerRunBindingRecord, authority WorkerRunBindingAuthority) bool {
	return binding != nil && binding.State != WorkerRunBindingStateTerminal &&
		binding.LeaseOwner != nil && *binding.LeaseOwner == authority.LeaseOwner &&
		binding.LeaseGeneration == authority.LeaseGeneration && binding.LeaseExpiresAt != nil
}

// RenewWorkerRunBindingLease renews one exact unexpired binding authority.
func (s *TaskStore) RenewWorkerRunBindingLease(ctx context.Context, request RenewWorkerRunBindingLeaseRequest) (WorkerRunBindingAuthority, error) {
	var zero WorkerRunBindingAuthority
	if s == nil || s.db == nil {
		return zero, fmt.Errorf("loom runtime bindings: store unavailable")
	}
	if err := validateWorkerRunBindingAuthority(request.Authority); err != nil {
		return zero, err
	}
	if request.LeaseTTL <= 0 {
		return zero, fmt.Errorf("loom runtime bindings: lease TTL must be positive")
	}
	now := s.runtimeBindingNow()
	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return zero, fmt.Errorf("loom runtime bindings renew begin: %w", err)
	}
	binding, err := loadWorkerRunBindingForUpdate(tx, request.Authority.BindingID)
	if err == sql.ErrNoRows {
		return zero, runtimeBindingConflict(tx, "run binding is absent")
	}
	if err != nil {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings load renewal binding: %w", err))
	}
	if !exactActiveRuntimeBinding(binding, request.Authority) || !now.Before(*binding.LeaseExpiresAt) {
		return zero, runtimeBindingConflict(tx, "run binding renewal authority is stale or expired")
	}
	newExpiry := now.Add(request.LeaseTTL)
	nowText, expiryText := runtimeBindingTimestamp(now), runtimeBindingTimestamp(newExpiry)
	result, err := tx.conn.ExecContext(tx.ctx, `
		UPDATE worker_run_bindings SET lease_expires_at=?, updated_at=?
		WHERE id=? AND state<>? AND lease_owner=? AND lease_generation=?
	`, expiryText, nowText, request.Authority.BindingID, WorkerRunBindingStateTerminal,
		request.Authority.LeaseOwner, request.Authority.LeaseGeneration)
	if err != nil {
		return zero, runtimeBindingWriteError(tx, "renew run binding", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings count renewal: %w", err))
	}
	if affected != 1 {
		return zero, runtimeBindingConflict(tx, "run binding renewal lost")
	}
	if binding.WorkerSessionID != nil {
		result, err = tx.conn.ExecContext(tx.ctx, `
			UPDATE worker_sessions SET lease_expires_at=?, updated_at=?
			WHERE id=? AND state=? AND active_task_id=? AND lease_owner=? AND lease_generation=?
		`, expiryText, nowText, *binding.WorkerSessionID, WorkerSessionStateLeased,
			binding.TaskID, request.Authority.LeaseOwner, request.Authority.LeaseGeneration)
		if err != nil {
			return zero, runtimeBindingWriteError(tx, "renew worker session", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings count session renewal: %w", err))
		}
		if affected != 1 {
			return zero, runtimeBindingConflict(tx, "worker session renewal lost")
		}
	}
	if err := tx.commit(); err != nil {
		return zero, fmt.Errorf("loom runtime bindings renew commit: %w", err)
	}
	return request.Authority, nil
}

// TakeoverWorkerRunBindingLease acquires one expired lease at the next generation.
func (s *TaskStore) TakeoverWorkerRunBindingLease(ctx context.Context, request TakeoverWorkerRunBindingLeaseRequest) (WorkerRunBindingAuthority, error) {
	var zero WorkerRunBindingAuthority
	if s == nil || s.db == nil {
		return zero, fmt.Errorf("loom runtime bindings: store unavailable")
	}
	if err := validateRuntimeBindingText("binding ID", request.BindingID); err != nil {
		return zero, err
	}
	if err := validateRuntimeBindingText("new lease owner", request.NewLeaseOwner); err != nil {
		return zero, err
	}
	if request.ExpectedLeaseGeneration <= 0 || request.LeaseTTL <= 0 {
		return zero, fmt.Errorf("loom runtime bindings: takeover generation and TTL must be positive")
	}
	now := s.runtimeBindingNow()
	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return zero, fmt.Errorf("loom runtime bindings takeover begin: %w", err)
	}
	binding, err := loadWorkerRunBindingForUpdate(tx, request.BindingID)
	if err == sql.ErrNoRows {
		return zero, runtimeBindingConflict(tx, "run binding is absent")
	}
	if err != nil {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings load takeover binding: %w", err))
	}
	if binding.State == WorkerRunBindingStateTerminal || binding.LeaseOwner == nil || binding.LeaseExpiresAt == nil ||
		binding.LeaseGeneration != request.ExpectedLeaseGeneration || now.Before(*binding.LeaseExpiresAt) {
		return zero, runtimeBindingConflict(tx, "run binding takeover authority is stale or unexpired")
	}
	if binding.LeaseGeneration == 1<<63-1 {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings: lease generation exhausted"))
	}
	newGeneration := binding.LeaseGeneration + 1
	newExpiry := now.Add(request.LeaseTTL)
	nowText, expiryText := runtimeBindingTimestamp(now), runtimeBindingTimestamp(newExpiry)
	result, err := tx.conn.ExecContext(tx.ctx, `
		UPDATE worker_run_bindings
		SET lease_owner=?, lease_generation=?, lease_expires_at=?, updated_at=?
		WHERE id=? AND state<>? AND lease_owner=? AND lease_generation=?
	`, request.NewLeaseOwner, newGeneration, expiryText, nowText, request.BindingID,
		WorkerRunBindingStateTerminal, *binding.LeaseOwner, binding.LeaseGeneration)
	if err != nil {
		return zero, runtimeBindingWriteError(tx, "take over run binding", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings count takeover: %w", err))
	}
	if affected != 1 {
		return zero, runtimeBindingConflict(tx, "run binding takeover lost")
	}
	if binding.WorkerSessionID != nil {
		result, err = tx.conn.ExecContext(tx.ctx, `
			UPDATE worker_sessions
			SET lease_owner=?, lease_generation=?, lease_expires_at=?, updated_at=?
			WHERE id=? AND state=? AND active_task_id=? AND lease_owner=? AND lease_generation=?
		`, request.NewLeaseOwner, newGeneration, expiryText, nowText, *binding.WorkerSessionID,
			WorkerSessionStateLeased, binding.TaskID, *binding.LeaseOwner, binding.LeaseGeneration)
		if err != nil {
			return zero, runtimeBindingWriteError(tx, "take over worker session", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings count session takeover: %w", err))
		}
		if affected != 1 {
			return zero, runtimeBindingConflict(tx, "worker session takeover lost")
		}
	}
	authority := WorkerRunBindingAuthority{BindingID: request.BindingID, LeaseOwner: request.NewLeaseOwner, LeaseGeneration: newGeneration}
	if err := tx.commit(); err != nil {
		return zero, fmt.Errorf("loom runtime bindings takeover commit: %w", err)
	}
	return authority, nil
}

// StartWorkerRunBinding records exact live authority before provider execution.
func (s *TaskStore) StartWorkerRunBinding(ctx context.Context, request StartWorkerRunBindingRequest) (WorkerRunBindingAuthority, error) {
	var zero WorkerRunBindingAuthority
	if s == nil || s.db == nil {
		return zero, fmt.Errorf("loom runtime bindings: store unavailable")
	}
	if err := validateWorkerRunBindingAuthority(request.Authority); err != nil {
		return zero, err
	}
	if err := validateLiveHandleIdentity(request.LiveHandle); err != nil {
		return zero, err
	}
	if err := validateRuntimeBindingText("execution ID", request.ExecutionID); err != nil {
		return zero, err
	}
	if request.ProviderSession != nil {
		if err := validateProviderSessionIdentity(*request.ProviderSession); err != nil {
			return zero, err
		}
	}
	if request.ProviderConnectionGeneration != nil {
		if *request.ProviderConnectionGeneration <= 0 || request.ProviderSession == nil {
			return zero, fmt.Errorf("loom runtime bindings: provider connection generation requires provider identity")
		}
	}
	now := s.runtimeBindingNow()
	nowText := runtimeBindingTimestamp(now)
	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return zero, fmt.Errorf("loom runtime bindings start begin: %w", err)
	}
	binding, err := loadWorkerRunBindingForUpdate(tx, request.Authority.BindingID)
	if err == sql.ErrNoRows {
		return zero, runtimeBindingConflict(tx, "run binding is absent")
	}
	if err != nil {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings load start binding: %w", err))
	}
	if binding.State != WorkerRunBindingStateReserved || !exactActiveRuntimeBinding(binding, request.Authority) {
		return zero, runtimeBindingConflict(tx, "run binding start authority is stale or already used")
	}
	if binding.ProviderSession != nil && request.ProviderSession != nil && *binding.ProviderSession != *request.ProviderSession {
		return zero, runtimeBindingConflict(tx, "provider session identity changed")
	}
	effectiveProvider := request.ProviderSession
	if effectiveProvider == nil {
		effectiveProvider = binding.ProviderSession
	}
	var providerName, providerSessionID, providerGeneration any
	if effectiveProvider != nil {
		providerName = effectiveProvider.ProviderName
		providerSessionID = effectiveProvider.SessionID
		providerGeneration = effectiveProvider.Generation
	}
	var connectionGeneration any
	if request.ProviderConnectionGeneration != nil {
		connectionGeneration = *request.ProviderConnectionGeneration
	}
	result, err := tx.conn.ExecContext(tx.ctx, `
		UPDATE worker_run_bindings
		SET provider_name=?, provider_session_id=?, provider_session_generation=?,
			provider_connection_generation=?, swarm_scope=?, swarm_handle_id=?,
			swarm_handle_generation=?, swarm_registry_generation=?, execution_id=?,
			state=?, started_at=?, updated_at=?
		WHERE id=? AND state=? AND lease_owner=? AND lease_generation=?
	`, providerName, providerSessionID, providerGeneration, connectionGeneration,
		request.LiveHandle.Scope, request.LiveHandle.HandleID, request.LiveHandle.HandleGeneration,
		request.LiveHandle.RegistryGeneration, request.ExecutionID, WorkerRunBindingStateRunning,
		nowText, nowText, request.Authority.BindingID, WorkerRunBindingStateReserved,
		request.Authority.LeaseOwner, request.Authority.LeaseGeneration)
	if err != nil {
		return zero, runtimeBindingWriteError(tx, "start run binding", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings count start: %w", err))
	}
	if affected != 1 {
		return zero, runtimeBindingConflict(tx, "run binding start lost")
	}
	if binding.WorkerSessionID != nil {
		session, loadErr := loadWorkerSessionForUpdate(tx, *binding.WorkerSessionID)
		if loadErr != nil {
			return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings load started session: %w", loadErr))
		}
		if session.State != WorkerSessionStateLeased || session.ActiveTaskID == nil || *session.ActiveTaskID != binding.TaskID ||
			session.LeaseOwner == nil || *session.LeaseOwner != request.Authority.LeaseOwner || session.LeaseGeneration != request.Authority.LeaseGeneration {
			return zero, runtimeBindingConflict(tx, "worker session start authority changed")
		}
		if session.ProviderSession != nil && effectiveProvider != nil && *session.ProviderSession != *effectiveProvider {
			return zero, runtimeBindingConflict(tx, "worker session provider identity changed")
		}
		if effectiveProvider != nil {
			result, err = tx.conn.ExecContext(tx.ctx, `
				UPDATE worker_sessions
				SET provider_name=?, provider_session_id=?, provider_session_generation=?, updated_at=?
				WHERE id=? AND state=? AND active_task_id=? AND lease_owner=? AND lease_generation=?
			`, effectiveProvider.ProviderName, effectiveProvider.SessionID, effectiveProvider.Generation,
				nowText, *binding.WorkerSessionID, WorkerSessionStateLeased, binding.TaskID,
				request.Authority.LeaseOwner, request.Authority.LeaseGeneration)
			if err != nil {
				return zero, runtimeBindingWriteError(tx, "bind provider session", err)
			}
			affected, err = result.RowsAffected()
			if err != nil {
				return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings count provider bind: %w", err))
			}
			if affected != 1 {
				return zero, runtimeBindingConflict(tx, "worker session provider bind lost")
			}
		}
	}
	if err := tx.commit(); err != nil {
		return zero, fmt.Errorf("loom runtime bindings start commit: %w", err)
	}
	return request.Authority, nil
}

// RecordWorkerRunBindingReturned records native return while retaining lease authority.
func (s *TaskStore) RecordWorkerRunBindingReturned(ctx context.Context, request ReturnWorkerRunBindingRequest) (WorkerRunBindingAuthority, error) {
	var zero WorkerRunBindingAuthority
	if s == nil || s.db == nil {
		return zero, fmt.Errorf("loom runtime bindings: store unavailable")
	}
	if err := validateWorkerRunBindingAuthority(request.Authority); err != nil {
		return zero, err
	}
	if request.Process != nil {
		if err := validateProcessIdentity(*request.Process); err != nil {
			return zero, err
		}
	}
	nowText := runtimeBindingTimestamp(s.runtimeBindingNow())
	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return zero, fmt.Errorf("loom runtime bindings return begin: %w", err)
	}
	binding, err := loadWorkerRunBindingForUpdate(tx, request.Authority.BindingID)
	if err == sql.ErrNoRows {
		return zero, runtimeBindingConflict(tx, "run binding is absent")
	}
	if err != nil {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings load returned binding: %w", err))
	}
	if binding.State != WorkerRunBindingStateRunning || !exactActiveRuntimeBinding(binding, request.Authority) {
		return zero, runtimeBindingConflict(tx, "run binding return authority is stale or already used")
	}
	var processPID, processStart, processTree any
	if request.Process != nil {
		processPID = request.Process.PID
		processStart = request.Process.StartFingerprint
		processTree = request.Process.TreeID
	}
	result, err := tx.conn.ExecContext(tx.ctx, `
		UPDATE worker_run_bindings
		SET process_pid=?, process_start_fingerprint=?, process_tree_id=?, state=?, returned_at=?, updated_at=?
		WHERE id=? AND state=? AND lease_owner=? AND lease_generation=?
	`, processPID, processStart, processTree, WorkerRunBindingStateReturned, nowText, nowText,
		request.Authority.BindingID, WorkerRunBindingStateRunning, request.Authority.LeaseOwner,
		request.Authority.LeaseGeneration)
	if err != nil {
		return zero, runtimeBindingWriteError(tx, "record run return", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings count return: %w", err))
	}
	if affected != 1 {
		return zero, runtimeBindingConflict(tx, "run binding return lost")
	}
	if err := tx.commit(); err != nil {
		return zero, fmt.Errorf("loom runtime bindings return commit: %w", err)
	}
	return request.Authority, nil
}

// FinalizeWorkerRunBinding terminalizes one exact Run Binding and releases its Worker Session.
func (s *TaskStore) FinalizeWorkerRunBinding(ctx context.Context, request FinalizeWorkerRunBindingRequest) (WorkerRunBindingAuthority, error) {
	var zero WorkerRunBindingAuthority
	if s == nil || s.db == nil {
		return zero, fmt.Errorf("loom runtime bindings: store unavailable")
	}
	if err := validateWorkerRunBindingAuthority(request.Authority); err != nil {
		return zero, err
	}
	if err := validateRuntimeBindingText("terminal reason", request.TerminalReason); err != nil {
		return zero, err
	}
	nowText := runtimeBindingTimestamp(s.runtimeBindingNow())
	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return zero, fmt.Errorf("loom runtime bindings finalize begin: %w", err)
	}
	binding, err := loadWorkerRunBindingForUpdate(tx, request.Authority.BindingID)
	if err == sql.ErrNoRows {
		return zero, runtimeBindingConflict(tx, "run binding is absent")
	}
	if err != nil {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings load final binding: %w", err))
	}
	if !exactActiveRuntimeBinding(binding, request.Authority) {
		return zero, runtimeBindingConflict(tx, "run binding final authority is stale or terminal")
	}
	result, err := tx.conn.ExecContext(tx.ctx, `
		UPDATE worker_run_bindings
		SET state=?, lease_owner=NULL, lease_expires_at=NULL, terminal_reason=?, terminal_at=?, updated_at=?
		WHERE id=? AND state<>? AND lease_owner=? AND lease_generation=?
	`, WorkerRunBindingStateTerminal, request.TerminalReason, nowText, nowText,
		request.Authority.BindingID, WorkerRunBindingStateTerminal, request.Authority.LeaseOwner,
		request.Authority.LeaseGeneration)
	if err != nil {
		return zero, runtimeBindingWriteError(tx, "finalize run binding", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings count finalization: %w", err))
	}
	if affected != 1 {
		return zero, runtimeBindingConflict(tx, "run binding finalization lost")
	}
	if binding.WorkerSessionID != nil {
		result, err = tx.conn.ExecContext(tx.ctx, `
			UPDATE worker_sessions
			SET state=?, active_task_id=NULL, lease_owner=NULL, lease_expires_at=NULL, updated_at=?
			WHERE id=? AND state=? AND active_task_id=? AND lease_owner=? AND lease_generation=?
		`, WorkerSessionStateAvailable, nowText, *binding.WorkerSessionID,
			WorkerSessionStateLeased, binding.TaskID, request.Authority.LeaseOwner,
			request.Authority.LeaseGeneration)
		if err != nil {
			return zero, runtimeBindingWriteError(tx, "release worker session", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return zero, rollbackRuntimeBinding(tx, fmt.Errorf("loom runtime bindings count session release: %w", err))
		}
		if affected != 1 {
			return zero, runtimeBindingConflict(tx, "worker session release lost")
		}
	}
	if err := tx.commit(); err != nil {
		return zero, fmt.Errorf("loom runtime bindings finalize commit: %w", err)
	}
	return request.Authority, nil
}
