// Package logger: StderrFallback writes log entries synchronously to os.Stderr.
// Used (a) before IPCSink establishes the daemon connection (bootstrap window),
// and (b) when IPCSink send fails after timeout (degradation path).
// (T013 — AIMUX-11 Phase 3, FR-4)
package logger

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// StderrFallback is a synchronous writer to os.Stderr. Format:
//
//	[stderr-fallback] <RFC3339Nano> [LEVEL] <message>\n
//
// Stderr is captured by Claude Code and surfaces to the operator during outages.
// No data loss on send: every Write either lands in stderr or returns an error.
type StderrFallback struct {
	mu sync.Mutex
	w  io.Writer // defaults to os.Stderr; injectable for tests
}

// NewStderrFallback returns a StderrFallback writing to os.Stderr.
func NewStderrFallback() *StderrFallback {
	return &StderrFallback{w: os.Stderr}
}

// newStderrFallbackWith returns a StderrFallback writing to a custom writer (tests).
func newStderrFallbackWith(w io.Writer) *StderrFallback {
	return &StderrFallback{w: w}
}

// Write emits a single line for the given log entry to stderr.
// Format: "[stderr-fallback] <time RFC3339Nano> [LEVEL] <message>\n".
func (s *StderrFallback) Write(level Level, t time.Time, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.w, "[stderr-fallback] %s [%s] %s\n",
		t.Format(time.RFC3339Nano), level.String(), message)
}

// WriteEntry emits a LogEntry value (convenience for IPCSink fallback path).
func (s *StderrFallback) WriteEntry(e LogEntry) {
	s.Write(e.Level, e.Time, e.Message)
}
