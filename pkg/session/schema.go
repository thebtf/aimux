package session

import (
	"database/sql"
	"fmt"
	"strings"
)

// migrateV2 applies schema migration v1 → v2:
//   - sessions: ADD daemon_uuid TEXT, aborted_at TEXT
//   - jobs:     ADD daemon_uuid TEXT, last_seen_at TEXT, aborted_at TEXT
//
// The migration is atomic (single transaction). The schema_version table
// used by migrate() in sqlite.go tracks version numbers; v2 adds row 2.
//
// Callers: migrate() in sqlite.go calls this when version < 2.
// The three columns are NULLABLE — existing rows are not touched.
func migrateV2(tx *sql.Tx) error {
	alters := []struct {
		stmt string
		desc string
	}{
		{`ALTER TABLE sessions ADD COLUMN daemon_uuid TEXT`, "sessions.daemon_uuid"},
		{`ALTER TABLE sessions ADD COLUMN aborted_at TEXT`, "sessions.aborted_at"},
		{`ALTER TABLE jobs ADD COLUMN daemon_uuid TEXT`, "jobs.daemon_uuid"},
		{`ALTER TABLE jobs ADD COLUMN last_seen_at TEXT`, "jobs.last_seen_at"},
		{`ALTER TABLE jobs ADD COLUMN aborted_at TEXT`, "jobs.aborted_at"},
	}

	for _, a := range alters {
		if _, err := tx.Exec(a.stmt); err != nil {
			// "duplicate column name" means the column already exists — safe to skip.
			// This can happen when migration ran partially before a crash.
			if strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
			return fmt.Errorf("migrateV2: %s: %w", a.desc, err)
		}
	}

	if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (2)`); err != nil {
		return fmt.Errorf("migrateV2: bump schema_version to 2: %w", err)
	}
	return nil
}

// migrateV2point1 applies schema migration v2 → v3:
//   - sessions: ADD aborted_job_ids TEXT (JSON array of aborted job IDs)
//
// Callers: migrate() in sqlite.go calls this when version < 3.
func migrateV2point1(tx *sql.Tx) error {
	if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN aborted_job_ids TEXT`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("migrateV2point1: sessions.aborted_job_ids: %w", err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (3)`); err != nil {
		return fmt.Errorf("migrateV2point1: bump schema_version to 3: %w", err)
	}
	return nil
}
