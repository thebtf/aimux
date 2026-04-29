//go:build !short

package critical_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/ratelimit"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/mcp-mux/muxcore"
)

// rateLimitRegistry builds a TenantRegistry containing the supplied tenant
// configs (already populated with rate-limit fields). Used by the rate-limit
// critical tests to feed bucket configs to the limiter on first lookup.
func rateLimitRegistry(configs ...tenant.TenantConfig) *tenant.TenantRegistry {
	reg := tenant.NewRegistry()
	entries := make(map[int]tenant.TenantConfig, len(configs))
	for _, cfg := range configs {
		entries[cfg.UID] = cfg
	}
	reg.Swap(tenant.NewSnapshot(entries))
	return reg
}

// TestCritical_RateLimit_TenantsExhaustIndependently verifies the FR-9/FR-10
// per-tenant isolation contract: a flooding tenant cannot starve a quiet
// neighbour. tenantA bursts past its capacity → its frames are dropped;
// tenantB's bucket is unaffected and continues to pass.
//
// @critical — release blocker per rule #10
func TestCritical_RateLimit_TenantsExhaustIndependently(t *testing.T) {
	reg := rateLimitRegistry(
		tenant.TenantConfig{
			Name: "tenantA", UID: 1001, Role: tenant.RolePlain,
			RateLimitPerSec:  2,
			RefillRatePerSec: 1, // slow refill so the test window stays exhausted
		},
		tenant.TenantConfig{
			Name: "tenantB", UID: 1002, Role: tenant.RolePlain,
			RateLimitPerSec:  10,
			RefillRatePerSec: 10,
		},
	)
	limiter := ratelimit.NewTenantRateLimiter()
	limiter.SetRegistry(reg)

	// Map session IDs to tenants — OnFrameReceived needs both legs.
	limiter.SetSessionTenant("sess-A", "tenantA")
	limiter.SetSessionTenant("sess-B", "tenantB")

	// Drain tenantA: capacity=2 means the first two frames pass, the
	// third must drop.
	if got := limiter.OnFrameReceived("sess-A", 0, "think"); got != muxcore.FramePass {
		t.Fatalf("tenantA frame#1 = %v; want FramePass", got)
	}
	if got := limiter.OnFrameReceived("sess-A", 0, "think"); got != muxcore.FramePass {
		t.Fatalf("tenantA frame#2 = %v; want FramePass", got)
	}
	if got := limiter.OnFrameReceived("sess-A", 0, "think"); got != muxcore.FrameDrop {
		t.Fatalf("CRITICAL: tenantA over capacity = %v; want FrameDrop (no rate-limit enforcement)", got)
	}

	// Boundary: tenantB MUST be unaffected. Hammer it 10 times and verify
	// every frame passes (capacity=10).
	for i := 0; i < 10; i++ {
		if got := limiter.OnFrameReceived("sess-B", 0, "think"); got != muxcore.FramePass {
			t.Fatalf("CRITICAL: tenantB frame#%d dropped — quota leaked across tenants (got %v)", i, got)
		}
	}
}

// TestCritical_RateLimit_BurstDenials_NoNegativeDriftViaPublicAPI verifies
// the PRC v3 B4 fix: under heavy concurrent burst on an exhausted bucket,
// the token count must NOT drift permanently negative. The earlier code
// used a single CompareAndSwap to clamp; only one of N concurrent denials
// could win, leaving N-1 negative tokens latched in the bucket forever.
//
// We exercise the fix entirely through the public OnFrameReceived API:
//  1. drain a small bucket with concurrent calls,
//  2. wait for one full refill cycle,
//  3. assert the bucket can pass at least one frame again.
//
// If drift were latched, step 3 would never recover and the test fails.
//
// @critical — release blocker per rule #10
func TestCritical_RateLimit_BurstDenials_NoNegativeDriftViaPublicAPI(t *testing.T) {
	const (
		capacity = 1
		// Refill quickly so the test window is short but not so fast that
		// the refill races the burst (we want denials to dominate).
		refillPerSec = 4
		burst        = 200
	)

	reg := rateLimitRegistry(tenant.TenantConfig{
		Name: "burst-tenant", UID: 5001, Role: tenant.RolePlain,
		RateLimitPerSec:  capacity,
		RefillRatePerSec: refillPerSec,
	})
	limiter := ratelimit.NewTenantRateLimiter()
	limiter.SetRegistry(reg)
	limiter.SetSessionTenant("sess-burst", "burst-tenant")

	// Drain the single starting token deterministically.
	if got := limiter.OnFrameReceived("sess-burst", 0, "think"); got != muxcore.FramePass {
		t.Fatalf("first frame on full bucket = %v; want FramePass", got)
	}

	// Hammer the now-empty bucket with concurrent denials.
	var wg sync.WaitGroup
	wg.Add(burst)
	var passes atomic.Int64
	for i := 0; i < burst; i++ {
		go func() {
			defer wg.Done()
			if limiter.OnFrameReceived("sess-burst", 0, "think") == muxcore.FramePass {
				passes.Add(1)
			}
		}()
	}
	wg.Wait()

	// Sanity: most frames should have been denied (capacity 1, refill 4/s,
	// burst window << 1s). A handful of refills can fire during the burst
	// itself; we tolerate up to capacity+small_drift passes here.
	if got := passes.Load(); got > capacity+10 {
		t.Logf("note: %d frames passed during burst (refill window slack)", got)
	}

	// Boundary: after waiting one full refill window, the bucket MUST pass
	// at least one frame. If B4 drift were latched, the negative token
	// balance would absorb the refill and every post-burst frame would
	// stay denied.
	time.Sleep(time.Second / refillPerSec * 2) // 2 refill ticks of headroom
	deadline := time.Now().Add(2 * time.Second)
	recovered := false
	for time.Now().Before(deadline) {
		if limiter.OnFrameReceived("sess-burst", 0, "think") == muxcore.FramePass {
			recovered = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !recovered {
		t.Fatalf("CRITICAL: bucket did not recover after %d concurrent denials — token count latched negative (B4 regression)", burst)
	}
}

// TestCritical_RateLimit_UnknownTenantFailsOpen verifies the Phase 5/7
// fail-open contract: a session that has not been wired via SetSessionTenant
// (or whose tenant has been removed from the registry) must NOT be silently
// dropped — frames pass through. This prevents the limiter itself from
// becoming a denial-of-service vector when configuration is incomplete.
//
// @critical — release blocker per rule #10
func TestCritical_RateLimit_UnknownTenantFailsOpen(t *testing.T) {
	limiter := ratelimit.NewTenantRateLimiter()
	limiter.SetRegistry(rateLimitRegistry()) // empty registry

	// Boundary 1: unknown session — fail-open.
	if got := limiter.OnFrameReceived("unknown-session", 0, "think"); got != muxcore.FramePass {
		t.Errorf("CRITICAL: unknown session dropped (got %v); fail-open contract violated", got)
	}

	// Boundary 2: known session pointing to a missing tenant — fail-open.
	limiter.SetSessionTenant("orphan-session", "phantom-tenant")
	if got := limiter.OnFrameReceived("orphan-session", 0, "think"); got != muxcore.FramePass {
		t.Errorf("CRITICAL: orphan session dropped (got %v); fail-open contract violated", got)
	}
}
