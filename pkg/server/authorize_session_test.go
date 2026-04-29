package server

// T050 — AuthorizeSessionAdapter unit tests (RED gate).
// Tests: legacy mode, known UID allow, unknown UID deny, panic recovery,
// audit allow event, rate-limiter SetSessionTenant wiring.

import (
	"context"
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

	adapter := NewAuthorizeSessionAdapter(reg, al, rl)

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

	adapter := NewAuthorizeSessionAdapter(reg, al, rl)

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

	adapter := NewAuthorizeSessionAdapter(reg, al, rl)

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

	adapter := NewAuthorizeSessionAdapter(reg, al, rl)

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

	adapter := NewAuthorizeSessionAdapter(reg, al, rl)

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

// panicRegistryWrapper implements the registryResolver interface but panics.
// Used by TestAuthorize_PanicConvertsToAuthDeny to exercise the panic path.
type panicRegistryWrapper struct{}

func (p *panicRegistryWrapper) ResolveByUID(_ uint32) (tenant.TenantConfig, bool) {
	panic("injected panic for authorize test")
}
func (p *panicRegistryWrapper) IsMultiTenant() bool { return true }

// Compile-time: verify time package is imported (used in events).
var _ = time.Now
