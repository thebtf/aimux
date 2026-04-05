package logger_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/logger"
)

func TestLogger_BasicLogging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	log, err := logger.New(path, logger.LevelDebug)
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

	log, err := logger.New(path, logger.LevelWarn)
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

	log, err := logger.New(path, logger.LevelError)
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

	log, err := logger.New(path, logger.LevelWarn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer log.Close()

	if log.GetLevel() != logger.LevelWarn {
		t.Errorf("GetLevel = %v, want %v", log.GetLevel(), logger.LevelWarn)
	}
}
