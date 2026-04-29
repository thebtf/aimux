// Package audit provides the non-blocking audit-log infrastructure for the aimux
// daemon. It defines the AuditEvent value type, the AuditLog interface, and the
// FileAuditLog production implementation.
//
// # Ownership Boundaries
//
// The audit package owns:
//   - The on-disk JSONL audit log file (one JSON object per line).
//   - The AuditLog interface that all emitters depend on.
//   - The FileAuditLog implementation that writes to that file.
//
// The audit package does NOT own:
//   - Deciding which events to emit — that is the caller's responsibility.
//   - Log rotation — deferred to Layer 5 / a future phase (lumberjack or
//     equivalent). The current implementation appends forever.
//   - Structured querying of audit events — raw JSONL is sufficient for grep
//     and jq-based incident investigation in v1.
//
// # Who Emits What
//
//   - DispatchMiddleware (Phase 5) — emits EventAllow / EventDeny /
//     EventRateLimited / EventCrossTenantBlocked for every incoming MCP tool call.
//   - TenantRegistry hot-reload path (Phase 3) — emits EventTenantConfigChange
//     whenever tenants.yaml is reloaded.
//   - LoomEngine submit path (Phase 6) — emits EventLoomSubmitRejected when a
//     task submission violates tenant policy.
//
// # Phase 5 Wire-Up
//
// Phase 5 (DispatchMiddleware) will inject a concrete AuditLog into every
// handler via constructor injection. The pattern is:
//
//	type DispatchMiddleware struct {
//	    auditLog audit.AuditLog
//	    // ...
//	}
//
//	func NewDispatchMiddleware(al audit.AuditLog, ...) *DispatchMiddleware { ... }
//
// Test code should inject a fake that records events in a slice:
//
//	type fakeAuditLog struct { mu sync.Mutex; events []audit.AuditEvent }
//	func (f *fakeAuditLog) Emit(ev audit.AuditEvent) { f.mu.Lock(); f.events = append(f.events, ev); f.mu.Unlock() }
//	func (f *fakeAuditLog) Close() error             { return nil }
//
// # Non-Blocking Guarantee (NFR-5)
//
// AuditLog.Emit must never block the caller. FileAuditLog enforces this via a
// buffered channel (default capacity 4096). If the channel is full, the event
// is dropped, a warning is logged, and the dropped counter is incremented.
// Dropped events are observable via FileAuditLog.DroppedCount() and will be
// exposed as a metric in a future phase.
//
// # File Security (NFR-11)
//
// The audit log file is created with mode 0600 (owner read/write only).
// It is opened with O_APPEND|O_CREATE to survive daemon restarts without
// truncating prior entries.
package audit
