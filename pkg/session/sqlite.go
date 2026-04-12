package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thebtf/aimux/pkg/types"
	_ "modernc.org/sqlite"
)

// Store provides SQLite persistence for sessions and jobs.
// Periodic snapshots (every 30s) dump in-memory state to SQLite.
// SQLite is the query interface for historical data; in-memory is authoritative for active state.
type Store struct {
	db *sql.DB
}

// NewStore opens or creates a SQLite database at the given path.
func NewStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}

	return &Store{db: db}, nil
}

// migrate creates tables if they don't exist.
func migrate(db *sql.DB) error {
	_, err := db.Exec(`
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

		CREATE INDEX IF NOT EXISTS idx_jobs_session ON jobs(session_id);
		CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
		CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
	`)
	if err != nil {
		return err
	}

	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN metadata_json TEXT`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}

	return nil
}

// SnapshotSession upserts a session into SQLite.
func (s *Store) SnapshotSession(sess *Session) error {
	metadataJSON, _ := json.Marshal(sess.Metadata)
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO sessions (id, cli, mode, cli_session_id, pid, status, turns, cwd, metadata_json, created_at, last_active_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.CLI, sess.Mode, sess.CLISessionID, sess.PID,
		sess.Status, sess.Turns, sess.CWD, string(metadataJSON),
		sess.CreatedAt.Format(time.RFC3339),
		sess.LastActiveAt.Format(time.RFC3339),
	)
	return err
}

// SnapshotJob upserts a job into SQLite.
func (s *Store) SnapshotJob(job *Job) error {
	var errorJSON, pheromonesJSON, pipelineJSON []byte

	if job.Error != nil {
		errorJSON, _ = json.Marshal(job.Error)
	}
	if len(job.Pheromones) > 0 {
		pheromonesJSON, _ = json.Marshal(job.Pheromones)
	}
	if job.Pipeline != nil {
		pipelineJSON, _ = json.Marshal(job.Pipeline)
	}

	var completedAt *string
	if job.CompletedAt != nil {
		t := job.CompletedAt.Format(time.RFC3339)
		completedAt = &t
	}

	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO jobs (id, session_id, cli, status, progress, content, exit_code,
			error_json, poll_count, pheromones_json, pipeline_json, pid, created_at, progress_updated_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.SessionID, job.CLI, job.Status, job.Progress, job.Content, job.ExitCode,
		string(errorJSON), job.PollCount, string(pheromonesJSON), string(pipelineJSON),
		job.PID, job.CreatedAt.Format(time.RFC3339), job.ProgressUpdatedAt.Format(time.RFC3339),
		completedAt,
	)
	return err
}

// SnapshotAll dumps all in-memory sessions and jobs to SQLite atomically.
func (s *Store) SnapshotAll(sessions *Registry, jobs *JobManager) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, sess := range sessions.List("") {
		metadataJSON, _ := json.Marshal(sess.Metadata)
		if _, execErr := tx.Exec(`
			INSERT OR REPLACE INTO sessions (id, cli, mode, cli_session_id, pid, status, turns, cwd, metadata_json, created_at, last_active_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sess.ID, sess.CLI, sess.Mode, sess.CLISessionID, sess.PID,
			sess.Status, sess.Turns, sess.CWD, string(metadataJSON),
			sess.CreatedAt.Format(time.RFC3339),
			sess.LastActiveAt.Format(time.RFC3339),
		); execErr != nil {
			return execErr
		}
	}

	for _, job := range jobs.ListRunning() {
		var errorJSON, pheromonesJSON, pipelineJSON []byte
		if job.Error != nil {
			errorJSON, _ = json.Marshal(job.Error)
		}
		if len(job.Pheromones) > 0 {
			pheromonesJSON, _ = json.Marshal(job.Pheromones)
		}
		if job.Pipeline != nil {
			pipelineJSON, _ = json.Marshal(job.Pipeline)
		}
		completedAt := ""
		if job.CompletedAt != nil {
			completedAt = job.CompletedAt.Format(time.RFC3339)
		}
		if _, execErr := tx.Exec(`
			INSERT OR REPLACE INTO jobs (id, session_id, cli, status, progress, content, exit_code,
				error_json, poll_count, pheromones_json, pipeline_json, pid, created_at, progress_updated_at, completed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			job.ID, job.SessionID, job.CLI, job.Status, job.Progress, job.Content, job.ExitCode,
			string(errorJSON), job.PollCount, string(pheromonesJSON), string(pipelineJSON),
			job.PID, job.CreatedAt.Format(time.RFC3339), job.ProgressUpdatedAt.Format(time.RFC3339),
			completedAt,
		); execErr != nil {
			return execErr
		}
	}

	return tx.Commit()
}

// RestoreJobs loads jobs from SQLite into the JobManager.
// Running jobs are marked as failed (process died mid-execution).
// Called once at server startup to survive process restarts.
// Non-terminal jobs are always restored; terminal historical rows are kept to a 1-hour window.
func (s *Store) RestoreJobs(jobs *JobManager) (int, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, cli, status, progress, content, exit_code,
		       error_json, poll_count, pheromones_json, pipeline_json, pid,
		       created_at, progress_updated_at, completed_at
		FROM jobs
		WHERE status NOT IN (?, ?)
		   OR (status IN (?, ?) AND unixepoch(created_at) > unixepoch('now', '-1 hour'))
		ORDER BY created_at DESC`,
		types.JobStatusCompleted, types.JobStatusFailed,
		types.JobStatusCompleted, types.JobStatusFailed,
	)
	if err != nil {
		return 0, fmt.Errorf("query jobs: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var (
			errorJSON, pheromonesJSON, pipelineJSON sql.NullString
			completedAtStr                          sql.NullString
			createdAtStr, progressUpdatedAtStr      string
		)

		j := &Job{}
		if err := rows.Scan(
			&j.ID, &j.SessionID, &j.CLI, &j.Status, &j.Progress, &j.Content, &j.ExitCode,
			&errorJSON, &j.PollCount, &pheromonesJSON, &pipelineJSON, &j.PID,
			&createdAtStr, &progressUpdatedAtStr, &completedAtStr,
		); err != nil {
			return count, fmt.Errorf("scan job row: %w", err)
		}

		if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
			j.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339, progressUpdatedAtStr); err == nil {
			j.ProgressUpdatedAt = t
		}
		if completedAtStr.Valid && completedAtStr.String != "" {
			if t, err := time.Parse(time.RFC3339, completedAtStr.String); err == nil {
				j.CompletedAt = &t
			}
		}

		if errorJSON.Valid && errorJSON.String != "" {
			var te TypedErrorJSON
			if err := json.Unmarshal([]byte(errorJSON.String), &te); err == nil {
				j.Error = &types.TypedError{
					Type:          types.ErrorType(te.Type),
					Message:       te.Message,
					PartialOutput: te.PartialOutput,
				}
			}
		}
		if pheromonesJSON.Valid && pheromonesJSON.String != "" {
			_ = json.Unmarshal([]byte(pheromonesJSON.String), &j.Pheromones)
		}
		if j.Pheromones == nil {
			j.Pheromones = make(map[string]string)
		}
		if pipelineJSON.Valid && pipelineJSON.String != "" {
			var ps types.PipelineStats
			if err := json.Unmarshal([]byte(pipelineJSON.String), &ps); err == nil {
				j.Pipeline = &ps
			}
		}

		// Running, created, or completing jobs all had their subprocess killed
		// when the server exited. Completing is a transient state between
		// Running and Completed — a restart interrupts the completion write.
		if j.Status == types.JobStatusRunning || j.Status == types.JobStatusCreated || j.Status == types.JobStatusCompleting {
			now := time.Now()
			j.Status = types.JobStatusFailed
			j.PID = 0
			j.CompletedAt = &now
			j.Error = &types.TypedError{
				Type:    types.ErrorTypeExecutor,
				Message: "process restarted",
			}
		}

		jobs.Restore(j)
		count++
	}
	if err := rows.Err(); err != nil {
		return count, fmt.Errorf("iterate job rows: %w", err)
	}
	return count, nil
}

// TypedErrorJSON is a helper for deserializing TypedError from SQLite (Cause field is not stored).
type TypedErrorJSON struct {
	Type          string `json:"type"`
	Message       string `json:"message"`
	PartialOutput string `json:"partial_output,omitempty"`
}

// Close closes the SQLite database.
func (s *Store) Close() error {
	return s.db.Close()
}
