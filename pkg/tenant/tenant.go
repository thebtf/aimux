package tenant

import "time"

// daemonInternalScope is the reserved TenantID for daemon-internal operations.
// It is intentionally unexported to prevent external callers from constructing
// a daemon-scoped TenantContext manually. Use NewDaemonContext() instead.
const daemonInternalScope = "_daemon_internal_"

// LegacyDefault is the TenantID used when no tenants.yaml is present (single-tenant
// legacy mode). All pre-migration sessions, jobs, and loom tasks carry this value.
// Safe to compare against TenantContext.TenantID in legacy-mode code paths.
const LegacyDefault = "legacy-default"

// TenantContext is an immutable value type that identifies the tenant and peer for a
// single MCP request. It is set once at session-open time and must not be modified
// after construction. Callers must treat TenantContext as read-only.
//
// Zero value is safe: TenantID="" indicates an unset context; handlers that receive
// a zero-value TenantContext must return -32603 (internal error).
type TenantContext struct {
	// TenantID is the logical tenant identifier, equal to TenantConfig.Name.
	// Use LegacyDefault when operating in single-tenant mode.
	TenantID string

	// PeerUID is the OS UID of the connecting peer (from SO_PEERCRED / getpeereid).
	// Zero value (0) is treated as unenrolled on non-Windows platforms.
	PeerUID int

	// SessionID is the muxcore session identifier that opened this context.
	SessionID string

	// RequestStartedAt records when the request entered the dispatch middleware.
	// Used for latency tracking and audit timestamps.
	RequestStartedAt time.Time
}

// NewDaemonContext returns a TenantContext for daemon-internal operations such as
// background goroutines, health checks, and schema migrations. The TenantID is set
// to the unexported daemonInternalScope constant, which cannot be reproduced by
// external callers, ensuring daemon context is unforgeable.
func NewDaemonContext() TenantContext {
	return TenantContext{
		TenantID:         daemonInternalScope,
		RequestStartedAt: time.Now(),
	}
}

// NewLegacyDefaultContext returns a TenantContext for use in legacy single-tenant mode
// (no tenants.yaml present). The TenantID is set to LegacyDefault and the SessionID
// is carried through for audit trail purposes.
//
// This constructor documents the known residual gap in pre-muxcore-#110 deployments:
// the TenantContext is placeholder-resolved at session open without OS peer credential
// verification. It will be replaced by ConnInfo-based lookup in Phase 8.
func NewLegacyDefaultContext(sessionID string) TenantContext {
	return TenantContext{
		TenantID:         LegacyDefault,
		SessionID:        sessionID,
		RequestStartedAt: time.Now(),
	}
}
