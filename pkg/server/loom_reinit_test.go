package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/metrics"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/tenant"
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

func newRecoverableStorelessServer(t *testing.T, engineName string) *Server {
	t.Helper()

	log, err := logger.New(filepath.Join(t.TempDir(), "test.log"), logger.LevelError, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("session.NewStore seed: %v", err)
	}
	store.Close()
	srv := &Server{
		cfg:        &config.Config{Server: config.ServerConfig{DBPath: dbPath, DefaultTimeoutSeconds: 10}},
		log:        log,
		metrics:    metrics.New(),
		sessions:   session.NewRegistry(),
		engineName: engineName,
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

func TestSessionsHealthReopensStoreAfterStartupFallback(t *testing.T) {
	t.Parallel()

	srv := newRecoverableStorelessServer(t, "test-reopen-store-health")

	result, err := srv.handleSessions(context.Background(), makeRequest("sessions", map[string]any{"action": "health"}))
	if err != nil {
		t.Fatalf("handleSessions health: %v", err)
	}
	data := parseResult(t, result)
	if data["loom_status"] != "ok" {
		t.Fatalf("loom_status = %v, want ok; loom_error=%v", data["loom_status"], data["loom_error"])
	}
	if srv.store == nil || srv.store.DB() == nil {
		t.Fatal("srv.store is nil after sessions health; want reopened SQLite store")
	}
	if srv.loom == nil {
		t.Fatal("srv.loom is nil after sessions health; want reinitialized Loom engine")
	}
}

func TestTaskRouterLoomReopensStoreAfterStartupFallback(t *testing.T) {
	t.Parallel()

	srv := newRecoverableStorelessServer(t, "test-reopen-store-task")

	got, err := srv.taskRouterLoom(context.Background())
	if err != nil {
		t.Fatalf("taskRouterLoom error: %v", err)
	}
	if got == nil {
		t.Fatal("taskRouterLoom returned nil; want reinitialized Loom client")
	}
	if srv.store == nil || srv.store.DB() == nil {
		t.Fatal("srv.store is nil after taskRouterLoom; want reopened SQLite store")
	}
	if srv.loom == nil {
		t.Fatal("srv.loom is nil after taskRouterLoom; want reinitialized Loom engine")
	}
}

func TestTaskRouterLoomReinitializesTenantScopedFromStore(t *testing.T) {
	t.Parallel()

	srv := newStoreBackedLoomlessServer(t)
	registry := tenant.NewRegistry()
	registry.Swap(tenant.NewSnapshot(map[int]tenant.TenantConfig{
		1001: {Name: "tenant-a", UID: 1001, Role: tenant.RolePlain},
	}))
	srv.dispatchMW = NewDispatchMiddleware(registry, audit.DiscardLog{})

	ctx := srv.dispatchMW.WithContext(context.Background(), tenant.TenantContext{
		TenantID: "tenant-a",
		Role:     tenant.RolePlain,
	})
	got, err := srv.taskRouterLoom(ctx)
	if err != nil {
		t.Fatalf("taskRouterLoom error: %v", err)
	}
	scoped, ok := got.(*loom.TenantScopedLoomEngine)
	if !ok {
		t.Fatalf("taskRouterLoom returned %T, want *loom.TenantScopedLoomEngine", got)
	}
	if scoped.TenantID() != "tenant-a" {
		t.Fatalf("TenantID = %q, want tenant-a", scoped.TenantID())
	}
}

func TestWireLoomRuntimeRetriesGCWhenContextArrivesAfterWorkers(t *testing.T) {
	t.Parallel()

	srv := newStoreBackedLoomlessServer(t)
	if _, err := srv.ensureLoom(context.Background()); err != nil {
		t.Fatalf("ensureLoom: %v", err)
	}
	if !srv.loomRuntimeWired {
		t.Fatal("loomRuntimeWired = false after ensureLoom; want workers wired")
	}

	taskStore, err := loom.NewTaskStore(srv.store.DB(), srv.engineName)
	if err != nil {
		t.Fatalf("loom.NewTaskStore: %v", err)
	}
	staleTask := &loom.Task{
		ID:         "gc-stale-retry",
		Status:     loom.TaskStatusRunning,
		WorkerType: loom.WorkerTypeThinker,
		ProjectID:  "proj-gc",
		Prompt:     "stale task",
		CreatedAt:  time.Now().Add(-30 * time.Minute).UTC(),
	}
	if err := taskStore.Create(staleTask); err != nil {
		t.Fatalf("Create stale task: %v", err)
	}

	gcCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.gcCtx = gcCtx
	srv.loomGCInterval = 10 * time.Millisecond
	srv.wireLoomRuntime()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(2 * time.Second)
	for {
		got, err := taskStore.Get(staleTask.ID)
		if err != nil {
			t.Fatalf("Get stale task: %v", err)
		}
		if got.Status == loom.TaskStatusFailed {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("stale task status = %s, want %s", got.Status, loom.TaskStatusFailed)
		case <-ticker.C:
		}
	}
}
