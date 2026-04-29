package server

// authorize_session.go — T046 (AIMUX-12 Phase 8)
//
// AuthorizeSessionAdapter implements the engine.Config.AuthorizeSession callback.
// It is a single-shot per-session admission gate invoked once per session AFTER
// the IPC handshake completes (peer credentials on ConnInfo are populated) and
// BEFORE any frame is dispatched to SessionHandler.
//
// Decision logic:
//  1. Legacy mode  (registry.IsMultiTenant() == false) → AuthAllow, TenantID=LegacyDefault.
//  2. Multi-tenant (≥1 enrolled tenants):
//     a. PeerUid found in registry, tenant NOT draining
//        → AuthAllow + emit allow audit event + wire rate-limiter.
//     b. PeerUid not found
//        → AuthDeny  + emit cross_tenant_blocked audit event.
//     c. PeerUid found, tenant IS draining (drainChecker reports IsDraining=true)
//        → AuthDeny  + emit cross_tenant_blocked audit event (B2/B6).
//
// Panics in Authorize are NOT recovered here — muxcore recovers them at the
// engine.Config.AuthorizeSession boundary and converts them to AuthDeny per the
// contract documented in engine.Config.
//
// The registryResolver interface makes the adapter testable with a mock
// without depending on the concrete *tenant.TenantRegistry from the test file.

import (
	"context"
	"fmt"
	"time"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/mcp-mux/muxcore"
)

// registryResolver is the subset of *tenant.TenantRegistry used by
// AuthorizeSessionAdapter. Tests may substitute a mock.
type registryResolver interface {
	ResolveByUID(uid uint32) (tenant.TenantConfig, bool)
	IsMultiTenant() bool
}

// sessionTenantSetter is the subset of *ratelimit.TenantRateLimiter used by
// AuthorizeSessionAdapter. Tests may substitute a recorder.
type sessionTenantSetter interface {
	SetSessionTenant(sessionID, tenantID string)
}

// drainChecker is the subset of *tenant.TenantDrainController used by
// AuthorizeSessionAdapter. Tests may substitute a fake. nil is allowed and
// disables drain enforcement (legitimate when no hot-reloader is wired, e.g.
// SSE/HTTP transports that have no SIGHUP path).
type drainChecker interface {
	IsDraining(tenantName string) bool
}

// AuthorizeSessionAdapter holds the dependencies for the per-session admission gate.
// Construct it with NewAuthorizeSessionAdapter; the zero value is not safe.
type AuthorizeSessionAdapter struct {
	registry registryResolver
	auditLog audit.AuditLog
	limiter  sessionTenantSetter
	drain    drainChecker // may be nil — disables drain enforcement
}

// NewAuthorizeSessionAdapter constructs a ready-to-use AuthorizeSessionAdapter.
//
//   - registry: the live *tenant.TenantRegistry (or a test double).
//   - auditLog: the audit event sink; Emit is called non-blockingly per decision.
//   - limiter:  the *ratelimit.TenantRateLimiter (or a test recorder); receives
//     SetSessionTenant on every AuthAllow in multi-tenant mode.
//   - drain:    the *tenant.TenantDrainController exposing IsDraining(tenantName).
//     Pass nil to disable drain enforcement (e.g. transports without
//     hot-reload). When non-nil, sessions whose resolved tenant is currently
//     draining are denied with cross_tenant_blocked. PRC v3 B2/B6.
func NewAuthorizeSessionAdapter(
	registry registryResolver,
	auditLog audit.AuditLog,
	limiter sessionTenantSetter,
	drain drainChecker,
) *AuthorizeSessionAdapter {
	return &AuthorizeSessionAdapter{
		registry: registry,
		auditLog: auditLog,
		limiter:  limiter,
		drain:    drain,
	}
}

// Authorize is the engine.Config.AuthorizeSession callback.
// It is called once per session after the IPC handshake completes.
// Panics propagate to the muxcore caller, which recovers them as AuthDeny.
func (a *AuthorizeSessionAdapter) Authorize(
	_ context.Context,
	conn muxcore.ConnInfo,
	project muxcore.ProjectContext,
) muxcore.SessionAuth {
	// Step 1: legacy-default mode — no tenants enrolled, allow all.
	if !a.registry.IsMultiTenant() {
		return muxcore.SessionAuth{
			Decision: muxcore.AuthAllow,
			TenantID: tenant.LegacyDefault,
		}
	}

	// Step 2: multi-tenant mode — resolve PeerUid → TenantConfig.
	uid := uint32(conn.PeerUid) // safe cast: PeerUid is a Unix UID (≤2^32-1)
	cfg, found := a.registry.ResolveByUID(uid)
	if !found {
		a.auditLog.Emit(audit.AuditEvent{
			Timestamp:   time.Now(),
			EventType:   audit.EventCrossTenantBlocked,
			OperatorUID: conn.PeerUid,
			ResourceID:  project.ID,
			Result:      "denied",
			Reason:      fmt.Sprintf("tenant resolution failed for uid=%d", conn.PeerUid),
		})
		return muxcore.SessionAuth{
			Decision: muxcore.AuthDeny,
			Reason:   fmt.Sprintf("tenant resolution failed for uid=%d", conn.PeerUid),
		}
	}

	// Step 3: drain protection — deny sessions to tenants currently being
	// removed via SIGHUP hot-reload (FR-12). Operator removes a hostile
	// tenant from tenants.yaml → BeginDrain starts the countdown → new
	// sessions for that tenant must be refused even before the snapshot
	// swap commits. PRC v3 B2/B6.
	//
	// The check is skipped when no drainChecker is wired (e.g. SSE/HTTP
	// transports that lack a SIGHUP path).
	if a.drain != nil && a.drain.IsDraining(cfg.Name) {
		a.auditLog.Emit(audit.AuditEvent{
			Timestamp:   time.Now(),
			EventType:   audit.EventCrossTenantBlocked,
			TenantID:    cfg.Name,
			OperatorUID: conn.PeerUid,
			ResourceID:  project.ID,
			Result:      "denied",
			Reason:      fmt.Sprintf("tenant %q is draining — admission refused", cfg.Name),
		})
		return muxcore.SessionAuth{
			Decision: muxcore.AuthDeny,
			Reason:   fmt.Sprintf("tenant %q is draining", cfg.Name),
		}
	}

	// Step 4: allow — emit audit event and wire rate-limiter.
	a.auditLog.Emit(audit.AuditEvent{
		Timestamp:   time.Now(),
		EventType:   audit.EventAllow,
		TenantID:    cfg.Name,
		OperatorUID: conn.PeerUid,
		ResourceID:  project.ID,
		Result:      "ok",
	})
	a.limiter.SetSessionTenant(project.ID, cfg.Name)

	return muxcore.SessionAuth{
		Decision: muxcore.AuthAllow,
		TenantID: cfg.Name,
	}
}
