package audit

// DiscardLog is a no-op AuditLog used as a fallback when the audit file cannot
// be opened or when callers explicitly opt out of audit emission.
//
// Use either as a zero value (audit.DiscardLog{}) or via pointer
// (&audit.DiscardLog{}) — both satisfy AuditLog. Methods are defined on the
// value receiver so pointer-or-value semantics work uniformly.
//
// Consolidates duplicate definitions previously copied across pkg/swarm and
// pkg/server (PRC v6 code-reviewer P3 finding).
type DiscardLog struct{}

// Emit drops the event without side effects.
func (DiscardLog) Emit(_ AuditEvent) {}

// Close is a no-op; DiscardLog holds no resources.
func (DiscardLog) Close() error { return nil }
