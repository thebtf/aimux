//go:build !short

package critical_test

import (
	"errors"
	"testing"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/server"
	"github.com/thebtf/aimux/pkg/tenant"
)

// TestCritical_Dispatch_UnenrolledUIDDenied verifies the PRC v3 B1
// privilege-escalation fix: in multi-tenant mode (registry has ≥1 enrolled
// tenants), an UNKNOWN UID MUST receive ErrTenantUnenrolled — never the
// LegacyDefault fallback. The legacy fallback carries RoleOperator on its
// snapshot, so silently mapping an unknown UID to it would grant operator
// privileges to a stranger. EmitUnenrolledBlocked must surface a
// cross_tenant_blocked audit event with the offending UID populated.
//
// @critical — release blocker per rule #10
func TestCritical_Dispatch_UnenrolledUIDDenied(t *testing.T) {
	// Multi-tenant mode: one enrolled tenant. Anyone else is hostile.
	reg := tenant.NewRegistry()
	reg.Swap(tenant.NewSnapshot(map[int]tenant.TenantConfig{
		1001: {Name: "tenantA", UID: 1001, Role: tenant.RoleOperator},
	}))
	rec := &criticalAuditRecorder{}
	mw := server.NewDispatchMiddleware(reg, rec)

	// Boundary 1: ResolveContext for an unenrolled UID must NOT return
	// LegacyDefault. It must error with ErrTenantUnenrolled.
	tc, err := mw.ResolveContext("hostile-session", 9999)
	if err == nil {
		t.Fatalf("CRITICAL: unenrolled UID resolved without error (tc=%+v) — privilege escalation B1 regression", tc)
	}
	if !errors.Is(err, server.ErrTenantUnenrolled) {
		t.Fatalf("CRITICAL: unenrolled UID returned %v; want ErrTenantUnenrolled", err)
	}
	if tc.TenantID == tenant.LegacyDefault {
		t.Fatalf("CRITICAL: unenrolled UID mapped to LegacyDefault (tenant=%q) — operator-role escalation",
			tc.TenantID)
	}
	if tc.TenantID != "" {
		t.Errorf("expected zero-value TenantContext on deny, got TenantID=%q", tc.TenantID)
	}

	// Boundary 2: EmitUnenrolledBlocked produces a cross_tenant_blocked
	// audit event carrying the offending UID + sessionID + toolName so that
	// operators can correlate the attack in their audit log.
	mw.EmitUnenrolledBlocked(9999, "hostile-session", "think")
	if !rec.hasEvent(func(ev audit.AuditEvent) bool {
		return ev.EventType == audit.EventCrossTenantBlocked &&
			ev.OperatorUID == 9999 &&
			ev.ResourceID == "hostile-session" &&
			ev.ToolName == "think"
	}) {
		t.Errorf("CRITICAL: missing cross_tenant_blocked audit event for unenrolled UID; got %+v", rec.Snapshot())
	}

	// Boundary 3: legitimate enrolled UID still passes through cleanly —
	// the deny path must not have damaged the happy-path resolution.
	tcA, err := mw.ResolveContext("session-A", 1001)
	if err != nil {
		t.Fatalf("CRITICAL: enrolled UID denied (B1 fix over-corrected): %v", err)
	}
	if tcA.TenantID != "tenantA" {
		t.Fatalf("enrolled UID resolved to %q; want tenantA", tcA.TenantID)
	}
}

// TestCritical_Dispatch_LegacyMode_UnknownUIDStillResolves verifies that the
// B1 fix does NOT regress single-tenant deployments. When the registry is
// EMPTY (no tenants.yaml), every UID — including ones that would be hostile
// in multi-tenant mode — resolves to LegacyDefault without error. This
// preserves the migration path for daemons that have not yet adopted
// tenants.yaml.
//
// @critical — release blocker per rule #10
func TestCritical_Dispatch_LegacyMode_UnknownUIDStillResolves(t *testing.T) {
	reg := tenant.NewRegistry() // empty → IsMultiTenant() == false
	rec := &criticalAuditRecorder{}
	mw := server.NewDispatchMiddleware(reg, rec)

	tc, err := mw.ResolveContext("legacy-session", 9999)
	if err != nil {
		t.Fatalf("CRITICAL: legacy mode rejected uid=9999 (%v) — single-tenant mode broken", err)
	}
	if tc.TenantID != tenant.LegacyDefault {
		t.Fatalf("CRITICAL: legacy mode resolved to %q, want LegacyDefault", tc.TenantID)
	}

	// Legacy mode must not emit deny audit events on every request — that
	// would flood operators of single-tenant deployments.
	for _, ev := range rec.Snapshot() {
		if ev.EventType == audit.EventDeny || ev.EventType == audit.EventCrossTenantBlocked {
			t.Errorf("CRITICAL: legacy mode emitted deny event %+v — false-positive flood", ev)
		}
	}
}
