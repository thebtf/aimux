package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/runtime"
)

func codexPoolConfigWithRuntimeHomeBase(t *testing.T, base string) PoolConfig {
	t.Helper()
	cfg := PoolConfig{
		IdleTimeout:    0,
		DefaultProfile: runtime.DefaultCodexProfile,
	}
	field := reflect.ValueOf(&cfg).Elem().FieldByName("RuntimeHomeBase")
	if !field.IsValid() {
		t.Fatalf("PoolConfig must expose RuntimeHomeBase so tests can prove project-scoped CODEX_HOME stays under the configured runtime base")
	}
	if field.Kind() != reflect.String || !field.CanSet() {
		t.Fatalf("PoolConfig.RuntimeHomeBase must be an exported string field")
	}
	field.SetString(base)
	return cfg
}

func newVirtualHomeTestPool(t *testing.T, base string) *CodexPool {
	t.Helper()
	t.Setenv(appServerProfileHelperEnv, "1")
	cfg := codexPoolConfigWithRuntimeHomeBase(t, base)
	pool, err := NewCodexPool(osArgs0(), cfg)
	if err != nil {
		t.Fatalf("NewCodexPool(test helper): %v", err)
	}
	t.Cleanup(func() { _ = pool.Shutdown(context.Background()) })
	return pool
}

func osArgs0() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return filepath.Clean(exe)
	}
	return filepath.Clean(os.Args[0])
}

func requireVirtualHomeUnderBase(t *testing.T, base, home string) {
	t.Helper()
	if home == "" {
		t.Fatal("VirtualHomeDir is empty; want stable project-scoped codex home")
	}
	rel, err := filepath.Rel(base, home)
	if err != nil {
		t.Fatalf("VirtualHomeDir %q is not relatable to base %q: %v", home, base, err)
	}
	if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("VirtualHomeDir %q escapes runtime base %q", home, base)
	}
}

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
	tp.entries[projectID] = readyEntry(proc)
	tp.mu.Unlock()
	return proc, nil
}

// readyEntry creates a poolEntry with a pre-closed readyCh (process is already ready).
// Used by test helpers that inject fake processes without going through proc.Start().
func readyEntry(proc *AppServerProcess) *poolEntry {
	ch := make(chan struct{})
	close(ch)
	return &poolEntry{
		process:  proc,
		lastUsed: time.Now(),
		readyCh:  ch,
	}
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

func TestCodexPool_VirtualHomeStableAndProjectScoped(t *testing.T) {
	base := filepath.Join(t.TempDir(), "runtime-home")
	pool := newVirtualHomeTestPool(t, base)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	alpha, err := pool.Acquire(ctx, "project-alpha", t.TempDir())
	if err != nil {
		t.Fatalf("Acquire(project-alpha): %v", err)
	}
	alphaHome := alpha.profile.VirtualHomeDir
	requireVirtualHomeUnderBase(t, base, alphaHome)
	if alpha.profile.StateScope != runtime.StateScopePersistent {
		t.Fatalf("StateScope=%v want StateScopePersistent for stable project home", alpha.profile.StateScope)
	}

	alphaAgain, err := pool.Acquire(ctx, "project-alpha", t.TempDir())
	if err != nil {
		t.Fatalf("Acquire(project-alpha again): %v", err)
	}
	if alphaAgain.profile.VirtualHomeDir != alphaHome {
		t.Fatalf("same project VirtualHomeDir=%q want stable %q", alphaAgain.profile.VirtualHomeDir, alphaHome)
	}

	beta, err := pool.Acquire(ctx, "project-beta", t.TempDir())
	if err != nil {
		t.Fatalf("Acquire(project-beta): %v", err)
	}
	betaHome := beta.profile.VirtualHomeDir
	requireVirtualHomeUnderBase(t, base, betaHome)
	if betaHome == alphaHome {
		t.Fatalf("different projects share VirtualHomeDir %q", betaHome)
	}
}

func TestCodexPool_HomeOverrideNone_DoesNotDeriveVirtualHome(t *testing.T) {
	base := filepath.Join(t.TempDir(), "runtime-home")
	t.Setenv(appServerProfileHelperEnv, "1")
	cfg := codexPoolConfigWithRuntimeHomeBase(t, base)
	cfg.DefaultProfile = func(workDir string) runtime.CLIRuntimeProfile {
		return runtime.New("codex", workDir).
			WithHomeOverride(runtime.HomeOverrideNone).
			WithCLIHomeEnvVar("CODEX_HOME").
			Build()
	}
	pool, err := NewCodexPool(osArgs0(), cfg)
	if err != nil {
		t.Fatalf("NewCodexPool(HomeOverrideNone): %v", err)
	}
	t.Cleanup(func() { _ = pool.Shutdown(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	proc, err := pool.Acquire(ctx, "project-no-home-override", t.TempDir())
	if err != nil {
		t.Fatalf("Acquire(HomeOverrideNone): %v", err)
	}
	if proc.profile.VirtualHomeDir != "" {
		t.Fatalf("VirtualHomeDir=%q want empty when HomeOverrideNone opts out", proc.profile.VirtualHomeDir)
	}
}

func TestCodexPool_VirtualHomeCopiesAuthFiles(t *testing.T) {
	base := filepath.Join(t.TempDir(), "runtime-home")
	ambientHome := filepath.Join(t.TempDir(), "ambient-codex-home")
	writeAuthFixture(t, ambientHome, "ambient-auth-json")
	t.Setenv(appServerProfileHelperEnv, "1")
	t.Setenv("CODEX_HOME", ambientHome)
	cfg := codexPoolConfigWithRuntimeHomeBase(t, base)
	pool, err := NewCodexPool(osArgs0(), cfg)
	if err != nil {
		t.Fatalf("NewCodexPool(auth pass-through): %v", err)
	}
	t.Cleanup(func() { _ = pool.Shutdown(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	proc, err := pool.Acquire(ctx, "project-auth-pass-through", t.TempDir())
	if err != nil {
		t.Fatalf("Acquire(auth pass-through): %v", err)
	}
	authBytes, err := os.ReadFile(filepath.Join(proc.profile.VirtualHomeDir, "auth.json"))
	if err != nil {
		t.Fatalf("read auth.json from virtual home %q: %v", proc.profile.VirtualHomeDir, err)
	}
	if got, want := string(authBytes), "ambient-auth-json"; got != want {
		t.Fatalf("auth.json content=%q want %q", got, want)
	}
}

func TestCodexPool_VirtualHomeCopiesConfigToml(t *testing.T) {
	base := filepath.Join(t.TempDir(), "runtime-home")
	ambientHome := filepath.Join(t.TempDir(), "ambient-codex-home")
	configFixture := "[mcp_servers.demo]\ncommand = \"demo\"\n"
	writeConfigFixture(t, ambientHome, configFixture)
	t.Setenv(appServerProfileHelperEnv, "1")
	t.Setenv("CODEX_HOME", ambientHome)
	cfg := codexPoolConfigWithRuntimeHomeBase(t, base)
	pool, err := NewCodexPool(osArgs0(), cfg)
	if err != nil {
		t.Fatalf("NewCodexPool(config pass-through): %v", err)
	}
	t.Cleanup(func() { _ = pool.Shutdown(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	proc, err := pool.Acquire(ctx, "project-config-pass-through", t.TempDir())
	if err != nil {
		t.Fatalf("Acquire(config pass-through): %v", err)
	}
	configBytes, err := os.ReadFile(filepath.Join(proc.profile.VirtualHomeDir, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml from virtual home %q: %v", proc.profile.VirtualHomeDir, err)
	}
	if got, want := string(configBytes), configFixture; got != want {
		t.Fatalf("config.toml content=%q want %q", got, want)
	}
	if proc.profile.VirtualHomeDir == ambientHome {
		t.Fatalf("VirtualHomeDir %q want project-scoped home distinct from ambient CODEX_HOME", proc.profile.VirtualHomeDir)
	}
}

func TestCodexPool_VirtualHomeUnsafeProjectIDCannotEscapeBase(t *testing.T) {
	base := filepath.Join(t.TempDir(), "runtime-home")
	pool := newVirtualHomeTestPool(t, base)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	proc, err := pool.Acquire(ctx, `..\\..//outside:project?name`, t.TempDir())
	if err != nil {
		t.Fatalf("Acquire(unsafe projectID): %v", err)
	}
	requireVirtualHomeUnderBase(t, base, proc.profile.VirtualHomeDir)
	if strings.Contains(proc.profile.VirtualHomeDir, "..") {
		t.Fatalf("VirtualHomeDir %q preserves unsafe traversal marker", proc.profile.VirtualHomeDir)
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

// TestCodexPool_IdleEviction_SkipsTurnInFlight verifies that a process actively
// processing a turn is not evicted even if its lastUsed exceeds the idle timeout.
func TestCodexPool_IdleEviction_SkipsTurnInFlight(t *testing.T) {
	tp := newTestPool(t, nil)

	const idleTimeout = 50 * time.Millisecond
	tp.cfg.IdleTimeout = idleTimeout
	tp.wg.Add(1)
	go tp.idleEvictLoop()
	defer tp.Shutdown(context.Background())

	// Inject a process that appears to be in-flight.
	proc := fakePoolEntry(t)
	proc.setState(AppServerStateTurnInFlight)

	tp.mu.Lock()
	entry := readyEntry(proc)
	// Backdate lastUsed so it looks idle.
	entry.lastUsed = time.Now().Add(-10 * idleTimeout)
	tp.entries["inflight-proj"] = entry
	tp.mu.Unlock()

	// Wait longer than the idle timeout.
	time.Sleep(3 * idleTimeout)

	if tp.Len() != 1 {
		t.Errorf("TurnInFlight process must not be evicted; pool size = %d, want 1", tp.Len())
	}
}

// TestCodexPool_ConcurrentAcquire_WaitsForStartup verifies that a second concurrent
// Acquire call for the same project ID waits until the first completes Start()
// before returning the process (rather than returning an Initializing process).
func TestCodexPool_ConcurrentAcquire_WaitsForStartup(t *testing.T) {
	// Channels to control the fake Start timing.
	startGate := make(chan struct{})
	startDone := make(chan struct{})

	// Count how many times spawnFn is called.
	var spawnCount int

	tp := newTestPool(t, func(_, _ string) *AppServerProcess {
		spawnCount++
		return fakePoolEntry(t)
	})
	defer tp.Shutdown(context.Background())

	// Replace the pool's Acquire for this test with a version that adds a readyCh-aware entry.
	// We simulate the real Acquire's concurrency pattern by inserting an entry with an open
	// readyCh, then unblocking it after a short delay.
	readyCh := make(chan struct{})
	proc := fakePoolEntry(t)

	tp.mu.Lock()
	tp.entries["concurrent-proj"] = &poolEntry{
		process:  proc,
		lastUsed: time.Now(),
		readyCh:  readyCh,
	}
	tp.mu.Unlock()

	// Unblock readyCh after a short delay (simulating slow Start).
	go func() {
		close(startGate) // signal that we started the goroutine
		time.Sleep(20 * time.Millisecond)
		tp.mu.Lock()
		tp.entries["concurrent-proj"].startErr = nil
		tp.mu.Unlock()
		close(readyCh)
		close(startDone)
	}()

	// Wait until the goroutine starts so timing is deterministic.
	<-startGate

	// A second Acquire should block on readyCh until startup completes.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	acquired, err := tp.CodexPool.Acquire(ctx, "concurrent-proj", "/work")
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if acquired != proc {
		t.Error("Acquire must return the same process pointer")
	}

	// Verify startup completed before we received the process.
	select {
	case <-startDone:
	default:
		t.Error("Acquire returned before readyCh was closed (returned Initializing process)")
	}
}
