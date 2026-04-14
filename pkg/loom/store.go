package loom

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const createTasksTable = `
CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'pending',
    worker_type TEXT NOT NULL,
    project_id TEXT NOT NULL,
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
CREATE INDEX IF NOT EXISTS idx_tasks_project_id ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
`

// TaskStore persists tasks in SQLite.
type TaskStore struct {
	db *sql.DB
}

// NewTaskStore initialises the tasks table and returns a TaskStore.
func NewTaskStore(db *sql.DB) (*TaskStore, error) {
	if _, err := db.Exec(createTasksTable); err != nil {
		return nil, fmt.Errorf("loom store: create schema: %w", err)
	}
	// Inherit WAL mode from parent DB (session.Store already sets WAL).
	// These PRAGMAs are idempotent — safe even if already set.
	db.Exec("PRAGMA journal_mode=WAL")  //nolint:errcheck
	db.Exec("PRAGMA synchronous=NORMAL") //nolint:errcheck
	return &TaskStore{db: db}, nil
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

	_, err = s.db.Exec(`
		INSERT INTO tasks
			(id, status, worker_type, project_id, prompt, cwd, env, cli, role, model,
			 effort, timeout, metadata, result, error, retries, created_at, dispatched_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, ?, ?, ?)`,
		task.ID,
		string(task.Status),
		string(task.WorkerType),
		task.ProjectID,
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
	)
	if err != nil {
		return fmt.Errorf("loom store: insert task: %w", err)
	}
	return nil
}

// Get retrieves a task by ID.
func (s *TaskStore) Get(id string) (*Task, error) {
	row := s.db.QueryRow(`
		SELECT id, status, worker_type, project_id, prompt, cwd, env, cli, role, model,
		       effort, timeout, metadata, result, error, retries, created_at, dispatched_at, completed_at
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
			SELECT id, status, worker_type, project_id, prompt, cwd, env, cli, role, model,
			       effort, timeout, metadata, result, error, retries, created_at, dispatched_at, completed_at
			FROM tasks WHERE project_id = ? ORDER BY created_at ASC`, projectID)
	} else {
		// Build IN clause with placeholders.
		placeholders := make([]interface{}, 0, len(statuses)+1)
		placeholders = append(placeholders, projectID)
		query := `
			SELECT id, status, worker_type, project_id, prompt, cwd, env, cli, role, model,
			       effort, timeout, metadata, result, error, retries, created_at, dispatched_at, completed_at
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
func (s *TaskStore) SetResult(id string, result string, errMsg string) error {
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`UPDATE tasks SET result = ?, error = ?, completed_at = ? WHERE id = ?`,
		result, errMsg, now, id,
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
func (s *TaskStore) MarkCrashed() (int, error) {
	res, err := s.db.Exec(
		`UPDATE tasks SET status = 'failed_crash' WHERE status IN ('dispatched', 'running')`,
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
