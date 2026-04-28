// Package server: T012 — HandleNotification dispatch test for log_forward.
// Verifies that aimuxHandler correctly routes "notifications/aimux/log_forward"
// to LogIngester, increments observability counters, and writes the entry to the
// daemon LocalSink with the expected [shim-?<id>-<sess>] format (FR-12, NFR-8).
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/mcp-mux/muxcore"
	"regexp"
)

// buildNotificationTestServer constructs a Server with a real log file so we can
// verify that HandleNotification actually writes lines to disk.
// Returns (server, logPath, cleanup).
func buildNotificationTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")

	cfg := &config.Config{
		Server: config.ServerConfig{
			LogLevel:              "debug",
			LogFile:               logPath,
			DefaultTimeoutSeconds: 10,
			LogMaxLineBytes:       0,
		},
		Roles: map[string]types.RolePreference{
			"default": {CLI: "echo-cli"},
		},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: 3,
			CooldownSeconds:  5,
			HalfOpenMaxCalls: 1,
		},
		CLIProfiles: map[string]*config.CLIProfile{
			"echo-cli": {
				Name:           "echo-cli",
				Binary:         testBinary(),
				TimeoutSeconds: 10,
				Capabilities:   []string{"default"},
			},
		},
	}

	log, err := logger.NewDaemon(logPath, logger.LevelDebug, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("NewDaemon logger: %v", err)
	}

	reg := driver.NewRegistry(cfg.CLIProfiles)
	router := routing.NewRouterWithProfiles(cfg.Roles, reg.EnabledCLIs(), cfg.CLIProfiles)

	srv := New(cfg, log, reg, router)
	if srv == nil {
		t.Fatal("server.New returned nil")
	}
	t.Cleanup(func() {
		srv.Shutdown()
		_ = log.Close()
	})
	return srv, logPath
}

// buildLogForwardNotification builds the raw JSON-RPC notification bytes for
// "notifications/aimux/log_forward" with the given LogEntry as params.
func buildLogForwardNotification(t *testing.T, entry logger.LogEntry) []byte {
	t.Helper()
	params, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal LogEntry: %v", err)
	}
	raw := fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/aimux/log_forward","params":%s}`, params)
	return []byte(raw)
}

// TestSessionHandler_LogForwardNotification_WritesEntry verifies that a valid
// log_forward notification writes a line to the daemon log file with the expected
// [shim-?<id>-<sess>] tag (FR-12 PeerCredsUnavailable path).
func TestSessionHandler_LogForwardNotification_WritesEntry(t *testing.T) {
	srv, logPath := buildNotificationTestServer(t)
	handler := srv.SessionHandler()

	// Advance to Phase B so fullDelegate is active.
	h := handler.(*aimuxHandler)
	srv.swapDelegateToFull(h, time.Now())

	nh, ok := handler.(muxcore.NotificationHandler)
	if !ok {
		t.Fatal("aimuxHandler does not implement muxcore.NotificationHandler")
	}

	project := muxcore.ProjectContext{
		ID:  muxcore.ProjectContextID("abcdef1234567890"),
		Cwd: t.TempDir(),
		Env: map[string]string{
			"CLAUDE_SESSION_ID": "abc12345ffff",
		},
	}

	entry := logger.LogEntry{
		Level:   logger.LevelInfo,
		Time:    time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC),
		Message: "hello from shim",
	}
	notification := buildLogForwardNotification(t, entry)

	// HandleNotification runs synchronously in tests (no goroutine wrapper needed here).
	nh.HandleNotification(context.Background(), project, notification)

	// PeerCredsUnavailable must be incremented (FR-12).
	if got := srv.logIngester.PeerCredsUnavailable.Load(); got != 1 {
		t.Errorf("PeerCredsUnavailable = %d, want 1", got)
	}

	// Flush the async log sink before reading.
	// DrainWithDeadline stops the drain goroutine and flushes remaining entries.
	if _, lost := srv.log.DrainWithDeadline(500 * time.Millisecond); lost > 0 {
		t.Logf("DrainWithDeadline lost %d entries (unexpected)", lost)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)

	// Session tag: CLAUDE_SESSION_ID[:8] = "abc12345" (verbatim from envelope env)
	// PID marker: "?" + project.ID[:8] (project.ID is muxcore-computed hash of cwd —
	// not predictable, so assert format via regex instead of exact substring).
	expectedPattern := regexp.MustCompile(`\[shim-\?[a-f0-9]{8}-abc12345\] hello from shim`)
	if !expectedPattern.MatchString(content) {
		t.Errorf("expected /\\[shim-\\?[a-f0-9]{8}-abc12345\\] hello from shim/ in log; got:\n%s", content)
	}
	if !strings.Contains(content, "hello from shim") {
		t.Errorf("expected 'hello from shim' in log; got:\n%s", content)
	}
}

// TestSessionHandler_LogForwardNotification_MalformedParams verifies that invalid
// JSON in params increments EnvelopeMalformed and does NOT panic.
func TestSessionHandler_LogForwardNotification_MalformedParams(t *testing.T) {
	srv, _ := buildNotificationTestServer(t)
	handler := srv.SessionHandler()

	h := handler.(*aimuxHandler)
	srv.swapDelegateToFull(h, time.Now())

	nh := handler.(muxcore.NotificationHandler)

	project := muxcore.ProjectContext{
		ID:  muxcore.ProjectContextID("proj1234"),
		Cwd: t.TempDir(),
	}

	// Malformed params: not a valid LogEntry JSON.
	raw := []byte(`{"jsonrpc":"2.0","method":"notifications/aimux/log_forward","params":"not-an-object"}`)
	nh.HandleNotification(context.Background(), project, raw)

	if got := srv.logIngester.EnvelopeMalformed.Load(); got != 1 {
		t.Errorf("EnvelopeMalformed = %d, want 1", got)
	}
}

// TestSessionHandler_LogForwardNotification_UnknownMethod verifies that
// notifications with other method names are silently ignored.
func TestSessionHandler_LogForwardNotification_UnknownMethod(t *testing.T) {
	srv, _ := buildNotificationTestServer(t)
	handler := srv.SessionHandler()

	h := handler.(*aimuxHandler)
	srv.swapDelegateToFull(h, time.Now())

	nh := handler.(muxcore.NotificationHandler)

	project := muxcore.ProjectContext{
		ID:  muxcore.ProjectContextID("proj5678"),
		Cwd: t.TempDir(),
	}

	// A standard MCP notification that is NOT log_forward.
	raw := []byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`)
	nh.HandleNotification(context.Background(), project, raw)

	// No counters incremented.
	if got := srv.logIngester.EnvelopeMalformed.Load(); got != 0 {
		t.Errorf("EnvelopeMalformed = %d, want 0 for unknown method", got)
	}
	if got := srv.logIngester.PeerCredsUnavailable.Load(); got != 0 {
		t.Errorf("PeerCredsUnavailable = %d, want 0 for unknown method", got)
	}
}

// TestSessionHandler_LogForwardNotification_SessionTagFallback verifies the fallback
// chain for session tag: no CLAUDE_SESSION_ID → project.ID[:8] used.
func TestSessionHandler_LogForwardNotification_SessionTagFallback(t *testing.T) {
	srv, logPath := buildNotificationTestServer(t)
	handler := srv.SessionHandler()

	h := handler.(*aimuxHandler)
	srv.swapDelegateToFull(h, time.Now())

	nh := handler.(muxcore.NotificationHandler)

	// No CLAUDE_SESSION_ID in env.
	project := muxcore.ProjectContext{
		ID:  muxcore.ProjectContextID("deadbeef99999999"),
		Cwd: t.TempDir(),
		Env: map[string]string{},
	}

	entry := logger.LogEntry{
		Level:   logger.LevelWarn,
		Time:    time.Now(),
		Message: "fallback session tag test",
	}
	notification := buildLogForwardNotification(t, entry)
	nh.HandleNotification(context.Background(), project, notification)

	if _, lost := srv.log.DrainWithDeadline(500 * time.Millisecond); lost > 0 {
		t.Logf("DrainWithDeadline lost %d entries", lost)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)

	// No CLAUDE_SESSION_ID set → session tag falls back to project.ID[:8].
	// Same project.ID also drives the pidMarker — both are the same 8-char prefix.
	// Format check via regex (project.ID is muxcore-computed and not predictable):
	expectedPattern := regexp.MustCompile(`\[shim-\?([a-f0-9]{8})-([a-f0-9]{8})\] fallback session tag test`)
	matches := expectedPattern.FindStringSubmatch(content)
	if matches == nil {
		t.Errorf("expected /\\[shim-\\?<hex8>-<hex8>\\] fallback session tag test/ in log; got:\n%s", content)
	} else if matches[1] != matches[2] {
		t.Errorf("expected pidMarker hex == sessionTag hex (both derived from project.ID), got pidMarker=%s sessionTag=%s", matches[1], matches[2])
	}
}
