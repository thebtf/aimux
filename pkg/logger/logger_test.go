package logger_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/logger"
)

// TestLogger_RotatesAtMaxSize verifies that lumberjack creates a backup file
// once the active log file exceeds MaxSizeMB.
func TestLogger_RotatesAtMaxSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aimux.log")

	// MaxSizeMB: 1 triggers rotation after 1 MB.
	log, err := logger.New(path, logger.LevelDebug, logger.RotationOpts{
		MaxSizeMB:  1,
		MaxBackups: 2,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Write enough data to exceed 1 MB. Each line is ~110 bytes; 10 000 lines ≈ 1.1 MB.
	// Channel buffer is 1024 — write in chunks with brief pauses so drain keeps up
	// without dropping entries (otherwise rotation never fires on heavy bursts).
	payload := strings.Repeat("x", 80)
	for chunk := 0; chunk < 100; chunk++ {
		for i := 0; i < 100; i++ {
			log.Info("rotation-test %05d %s", chunk*100+i, payload)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A backup file is created alongside the active log file. It matches aimux-YYYY-…log.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	backupFound := false
	for _, e := range entries {
		name := e.Name()
		// Lumberjack names backups like: aimux-2006-01-02T15-04-05.000.log
		if strings.HasPrefix(name, "aimux-") && strings.HasSuffix(name, ".log") {
			backupFound = true
			break
		}
	}

	if !backupFound {
		t.Errorf("expected a rotated backup file in %s, found none; entries: %v", dir, entries)
	}
}

// TestLogger_TruncatesLongLine verifies that lines exceeding MaxLineBytes are
// truncated and the "[truncated" marker is appended.
func TestLogger_TruncatesLongLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trunc.log")

	log, err := logger.New(path, logger.LevelDebug, logger.RotationOpts{
		MaxLineBytes: 100,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Log a message whose formatted line will be much longer than 100 bytes.
	log.Info("%s", strings.Repeat("A", 5000))

	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("no lines written to log")
	}

	line := lines[0]
	if len(line) > 100 {
		t.Errorf("line length %d exceeds MaxLineBytes 100; line=%q", len(line), line[:min(len(line), 120)])
	}
	if !strings.Contains(line, "...[truncated") {
		t.Errorf("expected truncation marker in line; got: %q", line)
	}
}

// TestLogger_RotationOptsZeroNoRotation verifies that RotationOpts{} (all zeros)
// produces no backup files for small writes — confirming the zero-value preserves
// old behavior (no rotation on small writes).
func TestLogger_RotationOptsZeroNoRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aimux.log")

	log, err := logger.New(path, logger.LevelDebug, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	log.Info("entry one")
	log.Info("entry two")
	log.Info("entry three")

	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "aimux-") && strings.HasSuffix(name, ".log") {
			t.Errorf("unexpected backup file %q with zero RotationOpts and tiny write", name)
		}
	}
}

func TestLogger_BasicLogging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	log, err := logger.New(path, logger.LevelDebug, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	log.Debug("debug message %d", 1)
	log.Info("info message %s", "test")
	log.Warn("warn message")
	log.Error("error message")

	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "[DEBUG] debug message 1") {
		t.Error("missing debug message")
	}
	if !strings.Contains(content, "[INFO] info message test") {
		t.Error("missing info message")
	}
	if !strings.Contains(content, "[WARN] warn message") {
		t.Error("missing warn message")
	}
	if !strings.Contains(content, "[ERROR] error message") {
		t.Error("missing error message")
	}
}

func TestLogger_LevelFiltering(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	log, err := logger.New(path, logger.LevelWarn, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	log.Debug("should not appear")
	log.Info("should not appear")
	log.Warn("should appear")
	log.Error("should appear")

	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)
	if strings.Contains(content, "should not appear") {
		t.Error("debug/info messages should be filtered out")
	}
	if !strings.Contains(content, "[WARN] should appear") {
		t.Error("warn message should be present")
	}
	if !strings.Contains(content, "[ERROR] should appear") {
		t.Error("error message should be present")
	}
}

func TestLogger_SetLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	log, err := logger.New(path, logger.LevelError, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	log.Info("before level change")

	log.SetLevel(logger.LevelInfo)

	log.Info("after level change")

	// Give async writer time to flush
	time.Sleep(10 * time.Millisecond)

	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)
	if strings.Contains(content, "before level change") {
		t.Error("message before level change should be filtered")
	}
	if !strings.Contains(content, "after level change") {
		t.Error("message after level change should be present")
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  logger.Level
	}{
		{"debug", logger.LevelDebug},
		{"DEBUG", logger.LevelDebug},
		{"info", logger.LevelInfo},
		{"warn", logger.LevelWarn},
		{"warning", logger.LevelWarn},
		{"error", logger.LevelError},
		{"ERROR", logger.LevelError},
		{"unknown", logger.LevelInfo}, // default
	}

	for _, tt := range tests {
		got := logger.ParseLevel(tt.input)
		if got != tt.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestLogger_GetLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	log, err := logger.New(path, logger.LevelWarn, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer log.Close()

	if log.GetLevel() != logger.LevelWarn {
		t.Errorf("GetLevel = %v, want %v", log.GetLevel(), logger.LevelWarn)
	}
}
