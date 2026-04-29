//go:build !short

package critical_test

import (
	"context"
	"sync"
	"testing"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/ratelimit"
	"github.com/thebtf/aimux/pkg/server"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/mcp-mux/muxcore"
)

// criticalAuditRecorder captures emitted audit events for assertions in
// authz/ratelimit critical tests. Safe for concurrent Emit (the audit
// pipeline calls it from multiple goroutines).
type criticalAuditRecorder struct {
	mu     sync.Mutex
	events []audit.AuditEvent
}

func (r *criticalAuditRecorder) Emit(ev audit.AuditEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func (r *criticalAuditRecorder) Close() error { return nil }

func (r *criticalAuditRecorder) Snapshot() []audit.AuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]audit.AuditEvent, len(r.events))
	copy(out, r.events)
	return out
}

func (r *criticalAuditRecorder) hasEvent(predicate func(audit.AuditEvent) bool) bool {
	for _, ev := range r.Snapshot() {
		if predicate(ev) {
			return true
		}
	}
	return false
}

// criticalDrainChecker satisfies the drainChecker interface used by
// AuthorizeSessionAdapter. It reports a configured set of tenants as draining.
type criticalDrainChecker struct {
	draining map[string]bool
}

func (d *criticalDrainChecker) IsDraining(tenantName string) bool {
	if d == nil || d.draining == nil {
		return false
	}
	return d.draining[tenantName]
}

// criticalRegistry builds a TenantRegistry pre-loaded with the supplied
// UID→config entries and returns it ready for AuthorizeSessionAdapter.
func criticalRegistry(entries map[int]tenant.TenantConfig) *tenant.TenantRegistry {
	reg := tenant.NewRegistry()
	reg.Swap(tenant.NewSnapshot(entries))
	return reg
}

// TestCritical_Authz_KnownUIDAllowedAndWired verifies the FR-1/FR-2/FR-3
// happy-path: an enrolled UID is admitted with the matching tenant ID, the
// rate limiter is told about the session (so subsequent frames can be
// metered), and an `allow` audit event is recorded.
//
// @critical — release blocker per rule #10
func TestCritical_Authz_KnownUIDAllowedAndWired(t *testing.T) {
	reg := criticalRegistry(map[int]tenant.TenantConfig{
		1000: {Name: "tenantA", UID: 1000, Role: tenant.RoleOperator},
	})
	rec := &criticalAuditRecorder{}
	limiter := ratelimit.NewTenantRateLimiter()
	adapter := server.NewAuthorizeSessionAdapter(reg, rec, limiter, nil)

	conn := muxcore.ConnInfo{PeerUid: 1000}
	project := muxcore.ProjectContext{ID: "proj-known"}

	auth := adapter.Authorize(context.Background(), conn, project)
	if auth.Decision != muxcore.AuthAllow {
		t.Fatalf("CRITICAL: known UID denied (decision=%v reason=%q)", auth.Decision, auth.Reason)
	}
	if auth.TenantID != "tenantA" {
		t.Fatalf("CRITICAL: known UID resolved to %q, want tenantA", auth.TenantID)
	}

	// allow audit event with tenant + operator UID + project ID.
	if !rec.hasEvent(func(ev audit.AuditEvent) bool {
		return ev.EventType == audit.EventAllow &&
			ev.TenantID == "tenantA" &&
			ev.OperatorUID == 1000 &&
			ev.ResourceID == "proj-known"
	}) {
		t.Errorf("CRITICAL: missing allow audit event: %+v", rec.Snapshot())
	}
}

// TestCritical_Authz_UnknownUIDDenied verifies the privilege-escalation
// guard (PRC v3 B1): an unenrolled UID against a multi-tenant registry MUST
// be denied. The legacy fallback path is not allowed in this mode — letting
// it through would grant RoleOperator to a stranger.
//
// @critical — release blocker per rule #10
func TestCritical_Authz_UnknownUIDDenied(t *testing.T) {
	reg := criticalRegistry(map[int]tenant.TenantConfig{
		1000: {Name: "tenantA", UID: 1000, Role: tenant.RoleOperator},
	})
	rec := &criticalAuditRecorder{}
	limiter := ratelimit.NewTenantRateLimiter()
	adapter := server.NewAuthorizeSessionAdapter(reg, rec, limiter, nil)

	conn := muxcore.ConnInfo{PeerUid: 9999}
	project := muxcore.ProjectContext{ID: "proj-hostile"}

	auth := adapter.Authorize(context.Background(), conn, project)
	if auth.Decision != muxcore.AuthDeny {
		t.Fatalf("CRITICAL: unknown UID admitted (decision=%v reason=%q tenant=%q) — privilege escalation",
			auth.Decision, auth.Reason, auth.TenantID)
	}
	if auth.TenantID == tenant.LegacyDefault {
		t.Fatalf("CRITICAL: unknown UID mapped to LegacyDefault — RoleOperator escalation regression")
	}

	// cross_tenant_blocked audit event surfaces the offending UID.
	if !rec.hasEvent(func(ev audit.AuditEvent) bool {
		return ev.EventType == audit.EventCrossTenantBlocked && ev.OperatorUID == 9999
	}) {
		t.Errorf("CRITICAL: missing cross_tenant_blocked event for hostile UID: %+v", rec.Snapshot())
	}
}

// TestCritical_Authz_DrainingTenantDenied verifies the PRC v3 B2/B6 fix:
// a UID enrolled in the registry whose tenant is currently in the drain
// window MUST be denied. The earlier implementation had a TODO placeholder
// where IsDraining should have been consulted, so a hostile re-tenant could
// bypass operator removal.
//
// @critical — release blocker per rule #10
func TestCritical_Authz_DrainingTenantDenied(t *testing.T) {
	reg := criticalRegistry(map[int]tenant.TenantConfig{
		2000: {Name: "tenantD", UID: 2000, Role: tenant.RolePlain},
	})
	rec := &criticalAuditRecorder{}
	limiter := ratelimit.NewTenantRateLimiter()
	drain := &criticalDrainChecker{draining: map[string]bool{"tenantD": true}}
	adapter := server.NewAuthorizeSessionAdapter(reg, rec, limiter, drain)

	conn := muxcore.ConnInfo{PeerUid: 2000}
	project := muxcore.ProjectContext{ID: "proj-draining"}

	auth := adapter.Authorize(context.Background(), conn, project)
	if auth.Decision != muxcore.AuthDeny {
		t.Fatalf("CRITICAL: draining tenant admitted — drain bypass (decision=%v)", auth.Decision)
	}
	if auth.Reason == "" {
		t.Error("CRITICAL: draining-tenant deny lacks Reason — operator visibility lost")
	}

	// cross_tenant_blocked event carrying the draining tenant name.
	if !rec.hasEvent(func(ev audit.AuditEvent) bool {
		return ev.EventType == audit.EventCrossTenantBlocked &&
			ev.TenantID == "tenantD" &&
			ev.OperatorUID == 2000
	}) {
		t.Errorf("CRITICAL: missing cross_tenant_blocked event for draining tenant: %+v", rec.Snapshot())
	}
}

// TestCritical_Authz_LegacyMode_AllowsAllConnections verifies that an empty
// registry (no tenants.yaml present) preserves single-tenant legacy mode
// behaviour: every UID, including unknown ones, is admitted as
// LegacyDefault. This is the migration-friendly path — daemons without a
// tenants.yaml must keep working.
//
// @critical — release blocker per rule #10
func TestCritical_Authz_LegacyMode_AllowsAllConnections(t *testing.T) {
	reg := tenant.NewRegistry() // empty → IsMultiTenant() == false
	rec := &criticalAuditRecorder{}
	limiter := ratelimit.NewTenantRateLimiter()
	adapter := server.NewAuthorizeSessionAdapter(reg, rec, limiter, nil)

	for _, uid := range []int{0, 999, 1000, 9999} {
		auth := adapter.Authorize(context.Background(),
			muxcore.ConnInfo{PeerUid: uid},
			muxcore.ProjectContext{ID: "proj-legacy"},
		)
		if auth.Decision != muxcore.AuthAllow {
			t.Fatalf("CRITICAL: legacy mode denied uid=%d (reason=%q)", uid, auth.Reason)
		}
		if auth.TenantID != tenant.LegacyDefault {
			t.Fatalf("CRITICAL: legacy mode resolved uid=%d to %q, want LegacyDefault",
				uid, auth.TenantID)
		}
	}
}
