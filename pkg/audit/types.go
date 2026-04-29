package audit

import "time"

// EventType is the classification of a single audit event. Use the typed
// constants below instead of raw strings to prevent typos at call sites.
//
// Event taxonomy:
//
//   Dispatch layer (DispatchMiddleware):
//     EventAllow              — tool call permitted
//     EventDeny               — tool call denied by policy
//     EventRateLimited        — request dropped by rate-limiter
//     EventCrossTenantBlocked — cross-tenant resource access blocked
//
//   Tenant config (TenantRegistry):
//     EventTenantConfigChange — hot-reload changed tenant configuration
//
//   Loom task layer (LoomEngine):
//     EventLoomSubmitRejected — task submission rejected by tenant policy
//
//   Swarm executor layer (pkg/swarm):
//     EventSwarmSpawn         — executor spawned for a tenant (multi-tenant mode only)
//     EventSwarmClose         — executor closed (multi-tenant mode only)
//     EventSwarmRestart       — executor restarted after health-failure
type EventType string

const (
	// EventAllow records that an incoming tool call was permitted.
	EventAllow EventType = "allow"

	// EventDeny records that an incoming tool call was denied by policy.
	EventDeny EventType = "deny"

	// EventRateLimited records that a request was dropped by the rate-limiter.
	EventRateLimited EventType = "rate_limited"

	// EventCrossTenantBlocked records that a cross-tenant resource access was blocked.
	EventCrossTenantBlocked EventType = "cross_tenant_blocked"

	// EventTenantConfigChange records a hot-reload event that changed tenant configuration.
	EventTenantConfigChange EventType = "tenant_config_change"

	// EventLoomSubmitRejected records that a loom task submission was rejected.
	EventLoomSubmitRejected EventType = "loom_submit_rejected"

	// EventSwarmSpawn records that a new executor was spawned for a tenant.
	// Emitted only in multi-tenant mode (anti-flood, FR-4).
	EventSwarmSpawn EventType = "swarm_spawn"

	// EventSwarmClose records that an executor was closed.
	// Reason field carries one of: "stateless-after-send", "shutdown",
	// "explicit-close", "health-failure".
	// Emitted only in multi-tenant mode (anti-flood, FR-4).
	EventSwarmClose EventType = "swarm_close"

	// EventSwarmRestart records that an executor was restarted after a health failure.
	// Emitted in both legacy and multi-tenant mode (error condition, always relevant).
	EventSwarmRestart EventType = "swarm_restart"
)

// AuditEvent is an immutable value type representing a single security-relevant
// event in the aimux daemon. All fields are set at construction time; the struct
// must not be mutated after being passed to AuditLog.Emit.
//
// JSON serialisation uses snake_case keys so that the on-disk JSONL format is
// human-readable and grep-friendly.
type AuditEvent struct {
	// Timestamp is when the event occurred. Set by the emitter, not by AuditLog.
	Timestamp time.Time `json:"timestamp"`

	// EventType classifies the event. Use the EventType constants.
	EventType EventType `json:"event_type"`

	// TenantID is the tenant that owns the session that produced this event.
	// Matches TenantContext.TenantID. Empty string means the tenant was unknown
	// at the time the event was emitted (should not happen in normal flow).
	TenantID string `json:"tenant_id"`

	// ResourceID identifies the resource being acted on (e.g. loom task ID,
	// session ID, or tool name when no other resource ID applies).
	ResourceID string `json:"resource_id,omitempty"`

	// OperatorUID is the OS-level UID of the peer that initiated the request.
	// Zero means unenrolled or not yet resolved (e.g. on Windows).
	OperatorUID int `json:"operator_uid,omitempty"`

	// ToolName is the MCP tool name that was invoked (e.g. "think", "deepresearch").
	ToolName string `json:"tool_name,omitempty"`

	// Result is a short human-readable outcome word (e.g. "ok", "denied", "dropped").
	Result string `json:"result,omitempty"`

	// Reason is an optional human-readable explanation of why the event occurred.
	Reason string `json:"reason,omitempty"`

	// ExtraFields carries arbitrary key-value pairs for event-specific metadata
	// that does not warrant a dedicated field on AuditEvent. Callers must not
	// mutate ExtraFields after passing the event to Emit.
	ExtraFields map[string]string `json:"extra_fields,omitempty"`
}
