// Package ratelimit — per-tenant token bucket rate limiter wired into the
// muxcore OnFrameReceived hook.
//
// Design notes:
//   - tokenBucket uses atomic.Int64 for both tokens and lastRefill; no mutex
//     on the hot path.
//   - CAS double-refill protection: only one goroutine per window wins the
//     CompareAndSwap on lastRefill; all others see an already-updated stamp.
//   - TenantRateLimiter stores buckets in a sync.Map keyed by tenant name.
//   - Sessions are mapped to tenant names via a separate sync.Map; unknown
//     sessions fail-open (FramePass) per Phase 5/7 fail-open contract.
package ratelimit

import (
	"sync"
	"sync/atomic"
	"time"

	muxcore "github.com/thebtf/mcp-mux/muxcore"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/tenant"
)

// tokenBucket is a per-tenant atomic token bucket. All fields are accessed
// without a mutex; correctness relies on atomic operations and CAS for refill.
type tokenBucket struct {
	// tokens is the current token count, stored as a fixed-point integer
	// (actual tokens = tokens). May temporarily go negative under load; Allow
	// treats any value < 1 as exhausted.
	tokens atomic.Int64

	// lastRefill is the Unix nanosecond timestamp of the last refill window.
	// Used as the CAS key to ensure exactly one goroutine per window performs
	// the refill.
	lastRefill atomic.Int64

	// capacity is the maximum number of tokens (== RateLimitPerSec).
	capacity int64

	// refillPerSec is the number of tokens added per second (== RefillRatePerSec).
	refillPerSec int64
}

// newTokenBucket creates a full bucket with the given capacity and refill rate.
//
// Guards against the divide-by-zero panic on the hot path: when refillPerSec is
// zero or negative, the bucket falls back to refillPerSec=capacity (one full
// refill per second). WithDefaults on TenantConfig protects YAML-driven values,
// but legacy snapshots and direct callers (LegacyDefaultSnapshot, future API
// surfaces) may still hand us an unset rate; this guard makes the constructor
// robust without requiring every caller to remember the contract.
func newTokenBucket(capacity, refillPerSec int64) *tokenBucket {
	if refillPerSec <= 0 {
		// Fallback: refill the bucket fully once per second. capacity may also
		// be zero on misconfigured input; guard that too so tokens.Store(0) is
		// safe and the first allow() call denies cleanly without panicking.
		if capacity <= 0 {
			capacity = 1
		}
		refillPerSec = capacity
	}
	b := &tokenBucket{
		capacity:     capacity,
		refillPerSec: refillPerSec,
	}
	b.tokens.Store(capacity)
	b.lastRefill.Store(time.Now().UnixNano())
	return b
}

// allow attempts to consume one token. Returns true when the token was consumed,
// false when the bucket is exhausted.
//
// Refill logic: compute elapsed time since lastRefill; if elapsed ≥ 1/refillPerSec
// attempt a CAS on lastRefill. The winner refills tokens; losers skip refill but
// still attempt to consume from the (now-refilled) bucket.
func (b *tokenBucket) allow() bool {
	now := time.Now().UnixNano()
	last := b.lastRefill.Load()

	// Nanoseconds per token at the configured refill rate.
	nsPerToken := int64(time.Second) / b.refillPerSec

	elapsed := now - last
	if elapsed >= nsPerToken {
		// Attempt to claim this refill window via CAS. Only one goroutine
		// per window wins; the rest skip refill and fall through to the
		// consume attempt below, reading the freshly-refilled token count.
		newLast := last + (elapsed/nsPerToken)*nsPerToken
		if b.lastRefill.CompareAndSwap(last, newLast) {
			// This goroutine won the refill race. Add tokens proportional
			// to elapsed windows, capped at capacity.
			toAdd := (elapsed / nsPerToken)
			current := b.tokens.Load()
			refilled := current + toAdd
			if refilled > b.capacity {
				refilled = b.capacity
			}
			// Store the refilled count. No loop needed: we won the CAS on
			// lastRefill, so no concurrent goroutine is also refilling.
			b.tokens.Store(refilled)
		}
	}

	// Consume one token atomically. AddInt64(-1) returns the new value.
	remaining := b.tokens.Add(-1)
	if remaining < 0 {
		// Give the token back — we can't hold a negative balance and the
		// caller is denied. Clamp at 0 rather than restoring exact value to
		// avoid obscure overflow scenarios under extreme concurrency.
		b.tokens.CompareAndSwap(remaining, 0)
		return false
	}
	return true
}

// TenantRateLimiter enforces per-tenant token bucket rate limits. It is safe
// for concurrent use by multiple goroutines.
//
// Sessions are associated with tenants via SetSessionTenant. Frames arriving
// on unknown sessions are passed through (fail-open) without consuming tokens.
type TenantRateLimiter struct {
	// buckets maps tenantID (name string) → *tokenBucket.
	buckets sync.Map

	// sessions maps sessionID (string) → tenantID (name string).
	sessions sync.Map

	// registry is the live TenantRegistry used to look up config on first
	// bucket creation. Stored so OnFrameReceived doesn't require the caller
	// to pass it separately.
	registry atomic.Pointer[tenant.TenantRegistry]

	// auditLog receives non-blocking rate_limited events. May be nil (no-op).
	auditLog atomic.Pointer[audit.AuditLog]
}

// NewTenantRateLimiter creates a ready-to-use TenantRateLimiter.
func NewTenantRateLimiter() *TenantRateLimiter {
	return &TenantRateLimiter{}
}

// SetRegistry stores the TenantRegistry so OnFrameReceived can resolve
// bucket configs without the registry being passed per-call. Call this once
// during daemon startup before wiring OnFrameReceived into engine.Config.
func (l *TenantRateLimiter) SetRegistry(reg *tenant.TenantRegistry) {
	l.registry.Store(reg)
}

// SetAuditLog attaches an audit.AuditLog that receives rate_limited events on
// every FrameDrop. The log's Emit method is called directly on the frame reader
// goroutine, so it must be non-blocking (FileAuditLog uses a buffered channel
// and satisfies this requirement). Calling SetAuditLog is optional; when nil
// (the default), audit events are silently dropped.
func (l *TenantRateLimiter) SetAuditLog(log audit.AuditLog) {
	l.auditLog.Store(&log)
}

// SetSessionTenant associates a muxcore sessionID with a tenant name.
// Call this when a session is authenticated (e.g. in AuthorizeSession).
// Subsequent OnFrameReceived calls for that sessionID will be rate-limited
// against the named tenant's bucket.
func (l *TenantRateLimiter) SetSessionTenant(sessionID, tenantID string) {
	l.sessions.Store(sessionID, tenantID)
}

// RemoveSession removes the session→tenant association. Call on session close
// to avoid unbounded map growth.
func (l *TenantRateLimiter) RemoveSession(sessionID string) {
	l.sessions.Delete(sessionID)
}

// Allow checks whether the named tenant is within its rate limit.
// It lazily creates a token bucket on first call using config from registry.
// Returns true (pass) when:
//   - A token was successfully consumed from the bucket.
//   - The tenant is not found in the registry (fail-open).
//
// Returns false (deny) only when the bucket is exhausted.
func (l *TenantRateLimiter) Allow(tenantID string, reg *tenant.TenantRegistry) bool {
	b := l.getOrCreateBucket(tenantID, reg)
	if b == nil {
		// Tenant not found in registry — fail-open.
		return true
	}
	return b.allow()
}

// OnFrameReceived is the muxcore engine.Config.OnFrameReceived callback.
// It maps sessionID → tenantID → token bucket and returns the verdict.
//
// Fail-open cases (returns FramePass without consuming tokens):
//   - sessionID not mapped via SetSessionTenant
//   - tenant name not found in the stored registry
//   - registry not set via SetRegistry
func (l *TenantRateLimiter) OnFrameReceived(sessionID string, _ int, method string) muxcore.FrameAction {
	tenantIDAny, ok := l.sessions.Load(sessionID)
	if !ok {
		// Unknown session — fail-open per Phase 5/7 contract.
		return muxcore.FramePass
	}
	tenantID, _ := tenantIDAny.(string)

	reg := l.registry.Load()
	if reg == nil {
		return muxcore.FramePass
	}

	if l.Allow(tenantID, reg) {
		return muxcore.FramePass
	}

	// Non-blocking audit emit: FileAuditLog.Emit uses a buffered channel and
	// never blocks. Calling it directly on the frame reader goroutine is safe.
	if logPtr := l.auditLog.Load(); logPtr != nil {
		(*logPtr).Emit(audit.AuditEvent{
			Timestamp: time.Now(),
			EventType: audit.EventRateLimited,
			TenantID:  tenantID,
			ToolName:  method,
			Result:    "dropped",
			Reason:    "token bucket exhausted",
		})
	}

	return muxcore.FrameDrop
}

// getOrCreateBucket returns the tokenBucket for tenantID, creating it lazily
// from the registry if it does not yet exist. Returns nil when the tenant is
// unknown in the registry.
func (l *TenantRateLimiter) getOrCreateBucket(tenantID string, reg *tenant.TenantRegistry) *tokenBucket {
	if v, ok := l.buckets.Load(tenantID); ok {
		return v.(*tokenBucket)
	}

	cfg, found := reg.ResolveByName(tenantID)
	if !found {
		return nil
	}

	b := newTokenBucket(cfg.RateLimitPerSec, cfg.RefillRatePerSec)
	// LoadOrStore: if another goroutine raced us, discard our bucket and use
	// the winner's. This is safe — both buckets start full at construction.
	actual, _ := l.buckets.LoadOrStore(tenantID, b)
	return actual.(*tokenBucket)
}
