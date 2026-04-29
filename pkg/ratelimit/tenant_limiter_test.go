package ratelimit

import (
	"sync"
	"testing"
	"time"

	muxcore "github.com/thebtf/mcp-mux/muxcore"

	"github.com/thebtf/aimux/pkg/tenant"
)

// buildTestRegistry returns a TenantRegistry pre-loaded with the given tenant configs.
func buildTestRegistry(configs []tenant.TenantConfig) *tenant.TenantRegistry {
	reg := tenant.NewRegistry()
	entries := make(map[int]tenant.TenantConfig, len(configs))
	for _, cfg := range configs {
		entries[cfg.UID] = cfg
	}
	reg.Swap(tenant.NewSnapshot(entries))
	return reg
}

func TestTenantRateLimiter_Allow_FirstCallSucceeds(t *testing.T) {
	reg := buildTestRegistry([]tenant.TenantConfig{
		{Name: "alice", UID: 1001, Role: tenant.RolePlain, RateLimitPerSec: 10, RefillRatePerSec: 10},
	})
	limiter := NewTenantRateLimiter()
	if !limiter.Allow("alice", reg) {
		t.Fatal("expected first Allow to return true for tenant alice")
	}
}

func TestTenantRateLimiter_Allow_DepletesAndDenies(t *testing.T) {
	// capacity=2 means exactly 2 tokens; 3rd call must return false.
	reg := buildTestRegistry([]tenant.TenantConfig{
		{Name: "bob", UID: 1002, Role: tenant.RolePlain, RateLimitPerSec: 2, RefillRatePerSec: 2},
	})
	limiter := NewTenantRateLimiter()
	if !limiter.Allow("bob", reg) {
		t.Fatal("1st call should succeed")
	}
	if !limiter.Allow("bob", reg) {
		t.Fatal("2nd call should succeed (capacity=2)")
	}
	if limiter.Allow("bob", reg) {
		t.Fatal("3rd call should be denied (bucket depleted)")
	}
}

func TestTenantRateLimiter_Allow_RefillRestores(t *testing.T) {
	// capacity=1, refill=2/sec → after 600ms at least 1 token refilled.
	reg := buildTestRegistry([]tenant.TenantConfig{
		{Name: "carol", UID: 1003, Role: tenant.RolePlain, RateLimitPerSec: 1, RefillRatePerSec: 2},
	})
	limiter := NewTenantRateLimiter()
	if !limiter.Allow("carol", reg) {
		t.Fatal("1st call should succeed")
	}
	if limiter.Allow("carol", reg) {
		t.Fatal("immediate 2nd call should be denied")
	}
	// Wait for refill: 2 tokens/sec → 1 token replenished in 500ms.
	time.Sleep(600 * time.Millisecond)
	if !limiter.Allow("carol", reg) {
		t.Fatal("call after refill interval should succeed")
	}
}

func TestTenantRateLimiter_PerTenantIsolation(t *testing.T) {
	reg := buildTestRegistry([]tenant.TenantConfig{
		{Name: "tenantA", UID: 2001, Role: tenant.RolePlain, RateLimitPerSec: 1, RefillRatePerSec: 1},
		{Name: "tenantB", UID: 2002, Role: tenant.RolePlain, RateLimitPerSec: 10, RefillRatePerSec: 10},
	})
	limiter := NewTenantRateLimiter()

	// Exhaust tenantA (capacity=1).
	if !limiter.Allow("tenantA", reg) {
		t.Fatal("tenantA first call should succeed")
	}
	if limiter.Allow("tenantA", reg) {
		t.Fatal("tenantA second call should be denied (exhausted)")
	}

	// tenantB must be unaffected.
	if !limiter.Allow("tenantB", reg) {
		t.Fatal("tenantB should not be blocked by tenantA exhaustion")
	}
}

func TestTenantRateLimiter_ConcurrentCAS_NoDoubleRefill(t *testing.T) {
	// Verify that concurrent goroutines racing to refill after bucket exhaustion
	// do NOT double-refill. Run with -race to catch data races.
	reg := buildTestRegistry([]tenant.TenantConfig{
		{Name: "concurrent", UID: 3001, Role: tenant.RolePlain, RateLimitPerSec: 5, RefillRatePerSec: 5},
	})
	limiter := NewTenantRateLimiter()

	// Exhaust the bucket.
	for i := 0; i < 5; i++ {
		limiter.Allow("concurrent", reg)
	}

	// Wait for refill window to open.
	time.Sleep(300 * time.Millisecond)

	// Launch concurrent goroutines attempting to refill at the same moment.
	const goroutines = 20
	results := make([]bool, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i] = limiter.Allow("concurrent", reg)
		}()
	}
	wg.Wait()

	// Count how many succeeded. With capacity=5 and 300ms elapsed at 5/sec,
	// we expect at most capacity (5) tokens to be granted — not goroutines (20).
	successCount := 0
	for _, ok := range results {
		if ok {
			successCount++
		}
	}
	if successCount > 5 {
		t.Fatalf("CAS double-refill protection failed: %d goroutines succeeded (max allowed=5)", successCount)
	}
}

func TestTenantRateLimiter_OnFrameReceived_ReturnsFrameDrop(t *testing.T) {
	// Mock registry with a tenant that has capacity=1.
	reg := buildTestRegistry([]tenant.TenantConfig{
		{Name: "framedrop-tenant", UID: 4001, Role: tenant.RolePlain, RateLimitPerSec: 1, RefillRatePerSec: 1},
	})
	limiter := NewTenantRateLimiter()
	// Wire the registry and session→tenant map so OnFrameReceived can resolve the tenant.
	limiter.SetRegistry(reg)
	limiter.SetSessionTenant("sess-001", "framedrop-tenant")

	// First frame should pass.
	action := limiter.OnFrameReceived("sess-001", 100, "tools/call")
	if action != muxcore.FramePass {
		t.Fatalf("expected FramePass on first call, got %v", action)
	}

	// Exhaust the bucket — capacity=1 so it's now depleted.
	// Second frame should be dropped.
	action = limiter.OnFrameReceived("sess-001", 100, "tools/call")
	if action != muxcore.FrameDrop {
		t.Fatalf("expected FrameDrop on depleted bucket, got %v", action)
	}
}

func BenchmarkTenantRateLimiter_OnFrameReceived(b *testing.B) {
	reg := buildTestRegistry([]tenant.TenantConfig{
		{Name: "bench-tenant", UID: 9001, Role: tenant.RolePlain, RateLimitPerSec: 1000000, RefillRatePerSec: 1000000},
	})
	limiter := NewTenantRateLimiter()
	limiter.SetRegistry(reg)
	limiter.SetSessionTenant("bench-sess", "bench-tenant")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = limiter.OnFrameReceived("bench-sess", 128, "tools/call")
	}
}
