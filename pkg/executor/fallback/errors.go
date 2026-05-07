package fallback

import (
	"errors"
	"fmt"
	"strings"
)

// FailedAttempt records one failed CLI dispatch attempt.
// Included in ErrAllFallbackExhausted and in successful Result.FailedAttempts.
type FailedAttempt struct {
	// CLI is the name of the CLI that was attempted (e.g., "codex").
	CLI string
	// Code is the string representation of the CLIErrorCode.
	Code string
	// Message is the human-readable error message from the CLI worker.
	Message string
}

// ErrAllFallbackExhausted is returned when all fallback candidates have been
// attempted and all failed with eligible errors. It carries a full per-CLI
// failure breakdown for debugging and MCP surface reporting (spec FR-9, ADR-005).
//
// It implements the error interface and can be identified with errors.As.
type ErrAllFallbackExhausted struct {
	// Attempts lists every CLI that was tried, in order.
	Attempts []FailedAttempt
	// SkippedNote explains why any CLIs were skipped (e.g., "health check failed").
	// Empty if no CLIs were skipped.
	SkippedNote string
}

// Error implements the error interface.
func (e *ErrAllFallbackExhausted) Error() string {
	if len(e.Attempts) == 0 {
		return "ERR_ALL_FALLBACK_EXHAUSTED: no CLIs were attempted"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("ERR_ALL_FALLBACK_EXHAUSTED: all %d CLI(s) failed", len(e.Attempts)))
	for _, a := range e.Attempts {
		sb.WriteString(fmt.Sprintf("; %s: %s (%s)", a.CLI, a.Code, a.Message))
	}
	if e.SkippedNote != "" {
		sb.WriteString(fmt.Sprintf("; skipped: %s", e.SkippedNote))
	}
	return sb.String()
}

// IsExhausted reports whether err is (or wraps) an ErrAllFallbackExhausted.
// Convenience helper so callers do not need to import errors.As directly.
func IsExhausted(err error) bool {
	var target *ErrAllFallbackExhausted
	return errors.As(err, &target)
}
