// Package driver — per-(cli, role) capability cache.
//
// AIMUX-16 CR-003 (FR-3): the static `profile.Capabilities[]` field is a
// *declared* set; this cache holds the *verified* set produced by role-shaped
// warmup probes. Routing reads the verified set; declared remains a fallback
// only on cache miss while an inline probe is in flight.
//
// Concurrency: the cache uses a sync.RWMutex. Reads (Get / VerifiedRoles /
// Snapshot) take the read lock; mutations (Set / setLocked) take the write
// lock. Refresher reads + writes are coordinated via the same lock.
//
// TTL semantics:
//   - Entry younger than TTL → fresh, used as-is.
//   - Entry older than TTL → stale; background refresher schedules a re-probe;
//     foreground dispatch may inline-re-probe ONLY for that tuple.
//   - Cache miss → routing uses declared as fallback while an inline probe runs.
package driver

import (
	"context"
	"sort"
	"sync"
	"time"
)

const (
	// DefaultCapabilityCacheTTL is the fallback TTL when config is unset / zero.
	// Per AIMUX-16 clarify Q2: 1h matches Anthropic / Gemini routing budgets.
	DefaultCapabilityCacheTTL = 1 * time.Hour

	// minRefreshInterval is the lower bound for the refresher tick.
	// Prevents pathological tight loops if TTL/2 is configured very small.
	minRefreshInterval = 5 * time.Second
)

// ProbeResult is a single per-(cli, role) probe outcome stored in the cache.
//
// Verified=true means the role-shaped probe produced a role-shaped response.
// Verified=false with non-nil Err means the probe ran and failed (e.g. CLI is
// alive but the response did not acknowledge the role). LastProbed is the
// wall-clock time the probe completed; it drives TTL staleness checks.
type ProbeResult struct {
	Verified   bool
	LastProbed time.Time
	Err        error
}

// CapabilityCache stores per-(cli, role) verified probe results.
//
// Zero value is NOT usable — construct via NewCapabilityCache. The cache is
// safe for concurrent use by multiple goroutines.
type CapabilityCache struct {
	ttl   time.Duration
	mu    sync.RWMutex
	now   func() time.Time // injectable clock for tests
	store map[capabilityKey]ProbeResult
}

// capabilityKey is the composite (cli, role) key used internally.
// A struct is used instead of "cli/role" string concatenation to avoid
// ambiguity when role names contain "/" (none currently, but future-proof).
type capabilityKey struct {
	CLI  string
	Role string
}

// NewCapabilityCache constructs an empty capability cache with the given TTL.
// A non-positive ttl resolves to DefaultCapabilityCacheTTL.
func NewCapabilityCache(ttl time.Duration) *CapabilityCache {
	if ttl <= 0 {
		ttl = DefaultCapabilityCacheTTL
	}
	return &CapabilityCache{
		ttl:   ttl,
		now:   time.Now,
		store: make(map[capabilityKey]ProbeResult),
	}
}

// TTL returns the configured staleness window. Read-only after construction.
func (c *CapabilityCache) TTL() time.Duration {
	return c.ttl
}

// Get returns the probe result for (cli, role) and whether the entry exists.
// Existence is independent of staleness — callers use IsStale to decide
// whether to trigger a re-probe.
func (c *CapabilityCache) Get(cli, role string) (ProbeResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.store[capabilityKey{CLI: cli, Role: role}]
	return r, ok
}

// IsVerified returns (verified, miss).
//   - miss=true means no entry exists for (cli, role) — caller should treat
//     declared capability as a soft fallback while an inline probe runs.
//   - miss=false, verified=true means the cached entry says the role was
//     verified by the most recent probe.
//   - miss=false, verified=false means the role probe ran and failed; routing
//     MUST exclude this CLI for this role.
func (c *CapabilityCache) IsVerified(cli, role string) (verified bool, miss bool) {
	r, ok := c.Get(cli, role)
	if !ok {
		return false, true
	}
	return r.Verified, false
}

// IsStale reports whether the entry's last-probe timestamp is older than TTL.
// Returns true for the zero ProbeResult (treat as never-probed → stale).
func (c *CapabilityCache) IsStale(r ProbeResult) bool {
	if r.LastProbed.IsZero() {
		return true
	}
	return c.now().Sub(r.LastProbed) >= c.ttl
}

// Set records a probe outcome for (cli, role). LastProbed is overwritten with
// the current clock value; callers do not need to set it explicitly.
func (c *CapabilityCache) Set(cli, role string, verified bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[capabilityKey{CLI: cli, Role: role}] = ProbeResult{
		Verified:   verified,
		LastProbed: c.now(),
		Err:        err,
	}
}

// SetWithTime is like Set but allows the caller to specify the timestamp.
// Used by the refresher when batching probes; tests use it via the injected
// clock to simulate stale entries.
func (c *CapabilityCache) SetWithTime(cli, role string, verified bool, err error, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[capabilityKey{CLI: cli, Role: role}] = ProbeResult{
		Verified:   verified,
		LastProbed: at,
		Err:        err,
	}
}

// Delete removes a (cli, role) entry. Used to evict bad entries explicitly;
// staleness is normally handled in-place by the refresher.
func (c *CapabilityCache) Delete(cli, role string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.store, capabilityKey{CLI: cli, Role: role})
}

// Snapshot returns a copy of every entry in the cache, keyed by cli → role →
// ProbeResult. The returned maps are independent of the cache; mutating them
// has no effect on cache state.
//
// Used by health-action observability (sessions tool) to render verified-vs-
// declared per CLI without leaking the internal map.
func (c *CapabilityCache) Snapshot() map[string]map[string]ProbeResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]map[string]ProbeResult, len(c.store))
	for k, v := range c.store {
		if _, ok := out[k.CLI]; !ok {
			out[k.CLI] = make(map[string]ProbeResult)
		}
		out[k.CLI][k.Role] = v
	}
	return out
}

// VerifiedRoles returns the sorted set of roles for which (cli) has Verified=true
// in the cache. Roles for which the entry is not yet present are NOT included —
// callers must treat absence as miss (declared used as fallback).
func (c *CapabilityCache) VerifiedRoles(cli string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var roles []string
	for k, v := range c.store {
		if k.CLI == cli && v.Verified {
			roles = append(roles, k.Role)
		}
	}
	sort.Strings(roles)
	return roles
}

// staleEntries returns every (cli, role) tuple whose LastProbed is older than TTL.
// Used by the refresher to schedule background re-probes.
func (c *CapabilityCache) staleEntries() []capabilityKey {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := c.now()
	var stale []capabilityKey
	for k, v := range c.store {
		if v.LastProbed.IsZero() || now.Sub(v.LastProbed) >= c.ttl {
			stale = append(stale, k)
		}
	}
	return stale
}

// ProbeFn is the per-(cli, role) probe contract used by the refresher.
// Implementations MUST honor ctx cancellation. Returning (verified=false, err)
// means the probe ran and the role is not verified; the caller stores the
// result with the current timestamp so the entry is not re-probed until
// the next TTL cycle.
type ProbeFn func(ctx context.Context, cli, role string) (verified bool, err error)

// CapabilityRefresher periodically re-probes stale cache entries.
//
// Lifecycle: NewCapabilityRefresher → Start (idempotent; spawns goroutine) →
// Stop (cancels ctx; goroutine drains and exits). The refresher is bound to
// a single CapabilityCache and a single ProbeFn; daemon construction wires
// it once and never reuses it.
type CapabilityRefresher struct {
	cache    *CapabilityCache
	probe    ProbeFn
	interval time.Duration

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// NewCapabilityRefresher creates a refresher with tick = max(TTL/2, minRefreshInterval).
// Pass a nil probe to disable refreshing (Start becomes a no-op).
func NewCapabilityRefresher(cache *CapabilityCache, probe ProbeFn) *CapabilityRefresher {
	interval := cache.TTL() / 2
	if interval < minRefreshInterval {
		interval = minRefreshInterval
	}
	return &CapabilityRefresher{
		cache:    cache,
		probe:    probe,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start launches the background goroutine. Safe to call from any goroutine,
// idempotent — repeated calls return immediately.
//
// The goroutine exits when:
//   - Stop is called (closes stopCh), OR
//   - parent ctx is cancelled.
func (r *CapabilityRefresher) Start(ctx context.Context) {
	if r.probe == nil {
		// No probe wired — refresher is inert. Mark started so Stop never blocks.
		r.startOnce.Do(func() { close(r.doneCh) })
		return
	}
	r.startOnce.Do(func() {
		go r.loop(ctx)
	})
}

// Stop cancels the refresher and waits for the goroutine to drain.
// Safe to call multiple times; safe to call before Start (becomes a no-op
// returning immediately); subsequent calls return immediately.
func (r *CapabilityRefresher) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	// If Start was never called, doneCh is still open and the goroutine never
	// launched — closing doneCh here unblocks the wait. startOnce.Do ensures
	// at most one of {Start-spawned goroutine, this branch} closes doneCh.
	r.startOnce.Do(func() {
		close(r.doneCh)
	})
	<-r.doneCh
}

func (r *CapabilityRefresher) loop(ctx context.Context) {
	defer close(r.doneCh)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.refreshOnce(ctx)
		}
	}
}

// refreshOnce probes every stale entry. Each probe runs sequentially so we
// don't fan out N goroutines against the same CLI; the per-probe budget is
// bounded by the ProbeFn implementation (typically warmup_timeout_seconds).
func (r *CapabilityRefresher) refreshOnce(ctx context.Context) {
	for _, k := range r.cache.staleEntries() {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		default:
		}
		verified, err := r.probe(ctx, k.CLI, k.Role)
		r.cache.Set(k.CLI, k.Role, verified, err)
	}
}

// Tick exposes the refresher interval for tests + observability.
func (r *CapabilityRefresher) Tick() time.Duration {
	return r.interval
}
