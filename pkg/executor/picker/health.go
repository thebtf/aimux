package picker

import (
	"context"
	"os/exec"
	"sync"
	"time"
)

// defaultHealthCacheTTL is the fallback TTL used when PickerConfig.HealthCacheTTL is zero.
const defaultHealthCacheTTL = 60 * time.Second

// healthEntry is one cached health check result.
type healthEntry struct {
	healthy   bool
	reason    string
	expiresAt time.Time
}

// HealthChecker probes whether a CLI binary is available on PATH and caches
// the result for the configured TTL. Results are goroutine-safe.
//
// The binary name resolver and the LookPath function are injected at
// construction time so tests can substitute without touching the filesystem.
type HealthChecker struct {
	cfg        *PickerConfig
	binaryFor  func(cli string) string       // resolves CLI name → binary name
	lookPath   func(name string) (string, error) // defaults to exec.LookPath
	cache      sync.Map                      // key: CLI name, value: *healthEntry
	activeCLIs []string
}

// NewHealthChecker constructs a HealthChecker.
//
//   - cfg: picker configuration (for HealthCacheTTL); must not be nil
//   - binaryFor: maps a CLI name (e.g., "codex") to a binary name (e.g., "codex"); must not be nil
//   - activeCLIs: the ordered list of CLIs to probe in WarmAll
//
// Pass nil for lookPath to use exec.LookPath.
func NewHealthChecker(cfg *PickerConfig, binaryFor func(string) string, activeCLIs []string, lookPath func(string) (string, error)) *HealthChecker {
	if cfg == nil {
		panic("picker: cfg must not be nil")
	}
	if binaryFor == nil {
		panic("picker: binaryFor must not be nil")
	}
	lp := lookPath
	if lp == nil {
		lp = exec.LookPath
	}
	return &HealthChecker{
		cfg:        cfg,
		binaryFor:  binaryFor,
		lookPath:   lp,
		activeCLIs: activeCLIs,
	}
}

// IsHealthy returns true if the CLI binary is present on PATH.
// Results are cached for HealthCacheTTL. A cache miss triggers a synchronous probe.
func (h *HealthChecker) IsHealthy(cli string) bool {
	healthy, _ := h.isHealthyWithReason(cli)
	return healthy
}

// isHealthyWithReason returns (healthy, reason) for the given CLI,
// using the cache if the entry is still fresh.
func (h *HealthChecker) isHealthyWithReason(cli string) (bool, string) {
	now := time.Now()

	if v, ok := h.cache.Load(cli); ok {
		entry := v.(*healthEntry)
		if now.Before(entry.expiresAt) {
			return entry.healthy, entry.reason
		}
	}

	// Cache miss or expired — probe synchronously.
	healthy, reason := h.Probe(cli)
	ttl := h.cfg.HealthCacheTTL
	if ttl <= 0 {
		ttl = defaultHealthCacheTTL
	}
	h.cache.Store(cli, &healthEntry{
		healthy:   healthy,
		reason:    reason,
		expiresAt: now.Add(ttl),
	})
	return healthy, reason
}

// Probe performs a live binary presence check for the given CLI.
// It does not use the cache and does not update the cache.
// Use IsHealthy for normal operation; Probe is exposed for testing and WarmAll.
func (h *HealthChecker) Probe(cli string) (bool, string) {
	binary := h.binaryFor(cli)
	if binary == "" {
		return false, "no binary name configured"
	}

	if _, err := h.lookPath(binary); err != nil {
		return false, "binary not found in PATH: " + binary
	}
	return true, ""
}

// WarmAll probes all active CLIs and populates the cache.
// Call this at daemon startup (NFR-2) so Pick never pays probe latency.
// Context cancellation aborts remaining probes.
func (h *HealthChecker) WarmAll(ctx context.Context) {
	for _, cli := range h.activeCLIs {
		select {
		case <-ctx.Done():
			return
		default:
		}
		healthy, reason := h.Probe(cli)
		ttl := h.cfg.HealthCacheTTL
		if ttl <= 0 {
			ttl = defaultHealthCacheTTL
		}
		h.cache.Store(cli, &healthEntry{
			healthy:   healthy,
			reason:    reason,
			expiresAt: time.Now().Add(ttl),
		})
	}
}
