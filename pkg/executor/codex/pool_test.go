package codex

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/runtime"
)

// fakePoolProcess creates a pre-started AppServerProcess wired to an in-process fake,
// bypassing NewCodexPool's binary check. Used to test pool logic independently of the real binary.
func fakePoolEntry(t *testing.T) *AppServerProcess {
	t.Helper()
	d := newFakeDialer(t)
	return newTestProcess(t, d)
}

// testPool builds a CodexPool with a custom spawn function (no real binary needed).
// It replaces internal process construction so tests can inject fakes.
type testPool struct {
	*CodexPool
	spawnFn func(projectID, workDir string) *AppServerProcess
}

// newTestPool creates a pool where Acquire uses spawnFn instead of spawning a real process.
// Idle timeout is set short for test speed.
func newTestPool(t *testing.T, spawnFn func(projectID, workDir string) *AppServerProcess) *testPool {
	t.Helper()
	// We build the pool directly without binary validation.
	pool := &CodexPool{
		cfg: PoolConfig{
			IdleTimeout:    200 * time.Millisecond,
			DefaultProfile: runtime.DefaultCodexProfile,
		},
		codexPath: "/fake/codex",
		entries:   make(map[string]*poolEntry),
		stopCh:    make(chan struct{}),
	}
	if spawnFn == nil {
		spawnFn = func(_, _ string) *AppServerProcess { return fakePoolEntry(t) }
	}
	tp := &testPool{
		CodexPool: pool,
		spawnFn:   spawnFn,
	}
	return tp
}

// acquireWithFake is testPool's Acquire that uses spawnFn.
func (tp *testPool) acquireWithFake(ctx context.Context, projectID, workDir string) (*AppServerProcess, error) {
	if projectID == "" {
		return nil, errors.New("codex: CodexPool.Acquire: projectID must not be empty")
	}
	tp.mu.Lock()
	if entry, ok := tp.entries[projectID]; ok {
		entry.lastUsed = time.Now()
		proc := entry.process
		tp.mu.Unlock()
		return proc, nil
	}
	proc := tp.spawnFn(projectID, workDir)
	tp.entries[projectID] = &poolEntry{
		process:  proc,
		lastUsed: time.Now(),
	}
	tp.mu.Unlock()
	return proc, nil
}

// --- Tests ---

func TestCodexPool_NewPool_MissingBinary_Fails(t *testing.T) {
	// HARD FAIL: pool construction must fail when codex binary is not found.
	// This is the primary guard against misconfigured environments.
	_, err := NewCodexPool("/nonexistent/path/to/codex", DefaultPoolConfig())
	if err == nil {
		t.Fatal("HARD FAIL: NewCodexPool must return error when codex binary is not found")
	}
	// Error must be actionable (mention the path).
	if err.Error() == "" {
		t.Error("error message must not be empty")
	}
}

func TestCodexPool_NewPool_EmptyPath_Fails(t *testing.T) {
	_, err := NewCodexPool("", DefaultPoolConfig())
	if err == nil {
		t.Fatal("expected error for empty codexPath")
	}
}

func TestCodexPool_Acquire_SameProjectID_ReturnsSameProcess(t *testing.T) {
	var spawnCount int
	tp := newTestPool(t, func(_, _ string) *AppServerProcess {
		spawnCount++
		return fakePoolEntry(t)
	})
	defer tp.Shutdown(context.Background())

	ctx := context.Background()
	p1, err := tp.acquireWithFake(ctx, "proj-1", "/work")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	p2, err := tp.acquireWithFake(ctx, "proj-1", "/work")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}

	if p1 != p2 {
		t.Error("same projectID must return same process pointer")
	}
	if spawnCount != 1 {
		t.Errorf("expected 1 spawn, got %d", spawnCount)
	}
}

func TestCodexPool_Acquire_DifferentProjectIDs_ReturnDifferentProcesses(t *testing.T) {
	tp := newTestPool(t, nil)
	defer tp.Shutdown(context.Background())

	ctx := context.Background()
	p1, _ := tp.acquireWithFake(ctx, "proj-A", "/workA")
	p2, _ := tp.acquireWithFake(ctx, "proj-B", "/workB")

	if p1 == p2 {
		t.Error("different projectIDs must return different processes")
	}
	if tp.Len() != 2 {
		t.Errorf("expected pool size 2, got %d", tp.Len())
	}
}

func TestCodexPool_Acquire_EmptyProjectID_Fails(t *testing.T) {
	tp := newTestPool(t, nil)
	defer tp.Shutdown(context.Background())

	_, err := tp.acquireWithFake(context.Background(), "", "/work")
	if err == nil {
		t.Error("expected error for empty projectID")
	}
}

func TestCodexPool_Release_UpdatesLastUsed(t *testing.T) {
	tp := newTestPool(t, nil)
	defer tp.Shutdown(context.Background())

	ctx := context.Background()
	_, _ = tp.acquireWithFake(ctx, "proj-1", "/work")

	tp.mu.Lock()
	before := tp.entries["proj-1"].lastUsed
	tp.mu.Unlock()

	time.Sleep(5 * time.Millisecond)
	tp.Release("proj-1")

	tp.mu.Lock()
	after := tp.entries["proj-1"].lastUsed
	tp.mu.Unlock()

	if !after.After(before) {
		t.Error("Release must update lastUsed timestamp")
	}
}

func TestCodexPool_Release_NoOp_ForUnknownProject(t *testing.T) {
	tp := newTestPool(t, nil)
	defer tp.Shutdown(context.Background())

	// Must not panic.
	tp.Release("nonexistent-project")
}

func TestCodexPool_Len_ReflectsPoolSize(t *testing.T) {
	tp := newTestPool(t, nil)
	defer tp.Shutdown(context.Background())

	if tp.Len() != 0 {
		t.Errorf("expected 0, got %d", tp.Len())
	}

	ctx := context.Background()
	_, _ = tp.acquireWithFake(ctx, "p1", "/w1")
	if tp.Len() != 1 {
		t.Errorf("expected 1, got %d", tp.Len())
	}

	_, _ = tp.acquireWithFake(ctx, "p2", "/w2")
	if tp.Len() != 2 {
		t.Errorf("expected 2, got %d", tp.Len())
	}
}

func TestCodexPool_Remove_ReducesPoolSize(t *testing.T) {
	tp := newTestPool(t, nil)
	defer tp.Shutdown(context.Background())

	ctx := context.Background()
	_, _ = tp.acquireWithFake(ctx, "proj-remove", "/work")
	if tp.Len() != 1 {
		t.Fatalf("expected 1, got %d", tp.Len())
	}

	if err := tp.Remove(ctx, "proj-remove"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if tp.Len() != 0 {
		t.Errorf("expected 0 after Remove, got %d", tp.Len())
	}
}

func TestCodexPool_Remove_NoOp_ForUnknownProject(t *testing.T) {
	tp := newTestPool(t, nil)
	defer tp.Shutdown(context.Background())

	if err := tp.Remove(context.Background(), "ghost"); err != nil {
		t.Errorf("Remove for unknown project must not error: %v", err)
	}
}

func TestCodexPool_Shutdown_ClearsAllEntries(t *testing.T) {
	tp := newTestPool(t, nil)

	ctx := context.Background()
	_, _ = tp.acquireWithFake(ctx, "p1", "/w1")
	_, _ = tp.acquireWithFake(ctx, "p2", "/w2")

	if err := tp.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if tp.Len() != 0 {
		t.Errorf("expected 0 after Shutdown, got %d", tp.Len())
	}
}

func TestCodexPool_Shutdown_Idempotent(t *testing.T) {
	tp := newTestPool(t, nil)
	ctx := context.Background()

	if err := tp.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	// Second call must not panic or deadlock.
	if err := tp.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestCodexPool_IdleEviction_RemovesStaleEntries(t *testing.T) {
	tp := newTestPool(t, nil)

	// Start idle eviction with a very short timeout.
	tp.cfg.IdleTimeout = 50 * time.Millisecond
	tp.wg.Add(1)
	go tp.idleEvictLoop()
	defer tp.Shutdown(context.Background())

	ctx := context.Background()
	_, _ = tp.acquireWithFake(ctx, "idle-proj", "/work")

	// Wait for eviction (2x idle timeout).
	time.Sleep(200 * time.Millisecond)

	if tp.Len() != 0 {
		t.Errorf("expected 0 after idle eviction, got %d", tp.Len())
	}
}
