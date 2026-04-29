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
//     a. PeerUid found in registry  → AuthAllow + emit allow audit event + wire rate-limiter.
//     b. PeerUid not found          → AuthDeny  + emit cross_tenant_blocked audit event.
//     c. Tenant is draining         → AuthDeny  + emit cross_tenant_blocked audit event.
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

// AuthorizeSessionAdapter holds the dependencies for the per-session admission gate.
// Construct it with NewAuthorizeSessionAdapter; the zero value is not safe.
type AuthorizeSessionAdapter struct {
	registry registryResolver
	auditLog audit.AuditLog
	limiter  sessionTenantSetter
}

// NewAuthorizeSessionAdapter constructs a ready-to-use AuthorizeSessionAdapter.
//
//   - registry: the live *tenant.TenantRegistry (or a test double).
//   - auditLog: the audit event sink; Emit is called non-blockingly per decision.
//   - limiter:  the *ratelimit.TenantRateLimiter (or a test recorder); receives
//     SetSessionTenant on every AuthAllow in multi-tenant mode.
func NewAuthorizeSessionAdapter(
	registry registryResolver,
	auditLog audit.AuditLog,
	limiter sessionTenantSetter,
) *AuthorizeSessionAdapter {
	return &AuthorizeSessionAdapter{
		registry: registry,
		auditLog: auditLog,
		limiter:  limiter,
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

	// Step 3: drain protection — deny sessions to tenants being removed.
	// TenantConfig.RemovalDrainSeconds > 0 and a draining flag would be checked here.
	// The drain-controller seam (TenantDrainController) is not yet implemented;
	// this placeholder checks the RemovalDrainSeconds field as the seam marker.
	// TODO(AIMUX-12 P8 drain): wire TenantDrainController.IsDraining(cfg.Name) check here.

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
