package loom

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// storeSecretPatterns are compiled once at init and applied to error messages
// before they reach the tasks.error column. The loom module is a standalone
// Go module and cannot import pkg/executor/redact, so patterns are inlined here.
// Pattern list MUST stay in sync with pkg/executor/redact/patterns.go (PatternVersion 2026-04-20).
// Update both when API key formats change.
// Order is load-bearing: specific sk-*-prefix patterns (project/svcacct/anthropic)
// MUST precede the generic legacy `sk-...` regex, which would otherwise swallow
// them under a wrong label.
var storeSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-proj-[A-Za-z0-9_\-]{20,}`),         // openai-key-project
	regexp.MustCompile(`sk-svcacct-[A-Za-z0-9_\-]{20,}`),      // openai-key-svcacct
	regexp.MustCompile(`sk-ant-api\d{2}-[A-Za-z0-9_\-]{20,}`), // anthropic-key
	regexp.MustCompile(`sk-[A-Za-z0-9_\-]{20,}`),              // openai-key-legacy (LAST of sk-*)
	regexp.MustCompile(`AIza[A-Za-z0-9_\-]{35,}`),             // google-ai-key
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9_\-\.=]{20,}`), // bearer-token
	regexp.MustCompile(`(?i)Authorization:\s*[^\s]{20,}`),     // auth-header
}

// redactErrorMsg scrubs known secret patterns from an error message before
// persisting it to the tasks.error column. The result column is NOT redacted.
func redactErrorMsg(s string) string {
	if s == "" {
		return s
	}
	for _, re := range storeSecretPatterns {
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

func init() {
	// Safety: the legacy TaskStore.MarkCrashed compatibility helper bulk-updates
	// dispatched/running rows via raw SQL. Production daemon recovery uses
	// LoomEngine.RecoverCrashed and per-task authority commits instead. This
	// assertion keeps the legacy helper aligned with the state machine.
	for _, from := range []TaskStatus{TaskStatusDispatched, TaskStatusRunning} {
		allowed := false
		for _, to := range validTransitions[from] {
			if to == TaskStatusFailedCrash {
				allowed = true
				break
			}
		}
		if !allowed {
			panic(fmt.Sprintf("loom store: MarkCrashed assumes %s→failed_crash is valid but state machine disagrees", from))
		}
	}
}

const createTasksTable = `
CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'pending',
    worker_type TEXT NOT NULL,
    project_id TEXT NOT NULL,
    request_id TEXT NOT NULL DEFAULT '',
    parent_task_id TEXT REFERENCES tasks(id),
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
    completed_at DATETIME,
    engine_name TEXT NOT NULL DEFAULT '',
    tenant_id TEXT NOT NULL DEFAULT '__legacy__'
);
CREATE INDEX IF NOT EXISTS idx_tasks_project_id ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
-- idx_tasks_engine_status created by migrateV3Columns AFTER engine_name column lands
-- on pre-v3 databases (AIMUX-10).
-- idx_tasks_tenant_id created by migrateV4Columns AFTER tenant_id column lands
-- on pre-AIMUX-12 databases.
`

// migrateRequestIDColumn adds the request_id column to an existing tasks table
// that was created before Phase 4a. The ALTER is silently ignored if the column
// already exists (SQLite returns "duplicate column name" error).
const migrateRequestIDColumn = `ALTER TABLE tasks ADD COLUMN request_id TEXT NOT NULL DEFAULT ''`

// migrateV2Columns lists the ALTER TABLE statements for session-durability
// Phase 1: daemon_uuid, last_seen_at, aborted_at.
// Each ALTER is run individually so a "duplicate column name" error on one
// does not block the others (idempotent by design).
var migrateV2Columns = []string{
	`ALTER TABLE tasks ADD COLUMN daemon_uuid TEXT`,
	`ALTER TABLE tasks ADD COLUMN last_seen_at TEXT`,
	`ALTER TABLE tasks ADD COLUMN aborted_at TEXT`,
}

// migrateV3Columns adds engine_name column and composite index for per-daemon
// task scoping (AIMUX-10 loom-task-scoping). Each statement is run individually;
// errors indicating the column/index already exists are silently ignored
// (idempotent migration pattern, matches migrateV2Columns).
var migrateV3Columns = []string{
	`ALTER TABLE tasks ADD COLUMN engine_name TEXT NOT NULL DEFAULT ''`,
	`CREATE INDEX IF NOT EXISTS idx_tasks_engine_status ON tasks(engine_name, status)`,
}

// migrateV4Columns adds tenant_id column and composite index for per-tenant
// task scoping (AIMUX-12 multi-tenant isolation). Each statement is run individually;
// duplicate-column and already-exists errors are silently ignored (idempotent).
// The default value '__legacy__' (LegacyTenantIDValue) ensures existing rows are
// attributed to the legacy-default tenant for single-tenant compat (ADR-011).
var migrateV4Columns = []string{
	`ALTER TABLE tasks ADD COLUMN tenant_id TEXT NOT NULL DEFAULT '__legacy__'`,
	`CREATE INDEX IF NOT EXISTS idx_tasks_tenant_id ON tasks(tenant_id, status)`,
}

// migrateV5Columns adds three live progress columns for DEF-13 / AIMUX-16 CR-005.
// They surface progress_tail / progress_lines / progress_updated_at on the
// MCP status response for Loom-managed tasks at parity with the legacy
// legacy job progress fields. Each statement is idempotent — duplicate-column errors
// are silently ignored.
//
// Reversibility: SQLite ≥ 3.35.0 supports `ALTER TABLE … DROP COLUMN`. The
// down migration is documented in MigrateV5Down (used by tests / operator
// recovery) and is the inverse of these statements.
var migrateV5Columns = []string{
	`ALTER TABLE tasks ADD COLUMN last_output_line TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE tasks ADD COLUMN progress_lines INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE tasks ADD COLUMN progress_updated_at DATETIME`,
}

// migrateV5DownColumns is the reverse of migrateV5Columns. Used by recovery
// tooling and tests to verify the v5 migration is reversible. Requires
// SQLite ≥ 3.35.0; the modernc.org/sqlite driver bundles a recent SQLite.
var migrateV5DownColumns = []string{
	`ALTER TABLE tasks DROP COLUMN progress_updated_at`,
	`ALTER TABLE tasks DROP COLUMN progress_lines`,
	`ALTER TABLE tasks DROP COLUMN last_output_line`,
}

// migrateV6Columns adds parent_task_id for AIMUX-21 sub-task tree observability.
// The index supports GetTree child lookups by parent id. Duplicate-column and
// already-exists errors are ignored by NewTaskStore, matching previous migration
// phases.
var migrateV6Columns = []string{
	`ALTER TABLE tasks ADD COLUMN parent_task_id TEXT REFERENCES tasks(id)`,
	`CREATE INDEX IF NOT EXISTS idx_tasks_parent_task_id ON tasks(parent_task_id)`,
}

// migrateV6DownColumns is the reverse of migrateV6Columns.
var migrateV6DownColumns = []string{
	`DROP INDEX IF EXISTS idx_tasks_parent_task_id`,
	`ALTER TABLE tasks DROP COLUMN parent_task_id`,
}

// migrateV7Statements adds the Loom task artifact projection table for
// AIMUX-23. The table is append-only evidence keyed by task_id and seq; it is
// never used as canonical task status.
var migrateV7Statements = []string{
	`CREATE TABLE IF NOT EXISTS task_artifacts (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		kind TEXT NOT NULL,
		event_type TEXT NOT NULL DEFAULT '',
		channel TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		payload_json TEXT NOT NULL DEFAULT '{}',
		content_length INTEGER NOT NULL DEFAULT 0,
		redacted INTEGER NOT NULL DEFAULT 0,
		truncated INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_task_artifacts_task_seq ON task_artifacts(task_id, seq)`,
}

// migrateV8Statements adds channel to pre-CR-011 artifact tables and indexes
// runtime slice lookups by task, kind, event type, channel, and cursor seq.
var migrateV8Statements = []string{
	`ALTER TABLE task_artifacts ADD COLUMN channel TEXT NOT NULL DEFAULT ''`,
	`CREATE INDEX IF NOT EXISTS idx_task_artifacts_runtime_slice ON task_artifacts(task_id, kind, event_type, channel, seq)`,
}

const migrateV9CancelRequestedAt = `ALTER TABLE tasks ADD COLUMN cancel_requested_at DATETIME`

const (
	migrateV10Version              = 10
	migrateV10BatchSize            = 256
	migrateV10UniqueIndex          = "idx_task_artifacts_task_event_seq"
	migrateV10CompatibilityTrigger = "trg_task_artifacts_event_seq_migrating"
	migrateV10SteadyStateTrigger   = "trg_task_artifacts_event_seq_steady"
)

const createLoomMigrationsTable = `
CREATE TABLE IF NOT EXISTS loom_migrations (
    version INTEGER PRIMARY KEY,
    state TEXT NOT NULL CHECK (state IN ('running','complete')),
    checkpoint_seq INTEGER NOT NULL,
    source_rows INTEGER NOT NULL,
    processed_rows INTEGER NOT NULL,
    batch_count INTEGER NOT NULL,
    started_at DATETIME NOT NULL,
    completed_at DATETIME
)`

const createV10CompatibilityTrigger = `
CREATE TRIGGER trg_task_artifacts_event_seq_migrating
AFTER INSERT ON task_artifacts
FOR EACH ROW WHEN NEW.event_seq IS NULL
BEGIN
    UPDATE task_artifacts
    SET event_seq = (
        SELECT COUNT(*)
        FROM task_artifacts AS prior
        WHERE prior.task_id = NEW.task_id
          AND prior.seq <= NEW.seq
    )
    WHERE seq = NEW.seq;
END`

const createV10SteadyStateTrigger = `
CREATE TRIGGER trg_task_artifacts_event_seq_steady
AFTER INSERT ON task_artifacts
FOR EACH ROW WHEN NEW.event_seq IS NULL
BEGIN
    UPDATE task_artifacts
    SET event_seq = COALESCE((
        SELECT MAX(prior.event_seq)
        FROM task_artifacts AS prior
        WHERE prior.task_id = NEW.task_id
          AND prior.seq <> NEW.seq
    ), 0) + 1
    WHERE seq = NEW.seq;
END`

const createPendingActionsTable = `
CREATE TABLE IF NOT EXISTS pending_actions (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id),
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    provider_request_id TEXT NOT NULL,
    connection_generation INTEGER NOT NULL,
    request_json TEXT NOT NULL,
    response_json TEXT,
    delivery_json TEXT,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    responded_at DATETIME,
    resolved_at DATETIME
)`

const pendingActionsProviderIndex = "idx_pending_actions_provider_generation"

var pendingActionsProviderIndexColumns = []string{"task_id", "provider_request_id", "connection_generation"}

const taskStorePragmaTimeout = 5 * time.Second

func sqliteBusyError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked")
}

func waitTaskStorePragmaRetry(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func taskStoreMainDatabaseIsMemory(ctx context.Context, db *sql.DB) (bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, filename string
		if err := rows.Scan(&seq, &name, &filename); err != nil {
			return false, err
		}
		if name == "main" {
			return filename == "", nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, errors.New("PRAGMA database_list has no main database")
}

func ensureTaskStoreJournalMode(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), taskStorePragmaTimeout)
	defer cancel()
	for {
		var mode string
		err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode)
		if err == nil {
			if strings.EqualFold(mode, "wal") {
				return nil
			}
			if strings.EqualFold(mode, "memory") {
				memoryBacked, inspectErr := taskStoreMainDatabaseIsMemory(ctx, db)
				if inspectErr != nil {
					return fmt.Errorf("inspect memory journal backing: %w", inspectErr)
				}
				if memoryBacked {
					// SQLite cannot enable WAL for an in-memory database. Loom's
					// shared-memory test/example stores intentionally use this mode.
					return nil
				}
			}
			err = db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode)
			if err == nil {
				if strings.EqualFold(mode, "wal") {
					return nil
				}
				return fmt.Errorf("journal mode is %q after WAL request", mode)
			}
		}
		if !sqliteBusyError(err) {
			return err
		}
		if err := waitTaskStorePragmaRetry(ctx); err != nil {
			return fmt.Errorf("waiting for WAL mode: %w", err)
		}
	}
}

func ensureTaskStoreSynchronousNormal(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), taskStorePragmaTimeout)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire synchronous connection: %w", err)
	}
	defer conn.Close()
	for {
		_, err := conn.ExecContext(ctx, "PRAGMA synchronous=NORMAL")
		if err == nil {
			var level int
			err = conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&level)
			if err == nil {
				if level != 1 {
					return fmt.Errorf("synchronous level is %d after NORMAL request", level)
				}
				return nil
			}
		}
		if !sqliteBusyError(err) {
			return err
		}
		if err := waitTaskStorePragmaRetry(ctx); err != nil {
			return fmt.Errorf("waiting to set synchronous mode: %w", err)
		}
	}
}

func migrateV9(db *sql.DB) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := beginAuthorityTransaction(ctx, db)
	if err != nil {
		return fmt.Errorf("begin v9 migration: %w", err)
	}
	defer func() {
		if tx.active {
			if rollbackErr := tx.rollback(); rollbackErr != nil {
				if err != nil {
					err = fmt.Errorf("%v; rollback v9 migration: %w", err, rollbackErr)
				} else {
					err = fmt.Errorf("rollback v9 migration: %w", rollbackErr)
				}
			}
		}
	}()

	if _, execErr := tx.conn.ExecContext(ctx, migrateV9CancelRequestedAt); execErr != nil && !strings.Contains(execErr.Error(), "duplicate column name") {
		return fmt.Errorf("add cancel_requested_at: %w", execErr)
	}
	if _, execErr := tx.conn.ExecContext(ctx, createPendingActionsTable); execErr != nil {
		return fmt.Errorf("create pending_actions: %w", execErr)
	}

	rows, err := tx.conn.QueryContext(ctx, `PRAGMA index_list('pending_actions')`)
	if err != nil {
		return fmt.Errorf("inspect pending_actions indexes: %w", err)
	}
	indexExists := false
	indexUnique := false
	indexOwned := false
	indexPartial := false
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			return fmt.Errorf("inspect pending_actions index row: %w", err)
		}
		if name == pendingActionsProviderIndex {
			indexExists = true
			indexUnique = unique == 1
			indexOwned = origin == "c"
			indexPartial = partial != 0
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("inspect pending_actions index rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close pending_actions index inspection: %w", err)
	}

	var columns []string
	if indexExists {
		rows, err = tx.conn.QueryContext(ctx, `PRAGMA index_info(`+pendingActionsProviderIndex+`)`)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", pendingActionsProviderIndex, err)
		}
		for rows.Next() {
			var seq, cid int
			var name string
			if err := rows.Scan(&seq, &cid, &name); err != nil {
				rows.Close()
				return fmt.Errorf("inspect %s row: %w", pendingActionsProviderIndex, err)
			}
			columns = append(columns, name)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("inspect %s rows: %w", pendingActionsProviderIndex, err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close %s inspection: %w", pendingActionsProviderIndex, err)
		}
	}

	indexMatches := indexExists && indexUnique && indexOwned && !indexPartial &&
		len(columns) == len(pendingActionsProviderIndexColumns)
	if indexMatches {
		for i := range columns {
			if columns[i] != pendingActionsProviderIndexColumns[i] {
				indexMatches = false
				break
			}
		}
	}
	if indexExists && !indexMatches {
		if _, err := tx.conn.ExecContext(ctx, `DROP INDEX IF EXISTS `+pendingActionsProviderIndex); err != nil {
			return fmt.Errorf("drop incompatible %s: %w", pendingActionsProviderIndex, err)
		}
	}
	if _, err := tx.conn.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS `+pendingActionsProviderIndex+
		` ON pending_actions(task_id, provider_request_id, connection_generation)`); err != nil {
		return fmt.Errorf("create %s: %w", pendingActionsProviderIndex, err)
	}
	if err := tx.commit(); err != nil {
		return fmt.Errorf("commit v9 migration: %w", err)
	}
	return nil
}

func runV10Transaction(db *sql.DB, phase string, fn func(context.Context, *sql.Conn) error) (err error) {
	ctx := context.Background()
	tx, err := beginAuthorityTransaction(ctx, db)
	if err != nil {
		return fmt.Errorf("begin v10 %s: %w", phase, err)
	}
	defer func() {
		if tx.active {
			if rollbackErr := tx.rollback(); rollbackErr != nil {
				if err != nil {
					err = fmt.Errorf("%v; rollback v10 %s: %w", err, phase, rollbackErr)
				} else {
					err = fmt.Errorf("rollback v10 %s: %w", phase, rollbackErr)
				}
			}
		}
	}()

	if err := fn(ctx, tx.conn); err != nil {
		return fmt.Errorf("v10 %s: %w", phase, err)
	}
	if err := tx.commit(); err != nil {
		return fmt.Errorf("commit v10 %s: %w", phase, err)
	}
	return nil
}

// expandV10 atomically installs every object needed to keep pre-v10 writers
// safe before releasing the first writer lock. The migration trigger derives
// task-local ordinals from immutable global row order because MAX(event_seq)
// is incomplete until the legacy NULL backlog has been backfilled.
func expandV10(db *sql.DB) (complete bool, err error) {
	err = runV10Transaction(db, "expansion", func(ctx context.Context, conn *sql.Conn) error {
		if _, execErr := conn.ExecContext(ctx, `ALTER TABLE task_artifacts ADD COLUMN event_seq INTEGER`); execErr != nil &&
			!strings.Contains(strings.ToLower(execErr.Error()), "duplicate column name") {
			return fmt.Errorf("add task_artifacts.event_seq: %w", execErr)
		}
		if _, execErr := conn.ExecContext(ctx, createLoomMigrationsTable); execErr != nil {
			return fmt.Errorf("create loom_migrations: %w", execErr)
		}

		var state string
		scanErr := conn.QueryRowContext(ctx, `SELECT state FROM loom_migrations WHERE version=?`, migrateV10Version).Scan(&state)
		switch {
		case errors.Is(scanErr, sql.ErrNoRows):
			var sourceRows int64
			if queryErr := conn.QueryRowContext(ctx,
				`SELECT count(*) FROM task_artifacts WHERE event_seq IS NULL`).Scan(&sourceRows); queryErr != nil {
				return fmt.Errorf("count v10 source rows: %w", queryErr)
			}
			startedAt := time.Now().UTC().Format(time.RFC3339Nano)
			if _, execErr := conn.ExecContext(ctx, `
				INSERT INTO loom_migrations(
					version,state,checkpoint_seq,source_rows,processed_rows,batch_count,started_at,completed_at
				) VALUES(?, 'running', 0, ?, 0, 0, ?, NULL)`,
				migrateV10Version, sourceRows, startedAt); execErr != nil {
				return fmt.Errorf("insert v10 running ledger: %w", execErr)
			}
			state = "running"
		case scanErr != nil:
			return fmt.Errorf("read v10 ledger: %w", scanErr)
		}

		switch state {
		case "complete":
			complete = true
			return nil
		case "running":
		default:
			return fmt.Errorf("invalid ledger state %q", state)
		}

		// A partial/restarted migration may have either trigger. Replacing them
		// while BEGIN IMMEDIATE owns the writer lock leaves no unguarded writer
		// interval outside this transaction.
		if _, execErr := conn.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+migrateV10SteadyStateTrigger); execErr != nil {
			return fmt.Errorf("drop premature steady-state trigger: %w", execErr)
		}
		if _, execErr := conn.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+migrateV10CompatibilityTrigger); execErr != nil {
			return fmt.Errorf("replace migration trigger: %w", execErr)
		}
		if _, execErr := conn.ExecContext(ctx, createV10CompatibilityTrigger); execErr != nil {
			return fmt.Errorf("create migration trigger: %w", execErr)
		}
		return nil
	})
	return complete, err
}

// backfillV10Batch advances at most 256 legacy rows and the durable ledger in
// one pinned transaction. Rows at or below checkpoint_seq are never revisited;
// a contradictory checkpoint therefore fails final validation instead of
// silently rewriting the accepted prefix.
func backfillV10Batch(db *sql.DB) (updated int64, err error) {
	err = runV10Transaction(db, "backfill", func(ctx context.Context, conn *sql.Conn) error {
		var state string
		var checkpoint, sourceRows, processedRows int64
		if queryErr := conn.QueryRowContext(ctx, `
			SELECT state,checkpoint_seq,source_rows,processed_rows
			FROM loom_migrations WHERE version=?`, migrateV10Version).Scan(
			&state, &checkpoint, &sourceRows, &processedRows,
		); queryErr != nil {
			return fmt.Errorf("read backfill ledger: %w", queryErr)
		}
		if state == "complete" {
			return nil
		}
		if state != "running" {
			return fmt.Errorf("invalid ledger state %q", state)
		}
		if processedRows < 0 || sourceRows < processedRows {
			return fmt.Errorf("invalid ledger counts source=%d processed=%d", sourceRows, processedRows)
		}

		var batchRows, lastSeq int64
		if queryErr := conn.QueryRowContext(ctx, `
			SELECT count(*), COALESCE(MAX(seq), 0)
			FROM (
				SELECT seq
				FROM task_artifacts
				WHERE event_seq IS NULL AND seq > ?
				ORDER BY seq
				LIMIT ?
			)`, checkpoint, migrateV10BatchSize).Scan(&batchRows, &lastSeq); queryErr != nil {
			return fmt.Errorf("select backfill batch: %w", queryErr)
		}
		if batchRows == 0 {
			return nil
		}

		result, execErr := conn.ExecContext(ctx, `
			UPDATE task_artifacts AS target
			SET event_seq = (
				SELECT COUNT(*)
				FROM task_artifacts AS prior
				WHERE prior.task_id = target.task_id
				  AND prior.seq <= target.seq
			)
			WHERE target.event_seq IS NULL
			  AND target.seq > ?
			  AND target.seq <= ?`, checkpoint, lastSeq)
		if execErr != nil {
			return fmt.Errorf("update backfill batch: %w", execErr)
		}
		rowsAffected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return fmt.Errorf("count updated backfill rows: %w", affectedErr)
		}
		if rowsAffected != batchRows {
			return fmt.Errorf("updated backfill rows=%d want=%d", rowsAffected, batchRows)
		}

		result, execErr = conn.ExecContext(ctx, `
			UPDATE loom_migrations
			SET checkpoint_seq=?,
				processed_rows=processed_rows+?,
				batch_count=batch_count+1
			WHERE version=? AND state='running'
			  AND checkpoint_seq=? AND processed_rows=?
			  AND processed_rows+? <= source_rows`,
			lastSeq, batchRows, migrateV10Version, checkpoint, processedRows, batchRows)
		if execErr != nil {
			return fmt.Errorf("advance backfill ledger: %w", execErr)
		}
		ledgerRows, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return fmt.Errorf("count advanced ledger rows: %w", affectedErr)
		}
		if ledgerRows != 1 {
			return fmt.Errorf("advanced ledger rows=%d want=1", ledgerRows)
		}
		updated = batchRows
		return nil
	})
	return updated, err
}

func finalizeV10(db *sql.DB) error {
	return runV10Transaction(db, "finalization", func(ctx context.Context, conn *sql.Conn) error {
		var state string
		var checkpoint, sourceRows, processedRows int64
		if queryErr := conn.QueryRowContext(ctx, `
			SELECT state,checkpoint_seq,source_rows,processed_rows
			FROM loom_migrations WHERE version=?`, migrateV10Version).Scan(
			&state, &checkpoint, &sourceRows, &processedRows,
		); queryErr != nil {
			return fmt.Errorf("read final ledger: %w", queryErr)
		}
		if state == "complete" {
			return nil
		}
		if state != "running" {
			return fmt.Errorf("invalid ledger state %q", state)
		}
		if sourceRows != processedRows {
			return fmt.Errorf("incomplete backfill source=%d processed=%d checkpoint=%d", sourceRows, processedRows, checkpoint)
		}

		var nullRows, duplicatePairs int64
		if queryErr := conn.QueryRowContext(ctx,
			`SELECT count(*) FROM task_artifacts WHERE event_seq IS NULL`).Scan(&nullRows); queryErr != nil {
			return fmt.Errorf("count NULL event sequences: %w", queryErr)
		}
		if queryErr := conn.QueryRowContext(ctx, `
			SELECT count(*) FROM (
				SELECT task_id,event_seq
				FROM task_artifacts
				WHERE event_seq IS NOT NULL
				GROUP BY task_id,event_seq
				HAVING count(*) > 1
			)`).Scan(&duplicatePairs); queryErr != nil {
			return fmt.Errorf("count duplicate event sequences: %w", queryErr)
		}
		if nullRows != 0 || duplicatePairs != 0 {
			return fmt.Errorf("event sequence validation failed: null=%d duplicates=%d", nullRows, duplicatePairs)
		}

		if _, execErr := conn.ExecContext(ctx, `DROP INDEX IF EXISTS `+migrateV10UniqueIndex); execErr != nil {
			return fmt.Errorf("replace event sequence index: %w", execErr)
		}
		if _, execErr := conn.ExecContext(ctx, `CREATE UNIQUE INDEX `+migrateV10UniqueIndex+
			` ON task_artifacts(task_id,event_seq)`); execErr != nil {
			return fmt.Errorf("create event sequence index: %w", execErr)
		}
		if _, execErr := conn.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+migrateV10CompatibilityTrigger); execErr != nil {
			return fmt.Errorf("drop migration trigger: %w", execErr)
		}
		if _, execErr := conn.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+migrateV10SteadyStateTrigger); execErr != nil {
			return fmt.Errorf("replace steady-state trigger: %w", execErr)
		}
		if _, execErr := conn.ExecContext(ctx, createV10SteadyStateTrigger); execErr != nil {
			return fmt.Errorf("create steady-state trigger: %w", execErr)
		}

		completedAt := time.Now().UTC().Format(time.RFC3339Nano)
		result, execErr := conn.ExecContext(ctx, `
			UPDATE loom_migrations
			SET state='complete', completed_at=COALESCE(completed_at, ?)
			WHERE version=? AND state='running'
			  AND source_rows=processed_rows`, completedAt, migrateV10Version)
		if execErr != nil {
			return fmt.Errorf("complete v10 ledger: %w", execErr)
		}
		rowsAffected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return fmt.Errorf("count completed ledger rows: %w", affectedErr)
		}
		if rowsAffected != 1 {
			return fmt.Errorf("completed ledger rows=%d want=1", rowsAffected)
		}
		return nil
	})
}

func migrateV10(db *sql.DB) error {
	complete, err := expandV10(db)
	if err != nil {
		return err
	}
	if complete {
		return nil
	}
	for {
		updated, err := backfillV10Batch(db)
		if err != nil {
			return err
		}
		if updated == 0 {
			break
		}
	}
	return finalizeV10(db)
}

// MigrateV5Down reverts the v5 progress columns. Returns an error on the
// first DROP that fails for a reason other than "no such column" (which is
// idempotent — the column was already absent).
func MigrateV5Down(db *sql.DB) error {
	for _, stmt := range migrateV5DownColumns {
		if _, err := db.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "no such column") {
				continue
			}
			return fmt.Errorf("loom store: migrate v5 down: %w", err)
		}
	}
	return nil
}

// MigrateV6Down reverts the parent_task_id column and index. Returns an error
// on the first DROP that fails for a reason other than "no such column" (which
// is idempotent — the column was already absent).
func MigrateV6Down(db *sql.DB) error {
	for _, stmt := range migrateV6DownColumns {
		if _, err := db.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "no such column") {
				continue
			}
			return fmt.Errorf("loom store: migrate v6 down: %w", err)
		}
	}
	return nil
}

// TaskStore persists tasks in SQLite.
type TaskStore struct {
	db         *sql.DB
	daemonUUID string // set via SetDaemonUUID; empty string means not configured
	engineName string // identifies owning daemon for query scoping (AIMUX-10)
}

// NewTaskStore initialises the tasks table and returns a TaskStore.
// engineName identifies the owning daemon and is used to scope task queries
// (MarkCrashed, List, Count). Returns an error if engineName is empty — silent
// fallback to empty identity is forbidden (spec C3 / FR-7).
func NewTaskStore(db *sql.DB, engineName string) (*TaskStore, error) {
	if engineName == "" {
		return nil, fmt.Errorf("loom store: engineName must not be empty")
	}
	if _, err := db.Exec(createTasksTable); err != nil {
		return nil, fmt.Errorf("loom store: create schema: %w", err)
	}
	// Migrate: add request_id column if not present (pre-Phase 4a databases).
	if _, err := db.Exec(migrateRequestIDColumn); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return nil, fmt.Errorf("loom store: migrate request_id: %w", err)
	}
	// Session-durability Phase 1: add daemon_uuid, last_seen_at, aborted_at.
	// Each ALTER is run individually; "duplicate column name" is silently ignored
	// (idempotent migration). Any other error is propagated — a partial schema
	// would cause Create() to fail on the first INSERT into the missing column.
	for _, stmt := range migrateV2Columns {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return nil, fmt.Errorf("loom store: migrate v2 columns: %w", err)
		}
	}
	// AIMUX-10: add engine_name column and composite index for per-daemon task scoping.
	// Each statement is idempotent: duplicate-column and already-exists errors are swallowed.
	for _, stmt := range migrateV3Columns {
		if _, err := db.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "duplicate column name") ||
				strings.Contains(err.Error(), "already exists") {
				continue
			}
			return nil, fmt.Errorf("loom store: migrate v3 columns: %w", err)
		}
	}
	// AIMUX-12: add tenant_id column and composite index for per-tenant task scoping.
	// Existing rows receive the default '__legacy__' sentinel (ADR-011).
	for _, stmt := range migrateV4Columns {
		if _, err := db.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "duplicate column name") ||
				strings.Contains(err.Error(), "already exists") {
				continue
			}
			return nil, fmt.Errorf("loom store: migrate v4 columns: %w", err)
		}
	}
	// DEF-13 / AIMUX-16 CR-005: add live progress columns. New tasks default
	// to empty/zero/null; existing rows receive the same defaults so reads of
	// pre-migration tasks return zero-valued progress fields rather than
	// stale/garbage data (EC-5.1 — "fields stay zero/empty, not stale").
	for _, stmt := range migrateV5Columns {
		if _, err := db.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "duplicate column name") ||
				strings.Contains(err.Error(), "already exists") {
				continue
			}
			return nil, fmt.Errorf("loom store: migrate v5 columns: %w", err)
		}
	}
	// AIMUX-21 T002: add parent_task_id column and child lookup index for
	// sub-task tree observability. Existing rows remain root tasks
	// (parent_task_id=NULL).
	for _, stmt := range migrateV6Columns {
		if _, err := db.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "duplicate column name") ||
				strings.Contains(err.Error(), "already exists") {
				continue
			}
			return nil, fmt.Errorf("loom store: migrate v6 columns: %w", err)
		}
	}
	// AIMUX-23 CR-001: create append-only task artifact projection table and
	// per-task cursor index. This migration is idempotent on fresh and existing
	// databases.
	for _, stmt := range migrateV7Statements {
		if _, err := db.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "already exists") {
				continue
			}
			return nil, fmt.Errorf("loom store: migrate v7 artifacts: %w", err)
		}
	}
	// AIMUX-23 CR-011: add runtime-event channel metadata and a bounded slice
	// lookup index. Existing projection rows keep the empty channel default.
	for _, stmt := range migrateV8Statements {
		if _, err := db.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "duplicate column name") ||
				strings.Contains(err.Error(), "already exists") {
				continue
			}
			return nil, fmt.Errorf("loom store: migrate v8 artifact runtime slices: %w", err)
		}
	}
	// AIMUX-26 CR-001: install the sole-authority cancellation marker and
	// durable task-scoped pending-action table. migrateV9 repairs the legacy
	// two-column correlation index without rewriting existing rows.
	if err := migrateV9(db); err != nil {
		return nil, fmt.Errorf("loom store: migrate v9 runtime authority: %w", err)
	}
	// AIMUX-26 CR-001: add task-local runtime-event ordering through the
	// restartable v10 ledger. Registration is deliberately after the accepted
	// v9 authority migration; expansion, bounded backfill, and cutover each use
	// pinned BEGIN IMMEDIATE transactions.
	if err := migrateV10(db); err != nil {
		return nil, fmt.Errorf("loom store: migrate v10 runtime event ledger: %w", err)
	}
	// Inherit WAL mode from parent DB (session.Store already sets WAL).
	// Reading first avoids an unnecessary write-PRAGMA racing a concurrent
	// constructor's immediate migration transaction when WAL is already active.
	if err := ensureTaskStoreJournalMode(db); err != nil {
		return nil, fmt.Errorf("loom store: set journal mode: %w", err)
	}
	if err := ensureTaskStoreSynchronousNormal(db); err != nil {
		return nil, fmt.Errorf("loom store: set synchronous mode: %w", err)
	}
	return &TaskStore{db: db, engineName: engineName}, nil
}

// SetDaemonUUID configures the daemon-lifetime UUID to be stamped on every
// new task row. Called once at startup by the main binary after generating
// the UUID via pkg/session.GetDaemonUUID(). Loom is a separate module and
// cannot import pkg/session directly, so the UUID is injected here.
func (s *TaskStore) SetDaemonUUID(uuid string) {
	s.daemonUUID = uuid
}

// Create inserts a new task into the store.
func (s *TaskStore) Create(task *Task) error {
	envJSON, err := marshalJSON(task.Env)
	if err != nil {
		return fmt.Errorf("loom store: marshal env: %w", err)
	}
	metaJSON, err := marshalJSON(task.Metadata)
	if err != nil {
		return fmt.Errorf("loom store: marshal metadata: %w", err)
	}

	lastSeenAt := time.Now().UTC().Format(time.RFC3339)

	_, err = s.db.Exec(`
		INSERT INTO tasks
			(id, status, worker_type, project_id, request_id, parent_task_id, prompt, cwd, env, cli, role, model,
			 effort, timeout, metadata, result, error, retries, created_at, dispatched_at, cancel_requested_at, completed_at,
			 daemon_uuid, last_seen_at, engine_name, tenant_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID,
		string(task.Status),
		string(task.WorkerType),
		task.ProjectID,
		task.RequestID,
		nullableString(task.ParentTaskID),
		task.Prompt,
		task.CWD,
		envJSON,
		task.CLI,
		task.Role,
		task.Model,
		task.Effort,
		task.Timeout,
		metaJSON,
		task.Retries,
		task.CreatedAt,
		task.DispatchedAt,
		task.CancelRequestedAt,
		task.CompletedAt,
		s.daemonUUID,
		lastSeenAt,
		s.engineName,
		task.TenantID,
	)
	if err != nil {
		return fmt.Errorf("loom store: insert task: %w", err)
	}
	return nil
}

// Import upserts an already-materialized historical task into the store.
// It is intentionally separate from Create: imports must preserve terminal
// result/error/progress fields from legacy state instead of starting with an
// empty active task row.
func (s *TaskStore) Import(task *Task) error {
	if task == nil {
		return fmt.Errorf("loom store: import task: nil task")
	}
	if task.ID == "" {
		return fmt.Errorf("loom store: import task: missing id")
	}
	if task.Status == "" {
		return fmt.Errorf("loom store: import task %s: missing status", task.ID)
	}
	if task.WorkerType == "" {
		return fmt.Errorf("loom store: import task %s: missing worker type", task.ID)
	}

	envJSON, err := marshalJSON(task.Env)
	if err != nil {
		return fmt.Errorf("loom store: import marshal env: %w", err)
	}
	metaJSON, err := marshalJSON(task.Metadata)
	if err != nil {
		return fmt.Errorf("loom store: import marshal metadata: %w", err)
	}

	createdAt := task.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	tenantID := task.TenantID
	if tenantID == "" {
		tenantID = LegacyTenantID
	}
	lastSeenAt := time.Now().UTC().Format(time.RFC3339)

	_, err = s.db.Exec(`
		INSERT INTO tasks
			(id, status, worker_type, project_id, request_id, parent_task_id, prompt, cwd, env, cli, role, model,
			 effort, timeout, metadata, result, error, retries, created_at, dispatched_at, cancel_requested_at, completed_at,
			 daemon_uuid, last_seen_at, engine_name, tenant_id, last_output_line, progress_lines, progress_updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status              = excluded.status,
			worker_type         = excluded.worker_type,
			project_id          = excluded.project_id,
			request_id          = excluded.request_id,
			parent_task_id      = excluded.parent_task_id,
			prompt              = excluded.prompt,
			cwd                 = excluded.cwd,
			env                 = excluded.env,
			cli                 = excluded.cli,
			role                = excluded.role,
			model               = excluded.model,
			effort              = excluded.effort,
			timeout             = excluded.timeout,
			metadata            = excluded.metadata,
			result              = excluded.result,
			error               = excluded.error,
			retries             = excluded.retries,
			created_at          = excluded.created_at,
			dispatched_at       = excluded.dispatched_at,
			cancel_requested_at = excluded.cancel_requested_at,
			completed_at        = excluded.completed_at,
			daemon_uuid         = excluded.daemon_uuid,
			last_seen_at        = excluded.last_seen_at,
			engine_name         = excluded.engine_name,
			tenant_id           = excluded.tenant_id,
			last_output_line    = excluded.last_output_line,
			progress_lines      = excluded.progress_lines,
			progress_updated_at = excluded.progress_updated_at`,
		task.ID,
		string(task.Status),
		string(task.WorkerType),
		task.ProjectID,
		task.RequestID,
		nullableString(task.ParentTaskID),
		task.Prompt,
		task.CWD,
		envJSON,
		task.CLI,
		task.Role,
		task.Model,
		task.Effort,
		task.Timeout,
		metaJSON,
		task.Result,
		redactErrorMsg(task.Error),
		task.Retries,
		createdAt,
		task.DispatchedAt,
		task.CancelRequestedAt,
		task.CompletedAt,
		s.daemonUUID,
		lastSeenAt,
		s.engineName,
		tenantID,
		task.LastOutputLine,
		task.ProgressLines,
		task.ProgressUpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("loom store: import task: %w", err)
	}
	return nil
}

// taskSelectColumns is the canonical column list for SELECT statements that
// hydrate a *Task via scanTask. Defining it once avoids drift between the
// query columns and scanTask's destination order.
const taskSelectColumns = `id, status, worker_type, project_id, request_id, parent_task_id, prompt, cwd, env, cli, role, model,
		       effort, timeout, metadata, result, error, retries, created_at, dispatched_at, cancel_requested_at, completed_at,
		       engine_name, tenant_id, last_output_line, progress_lines, progress_updated_at`

// Get retrieves a task by ID (cross-tenant — use GetForTenant for scoped access).
func (s *TaskStore) Get(id string) (*Task, error) {
	return s.GetContext(context.Background(), id)
}

// GetContext retrieves a task by ID with caller-controlled cancellation.
func (s *TaskStore) GetContext(ctx context.Context, id string) (*Task, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+taskSelectColumns+`
		FROM tasks WHERE id = ?`, id)

	return scanTask(row)
}

// GetForTenant retrieves a task by ID only if it belongs to the given tenantID.
// Returns ErrTaskNotFound when the task does not exist OR belongs to a different tenant
// (defence-in-depth: NEVER reveal task existence to a foreign tenant via 403).
func (s *TaskStore) GetForTenant(id, tenantID string) (*Task, error) {
	return s.GetForTenantContext(context.Background(), id, tenantID)
}

// GetForTenantContext retrieves a tenant-scoped task with caller-controlled cancellation.
func (s *TaskStore) GetForTenantContext(ctx context.Context, id, tenantID string) (*Task, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+taskSelectColumns+`
		FROM tasks WHERE id = ? AND tenant_id = ?`, id, tenantID)

	task, err := scanTask(row)
	if err != nil {
		// sql.ErrNoRows means task not found OR belongs to a different tenant.
		// Both cases must return ErrTaskNotFound (CHK079 fix: no 403 disclosure).
		if isNoRows(err) {
			return nil, ErrTaskNotFound
		}
		return nil, err
	}
	return task, nil
}

// GetForTenantInEngine retrieves a task by ID only if it belongs to the given
// tenantID and this store's engine. Use this for execution paths that must not
// inherit task context across daemon/worktree boundaries.
func (s *TaskStore) GetForTenantInEngine(id, tenantID string) (*Task, error) {
	return s.GetForTenantInEngineContext(context.Background(), id, tenantID)
}

// GetForTenantInEngineContext retrieves an engine- and tenant-scoped task with
// caller-controlled cancellation.
func (s *TaskStore) GetForTenantInEngineContext(ctx context.Context, id, tenantID string) (*Task, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+taskSelectColumns+`
		FROM tasks WHERE id = ? AND tenant_id = ? AND engine_name = ?`, id, tenantID, s.engineName)

	task, err := scanTask(row)
	if err != nil {
		if isNoRows(err) {
			return nil, ErrTaskNotFound
		}
		return nil, err
	}
	return task, nil
}

// List returns tasks for a project, optionally filtered by status values.
// Scoped by engine_name only — use ListForTenant for tenant-scoped access.
func (s *TaskStore) List(projectID string, statuses ...TaskStatus) ([]*Task, error) {
	return s.listInternal(projectID, "", statuses...)
}

// ListForTenant returns tasks for a project scoped to the given tenantID,
// optionally filtered by status values. Only tasks owned by tenantID are returned.
func (s *TaskStore) ListForTenant(projectID, tenantID string, statuses ...TaskStatus) ([]*Task, error) {
	return s.listInternal(projectID, tenantID, statuses...)
}

// listInternal is the shared implementation for List and ListForTenant.
// When tenantID is non-empty, results are additionally filtered by tenant_id.
func (s *TaskStore) listInternal(projectID, tenantID string, statuses ...TaskStatus) ([]*Task, error) {
	var (
		rows *sql.Rows
		err  error
	)

	base := `
		SELECT ` + taskSelectColumns + `
		FROM tasks WHERE project_id = ? AND engine_name = ?`

	placeholders := []interface{}{projectID, s.engineName}

	if tenantID != "" {
		base += ` AND tenant_id = ?`
		placeholders = append(placeholders, tenantID)
	}

	if len(statuses) == 0 {
		rows, err = s.db.Query(base+` ORDER BY created_at ASC`, placeholders...)
	} else {
		query := base + ` AND status IN (`
		for i, st := range statuses {
			if i > 0 {
				query += ","
			}
			query += "?"
			placeholders = append(placeholders, string(st))
		}
		query += ") ORDER BY created_at ASC"
		rows, err = s.db.Query(query, placeholders...)
	}
	if err != nil {
		return nil, fmt.Errorf("loom store: list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("loom store: scan task: %w", err)
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *TaskStore) listRecoveryCandidates() ([]*Task, error) {
	rows, err := s.db.Query(`
		SELECT `+taskSelectColumns+`
		FROM tasks
		WHERE engine_name = ? AND status IN (?,?,?,?,?)
		ORDER BY created_at ASC, id ASC`,
		s.engineName,
		TaskStatusDispatched,
		TaskStatusRunning,
		TaskStatusInputRequired,
		TaskStatusRetrying,
		TaskStatusCancelling,
	)
	if err != nil {
		return nil, fmt.Errorf("loom store: list recovery candidates: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("loom store: scan recovery candidate: %w", scanErr)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loom store: iterate recovery candidates: %w", err)
	}
	return tasks, nil
}

// ListAll returns tasks across all engines and projects, optionally filtered by status.
// Unlike List, it applies no engine_name or project_id filter — use for cross-daemon
// global views (AIMUX-10 FR-5, sessions tool all=true opt-in).
func (s *TaskStore) ListAll(statuses ...TaskStatus) ([]*Task, error) {
	var (
		rows *sql.Rows
		err  error
	)

	if len(statuses) == 0 {
		rows, err = s.db.Query(`
			SELECT ` + taskSelectColumns + `
			FROM tasks ORDER BY created_at ASC`)
	} else {
		query := `
			SELECT ` + taskSelectColumns + `
			FROM tasks WHERE status IN (`
		placeholders := make([]interface{}, 0, len(statuses))
		for i, st := range statuses {
			if i > 0 {
				query += ","
			}
			query += "?"
			placeholders = append(placeholders, string(st))
		}
		query += ") ORDER BY created_at ASC"
		rows, err = s.db.Query(query, placeholders...)
	}
	if err != nil {
		return nil, fmt.Errorf("loom store: list all tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("loom store: scan task: %w", scanErr)
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// ListChildren returns direct child tasks for a parent in deterministic
// creation order. It intentionally applies no engine/project filter: the
// parent_task_id edge is the tree boundary, and later CR-002 invariants enforce
// project consistency at Submit time.
func (s *TaskStore) ListChildren(parentID string) ([]*Task, error) {
	rows, err := s.db.Query(`
		SELECT `+taskSelectColumns+`
		FROM tasks WHERE parent_task_id = ?
		ORDER BY created_at ASC, id ASC`, parentID)
	if err != nil {
		return nil, fmt.Errorf("loom store: list child tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("loom store: scan child task: %w", scanErr)
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// CountForTenant returns the number of in-flight tasks (pending, dispatched, running)
// for the given tenantID. Used by TenantScopedLoomEngine for quota enforcement (T060).
// This query uses live SQL count (not cached) to avoid race window issues per IF-WRONG directive.
func (s *TaskStore) CountForTenant(tenantID string) (int, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM tasks
		WHERE tenant_id = ? AND engine_name = ? AND status IN ('pending', 'dispatched', 'running')`,
		tenantID, s.engineName,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("loom store: count for tenant: %w", err)
	}
	return count, nil
}

// UpdateStatus transitions a task from `from` to `to`, enforcing state machine rules.
// Returns an error if the current status does not match `from` or the transition is invalid.
func (s *TaskStore) UpdateStatus(id string, from, to TaskStatus) error {
	if !from.CanTransitionTo(to) {
		return fmt.Errorf("loom store: invalid transition %s → %s", from, to)
	}

	var extra string
	var args []interface{}

	switch to {
	case TaskStatusDispatched:
		now := time.Now().UTC()
		extra = ", dispatched_at = ?"
		args = []interface{}{string(to), now, string(from), id}
	default:
		args = []interface{}{string(to), string(from), id}
	}

	query := fmt.Sprintf("UPDATE tasks SET status = ?%s WHERE status = ? AND id = ?", extra)
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("loom store: update status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("loom store: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("loom store: task %s not found in status %s (transition %s → %s rejected)", id, from, from, to)
	}
	return nil
}

// SetResult stores the execution result and marks completed_at for an active task.
// errMsg is redacted before storage — secrets (API keys, Bearer tokens) are
// replaced with [REDACTED]. result is stored verbatim (callers own its content).
func (s *TaskStore) SetResult(id string, result string, errMsg string) error {
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`UPDATE tasks
		 SET result = ?, error = ?, completed_at = ?
		 WHERE id = ? AND status IN ('pending', 'dispatched', 'running', 'retrying')`,
		result, redactErrorMsg(errMsg), now, id,
	)
	if err != nil {
		return fmt.Errorf("loom store: set result: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("loom store: task %s not found or not active", id)
	}
	return nil
}

// SetMetadata stores the latest worker metadata for an active task.
func (s *TaskStore) SetMetadata(id string, metadata map[string]any) error {
	metaJSON, err := marshalJSON(metadata)
	if err != nil {
		return fmt.Errorf("loom store: marshal metadata: %w", err)
	}
	res, err := s.db.Exec(
		`UPDATE tasks
		 SET metadata = ?
		 WHERE id = ? AND status IN ('pending', 'dispatched', 'running', 'retrying')`,
		metaJSON, id,
	)
	if err != nil {
		return fmt.Errorf("loom store: set metadata: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("loom store: task %s not found or not active", id)
	}
	return nil
}

// IncrementRetries bumps the retry count for a task.
func (s *TaskStore) IncrementRetries(id string) error {
	res, err := s.db.Exec(`UPDATE tasks SET retries = retries + 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("loom store: increment retries: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("loom store: task %s not found", id)
	}
	return nil
}

// MarkCrashed is a legacy direct-store compatibility helper that sets
// status='failed_crash' for dispatched/running tasks owned by this store's
// engine. It returns the number of rows marked.
//
// It intentionally preserves its historical raw-SQL behavior, but it does not
// write authority facts, fence open actions, cover input_required/retrying/
// cancelling, or emit events. New production startup code must call
// LoomEngine.RecoverCrashed instead.
func (s *TaskStore) MarkCrashed() (int, error) {
	res, err := s.db.Exec(
		`UPDATE tasks SET status = 'failed_crash' WHERE status IN ('dispatched', 'running') AND engine_name = ?`,
		s.engineName,
	)
	if err != nil {
		return 0, fmt.Errorf("loom store: mark crashed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("loom store: mark crashed rows affected: %w", err)
	}
	return int(n), nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (*Task, error) {
	var (
		task              Task
		envJSON           string
		metaJSON          string
		parentTaskID      sql.NullString
		dispatchedAt      sql.NullTime
		cancelRequestedAt sql.NullTime
		completedAt       sql.NullTime
		progressUpdatedAt sql.NullTime
	)

	err := s.Scan(
		&task.ID,
		&task.Status,
		&task.WorkerType,
		&task.ProjectID,
		&task.RequestID,
		&parentTaskID,
		&task.Prompt,
		&task.CWD,
		&envJSON,
		&task.CLI,
		&task.Role,
		&task.Model,
		&task.Effort,
		&task.Timeout,
		&metaJSON,
		&task.Result,
		&task.Error,
		&task.Retries,
		&task.CreatedAt,
		&dispatchedAt,
		&cancelRequestedAt,
		&completedAt,
		&task.EngineName,
		&task.TenantID,
		&task.LastOutputLine,
		&task.ProgressLines,
		&progressUpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if parentTaskID.Valid {
		task.ParentTaskID = parentTaskID.String
	}

	if err := unmarshalJSON(envJSON, &task.Env); err != nil {
		return nil, fmt.Errorf("unmarshal env: %w", err)
	}
	if err := unmarshalJSON(metaJSON, &task.Metadata); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}

	if dispatchedAt.Valid {
		t := dispatchedAt.Time
		task.DispatchedAt = &t
	}
	if cancelRequestedAt.Valid {
		t := cancelRequestedAt.Time
		task.CancelRequestedAt = &t
	}
	if completedAt.Valid {
		t := completedAt.Time
		task.CompletedAt = &t
	}
	if progressUpdatedAt.Valid {
		t := progressUpdatedAt.Time
		task.ProgressUpdatedAt = &t
	}

	return &task, nil
}

func marshalJSON(v any) (string, error) {
	if v == nil {
		return "{}", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalJSON(s string, v any) error {
	if s == "" || s == "{}" || s == "null" {
		return nil
	}
	return json.Unmarshal([]byte(s), v)
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// isNoRows returns true when err wraps sql.ErrNoRows.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
