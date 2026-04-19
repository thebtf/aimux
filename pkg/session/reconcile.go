package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

// ReconcileOnStartup scans for rows owned by a different daemon UUID and
// reconciles orphaned running jobs into aborted state in one transaction.
//
// Implementation uses set-based SQL (filter by daemon_uuid directly) to avoid
// building large IN-clause lists that would exceed SQLite's SQLITE_LIMIT_VARIABLE_NUMBER
// (default 999). All timestamps are passed as Go-formatted RFC3339 strings to
// ensure consistency with time.Parse(time.RFC3339, ...) in RestoreJobs.
//
// Returns:
//   - orphanedJobIDs: IDs of all orphaned jobs found (any status).
//   - orphanedSessionIDs: IDs of all orphaned sessions found.
//   - abortedJobCount: number of jobs actually transitioned to "aborted" this run
//     (subset of orphaned jobs that were in running state). Used for accurate logging.
//   - err: first error encountered, if any.
func ReconcileOnStartup(ctx context.Context, db *sql.DB, currentUUID string) (orphanedJobIDs []string, orphanedSessionIDs []string, abortedJobCount int, err error) {
	tx, txErr := db.BeginTx(ctx, nil)
	if txErr != nil {
		return nil, nil, 0, fmt.Errorf("reconcile begin tx: %w", txErr)
	}
	defer tx.Rollback()

	// Collect orphaned IDs for the return value (callers use these for logging).
	// These queries are unbounded reads — we only store IDs (strings), which is
	// acceptable since the caller is informed of the count via the return value.
	orphanedJobIDs, err = scanIDs(ctx, tx, `SELECT id FROM jobs WHERE (daemon_uuid != ? OR daemon_uuid IS NULL)`, currentUUID)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("reconcile scan orphaned jobs: %w", err)
	}
	orphanedSessionIDs, err = scanIDs(ctx, tx, `SELECT id FROM sessions WHERE (daemon_uuid != ? OR daemon_uuid IS NULL)`, currentUUID)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("reconcile scan orphaned sessions: %w", err)
	}

	// Step 1: Collect the session_id + job_id of every orphaned running job using
	// a set-based query (no IN clause — filters by daemon_uuid directly).
	// This avoids the SQLITE_LIMIT_VARIABLE_NUMBER=999 ceiling on IN lists.
	//
	// Note: runningRows is closed explicitly (not deferred) because modernc/sqlite
	// uses MaxOpenConns(1). An open *sql.Rows on the same connection would block
	// subsequent queries (the UPDATE in Step 2 and rollup query in Step 3).
	abortedBySession := map[string][]string{}
	{
		runningRows, queryErr := tx.QueryContext(ctx,
			`SELECT id, session_id FROM jobs
			  WHERE (daemon_uuid != ? OR daemon_uuid IS NULL)
			    AND status = ?`,
			currentUUID, string(types.JobStatusRunning),
		)
		if queryErr != nil {
			return nil, nil, 0, fmt.Errorf("reconcile select orphaned running jobs: %w", queryErr)
		}

		for runningRows.Next() {
			var jobID, sessionID string
			if scanErr := runningRows.Scan(&jobID, &sessionID); scanErr != nil {
				runningRows.Close()
				return nil, nil, 0, fmt.Errorf("reconcile scan orphaned running job: %w", scanErr)
			}
			abortedBySession[sessionID] = append(abortedBySession[sessionID], jobID)
		}
		iterErr := runningRows.Err()
		runningRows.Close()
		if iterErr != nil {
			return nil, nil, 0, fmt.Errorf("reconcile iterate orphaned running jobs: %w", iterErr)
		}
	}

	// Step 2: Abort all orphaned running jobs in one set-based UPDATE.
	// Pass the current time as a Go-formatted RFC3339 string so that RestoreJobs
	// (which uses time.Parse(time.RFC3339, ...)) can parse it correctly.
	// SQLite's datetime('now') returns "YYYY-MM-DD HH:MM:SS" without T/Z, which
	// is not RFC3339 and would silently produce zero-value timestamps on restore.
	nowRFC3339 := time.Now().UTC().Format(time.RFC3339)
	if len(abortedBySession) > 0 {
		result, updateErr := tx.ExecContext(ctx,
			`UPDATE jobs SET status = ?, aborted_at = ?
			  WHERE (daemon_uuid != ? OR daemon_uuid IS NULL)
			    AND status = ?`,
			string(types.JobStatusAborted), nowRFC3339,
			currentUUID, string(types.JobStatusRunning),
		)
		if updateErr != nil {
			return nil, nil, 0, fmt.Errorf("reconcile abort orphaned running jobs: %w", updateErr)
		}
		if n, rowsErr := result.RowsAffected(); rowsErr == nil {
			abortedJobCount = int(n)
		}
	}

	// Step 3: Session rollup — fetch status counts for ALL affected sessions in
	// one query (avoids N+1 per-session queries when many sessions are affected).
	if len(abortedBySession) > 0 {
		type sessionStats struct {
			total   int
			aborted int
			active  int
		}
		statsMap := map[string]*sessionStats{}
		for sid := range abortedBySession {
			statsMap[sid] = &sessionStats{}
		}

		// Collect the unique affected session IDs into a slice for the IN filter.
		// The affected set is bounded by the number of unique sessions that had
		// running jobs — typically small (bounded by active CLI processes at
		// crash time, not by total historical job count). This keeps the IN clause
		// safe under the 999-variable limit.
		affectedSessionIDs := make([]string, 0, len(abortedBySession))
		for sid := range abortedBySession {
			affectedSessionIDs = append(affectedSessionIDs, sid)
		}

		placeholders := sqlPlaceholders(len(affectedSessionIDs))
		rollupSQL := fmt.Sprintf(
			`SELECT session_id, status, COUNT(*) FROM jobs WHERE session_id IN (%s) GROUP BY session_id, status`,
			placeholders,
		)
		rollupArgs := make([]any, len(affectedSessionIDs))
		for i, sid := range affectedSessionIDs {
			rollupArgs[i] = sid
		}

		// rollupRows is also closed explicitly — the session UPDATE queries in
		// Step 4 use the same single-connection transaction.
		rollupRows, rollupErr := tx.QueryContext(ctx, rollupSQL, rollupArgs...)
		if rollupErr != nil {
			return nil, nil, 0, fmt.Errorf("reconcile session rollup stats: %w", rollupErr)
		}

		for rollupRows.Next() {
			var sessID, status string
			var count int
			if scanErr := rollupRows.Scan(&sessID, &status, &count); scanErr != nil {
				rollupRows.Close()
				return nil, nil, 0, fmt.Errorf("reconcile session rollup scan: %w", scanErr)
			}
			st, ok := statsMap[sessID]
			if !ok {
				continue
			}
			st.total += count
			if status == string(types.JobStatusAborted) {
				st.aborted += count
			}
			if status == string(types.JobStatusCreated) || status == string(types.JobStatusRunning) || status == string(types.JobStatusCompleting) {
				st.active += count
			}
		}
		rollupIterErr := rollupRows.Err()
		rollupRows.Close()
		if rollupIterErr != nil {
			return nil, nil, 0, fmt.Errorf("reconcile session rollup iterate: %w", rollupIterErr)
		}

		// Step 4: Update each affected session based on the rollup stats.
		//
		// aborted_job_ids is set to the jobs aborted in THIS reconciliation run.
		// A session appears in abortedBySession only when it had running jobs to
		// abort — once fully reconciled, no running jobs remain, so subsequent
		// daemon restarts never revisit the same session. No prior-run data is
		// overwritten in practice (the invariant is enforced by the daemon_uuid
		// ownership model: a session owned by daemon A is either still active on A
		// or fully aborted; it cannot have running jobs when daemon B starts).
		for sessionID, jobIDs := range abortedBySession {
			sort.Strings(jobIDs)
			jobIDsJSON, marshalErr := json.Marshal(jobIDs)
			if marshalErr != nil {
				return nil, nil, 0, fmt.Errorf("reconcile marshal aborted job ids for session %s: %w", sessionID, marshalErr)
			}

			st := statsMap[sessionID]
			shouldAbortSession := st != nil && st.total > 0 && st.aborted > 0 && st.active == 0
			if shouldAbortSession {
				if _, execErr := tx.ExecContext(ctx,
					`UPDATE sessions SET aborted_at = ?, aborted_job_ids = ?, status = ? WHERE id = ?`,
					nowRFC3339, string(jobIDsJSON),
					string(types.SessionStatusAborted),
					sessionID,
				); execErr != nil {
					return nil, nil, 0, fmt.Errorf("reconcile update aborted session %s: %w", sessionID, execErr)
				}
				continue
			}

			if _, execErr := tx.ExecContext(ctx,
				`UPDATE sessions SET aborted_at = ?, aborted_job_ids = ? WHERE id = ?`,
				nowRFC3339, string(jobIDsJSON),
				sessionID,
			); execErr != nil {
				return nil, nil, 0, fmt.Errorf("reconcile update session rollup %s: %w", sessionID, execErr)
			}
		}
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return nil, nil, 0, fmt.Errorf("reconcile commit tx: %w", commitErr)
	}
	return orphanedJobIDs, orphanedSessionIDs, abortedJobCount, nil
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
