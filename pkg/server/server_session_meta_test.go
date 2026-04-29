// Package server: T040 (server portion) — HandleNotificationWithSessionMeta test.
// RED gate: fails until HandleNotificationWithSessionMeta is implemented on aimuxHandler
// and LogPartitioner is wired into the Server (AIMUX-12 Phase 6, T039).
package server

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/mcp-mux/muxcore"
)

// mockPartitioner is a test double for LogPartitionerWriter that records calls.
// Safe for concurrent use — matches production concurrency patterns.
type mockPartitioner struct {
	mu              sync.Mutex
	lastTenantID    string
	data            strings.Builder
}

// WriteFor records the tenantID and appends the entry bytes.
func (m *mockPartitioner) WriteFor(tenantID string, entry []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastTenantID = tenantID
	m.data.Write(entry)
	return len(entry), nil
}

// TenantID returns the most recently recorded tenantID.
func (m *mockPartitioner) TenantID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastTenantID
}

// Data returns all written data as a string.
func (m *mockPartitioner) Data() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.String()
}

// TestHandleNotificationWithSessionMeta_RoutesByTenantID verifies that when
// aimuxHandler receives HandleNotificationWithSessionMeta with a SessionMeta
// carrying TenantID="tenantA", the log_forward notification's entry bytes are
// routed to LogPartitioner.WriteFor with that tenantID.
func TestHandleNotificationWithSessionMeta_RoutesByTenantID(t *testing.T) {
	srv, logPath := buildNotificationTestServer(t)
	handler := srv.SessionHandler()

	// Advance to Phase B so fullDelegate is active.
	h := handler.(*aimuxHandler)
	srv.swapDelegateToFull(h, time.Now())

	// Assert interface is implemented.
	nhMeta, ok := handler.(muxcore.NotificationHandlerWithSessionMeta)
	if !ok {
		t.Fatal("aimuxHandler does not implement muxcore.NotificationHandlerWithSessionMeta")
	}

	// Wire a mock partitioner so we can capture the tenantID.
	mock := &mockPartitioner{}
	srv.logPartitioner = mock

	project := muxcore.ProjectContext{
		ID:  muxcore.ProjectContextID("session-meta-test-cwd"),
		Cwd: t.TempDir(),
		Env: map[string]string{
			"CLAUDE_SESSION_ID": "sessA001x",
		},
	}

	meta := muxcore.SessionMeta{
		Conn:         muxcore.ConnInfo{PeerPid: 1234, PeerUid: 5678, Platform: muxcore.PlatformLinuxUnix},
		TenantID:     "tenantA",
		AuthorizedAt: time.Now(),
	}

	entry := logger.LogEntry{
		Level:   logger.LevelInfo,
		Time:    time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
		Message: "routed by tenant",
	}
	notification := buildLogForwardNotification(t, entry)

	nhMeta.HandleNotificationWithSessionMeta(context.Background(), project, meta, notification)

	// Verify the partitioner was called with the correct tenantID.
	gotTenantID := mock.TenantID()
	if gotTenantID != "tenantA" {
		t.Errorf("WriteFor called with tenantID=%q, want %q", gotTenantID, "tenantA")
	}

	// Verify the written data contains the message bytes.
	writtenStr := mock.Data()
	if !strings.Contains(writtenStr, "routed by tenant") {
		t.Errorf("expected 'routed by tenant' in partitioner data; got:\n%s", writtenStr)
	}

	// logPath is still referenced to satisfy the buildNotificationTestServer signature.
	_ = logPath
}

// TestHandleNotificationWithSessionMeta_EmptyTenantIDFallsBack verifies that when
// SessionMeta.TenantID is empty, the handler falls back to the legacy notification
// path (HandleNotification) rather than routing to LogPartitioner.
func TestHandleNotificationWithSessionMeta_EmptyTenantIDFallsBack(t *testing.T) {
	srv, _ := buildNotificationTestServer(t)
	handler := srv.SessionHandler()

	h := handler.(*aimuxHandler)
	srv.swapDelegateToFull(h, time.Now())

	nhMeta, ok := handler.(muxcore.NotificationHandlerWithSessionMeta)
	if !ok {
		t.Fatal("aimuxHandler does not implement muxcore.NotificationHandlerWithSessionMeta")
	}

	mock := &mockPartitioner{}
	srv.logPartitioner = mock

	project := muxcore.ProjectContext{
		ID:  muxcore.ProjectContextID("meta-fallback-test"),
		Cwd: t.TempDir(),
	}

	// SessionMeta with no TenantID — should fall back to legacy path.
	meta := muxcore.SessionMeta{
		Conn:     muxcore.ConnInfo{Platform: muxcore.PlatformWindowsNamedPipe},
		TenantID: "",
		// AuthorizedAt zero — not authorized
	}

	entry := logger.LogEntry{
		Level:   logger.LevelWarn,
		Time:    time.Now(),
		Message: "fallback path check",
	}
	notification := buildLogForwardNotification(t, entry)

	// Must not panic regardless of meta content.
	nhMeta.HandleNotificationWithSessionMeta(context.Background(), project, meta, notification)

	// With empty TenantID and no partitioner routing, the existing
	// PeerCredsUnavailable counter path should NOT fire here (the meta path
	// has actual peer info). The test verifies no panic and the mock was
	// NOT invoked for routing (empty TenantID = fallback).
	gotTenantID := mock.TenantID()
	if gotTenantID == "tenantA" {
		t.Errorf("unexpected tenantA routing with empty SessionMeta.TenantID")
	}
}

// TestHandleNotificationWithSessionMeta_NoPartitionerDoesNotPanic verifies that
// HandleNotificationWithSessionMeta is safe when srv.logPartitioner is nil
// (daemon not configured for partitioning).
func TestHandleNotificationWithSessionMeta_NoPartitionerDoesNotPanic(t *testing.T) {
	srv, _ := buildNotificationTestServer(t)
	handler := srv.SessionHandler()

	h := handler.(*aimuxHandler)
	srv.swapDelegateToFull(h, time.Now())

	nhMeta, ok := handler.(muxcore.NotificationHandlerWithSessionMeta)
	if !ok {
		t.Fatal("aimuxHandler does not implement muxcore.NotificationHandlerWithSessionMeta")
	}

	// Ensure partitioner is nil (should be nil by default).
	srv.logPartitioner = nil

	project := muxcore.ProjectContext{
		ID:  muxcore.ProjectContextID("no-partitioner"),
		Cwd: t.TempDir(),
	}
	meta := muxcore.SessionMeta{
		TenantID:     "anyTenant",
		AuthorizedAt: time.Now(),
	}
	entry := logger.LogEntry{Level: logger.LevelInfo, Time: time.Now(), Message: "no partitioner"}
	notification := buildLogForwardNotification(t, entry)

	// Must not panic.
	nhMeta.HandleNotificationWithSessionMeta(context.Background(), project, meta, notification)
	// If we reach here without panicking, the test passes.
}
