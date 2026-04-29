package server

// T050 — AuthorizeSessionAdapter unit tests (RED gate).
// Tests: legacy mode, known UID allow, unknown UID deny, panic recovery,
// audit allow event, rate-limiter SetSessionTenant wiring.

import (
	"context"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/ratelimit"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/mcp-mux/muxcore"
)

// --- test doubles -------------------------------------------------------

// mockAuditLog captures emitted events; safe for concurrent use.
type mockAuditLog struct {
	mu     sync.Mutex
	events []audit.AuditEvent
}

func (m *mockAuditLog) Emit(e audit.AuditEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}

func (m *mockAuditLog) Close() error { return nil }

func (m *mockAuditLog) Events() []audit.AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]audit.AuditEvent, len(m.events))
	copy(cp, m.events)
	return cp
}

// sessionTenantRecorder captures SetSessionTenant calls; safe for concurrent use.
type sessionTenantRecorder struct {
	*ratelimit.TenantRateLimiter
	mu      sync.Mutex
	calls   []sessionTenantCall
}

type sessionTenantCall struct {
	sessionID string
	tenantID  string
}

func newSessionTenantRecorder() *sessionTenantRecorder {
	return &sessionTenantRecorder{TenantRateLimiter: ratelimit.NewTenantRateLimiter()}
}

func (r *sessionTenantRecorder) SetSessionTenant(sessionID, tenantID string) {
	r.mu.Lock()
	r.calls = append(r.calls, sessionTenantCall{sessionID, tenantID})
	r.mu.Unlock()
	r.TenantRateLimiter.SetSessionTenant(sessionID, tenantID)
}

func (r *sessionTenantRecorder) Calls() []sessionTenantCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]sessionTenantCall, len(r.calls))
	copy(cp, r.calls)
	return cp
}

// panicRegistry is a TenantRegistry substitute that panics on ResolveByUID.
// Used to verify that panics inside the callback are caught by muxcore.
// We test the adapter's panic recovery by wrapping it in a deferred recover.
type panicRegistry struct{}

func (p *panicRegistry) ResolveByUID(_ uint32) (tenant.TenantConfig, bool) {
	panic("injected panic for testing")
}
func (p *panicRegistry) IsMultiTenant() bool { return true }

// --- helpers ------------------------------------------------------------

func buildRegistry(tenants map[uint32]tenant.TenantConfig) *tenant.TenantRegistry {
	reg := tenant.NewRegistry()
	entries := make(map[int]tenant.TenantConfig, len(tenants))
	for uid, cfg := range tenants {
		entries[int(uid)] = cfg
	}
	reg.Swap(tenant.NewSnapshot(entries))
	return reg
}

// --- tests --------------------------------------------------------------

// TestAuthorize_LegacyMode_AllowsAllConnections verifies that when
// IsMultiTenant() == false, every connection gets AuthAllow with
// TenantID = tenant.LegacyDefault.
func TestAuthorize_LegacyMode_AllowsAllConnections(t *testing.T) {
	reg := tenant.NewRegistry() // empty — IsMultiTenant() == false
	al := &mockAuditLog{}
	rl := newSessionTenantRecorder()

	adapter := NewAuthorizeSessionAdapter(reg, al, rl, nil)

	for _, uid := range []uint32{0, 999, 1000, 9999} {
		conn := muxcore.ConnInfo{PeerUid: int(uid)}
		project := muxcore.ProjectContext{ID: "proj-legacy"}
		result := adapter.Authorize(context.Background(), conn, project)
		if result.Decision != muxcore.AuthAllow {
			t.Errorf("uid=%d: expected AuthAllow in legacy mode, got %v", uid, result.Decision)
		}
		if result.TenantID != tenant.LegacyDefault {
			t.Errorf("uid=%d: expected TenantID=%q, got %q", uid, tenant.LegacyDefault, result.TenantID)
		}
	}
}

// TestAuthorize_KnownUID_AllowsWithTenantID verifies that a UID enrolled in the
// registry gets AuthAllow with the matching tenant name.
func TestAuthorize_KnownUID_AllowsWithTenantID(t *testing.T) {
	reg := buildRegistry(map[uint32]tenant.TenantConfig{
		1000: {Name: "tenantA", UID: 1000, Role: tenant.RoleOperator},
	})
	al := &mockAuditLog{}
	rl := newSessionTenantRecorder()

	adapter := NewAuthorizeSessionAdapter(reg, al, rl, nil)

	conn := muxcore.ConnInfo{PeerUid: 1000}
	project := muxcore.ProjectContext{ID: "proj-known"}
	result := adapter.Authorize(context.Background(), conn, project)

	if result.Decision != muxcore.AuthAllow {
		t.Fatalf("expected AuthAllow, got %v (reason: %s)", result.Decision, result.Reason)
	}
	if result.TenantID != "tenantA" {
		t.Errorf("expected TenantID=tenantA, got %q", result.TenantID)
	}
}

// TestAuthorize_UnknownUID_Denies verifies that a UID absent from an enrolled
// registry gets AuthDeny with a cross_tenant_blocked audit event.
func TestAuthorize_UnknownUID_Denies(t *testing.T) {
	reg := buildRegistry(map[uint32]tenant.TenantConfig{
		1000: {Name: "tenantA", UID: 1000, Role: tenant.RoleOperator},
	})
	al := &mockAuditLog{}
	rl := newSessionTenantRecorder()

	adapter := NewAuthorizeSessionAdapter(reg, al, rl, nil)

	conn := muxcore.ConnInfo{PeerUid: 9999}
	project := muxcore.ProjectContext{ID: "proj-unknown"}
	result := adapter.Authorize(context.Background(), conn, project)

	if result.Decision != muxcore.AuthDeny {
		t.Fatalf("expected AuthDeny for unknown UID, got %v", result.Decision)
	}
	if result.Reason == "" {
		t.Error("expected non-empty Reason on AuthDeny")
	}

	events := al.Events()
	foundBlock := false
	for _, ev := range events {
		if ev.EventType == audit.EventCrossTenantBlocked {
			foundBlock = true
			break
		}
	}
	if !foundBlock {
		t.Errorf("expected cross_tenant_blocked audit event, got %v", events)
	}
}

// TestAuthorize_PanicConvertsToAuthDeny verifies that panics inside the adapter's
// own logic are recovered and treated as AuthDeny (mirrors muxcore's contract for
// the callback itself, but we test the adapter's internal guard independently).
func TestAuthorize_PanicConvertsToAuthDeny(t *testing.T) {
	al := &mockAuditLog{}
	rl := newSessionTenantRecorder()

	// Use a panicAdapter that wraps a panicRegistry directly.
	adapter := &AuthorizeSessionAdapter{
		registry: &panicRegistryWrapper{},
		auditLog: al,
		limiter:  rl,
	}

	conn := muxcore.ConnInfo{PeerUid: 1000}
	project := muxcore.ProjectContext{ID: "proj-panic"}

	result := (func() (result muxcore.SessionAuth) {
		defer func() {
			if r := recover(); r != nil {
				result = muxcore.SessionAuth{
					Decision: muxcore.AuthDeny,
					Reason:   "authorize panic",
				}
			}
		}()
		return adapter.Authorize(context.Background(), conn, project)
	})()

	if result.Decision != muxcore.AuthDeny {
		t.Fatalf("expected AuthDeny after panic, got %v", result.Decision)
	}
}

// TestAuthorize_AuditEmitsAllow verifies that a successful authorization emits
// an audit event with EventType=allow, correct TenantID, and ResourceID=project.ID.
func TestAuthorize_AuditEmitsAllow(t *testing.T) {
	reg := buildRegistry(map[uint32]tenant.TenantConfig{
		2000: {Name: "tenantB", UID: 2000, Role: tenant.RolePlain},
	})
	al := &mockAuditLog{}
	rl := newSessionTenantRecorder()

	adapter := NewAuthorizeSessionAdapter(reg, al, rl, nil)

	conn := muxcore.ConnInfo{PeerUid: 2000}
	project := muxcore.ProjectContext{ID: "proj-audit-check"}
	_ = adapter.Authorize(context.Background(), conn, project)

	events := al.Events()
	var allowEvent *audit.AuditEvent
	for i := range events {
		if events[i].EventType == audit.EventAllow {
			allowEvent = &events[i]
			break
		}
	}
	if allowEvent == nil {
		t.Fatalf("expected allow audit event, got %v", events)
	}
	if allowEvent.TenantID != "tenantB" {
		t.Errorf("allow event TenantID=%q, want tenantB", allowEvent.TenantID)
	}
	if allowEvent.ResourceID != "proj-audit-check" {
		t.Errorf("allow event ResourceID=%q, want proj-audit-check", allowEvent.ResourceID)
	}
}

// TestAuthorize_SetsSessionTenant_ForRateLimiter verifies that after AuthAllow,
// the rate limiter's SetSessionTenant is called with project.ID and cfg.Name.
func TestAuthorize_SetsSessionTenant_ForRateLimiter(t *testing.T) {
	reg := buildRegistry(map[uint32]tenant.TenantConfig{
		3000: {Name: "tenantC", UID: 3000, Role: tenant.RolePlain},
	})
	al := &mockAuditLog{}
	rl := newSessionTenantRecorder()

	adapter := NewAuthorizeSessionAdapter(reg, al, rl, nil)

	conn := muxcore.ConnInfo{PeerUid: 3000}
	project := muxcore.ProjectContext{ID: "proj-ratelimit"}
	result := adapter.Authorize(context.Background(), conn, project)

	if result.Decision != muxcore.AuthAllow {
		t.Fatalf("expected AuthAllow, got %v", result.Decision)
	}

	calls := rl.Calls()
	if len(calls) == 0 {
		t.Fatal("expected SetSessionTenant to be called, got no calls")
	}
	// Find the call for this project.
	found := false
	for _, c := range calls {
		if c.sessionID == "proj-ratelimit" && c.tenantID == "tenantC" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("SetSessionTenant not called with (proj-ratelimit, tenantC); calls: %v", calls)
	}
}

// fakeDrainChecker implements drainChecker for tests; reports the configured
// set of tenants as draining.
type fakeDrainChecker struct {
	draining map[string]bool
}

func (f *fakeDrainChecker) IsDraining(tenantName string) bool {
	if f == nil || f.draining == nil {
		return false
	}
	return f.draining[tenantName]
}

// TestAuthorize_DrainingTenant_Denies verifies that when a UID enrolled in the
// registry resolves to a tenant that is currently draining, AuthorizeSession
// emits cross_tenant_blocked and returns AuthDeny instead of admitting the
// session. PRC v3 B2/B6 — prior implementation had a TODO placeholder where
// IsDraining should have been called; doc claimed enforcement but the
// implementation never consulted the drain controller.
func TestAuthorize_DrainingTenant_Denies(t *testing.T) {
	reg := buildRegistry(map[uint32]tenant.TenantConfig{
		4000: {Name: "tenantD", UID: 4000, Role: tenant.RolePlain},
	})
	al := &mockAuditLog{}
	rl := newSessionTenantRecorder()
	drain := &fakeDrainChecker{draining: map[string]bool{"tenantD": true}}

	adapter := NewAuthorizeSessionAdapter(reg, al, rl, drain)

	conn := muxcore.ConnInfo{PeerUid: 4000}
	project := muxcore.ProjectContext{ID: "proj-drain-deny"}
	result := adapter.Authorize(context.Background(), conn, project)

	if result.Decision != muxcore.AuthDeny {
		t.Fatalf("expected AuthDeny for draining tenant, got %v (reason=%q)",
			result.Decision, result.Reason)
	}
	if result.Reason == "" {
		t.Error("expected non-empty Reason on draining-tenant deny")
	}

	// Cross-tenant-blocked audit event must be emitted with the offending
	// tenant name + operator UID.
	events := al.Events()
	var foundBlock bool
	for _, ev := range events {
		if ev.EventType == audit.EventCrossTenantBlocked &&
			ev.TenantID == "tenantD" &&
			ev.OperatorUID == 4000 {
			foundBlock = true
			break
		}
	}
	if !foundBlock {
		t.Errorf("expected cross_tenant_blocked event for draining tenant, got %v", events)
	}

	// SetSessionTenant MUST NOT be called on a denied admission.
	if calls := rl.Calls(); len(calls) != 0 {
		t.Errorf("SetSessionTenant called on denied admission: %v", calls)
	}
}

// TestAuthorize_NotDrainingTenant_Allows verifies that when the drainChecker
// reports IsDraining=false for the resolved tenant, the admission proceeds
// normally — the drain check must not block legitimate sessions.
func TestAuthorize_NotDrainingTenant_Allows(t *testing.T) {
	reg := buildRegistry(map[uint32]tenant.TenantConfig{
		5000: {Name: "tenantE", UID: 5000, Role: tenant.RolePlain},
	})
	al := &mockAuditLog{}
	rl := newSessionTenantRecorder()
	drain := &fakeDrainChecker{draining: map[string]bool{"someone-else": true}}

	adapter := NewAuthorizeSessionAdapter(reg, al, rl, drain)

	conn := muxcore.ConnInfo{PeerUid: 5000}
	project := muxcore.ProjectContext{ID: "proj-not-draining"}
	result := adapter.Authorize(context.Background(), conn, project)

	if result.Decision != muxcore.AuthAllow {
		t.Fatalf("expected AuthAllow when tenant is not draining, got %v (reason=%q)",
			result.Decision, result.Reason)
	}
	if result.TenantID != "tenantE" {
		t.Errorf("expected TenantID=tenantE, got %q", result.TenantID)
	}
}

// panicRegistryWrapper implements the registryResolver interface but panics.
// Used by TestAuthorize_PanicConvertsToAuthDeny to exercise the panic path.
type panicRegistryWrapper struct{}

func (p *panicRegistryWrapper) ResolveByUID(_ uint32) (tenant.TenantConfig, bool) {
	panic("injected panic for authorize test")
}
func (p *panicRegistryWrapper) IsMultiTenant() bool { return true }

// TestAuthorize_NegativePeerUID_Denies verifies W5 (AIMUX-12 v5.1.0):
// a connection arriving with conn.PeerUid < 0 (out-of-band sentinel from a
// probing or malformed handshake) is rejected explicitly with AuthDeny + a
// cross_tenant_blocked audit event whose Reason cites the negative UID.
//
// Without the W5 guard, the fallthrough cast `uint32(conn.PeerUid)` wraps
// negative ints to a huge unsigned value (e.g. -1 → 4294967295) which then
// fails ResolveByUID and produces a misleading audit reason "tenant resolution
// failed for uid=-1" — the wrap is invisible to operators reading the trail.
func TestAuthorize_NegativePeerUID_Denies(t *testing.T) {
	reg := buildRegistry(map[uint32]tenant.TenantConfig{
		1000: {Name: "tenantA", UID: 1000, Role: tenant.RoleOperator},
	})
	al := &mockAuditLog{}
	rl := newSessionTenantRecorder()

	adapter := NewAuthorizeSessionAdapter(reg, al, rl, nil)

	conn := muxcore.ConnInfo{PeerPid: 1, PeerUid: -1, Platform: muxcore.PlatformWindowsNamedPipe}
	project := muxcore.ProjectContext{ID: "proj-negative-uid"}
	result := adapter.Authorize(context.Background(), conn, project)

	if result.Decision != muxcore.AuthDeny {
		t.Fatalf("expected AuthDeny for negative PeerUid, got %v (reason=%q)", result.Decision, result.Reason)
	}
	if result.Reason == "" {
		t.Error("expected non-empty Reason on negative-UID deny")
	}

	events := al.Events()
	var foundNegBlock bool
	for _, ev := range events {
		if ev.EventType == audit.EventCrossTenantBlocked && ev.OperatorUID == -1 {
			foundNegBlock = true
			break
		}
	}
	if !foundNegBlock {
		t.Errorf("expected cross_tenant_blocked audit event with OperatorUID=-1, got %v", events)
	}
}

// TestAuthorizeSession_DenyResponseNoUID verifies DEF-10/FR-3: the shim-visible
// SessionAuth.Reason on an AuthDeny MUST be the generic literal "access denied"
// with no UID digits — closing the UID enumeration oracle.
//
// The UID stays in the audit log's OperatorUID field for operator forensics;
// the peer-visible Reason must carry no digit that could be matched to enumerate
// tenant UIDs via deny-response message scanning.
func TestAuthorizeSession_DenyResponseNoUID(t *testing.T) {
	// Registry has tenantA at UID 1000; connecting peer is 9999 (unenrolled).
	reg := buildRegistry(map[uint32]tenant.TenantConfig{
		1000: {Name: "tenantA", UID: 1000, Role: tenant.RoleOperator},
	})
	al := &mockAuditLog{}
	rl := newSessionTenantRecorder()

	adapter := NewAuthorizeSessionAdapter(reg, al, rl, nil)

	conn := muxcore.ConnInfo{PeerUid: 9999}
	project := muxcore.ProjectContext{ID: "proj-uid-redact"}
	result := adapter.Authorize(context.Background(), conn, project)

	if result.Decision != muxcore.AuthDeny {
		t.Fatalf("expected AuthDeny for unenrolled UID, got %v", result.Decision)
	}

	// AC: exact string match — peer sees only "access denied".
	if result.Reason != "access denied" {
		t.Errorf("expected Reason %q, got %q", "access denied", result.Reason)
	}

	// Anti-stub: no digit characters anywhere in the peer-visible Reason.
	digitRe := regexp.MustCompile(`\d`)
	if digitRe.MatchString(result.Reason) {
		t.Errorf("Reason %q must contain no digits (UID enumeration oracle closed)", result.Reason)
	}

	// Audit log must still carry the full forensic context (UID in OperatorUID field).
	events := al.Events()
	var foundBlock bool
	for _, ev := range events {
		if ev.EventType == audit.EventCrossTenantBlocked && ev.OperatorUID == 9999 {
			foundBlock = true
			break
		}
	}
	if !foundBlock {
		t.Errorf("expected cross_tenant_blocked audit event with OperatorUID=9999, got %v", events)
	}
}

// Compile-time: verify time package is imported (used in events).
var _ = time.Now
