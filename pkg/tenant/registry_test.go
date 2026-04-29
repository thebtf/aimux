package tenant_test

import (
	"sync"
	"testing"

	"github.com/thebtf/aimux/pkg/tenant"
)

// --- T004: TenantRegistry unit tests ---

func TestRegistry_ResolveKnownUID(t *testing.T) {
	reg := tenant.NewRegistry()
	snap := tenant.NewSnapshot(map[int]tenant.TenantConfig{
		1001: {Name: "alice", UID: 1001, Role: tenant.RolePlain},
	})
	reg.Swap(snap)

	cfg, ok := reg.Resolve(1001)
	if !ok {
		t.Fatal("expected Resolve(1001) to return true for known UID")
	}
	if cfg.Name != "alice" {
		t.Fatalf("expected Name=alice, got %q", cfg.Name)
	}
}

func TestRegistry_ResolveUnknownUID(t *testing.T) {
	reg := tenant.NewRegistry()
	snap := tenant.NewSnapshot(map[int]tenant.TenantConfig{
		1001: {Name: "alice", UID: 1001, Role: tenant.RolePlain},
	})
	reg.Swap(snap)

	_, ok := reg.Resolve(9999)
	if ok {
		t.Fatal("expected Resolve(9999) to return false for unknown UID")
	}
}

func TestRegistry_ResolveOnEmpty(t *testing.T) {
	reg := tenant.NewRegistry()
	// No Swap called — registry is empty.
	_, ok := reg.Resolve(1001)
	if ok {
		t.Fatal("expected Resolve on empty registry to return false")
	}
}

func TestRegistry_HotReloadSwap(t *testing.T) {
	reg := tenant.NewRegistry()

	snap1 := tenant.NewSnapshot(map[int]tenant.TenantConfig{
		1001: {Name: "alice", UID: 1001, Role: tenant.RolePlain},
	})
	reg.Swap(snap1)

	snap2 := tenant.NewSnapshot(map[int]tenant.TenantConfig{
		2002: {Name: "bob", UID: 2002, Role: tenant.RoleOperator},
	})
	reg.Swap(snap2)

	// alice must be gone after swap
	_, ok := reg.Resolve(1001)
	if ok {
		t.Fatal("after Swap, previously-known UID 1001 must no longer resolve")
	}
	// bob must be present
	cfg, ok := reg.Resolve(2002)
	if !ok {
		t.Fatal("after Swap, UID 2002 must resolve")
	}
	if cfg.Name != "bob" {
		t.Fatalf("expected Name=bob, got %q", cfg.Name)
	}
}

func TestRegistry_HotReloadSwap_RaceFree(t *testing.T) {
	// Run with -race to verify no data races during concurrent Resolve + Swap.
	reg := tenant.NewRegistry()

	snap := tenant.NewSnapshot(map[int]tenant.TenantConfig{
		1001: {Name: "alice", UID: 1001, Role: tenant.RolePlain},
	})
	reg.Swap(snap)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					reg.Resolve(1001)
				}
			}
		}()
	}

	// Concurrent swapper
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			newSnap := tenant.NewSnapshot(map[int]tenant.TenantConfig{
				1001: {Name: "alice", UID: 1001, Role: tenant.RolePlain},
			})
			reg.Swap(newSnap)
		}
		close(stop)
	}()

	wg.Wait()
}

func TestRegistry_IsMultiTenant_EmptySnapshot(t *testing.T) {
	reg := tenant.NewRegistry()
	if reg.IsMultiTenant() {
		t.Fatal("empty registry must not report IsMultiTenant=true")
	}
}

func TestRegistry_IsMultiTenant_NonEmptySnapshot(t *testing.T) {
	reg := tenant.NewRegistry()
	snap := tenant.NewSnapshot(map[int]tenant.TenantConfig{
		1001: {Name: "alice", UID: 1001, Role: tenant.RolePlain},
	})
	reg.Swap(snap)

	if !reg.IsMultiTenant() {
		t.Fatal("registry with ≥1 tenant must report IsMultiTenant=true")
	}
}
