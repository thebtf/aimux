package server

import (
	"context"
	"errors"
	"time"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/tenant"
)

// ErrTenantUnenrolled is returned by DispatchMiddleware.ResolveContext (and
// the dispatch path in HandleRequest) when the connecting UID is absent from
// an enrolled multi-tenant registry. The caller MUST treat this as a deny
// decision: emit a cross_tenant_blocked audit event and return a JSON-RPC
// error to the client, NEVER fall back to LegacyDefault. PRC v3 B1.
var ErrTenantUnenrolled = errors.New("tenant unenrolled — multi-tenant registry rejects unknown UID")

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
// The middleware operates in one of two modes:
//
//  1. Legacy-default mode (tenants.yaml absent / registry has no tenants):
//     Every request receives tenant.LegacyDefault context; single-tenant behaviour is
//     preserved byte-identically. No deny events are emitted. This is the legitimate
//     single-tenant deployment path.
//
//  2. Multi-tenant mode (registry has ≥1 enrolled tenants):
//     The connecting UID is looked up in the registry. Known UIDs get the matching config.
//     UNKNOWN UIDs in this mode are rejected with ErrTenantUnenrolled — the caller emits
//     a cross_tenant_blocked audit event and refuses the request. Mapping unknown UIDs
//     to LegacyDefault (which carries RoleOperator) would be a privilege escalation in
//     a multi-tenant deployment; PRC v3 B1 closes this fail-open hole.
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
// Behaviour:
//   - Legacy-default mode (registry has no enrolled tenants):
//     returns tenant.NewLegacyDefaultContext(sessionID). No deny audit event is emitted.
//   - Multi-tenant mode, known peerUID:
//     returns a TenantContext with TenantID == TenantConfig.Name.
//   - Multi-tenant mode, UNKNOWN peerUID:
//     returns ErrTenantUnenrolled. Caller MUST emit a cross_tenant_blocked
//     audit event and reject the request — a hostile UID is attempting to
//     connect against an enrolled registry. PRC v3 B1.
//
// RequestStartedAt is set to time.Now() on every successful resolution.
func (m *DispatchMiddleware) ResolveContext(sessionID string, peerUID int) (tenant.TenantContext, error) {
	// Legacy-default mode: registry has no enrolled tenants.
	if !m.registry.IsMultiTenant() {
		return tenant.NewLegacyDefaultContext(sessionID), nil
	}

	// Multi-tenant mode: look up the connecting peer's UID.
	cfg, ok := m.registry.Resolve(peerUID)
	if !ok {
		// Unknown UID against an enrolled registry — privilege escalation
		// path. Refuse to map to LegacyDefault (which carries RoleOperator)
		// and let the caller emit cross_tenant_blocked + reject the request.
		return tenant.TenantContext{}, ErrTenantUnenrolled
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

// EmitUnenrolledBlocked records a cross_tenant_blocked audit event for a UID
// that is not present in the multi-tenant registry. There is no resolved
// TenantContext at this point — the deny happens BEFORE the request is mapped
// to a tenant — so the event carries only the offending peerUID and an
// explanatory reason. PRC v3 B1.
func (m *DispatchMiddleware) EmitUnenrolledBlocked(peerUID int, sessionID, toolName string) {
	m.auditLog.Emit(audit.AuditEvent{
		Timestamp:   time.Now(),
		EventType:   audit.EventCrossTenantBlocked,
		OperatorUID: peerUID,
		ResourceID:  sessionID,
		ToolName:    toolName,
		Result:      "denied",
		Reason:      "unenrolled UID against multi-tenant registry",
	})
}
