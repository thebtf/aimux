package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/thebtf/aimux/pkg/types"
)

// ReconcileOnStartup scans for rows owned by a different daemon UUID and
// reconciles orphaned running jobs into aborted state in one transaction.
func ReconcileOnStartup(ctx context.Context, db *sql.DB, currentUUID string) (orphanedJobIDs []string, orphanedSessionIDs []string, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("reconcile begin tx: %w", err)
	}
	defer tx.Rollback()

	orphanedJobIDs, err = scanIDs(ctx, tx, `SELECT id FROM jobs WHERE daemon_uuid != ? OR daemon_uuid IS NULL`, currentUUID)
	if err != nil {
		return nil, nil, fmt.Errorf("reconcile scan orphaned jobs: %w", err)
	}
	orphanedSessionIDs, err = scanIDs(ctx, tx, `SELECT id FROM sessions WHERE daemon_uuid != ? OR daemon_uuid IS NULL`, currentUUID)
	if err != nil {
		return nil, nil, fmt.Errorf("reconcile scan orphaned sessions: %w", err)
	}

	abortedBySession := map[string][]string{}
	if len(orphanedJobIDs) > 0 {
		placeholders := sqlPlaceholders(len(orphanedJobIDs))
		selectRunningSQL := fmt.Sprintf(`SELECT id, session_id FROM jobs WHERE id IN (%s) AND status = ?`, placeholders)
		args := make([]any, 0, len(orphanedJobIDs)+1)
		for _, id := range orphanedJobIDs {
			args = append(args, id)
		}
		args = append(args, string(types.JobStatusRunning))

		runningRows, queryErr := tx.QueryContext(ctx, selectRunningSQL, args...)
		if queryErr != nil {
			return nil, nil, fmt.Errorf("reconcile select orphaned running jobs: %w", queryErr)
		}
		for runningRows.Next() {
			var jobID string
			var sessionID string
			if scanErr := runningRows.Scan(&jobID, &sessionID); scanErr != nil {
				runningRows.Close()
				return nil, nil, fmt.Errorf("reconcile scan orphaned running job: %w", scanErr)
			}
			abortedBySession[sessionID] = append(abortedBySession[sessionID], jobID)
		}
		if rowsErr := runningRows.Err(); rowsErr != nil {
			runningRows.Close()
			return nil, nil, fmt.Errorf("reconcile iterate orphaned running jobs: %w", rowsErr)
		}
		if closeErr := runningRows.Close(); closeErr != nil {
			return nil, nil, fmt.Errorf("reconcile close orphaned running job rows: %w", closeErr)
		}

		updateSQL := fmt.Sprintf(
			`UPDATE jobs SET status = ?, aborted_at = datetime('now') WHERE id IN (%s) AND status = ?`,
			placeholders,
		)
		updateArgs := make([]any, 0, len(orphanedJobIDs)+2)
		updateArgs = append(updateArgs, string(types.JobStatusAborted))
		for _, id := range orphanedJobIDs {
			updateArgs = append(updateArgs, id)
		}
		updateArgs = append(updateArgs, string(types.JobStatusRunning))
		if _, updateErr := tx.ExecContext(ctx, updateSQL, updateArgs...); updateErr != nil {
			return nil, nil, fmt.Errorf("reconcile abort orphaned running jobs: %w", updateErr)
		}
	}

	for sessionID, jobIDs := range abortedBySession {
		sort.Strings(jobIDs)
		jobIDsJSON, marshalErr := json.Marshal(jobIDs)
		if marshalErr != nil {
			return nil, nil, fmt.Errorf("reconcile marshal aborted job ids for session %s: %w", sessionID, marshalErr)
		}

		rows, queryErr := tx.QueryContext(ctx, `SELECT status, COUNT(*) FROM jobs WHERE session_id = ? GROUP BY status`, sessionID)
		if queryErr != nil {
			return nil, nil, fmt.Errorf("reconcile session rollup stats %s: %w", sessionID, queryErr)
		}

		totalJobs := 0
		abortedJobs := 0
		activeJobs := 0
		for rows.Next() {
			var status string
			var count int
			if scanErr := rows.Scan(&status, &count); scanErr != nil {
				rows.Close()
				return nil, nil, fmt.Errorf("reconcile session rollup scan %s: %w", sessionID, scanErr)
			}
			totalJobs += count
			if status == string(types.JobStatusAborted) {
				abortedJobs += count
			}
			if status == string(types.JobStatusCreated) || status == string(types.JobStatusRunning) || status == string(types.JobStatusCompleting) {
				activeJobs += count
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("reconcile session rollup iterate %s: %w", sessionID, rowsErr)
		}
		if closeErr := rows.Close(); closeErr != nil {
			return nil, nil, fmt.Errorf("reconcile session rollup close %s: %w", sessionID, closeErr)
		}

		shouldAbortSession := totalJobs > 0 && abortedJobs > 0 && activeJobs == 0
		if shouldAbortSession {
			if _, execErr := tx.ExecContext(
				ctx,
				`UPDATE sessions SET aborted_at = datetime('now'), aborted_job_ids = ?, status = ? WHERE id = ?`,
				string(jobIDsJSON),
				string(types.SessionStatusAborted),
				sessionID,
			); execErr != nil {
				return nil, nil, fmt.Errorf("reconcile update aborted session %s: %w", sessionID, execErr)
			}
			continue
		}

		if _, execErr := tx.ExecContext(
			ctx,
			`UPDATE sessions SET aborted_at = datetime('now'), aborted_job_ids = ? WHERE id = ?`,
			string(jobIDsJSON),
			sessionID,
		); execErr != nil {
			return nil, nil, fmt.Errorf("reconcile update session rollup %s: %w", sessionID, execErr)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("reconcile commit tx: %w", err)
	}
	return orphanedJobIDs, orphanedSessionIDs, nil
}

func scanIDs(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func sqlPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}
