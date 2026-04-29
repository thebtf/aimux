package tenant

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"
)

// reloadCoalesceWindow is the minimum interval between successive reloads.
// Multiple SIGHUP signals within this window are coalesced into a single reload.
const reloadCoalesceWindow = 5 * time.Second

// TenantDrainController manages the graceful removal of tenants from the registry.
// In Phase 1 this is a stub that sets a draining flag and starts a countdown timer.
// Phase 8 will wire the TenantDrained event to force-close affected sessions.
type TenantDrainController struct {
	mu       sync.Mutex
	draining map[string]bool // tenantName → draining
}

// newDrainController returns an empty TenantDrainController.
func newDrainController() *TenantDrainController {
	return &TenantDrainController{draining: make(map[string]bool)}
}

// BeginDrain marks the named tenant as draining and starts a countdown timer.
// When the timer expires the entry is removed from the draining map (so the
// next time the same tenant name re-enrolls it is not stuck draining) and a
// TenantDrained log entry is emitted (Phase 8: event bus).
//
// ctx is honoured for cancellation: when the daemon shuts down before the
// drain window expires, the goroutine exits without emitting the timeout log
// and still removes the map entry so the controller is safe for re-use in
// tests that share a controller across reloads.
//
// PRC v3 B8 — prior implementation leaked one goroutine + map entry per drain
// cycle (no context, no map cleanup). Repeated SIGHUP add/remove churn on
// the same UID held the tenant in "draining" forever from the POV of the
// adapter's IsDraining check, denying admission indefinitely.
func (d *TenantDrainController) BeginDrain(ctx context.Context, tenantName string, drainSeconds int) {
	d.mu.Lock()
	d.draining[tenantName] = true
	d.mu.Unlock()

	if drainSeconds <= 0 {
		drainSeconds = defaultRemovalDrainSeconds
	}
	go func() {
		timer := time.NewTimer(time.Duration(drainSeconds) * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
			// Phase 8: emit TenantDrained event here to force-close sessions.
			log.Printf("tenant drain: %q drain window expired after %ds — sessions may be force-closed in Phase 8", tenantName, drainSeconds)
		case <-ctx.Done():
			// Daemon shutting down — exit without the timeout log.
		}

		// Map cleanup runs on both paths so that a re-enrolled tenant
		// (same name) does not inherit the draining flag from a previous
		// cycle.
		d.mu.Lock()
		delete(d.draining, tenantName)
		d.mu.Unlock()
	}()
}

// IsDraining reports whether tenantName is currently in the drain window.
func (d *TenantDrainController) IsDraining(tenantName string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.draining[tenantName]
}

// ConfigHotReloader listens for SIGHUP on a signal channel and reloads tenants.yaml.
// Successive signals within reloadCoalesceWindow are coalesced into a single reload.
// On reload failure the previous snapshot is retained; the error is logged.
type ConfigHotReloader struct {
	path     string
	registry *TenantRegistry
	drain    *TenantDrainController
	sigCh    <-chan os.Signal // injected for testability; nil means use SIGHUP
}

// NewConfigHotReloader is the canonical constructor for ConfigHotReloader.
// Pass a non-nil sigCh to inject a test signal channel instead of the real
// SIGHUP notification; pass nil for production use (Run will register for
// the platform-specific signal).
func NewConfigHotReloader(path string, registry *TenantRegistry, sigCh <-chan os.Signal) *ConfigHotReloader {
	return &ConfigHotReloader{
		path:     path,
		registry: registry,
		drain:    newDrainController(),
		sigCh:    sigCh,
	}
}

// DrainController returns the TenantDrainController used by this reloader.
// Callers may use it to inspect drain state in tests or wire Phase 8 events.
func (h *ConfigHotReloader) DrainController() *TenantDrainController {
	return h.drain
}

// Run starts the hot-reload loop. It blocks until ctx is cancelled.
// When h.sigCh is nil, Run registers for the platform-specific reload signal
// (SIGHUP on Unix). When h.sigCh is non-nil, Run consumes signals from the
// injected channel — used for unit tests to avoid sending real SIGHUP to the
// test process.
//
// ctx is propagated to drain goroutines started by reload() via BeginDrain so
// they exit cleanly on daemon shutdown.
func (h *ConfigHotReloader) Run(ctx context.Context) {
	ch := h.sigCh
	if ch == nil {
		// Production: register for real SIGHUP (platform-specific signal defined
		// in hotreload_unix.go / hotreload_windows.go).
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, reloadSignal)
		defer signal.Stop(sigCh)
		ch = sigCh
	}

	var lastReload time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			now := time.Now()
			if !lastReload.IsZero() && now.Sub(lastReload) < reloadCoalesceWindow {
				log.Printf("tenant hot-reload: SIGHUP coalesced (last reload %s ago, window %s)", now.Sub(lastReload).Round(time.Millisecond), reloadCoalesceWindow)
				continue
			}
			h.reload(ctx)
			lastReload = time.Now()
		}
	}
}

// reload reads tenants.yaml, validates it, and swaps the registry snapshot.
// On failure the existing snapshot is retained and the error is logged.
//
// ctx is forwarded to BeginDrain for any tenant that disappears between
// snapshots so the drain goroutine exits cleanly when the daemon shuts down.
func (h *ConfigHotReloader) reload(ctx context.Context) {
	oldSnap := h.registry.snapshot.Load()

	newSnap, err := LoadFromFile(h.path)
	if err != nil {
		log.Printf("tenant hot-reload: failed to reload %q: %v (retaining previous config)", h.path, err)
		return
	}

	// Compute removed tenants: UIDs present in oldSnap but absent in newSnap.
	if oldSnap != nil {
		for uid, cfg := range oldSnap.byUID {
			if _, stillPresent := newSnap.byUID[uid]; !stillPresent {
				log.Printf("tenant hot-reload: tenant %q (uid=%d) removed — starting drain window (%ds)", cfg.Name, uid, cfg.RemovalDrainSeconds)
				h.drain.BeginDrain(ctx, cfg.Name, cfg.RemovalDrainSeconds)
			}
		}
	}

	h.registry.Swap(newSnap)
	log.Printf("tenant hot-reload: reloaded %q — %d tenant(s) enrolled", h.path, len(newSnap.byUID))
}

