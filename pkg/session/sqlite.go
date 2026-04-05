package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

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
	return err
}

// SnapshotSession upserts a session into SQLite.
func (s *Store) SnapshotSession(sess *Session) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO sessions (id, cli, mode, cli_session_id, pid, status, turns, cwd, created_at, last_active_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.CLI, sess.Mode, sess.CLISessionID, sess.PID,
		sess.Status, sess.Turns, sess.CWD,
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
		if _, execErr := tx.Exec(`
			INSERT OR REPLACE INTO sessions (id, cli, mode, cli_session_id, pid, status, turns, cwd, created_at, last_active_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sess.ID, sess.CLI, sess.Mode, sess.CLISessionID, sess.PID,
			sess.Status, sess.Turns, sess.CWD,
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

// Close closes the SQLite database.
func (s *Store) Close() error {
	return s.db.Close()
}
