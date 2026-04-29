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
// When the timer expires it emits a TenantDrained log entry (Phase 8: event bus).
func (d *TenantDrainController) BeginDrain(tenantName string, drainSeconds int) {
	d.mu.Lock()
	d.draining[tenantName] = true
	d.mu.Unlock()

	if drainSeconds <= 0 {
		drainSeconds = defaultRemovalDrainSeconds
	}
	go func() {
		time.Sleep(time.Duration(drainSeconds) * time.Second)
		// Phase 8: emit TenantDrained event here to force-close sessions.
		log.Printf("tenant drain: %q drain window expired after %ds — sessions may be force-closed in Phase 8", tenantName, drainSeconds)
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
			h.reload()
			lastReload = time.Now()
		}
	}
}

// reload reads tenants.yaml, validates it, and swaps the registry snapshot.
// On failure the existing snapshot is retained and the error is logged.
func (h *ConfigHotReloader) reload() {
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
				h.drain.BeginDrain(cfg.Name, cfg.RemovalDrainSeconds)
			}
		}
	}

	h.registry.Swap(newSnap)
	log.Printf("tenant hot-reload: reloaded %q — %d tenant(s) enrolled", h.path, len(newSnap.byUID))
}

