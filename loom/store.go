package loom

import (
	"database/sql"
	"encoding/json"
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
	regexp.MustCompile(`sk-proj-[A-Za-z0-9_\-]{20,}`),                   // openai-key-project
	regexp.MustCompile(`sk-svcacct-[A-Za-z0-9_\-]{20,}`),               // openai-key-svcacct
	regexp.MustCompile(`sk-ant-api\d{2}-[A-Za-z0-9_\-]{20,}`),          // anthropic-key
	regexp.MustCompile(`sk-[A-Za-z0-9_\-]{20,}`),                        // openai-key-legacy (LAST of sk-*)
	regexp.MustCompile(`AIza[A-Za-z0-9_\-]{35,}`),                       // google-ai-key
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9_\-\.=]{20,}`),          // bearer-token
	regexp.MustCompile(`(?i)Authorization:\s*[^\s]{20,}`),               // auth-header
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
	// Safety: MarkCrashed bulk-updates rows to failed_crash via raw SQL (bypassing
	// UpdateStatus) for performance during startup crash recovery. This init assertion
	// ensures the state machine still permits these transitions so the raw SQL cannot
	// silently diverge from CanTransitionTo validation if validTransitions is updated.
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
    engine_name TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_tasks_project_id ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
-- idx_tasks_engine_status created by migrateV3Columns AFTER engine_name column lands
-- on pre-v3 databases (AIMUX-10).
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
	// Ignore "duplicate column name" errors — ALTER is idempotent by design.
	db.Exec(migrateRequestIDColumn) //nolint:errcheck
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
	// Inherit WAL mode from parent DB (session.Store already sets WAL).
	// These PRAGMAs are idempotent — safe even if already set.
	db.Exec("PRAGMA journal_mode=WAL")  //nolint:errcheck
	db.Exec("PRAGMA synchronous=NORMAL") //nolint:errcheck
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
			(id, status, worker_type, project_id, request_id, prompt, cwd, env, cli, role, model,
			 effort, timeout, metadata, result, error, retries, created_at, dispatched_at, completed_at,
			 daemon_uuid, last_seen_at, engine_name)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, ?, ?, ?, ?, ?, ?)`,
		task.ID,
		string(task.Status),
		string(task.WorkerType),
		task.ProjectID,
		task.RequestID,
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
		task.CompletedAt,
		s.daemonUUID,
		lastSeenAt,
		s.engineName,
	)
	if err != nil {
		return fmt.Errorf("loom store: insert task: %w", err)
	}
	return nil
}

// Get retrieves a task by ID.
func (s *TaskStore) Get(id string) (*Task, error) {
	row := s.db.QueryRow(`
		SELECT id, status, worker_type, project_id, request_id, prompt, cwd, env, cli, role, model,
		       effort, timeout, metadata, result, error, retries, created_at, dispatched_at, completed_at,
		       engine_name
		FROM tasks WHERE id = ?`, id)

	return scanTask(row)
}

// List returns tasks for a project, optionally filtered by status values.
func (s *TaskStore) List(projectID string, statuses ...TaskStatus) ([]*Task, error) {
	var (
		rows *sql.Rows
		err  error
	)

	if len(statuses) == 0 {
		rows, err = s.db.Query(`
			SELECT id, status, worker_type, project_id, request_id, prompt, cwd, env, cli, role, model,
			       effort, timeout, metadata, result, error, retries, created_at, dispatched_at, completed_at,
			       engine_name
			FROM tasks WHERE project_id = ? ORDER BY created_at ASC`, projectID)
	} else {
		// Build IN clause with placeholders.
		placeholders := make([]interface{}, 0, len(statuses)+1)
		placeholders = append(placeholders, projectID)
		query := `
			SELECT id, status, worker_type, project_id, request_id, prompt, cwd, env, cli, role, model,
			       effort, timeout, metadata, result, error, retries, created_at, dispatched_at, completed_at,
			       engine_name
			FROM tasks WHERE project_id = ? AND status IN (`
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

// SetResult stores the execution result and marks completed_at.
// errMsg is redacted before storage — secrets (API keys, Bearer tokens) are
// replaced with [REDACTED]. result is stored verbatim (callers own its content).
func (s *TaskStore) SetResult(id string, result string, errMsg string) error {
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`UPDATE tasks SET result = ?, error = ?, completed_at = ? WHERE id = ?`,
		result, redactErrorMsg(errMsg), now, id,
	)
	if err != nil {
		return fmt.Errorf("loom store: set result: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("loom store: task %s not found", id)
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

// MarkCrashed sets status='failed_crash' for all dispatched or running tasks.
// Returns the number of tasks marked.
//
// Raw SQL is used intentionally: on daemon startup this bulk-updates every
// in-flight row in a single statement, which is both simpler and faster than
// iterating with UpdateStatus. The init() assertion above ensures the state
// machine continues to permit these transitions so the raw SQL can never
// silently diverge from CanTransitionTo validation.
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
		task         Task
		envJSON      string
		metaJSON     string
		dispatchedAt sql.NullTime
		completedAt  sql.NullTime
	)

	err := s.Scan(
		&task.ID,
		&task.Status,
		&task.WorkerType,
		&task.ProjectID,
		&task.RequestID,
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
		&completedAt,
		&task.EngineName,
	)
	if err != nil {
		return nil, err
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
	if completedAt.Valid {
		t := completedAt.Time
		task.CompletedAt = &t
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
