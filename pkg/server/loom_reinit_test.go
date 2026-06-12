package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/metrics"
	"github.com/thebtf/aimux/pkg/session"
)

func newStoreBackedLoomlessServer(t *testing.T) *Server {
	t.Helper()

	log, err := logger.New(filepath.Join(t.TempDir(), "test.log"), logger.LevelError, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}
	store, err := session.NewStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	srv := &Server{
		cfg:        &config.Config{Server: config.ServerConfig{DefaultTimeoutSeconds: 10}},
		log:        log,
		metrics:    metrics.New(),
		sessions:   session.NewRegistry(),
		store:      store,
		engineName: "test-reinit",
	}
	t.Cleanup(func() {
		srv.Shutdown()
		_ = log.Close()
	})
	return srv
}

func TestTaskRouterLoomReinitializesFromStore(t *testing.T) {
	t.Parallel()

	srv := newStoreBackedLoomlessServer(t)

	got, err := srv.taskRouterLoom(context.Background())
	if err != nil {
		t.Fatalf("taskRouterLoom error: %v", err)
	}
	if got == nil {
		t.Fatal("taskRouterLoom returned nil; want reinitialized Loom client")
	}
	if srv.loom == nil {
		t.Fatal("srv.loom is nil after taskRouterLoom; want reinitialized Loom engine")
	}
	if _, err := srv.loom.ListEngine(); err != nil {
		t.Fatalf("reinitialized Loom ListEngine: %v", err)
	}
}

func TestSessionsHealthReinitializesLoomFromStore(t *testing.T) {
	t.Parallel()

	srv := newStoreBackedLoomlessServer(t)

	result, err := srv.handleSessions(context.Background(), makeRequest("sessions", map[string]any{"action": "health"}))
	if err != nil {
		t.Fatalf("handleSessions health: %v", err)
	}
	data := parseResult(t, result)
	if data["loom_status"] != "ok" {
		t.Fatalf("loom_status = %v, want ok", data["loom_status"])
	}
	if srv.loom == nil {
		t.Fatal("srv.loom is nil after sessions health; want reinitialized Loom engine")
	}
}
