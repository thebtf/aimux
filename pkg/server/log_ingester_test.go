// Package server: tests for LogIngester (T009 — AIMUX-11 Phase 1).
package server_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/server"
)

// newTestIngester builds a LogIngester backed by a file sink for assertions.
// Returns the ingester and a function to close the sink and read the log file.
func newTestIngester(t *testing.T, maxLineBytes int) (*server.LogIngester, func() string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	log, err := logger.NewDaemon(path, logger.LevelDebug, logger.RotationOpts{MaxLineBytes: maxLineBytes})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	ingester := server.NewLogIngester(log.LocalSink(), maxLineBytes)

	readLog := func() string {
		if err := log.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return ""
			}
			t.Fatalf("ReadFile: %v", err)
		}
		return string(data)
	}
	return ingester, readLog
}

// TestLogIngester_Receive_FormatCheck verifies that forwarded entries are written
// with the [shim-pid-sess] prefix format.
func TestLogIngester_Receive_FormatCheck(t *testing.T) {
	ingester, readLog := newTestIngester(t, 0)

	envelopes := []logger.LogEntry{
		{Level: logger.LevelInfo, Time: time.Now(), Message: "message one"},
		{Level: logger.LevelWarn, Time: time.Now(), Message: "message two"},
		{Level: logger.LevelError, Time: time.Now(), Message: "message three"},
	}

	// peerPid=12345, sess="512e062d" — the line must contain [shim-12345-512e062d].
	for _, e := range envelopes {
		if err := ingester.Receive(e, 12345, "512e062d"); err != nil {
			t.Fatalf("Receive: %v", err)
		}
	}

	content := readLog()

	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d; content=%q", len(lines), content)
	}

	for i, line := range lines[:3] {
		if !strings.Contains(line, "[shim-12345-512e062d]") {
			t.Errorf("line %d missing [shim-12345-512e062d]: %q", i, line)
		}
		if !strings.Contains(line, envelopes[i].Message) {
			t.Errorf("line %d missing message %q: %q", i, envelopes[i].Message, line)
		}
	}
}

// TestLogIngester_Sanitization verifies that newline characters in messages are
// replaced with the escape sequence so the log remains single-line-per-entry.
func TestLogIngester_Sanitization(t *testing.T) {
	ingester, readLog := newTestIngester(t, 0)

	e := logger.LogEntry{
		Level:   logger.LevelInfo,
		Time:    time.Now(),
		Message: "hello\nworld",
	}
	if err := ingester.Receive(e, 42, "testsess"); err != nil {
		t.Fatalf("Receive: %v", err)
	}

	content := readLog()

	// Must be exactly 1 log line (the trailing \n from format is excluded from counting).
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d; content=%q", len(lines), content)
	}

	if !strings.Contains(content, `hello\nworld`) {
		t.Errorf("expected escaped newline 'hello\\nworld' in output; got: %q", content)
	}

	if strings.Contains(content, "hello\nworld") {
		t.Error("raw newline must not appear in log output")
	}
}

// TestLogIngester_OversizeRejected verifies that messages exceeding maxLineBytes
// are rejected and the counter incremented.
func TestLogIngester_OversizeRejected(t *testing.T) {
	ingester, readLog := newTestIngester(t, 100)

	bigMsg := strings.Repeat("X", 200)
	e := logger.LogEntry{Level: logger.LevelInfo, Time: time.Now(), Message: bigMsg}
	if err := ingester.Receive(e, 1, "sess"); err == nil {
		t.Fatal("expected error for oversized message, got nil")
	}
	if ingester.EnvelopeMalformed.Load() != 1 {
		t.Errorf("EnvelopeMalformed counter = %d, want 1", ingester.EnvelopeMalformed.Load())
	}

	// Log file should be empty since the oversized entry was rejected.
	content := readLog()
	if strings.TrimSpace(content) != "" {
		t.Errorf("log file should be empty after rejected entry; got: %q", content)
	}
}
