// Package tenant provides the domain foundation for AIMUX multi-tenant isolation.
//
// # Ownership Boundaries
//
// Every resource managed by the aimux daemon (MCP sessions, loom tasks, audit log
// entries, log partition files) is owned by exactly one tenant. Cross-tenant access
// is structurally prohibited: data-layer queries inject a tenant_id predicate via
// TenantScopedStore / TenantScopedLoomEngine wrappers, ensuring that tenant A can
// never observe or modify tenant B's resources at the storage layer.
//
// # Immutability Contract
//
// TenantContext and TenantConfig are value types. They must not be mutated after
// construction:
//
//   - TenantContext is assigned once at session-open time (NewLegacyDefaultContext or
//     the Phase 8 ConnInfo-based resolver) and lives for the session lifetime.
//   - TenantConfig copies are produced by TenantConfig.WithDefaults() — the receiver
//     is never modified.
//   - Snapshot is constructed by NewSnapshot() and then stored atomically via
//     TenantRegistry.Swap(). No field of a snapshot is ever written after Swap.
//
// # Legacy Single-Tenant Mode
//
// When no tenants.yaml is present, the daemon operates in legacy-default mode:
// TenantRegistry.IsMultiTenant() returns false, and all contexts carry TenantID
// equal to the LegacyDefault constant ("legacy-default"). Pre-existing sessions.db
// and loom task rows default to this tenant_id via schema migration.
//
// # Daemon-Internal Scope
//
// Background goroutines, schema migrations, and health checks that run outside any
// user session use NewDaemonContext(). The resulting TenantID is the unexported
// daemonInternalScope constant ("_daemon_internal_"), which cannot be constructed
// by external callers, making the daemon scope unforgeable.
//
// # Thread Safety
//
// TenantRegistry.Resolve and IsMultiTenant are safe for concurrent use with no
// locking — they perform a single atomic pointer load. TenantRegistry.Swap is also
// concurrency-safe and may be called from a background SIGHUP goroutine while
// foreground handlers are reading the registry.
package tenant
