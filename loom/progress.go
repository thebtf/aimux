package loom

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// progressLineMaxBytes caps LastOutputLine at 100 UTF-8 bytes to bound the
// SQL row size and keep the MCP status payload compact. Picked to match the
// legacy JobManager semantics (DEF-13) so callers comparing the two
// representations see identical truncation behaviour.
const progressLineMaxBytes = 100

// truncateUTF8 returns s truncated to at most maxBytes UTF-8 bytes without
// splitting a multi-byte codepoint. Mirrors pkg/util.TruncateUTF8 — duplicated
// here because loom is a separate Go module and cannot import pkg/util.
//
//   - len(result) <= maxBytes
//   - result is always valid UTF-8 (never produces mojibake)
//   - ASCII inputs are handled identically to multi-byte inputs
func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	boundary := 0
	for boundary < maxBytes {
		_, size := utf8.DecodeRuneInString(s[boundary:])
		if boundary+size > maxBytes {
			break
		}
		boundary += size
	}
	return s[:boundary]
}

// lastNonEmptyLine returns the last non-whitespace-only segment when s is
// split by newlines. Returns "" when s is empty or contains only whitespace.
// Walks the string in reverse via strings.LastIndex to avoid allocating a
// []string for the whole input — O(n) worst case, no per-line allocations.
func lastNonEmptyLine(s string) string {
	for {
		idx := strings.LastIndex(s, "\n")
		line := s[idx+1:]
		if strings.TrimSpace(line) != "" {
			return line
		}
		if idx == -1 {
			break
		}
		s = s[:idx]
	}
	return ""
}

// ProgressInfo carries the task identity fields required to emit a fully-
// populated EventTaskProgress. Returned from TaskStore.AppendProgress when
// (and only when) a row was actually updated — callers use OK to decide
// whether to emit an event so unknown / cancelled tasks remain truly no-op.
type ProgressInfo struct {
	OK        bool
	ProjectID string
	RequestID string
}

// AppendProgress records a single progress line for taskID. The line is
// truncated UTF-8-safely to ≤100 bytes before storage; ProgressLines is
// incremented by 1 plus the count of embedded newlines (matches legacy
// JobManager semantics — see pkg/session/jobs.go:242 which uses the same
// `1 + strings.Count(line, "\n")` formula). ProgressUpdatedAt is set to
// time.Now().UTC().
//
// Sensitive content (API keys, bearer tokens, Authorization headers) is
// scrubbed from the tail via redactErrorMsg before storage so the field —
// surfaced through the MCP status response as last_output_line — cannot
// leak credentials echoed by a CLI tool into its own progress stream.
// Same secret pattern set as tasks.error redaction (store.go).
//
// Whitespace-only lines do NOT update LastOutputLine — the previous value
// is preserved so the operator-visible tail remains the most recent
// signal-bearing line. ProgressLines and ProgressUpdatedAt are always
// updated so callers polling for activity-since timestamps still see motion.
//
// Concurrency: SQLite WAL mode + per-row UPDATE serialises writes against
// the same task ID at the engine level. Callers MAY invoke AppendProgress
// from arbitrary goroutines without external locking (EC-5.3).
//
// Returns ProgressInfo{OK: true, ProjectID, RequestID} when the row was
// updated and an event should be emitted with those identity fields. Returns
// ProgressInfo{OK: false} (and a nil error) when the task no longer exists
// — progress for an unknown / cancelled task is a no-op rather than an
// error so a slow worker emitting after a Cancel does not surface a noisy
// failure, and the caller does NOT emit an event for a row that was never
// updated. Returns a non-nil error only on SQL failure.
func (s *TaskStore) AppendProgress(taskID, line string) (ProgressInfo, error) {
	// Compute the new tail. Embedded newlines are valid — split off the
	// last non-empty segment, redact secrets, and truncate. If the entire
	// line is whitespace, keep the existing LastOutputLine (the WHERE-
	// branch below skips the last_output_line column entirely).
	nextTail := lastNonEmptyLine(line)
	truncated := ""
	if nextTail != "" {
		truncated = truncateUTF8(redactErrorMsg(nextTail), progressLineMaxBytes)
	}

	// 1 line per call + one per embedded newline. Matches legacy JobManager
	// arithmetic exactly (pkg/session/jobs.go:242) so callers comparing the
	// two representations see identical counts; that parity is an explicit
	// CR-005 NFR. A trailing "\n" therefore counts as a separator AND adds 1
	// — same as the legacy code, by design.
	deltaLines := int64(1 + strings.Count(line, "\n"))

	now := time.Now().UTC()

	// SQLite UPDATE ... RETURNING (>= 3.35.0; modernc.org/sqlite v1.48.1
	// supports it) lets a single round-trip both apply the update AND read
	// project_id / request_id from the row that was actually touched. When
	// the WHERE clause matches no rows the RETURNING set is empty and
	// QueryRow().Scan() reports sql.ErrNoRows, which we translate to the
	// no-op return. This replaces the prior Exec + RowsAffected pattern
	// where the unknown-task branch was indistinguishable from a successful
	// update at the caller.
	var (
		row    *sql.Row
		projID string
		reqID  string
	)
	if truncated != "" {
		row = s.db.QueryRow(
			`UPDATE tasks
			 SET last_output_line = ?,
			     progress_lines = progress_lines + ?,
			     progress_updated_at = ?
			 WHERE id = ?
			 RETURNING project_id, request_id`,
			truncated, deltaLines, now, taskID,
		)
	} else {
		// Whitespace-only line — preserve existing tail, bump counter + ts.
		row = s.db.QueryRow(
			`UPDATE tasks
			 SET progress_lines = progress_lines + ?,
			     progress_updated_at = ?
			 WHERE id = ?
			 RETURNING project_id, request_id`,
			deltaLines, now, taskID,
		)
	}
	if err := row.Scan(&projID, &reqID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProgressInfo{OK: false}, nil
		}
		return ProgressInfo{}, fmt.Errorf("loom store: append progress: %w", err)
	}
	return ProgressInfo{OK: true, ProjectID: projID, RequestID: reqID}, nil
}
