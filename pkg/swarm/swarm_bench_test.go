package swarm_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
)

// aliveFactory returns an executor factory producing always-alive mocks.
// Used by benchmarks to avoid factory cost polluting the measured path.
func aliveFactory() func(string) (types.ExecutorV2, error) {
	return func(_ string) (types.ExecutorV2, error) {
		return &mockExecutorV2{alive: types.HealthAlive}, nil
	}
}

// BenchmarkSwarm_Get measures the Stateful cache-hit path in legacy mode
// (empty TenantContext → LegacyDefault partition). The registry is pre-populated
// with one handle before the timer starts so every loop iteration exercises the
// cache-hit branch only — no factory call, no spawn overhead.
//
// Target: ≤ 200 ns/op overhead vs pre-AIMUX-13 baseline (NFR-1).
// The overhead is the cost of tenant.FromContext + registryKey + registry lookup.
func BenchmarkSwarm_Get(b *testing.B) {
	s := swarm.New(aliveFactory(), nil)
	ctx := context.Background()

	// Pre-populate the registry with a Stateful handle.
	if _, err := s.Get(ctx, "codex", swarm.Stateful); err != nil {
		b.Fatalf("pre-populate Get: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := s.Get(ctx, "codex", swarm.Stateful)
		if err != nil {
			b.Fatalf("Get: %v", err)
		}
	}
}

// BenchmarkSwarm_Get_Stateless measures the full spawn cost for Stateless mode.
// Every Get spawns a fresh executor — factory call dominates.
// This is a reference benchmark only; NFR-1 does NOT apply to the Stateless path.
func BenchmarkSwarm_Get_Stateless(b *testing.B) {
	s := swarm.New(aliveFactory(), nil)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := s.Get(ctx, "codex", swarm.Stateless)
		if err != nil {
			b.Fatalf("Get: %v", err)
		}
	}
}

// BenchmarkSwarm_Get_Concurrent_100Tenants verifies NFR-2 linear scaling:
// 100 distinct tenants each making 10 Stateful Gets on the same executor name.
// Expected outcome: 100 independent handles (one per tenant partition), no cross-tenant
// interference, no deadlock. Scaling should be linear in tenant count.
func BenchmarkSwarm_Get_Concurrent_100Tenants(b *testing.B) {
	const tenantCount = 100
	const getsPerTenant = 10

	s := swarm.New(aliveFactory(), nil)

	// Build tenant contexts once outside the timed loop.
	ctxs := make([]context.Context, tenantCount)
	for i := 0; i < tenantCount; i++ {
		tc := tenant.TenantContext{
			TenantID:         fmt.Sprintf("tenant-%03d", i),
			RequestStartedAt: time.Now(),
		}
		ctxs[i] = tenant.WithContext(context.Background(), tc)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for iter := 0; iter < b.N; iter++ {
		var wg sync.WaitGroup
		wg.Add(tenantCount)
		for i := 0; i < tenantCount; i++ {
			tCtx := ctxs[i]
			go func() {
				defer wg.Done()
				for j := 0; j < getsPerTenant; j++ {
					_, err := s.Get(tCtx, "codex", swarm.Stateful)
					if err != nil {
						b.Errorf("Get: %v", err)
						return
					}
				}
			}()
		}
		wg.Wait()
	}
}

// TestSwarm_SameTenantConcurrentGet verifies that 50 goroutines of the same
// tenant concurrently calling Get(Stateful) on the same name receive exactly
// one cached Handle — no double-spawn TOCTOU race (BUG-003 protection preserved
// after tenant partitioning was wired in).
//
// Anti-stub check: removing the find-or-spawn write-lock in Get (T005/BUG-003 fix)
// would allow multiple goroutines to spawn independent handles, producing len(seen) > 1
// and failing this test.
func TestSwarm_SameTenantConcurrentGet(t *testing.T) {
	t.Parallel()

	var spawnCount int
	var spawnMu sync.Mutex
	factory := func(name string) (types.ExecutorV2, error) {
		spawnMu.Lock()
		spawnCount++
		spawnMu.Unlock()
		// Small sleep to widen the race window.
		time.Sleep(time.Microsecond)
		return &mockExecutorV2{alive: types.HealthAlive}, nil
	}

	s := swarm.New(factory, nil)

	tc := tenant.TenantContext{
		TenantID:         "single-tenant",
		RequestStartedAt: time.Now(),
	}
	ctx := tenant.WithContext(context.Background(), tc)

	const goroutines = 50
	handleIDs := make(chan string, goroutines)
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := s.Get(ctx, "codex", swarm.Stateful)
			if err != nil {
				t.Errorf("Get: %v", err)
				return
			}
			handleIDs <- h.ID
		}()
	}

	wg.Wait()
	close(handleIDs)

	seen := make(map[string]struct{})
	for id := range handleIDs {
		seen[id] = struct{}{}
	}

	// All goroutines must have received the same handle (exactly 1 unique ID).
	if len(seen) != 1 {
		t.Errorf("same-tenant concurrent Get: expected 1 unique handle ID, got %d (TOCTOU race)", len(seen))
	}

	// Exactly one factory call must have occurred.
	spawnMu.Lock()
	sc := spawnCount
	spawnMu.Unlock()
	if sc != 1 {
		t.Errorf("same-tenant concurrent Get: expected 1 factory call, got %d", sc)
	}
}
