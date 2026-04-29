package server

import (
	"context"
	"time"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/tenant"
)

// discardAuditLog is a no-op AuditLog used when the audit file cannot be opened
// at daemon startup. Events are silently dropped; a startup warning is logged
// by the caller. It satisfies the audit.AuditLog interface.
type discardAuditLog struct{}

func (discardAuditLog) Emit(_ audit.AuditEvent) {}
func (discardAuditLog) Close() error             { return nil }

// tenantContextKey is the unexported context key used by DispatchMiddleware to store
// a resolved TenantContext. Using a private type prevents collisions with other packages.
type tenantContextKey struct{}

// TenantContextFromContext retrieves the TenantContext injected by DispatchMiddleware.
// Returns (TenantContext, true) when present, (zero, false) when absent.
//
// Tool handlers that need the tenant identity for audit, scoping, or policy decisions
// should call this instead of relying on muxcore.ProjectContext.
func TenantContextFromContext(ctx context.Context) (tenant.TenantContext, bool) {
	tc, ok := ctx.Value(tenantContextKey{}).(tenant.TenantContext)
	return tc, ok
}

// DispatchMiddleware resolves a TenantContext at the entry point of every MCP tool
// dispatch and emits audit events for allow/deny decisions.
//
// Until muxcore #109/#110 lands (Phase 8), the middleware operates in one of two modes:
//
//  1. Legacy-default mode (tenants.yaml absent / registry has no tenants):
//     Every request receives tenant.LegacyDefault context; single-tenant behaviour is
//     preserved byte-identically. No deny events are emitted. This is fail-open per the
//     Phase 5 plan.md annotation.
//
//  2. Multi-tenant mode (registry has ≥1 enrolled tenants):
//     The connecting UID is looked up in the registry. Known UIDs get the matching config;
//     unknown UIDs fall back to LegacyDefault (muxcore #110 will harden this to a deny).
//
// DispatchMiddleware is safe for concurrent use.
type DispatchMiddleware struct {
	registry *tenant.TenantRegistry
	auditLog audit.AuditLog
}

// NewDispatchMiddleware creates a DispatchMiddleware.
//
//   - registry: the live TenantRegistry (may be empty — triggers legacy-default mode).
//   - auditLog: the audit event sink; receives allow/deny/cross_tenant_blocked events.
//
// Both arguments are required. Passing nil for either will cause panics at call sites.
func NewDispatchMiddleware(registry *tenant.TenantRegistry, auditLog audit.AuditLog) *DispatchMiddleware {
	return &DispatchMiddleware{
		registry: registry,
		auditLog: auditLog,
	}
}

// ResolveContext resolves a TenantContext for the incoming connection identified by
// sessionID and peerUID.
//
// Fail-open contract (legacy-default mode):
//   - When the registry is empty (IsMultiTenant() == false), returns
//     tenant.NewLegacyDefaultContext(sessionID). No deny audit event is emitted.
//   - When the registry has tenants but peerUID is not enrolled, returns
//     tenant.NewLegacyDefaultContext(sessionID). This is the pre-muxcore-#110 gap;
//     Phase 8 will convert this path into a deny.
//
// Multi-tenant mode:
//   - Known peerUID → TenantContext with TenantID == TenantConfig.Name.
//   - RequestStartedAt is always set to time.Now().
func (m *DispatchMiddleware) ResolveContext(ctx context.Context, sessionID string, peerUID int) (tenant.TenantContext, error) {
	_ = ctx // reserved for Phase 8 ConnInfo lookup

	// Legacy-default mode: registry has no enrolled tenants.
	if !m.registry.IsMultiTenant() {
		return tenant.NewLegacyDefaultContext(sessionID), nil
	}

	// Multi-tenant mode: look up the connecting peer's UID.
	cfg, ok := m.registry.Resolve(peerUID)
	if !ok {
		// Unknown UID — fail-open until muxcore #110 provides reliable auth.
		// Phase 8 will replace this with a deny + EventCrossTenantBlocked.
		return tenant.NewLegacyDefaultContext(sessionID), nil
	}

	return tenant.TenantContext{
		TenantID:         cfg.Name,
		PeerUid:          peerUID,
		SessionID:        sessionID,
		RequestStartedAt: time.Now(),
	}, nil
}

// WithContext returns a new context with tc stored under the tenantContextKey.
// Tool handlers retrieve it via TenantContextFromContext.
func (m *DispatchMiddleware) WithContext(ctx context.Context, tc tenant.TenantContext) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tc)
}

// IsMultiTenant returns true when the registry has ≥1 enrolled tenants.
// It is a thin proxy to TenantRegistry.IsMultiTenant — kept here so callers
// don't need direct access to the registry.
func (m *DispatchMiddleware) IsMultiTenant() bool {
	return m.registry.IsMultiTenant()
}

// EmitAllow records an allow audit event for the resolved tenant and tool.
// Call this after successful tenant resolution and before dispatching to the tool handler.
func (m *DispatchMiddleware) EmitAllow(tc tenant.TenantContext, toolName string) {
	m.auditLog.Emit(audit.AuditEvent{
		Timestamp:   time.Now(),
		EventType:   audit.EventAllow,
		TenantID:    tc.TenantID,
		OperatorUID: tc.PeerUid,
		ToolName:    toolName,
		Result:      "ok",
	})
}

// EmitCrossTenantBlocked records a cross_tenant_blocked audit event.
// Call this when a request from one tenant attempts to access a resource owned by another.
func (m *DispatchMiddleware) EmitCrossTenantBlocked(tc tenant.TenantContext, resourceID, toolName string) {
	m.auditLog.Emit(audit.AuditEvent{
		Timestamp:   time.Now(),
		EventType:   audit.EventCrossTenantBlocked,
		TenantID:    tc.TenantID,
		OperatorUID: tc.PeerUid,
		ResourceID:  resourceID,
		ToolName:    toolName,
		Result:      "denied",
		Reason:      "cross-tenant access blocked",
	})
}
