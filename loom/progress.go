package loom

import (
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

// AppendProgress records a single progress line for taskID. The line is
// truncated UTF-8-safely to ≤100 bytes before storage; ProgressLines is
// incremented by 1 plus the count of embedded newlines (matches legacy
// JobManager semantics). ProgressUpdatedAt is set to time.Now().UTC().
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
// Returns an error only on SQL failure; no row affected (taskID unknown)
// returns nil — progress for an unknown task is a no-op rather than an
// error so a slow worker emitting after a Cancel does not surface a noisy
// failure.
func (s *TaskStore) AppendProgress(taskID, line string) error {
	// Compute the new tail. Embedded newlines are valid — split off the
	// last non-empty segment and truncate. If the entire line is whitespace,
	// keep the existing LastOutputLine (passed via COALESCE in SQL below).
	nextTail := lastNonEmptyLine(line)
	truncated := ""
	if nextTail != "" {
		truncated = truncateUTF8(nextTail, progressLineMaxBytes)
	}

	// 1 line per call + one per embedded newline (final \n with no trailing
	// content does not count an extra line — strings.Count counts the
	// separator). Matches legacy JobManager arithmetic (pkg/session/jobs.go).
	deltaLines := int64(1 + strings.Count(line, "\n"))

	now := time.Now().UTC()

	var res interface {
		RowsAffected() (int64, error)
	}
	var execErr error
	if truncated != "" {
		res, execErr = s.db.Exec(
			`UPDATE tasks
			 SET last_output_line = ?,
			     progress_lines = progress_lines + ?,
			     progress_updated_at = ?
			 WHERE id = ?`,
			truncated, deltaLines, now, taskID,
		)
	} else {
		// Whitespace-only line — preserve existing tail, bump counter + ts.
		res, execErr = s.db.Exec(
			`UPDATE tasks
			 SET progress_lines = progress_lines + ?,
			     progress_updated_at = ?
			 WHERE id = ?`,
			deltaLines, now, taskID,
		)
	}
	if execErr != nil {
		return fmt.Errorf("loom store: append progress: %w", execErr)
	}
	if _, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("loom store: append progress rows affected: %w", err)
	}
	return nil
}
