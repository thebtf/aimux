package server

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/tenant"
)

// BenchmarkDispatchMiddlewareOverhead measures the wall-clock cost of
// DispatchMiddleware.ResolveContext → WithContext in legacy-default mode
// (no tenants.yaml, registry empty).
//
// Target: < 50µs per op (NFR-10). Expected: well under 1µs in legacy mode
// because the hot path is a single IsMultiTenant atomic load + two allocations.
func BenchmarkDispatchMiddlewareOverhead(b *testing.B) {
	reg := tenant.NewRegistry() // empty registry — legacy-default mode
	fal := &fakeAuditLog{}
	mw := NewDispatchMiddleware(reg, fal)

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tc, err := mw.ResolveContext("bench-session", 0)
		if err != nil {
			b.Fatal(err)
		}
		_ = mw.WithContext(ctx, tc)
	}
}

// BenchmarkDispatchMiddlewareOverhead_MultiTenant measures the overhead in
// multi-tenant mode where the UID is enrolled in the registry (map lookup path).
func BenchmarkDispatchMiddlewareOverhead_MultiTenant(b *testing.B) {
	reg := tenant.NewRegistry()
	snap := tenant.NewSnapshot(map[int]tenant.TenantConfig{
		9999: {Name: "bench-tenant", UID: 9999, Role: tenant.RoleOperator},
	})
	reg.Swap(snap)

	fal := &fakeAuditLog{}
	mw := NewDispatchMiddleware(reg, fal)

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tc, err := mw.ResolveContext("bench-session", 9999)
		if err != nil {
			b.Fatal(err)
		}
		_ = mw.WithContext(ctx, tc)
	}
}
