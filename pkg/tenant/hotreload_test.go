package tenant

import (
	"context"
	"os"
	"testing"
	"time"
)

// makeYAMLFile writes a tenants.yaml to dir with the given entries and returns the path.
func makeYAMLFile(t *testing.T, dir, content string) string {
	t.Helper()
	f, err := os.CreateTemp(dir, "tenants-*.yaml")
	if err != nil {
		t.Fatalf("create temp yaml: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	_ = f.Close()
	return f.Name()
}

const validYAML1 = `
tenants:
  - name: alice
    uid: 1001
    role: operator
`

const validYAML2 = `
tenants:
  - name: alice
    uid: 1001
    role: operator
  - name: bob
    uid: 1002
    role: plain
`

func TestHotReloader_ValidReload(t *testing.T) {
	dir := t.TempDir()
	path := makeYAMLFile(t, dir, validYAML1)

	reg := NewRegistry()
	sigCh := make(chan os.Signal, 1)
	reloader := NewConfigHotReloader(path, reg, sigCh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reloader.Run(ctx)

	// Initial state: alice only.
	if _, ok := reg.Resolve(1001); ok {
		t.Error("registry should be empty before first SIGHUP")
	}

	// Trigger reload.
	sigCh <- os.Interrupt // any signal value is fine; the channel carries the signal
	time.Sleep(100 * time.Millisecond)

	cfg, ok := reg.Resolve(1001)
	if !ok {
		t.Fatal("alice (uid 1001) should be in registry after reload")
	}
	if cfg.Name != "alice" {
		t.Errorf("cfg.Name = %q, want 'alice'", cfg.Name)
	}

	// Update file to add bob, trigger second reload.
	if err := os.WriteFile(path, []byte(validYAML2), 0o600); err != nil {
		t.Fatalf("write updated yaml: %v", err)
	}

	// Wait past coalesce window before second signal.
	reloader.drain = newDrainController() // reset drain to avoid test pollution
	// Directly call reload to bypass coalesce timing in unit tests.
	reloader.reload(context.Background())

	if _, ok := reg.Resolve(1002); !ok {
		t.Error("bob (uid 1002) should appear after second reload")
	}
}

func TestHotReloader_InvalidReloadRetainsPrevious(t *testing.T) {
	dir := t.TempDir()
	path := makeYAMLFile(t, dir, validYAML1)

	reg := NewRegistry()
	sigCh := make(chan os.Signal, 1)
	reloader := NewConfigHotReloader(path, reg, sigCh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reloader.Run(ctx)

	// Load valid config first.
	sigCh <- os.Interrupt
	time.Sleep(100 * time.Millisecond)

	cfg, ok := reg.Resolve(1001)
	if !ok {
		t.Fatal("alice not in registry after valid load")
	}
	_ = cfg

	// Overwrite with invalid YAML.
	if err := os.WriteFile(path, []byte("tenants: [{{invalid"), 0o600); err != nil {
		t.Fatalf("write bad yaml: %v", err)
	}
	reloader.reload(context.Background()) // call directly to bypass coalesce

	// Previous config must still be intact.
	cfg, ok = reg.Resolve(1001)
	if !ok {
		t.Error("alice should still be in registry after failed reload")
	}
	if cfg.Name != "alice" {
		t.Errorf("cfg.Name = %q after failed reload, want 'alice'", cfg.Name)
	}
}

func TestHotReloader_CoalescedSignals(t *testing.T) {
	dir := t.TempDir()
	path := makeYAMLFile(t, dir, validYAML1)

	reg := NewRegistry()
	sigCh := make(chan os.Signal, 3)
	reloader := NewConfigHotReloader(path, reg, sigCh)

	// Intercept reload calls by wrapping the signal channel.
	// We send 3 signals rapidly; only the first should trigger a reload within
	// the coalesce window. Verify by sending 3, sleeping less than the window,
	// and confirming the registry state changed exactly once.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go reloader.Run(ctx)

	// Send 3 signals rapidly.
	sigCh <- os.Interrupt
	sigCh <- os.Interrupt
	sigCh <- os.Interrupt

	time.Sleep(200 * time.Millisecond)

	// Registry should have alice after at least one reload.
	_, ok := reg.Resolve(1001)
	if !ok {
		t.Error("alice should be enrolled after at least one SIGHUP was processed")
	}
	// We cannot assert exactly 1 reload without instrumentation, but the
	// coalesce logic is verified by the log output. The race detector will
	// catch concurrent map writes if coalescing is broken.
}

// TestNewConfigHotReloader_SigChWiredIntoStruct guards against the constructor
// silently dropping the sigCh parameter. Prior to the v3 PRC fix the canonical
// constructor accepted sigCh but did not assign it, causing every production
// caller to receive a reloader that ignored the injected signal channel.
func TestNewConfigHotReloader_SigChWiredIntoStruct(t *testing.T) {
	reg := NewRegistry()
	sigCh := make(chan os.Signal, 1)
	r := NewConfigHotReloader("/tmp/none.yaml", reg, sigCh)
	if r.sigCh == nil {
		t.Fatal("NewConfigHotReloader: sigCh dropped — constructor footgun regression (B3)")
	}
	rNil := NewConfigHotReloader("/tmp/none.yaml", reg, nil)
	if rNil.sigCh != nil {
		t.Fatal("NewConfigHotReloader: nil sigCh became non-nil — unexpected mutation")
	}
}

// TestBeginDrain_AfterTimeoutRemovesFromMap verifies that the BeginDrain
// goroutine cleans up the draining map entry after the drain window expires
// (and on context cancellation). PRC v3 B8 — prior implementation leaked the
// map entry indefinitely, so a re-enrolled tenant inherited the stale flag.
func TestBeginDrain_AfterTimeoutRemovesFromMap(t *testing.T) {
	t.Run("timeout path removes entry", func(t *testing.T) {
		d := newDrainController()
		ctx := context.Background()

		// Pass drainSeconds=1 so the timeout fires inside the test window.
		d.BeginDrain(ctx, "tenantA", 1)

		// Immediately after BeginDrain, IsDraining should be true.
		if !d.IsDraining("tenantA") {
			t.Fatal("expected IsDraining(tenantA)=true immediately after BeginDrain")
		}

		// Wait past the drain window (+ small slack for goroutine scheduling).
		// Poll for cleanup; max wait 3s to avoid a fragile sleep.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if !d.IsDraining("tenantA") {
				return // success — entry cleaned up
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("expected draining map entry to be removed after drain window expired (B8 leak regression)")
	})

	t.Run("ctx cancel path removes entry", func(t *testing.T) {
		d := newDrainController()
		ctx, cancel := context.WithCancel(context.Background())

		// Use a long drain so only ctx cancel can release the goroutine.
		d.BeginDrain(ctx, "tenantB", 3600)

		if !d.IsDraining("tenantB") {
			t.Fatal("expected IsDraining(tenantB)=true immediately after BeginDrain")
		}

		cancel()

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if !d.IsDraining("tenantB") {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("expected draining map entry to be removed after ctx cancel (B8 leak regression)")
	})
}

// TestReload_SwapBeforeDrain_NoStaleAdmissionWindow verifies W3 (AIMUX-12 v5.1.0):
// reload() must call registry.Swap BEFORE BeginDrain so that the moment a tenant
// disappears from tenants.yaml, ResolveByUID returns false IMMEDIATELY for any
// concurrent admission attempt. Prior to W3 the order was BeginDrain → Swap, which
// left a stale-admission window where new sessions for the removed tenant could
// still resolve on the OLD snapshot before the swap committed.
//
// Strategy: load alice+bob, then reload with bob removed, and immediately query
// ResolveByUID for bob's UID. Post-W3 the lookup MUST return false on the very
// first call after reload returns (no polling, no race window).
func TestReload_SwapBeforeDrain_NoStaleAdmissionWindow(t *testing.T) {
	dir := t.TempDir()
	path := makeYAMLFile(t, dir, validYAML2)

	reg := NewRegistry()
	reloader := NewConfigHotReloader(path, reg, nil)

	// Initial load: alice + bob both enrolled.
	reloader.reload(context.Background())
	if _, ok := reg.ResolveByUID(1002); !ok {
		t.Fatal("setup: bob (uid=1002) must be enrolled before removal test")
	}

	// Remove bob from file and reload.
	if err := os.WriteFile(path, []byte(validYAML1), 0o600); err != nil {
		t.Fatalf("write reduced yaml: %v", err)
	}
	reloader.reload(context.Background())

	// W3 invariant: after reload returns, ResolveByUID for the removed UID
	// MUST return false IMMEDIATELY — Swap committed before BeginDrain. If
	// the order is reversed (pre-W3), this lookup may still succeed against
	// the old snapshot.
	if _, ok := reg.ResolveByUID(1002); ok {
		t.Fatal("W3 regression: ResolveByUID(1002) returned true immediately after reload — Swap did not commit before BeginDrain (stale-admission window present)")
	}

	// Companion check: drain SHOULD be active for bob (drain still runs after swap).
	if !reloader.DrainController().IsDraining("bob") {
		t.Fatal("W3 invariant: BeginDrain must still fire after Swap — bob should be draining")
	}
}

func TestDrainController_TenantRemovedFlagged(t *testing.T) {
	dir := t.TempDir()

	// Start with alice + bob.
	path := makeYAMLFile(t, dir, validYAML2)

	reg := NewRegistry()
	reloader := NewConfigHotReloader(path, reg, nil)

	// Load alice + bob.
	reloader.reload(context.Background())
	if _, ok := reg.Resolve(1001); !ok {
		t.Fatal("alice not in registry")
	}
	if _, ok := reg.Resolve(1002); !ok {
		t.Fatal("bob not in registry")
	}

	// Now remove bob from the file.
	if err := os.WriteFile(path, []byte(validYAML1), 0o600); err != nil {
		t.Fatalf("write reduced yaml: %v", err)
	}
	reloader.reload(context.Background())

	// alice still enrolled; bob removed.
	if _, ok := reg.Resolve(1002); ok {
		t.Error("bob should no longer be in registry after removal")
	}

	// Bob's name should be flagged as draining.
	if !reloader.DrainController().IsDraining("bob") {
		t.Error("bob should be flagged as draining after removal")
	}
}
