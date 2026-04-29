// Package swarm manages ExecutorV2 lifecycle: spawn, get, send, health check,
// restart, and shutdown. It is the SOLE entry point for executor access in
// aimux v5 — callers never touch an ExecutorV2 directly.
package swarm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
)

// ErrHandleNotFound is returned when Get cannot locate a handle or when Send /
// SendStream detect a cross-tenant access attempt. The error is intentionally
// undifferentiated (CHK079): a caller cannot determine whether the handle never
// existed or belongs to a different tenant.
var ErrHandleNotFound = errors.New("swarm: handle not found")

// GetOption is a functional option for Get.
type GetOption func(*getOpts)

type getOpts struct {
	scope string
}

// WithScope binds the returned handle to a session-specific scope so that two
// sessions requesting the same executor name receive independent handles.
// Without WithScope the handle is global (backward-compatible behaviour).
func WithScope(scope string) GetOption {
	return func(o *getOpts) { o.scope = scope }
}

// registryKey returns the composite key used in the Swarm registry.
//
// The key encodes (tenantID, scope, name) as a "|"-separated string. Separator
// collision is impossible because tenantID is constrained by AIMUX-12 W1
// sanitizeTenantID (NFC normalization + ASCII allowlist [a-zA-Z0-9_-] in
// pkg/logger/log_partitioner.go). The scope and name fields are controlled by
// internal call sites. An empty tenantID (legacy-default mode, FR-4) produces
// a key identical to the pre-AIMUX-13 two-argument form — backward compat is
// preserved because all legacy callers use the same empty tenantID partition.
//
// Before AIMUX-13: registryKey(scope, name) → scope+":"+name (or just name)
// After  AIMUX-13: registryKey(tenantID, scope, name) → tenantID+"|"+scope+"|"+name
//
// All callers MUST canonicalize tenantID via canonicalTenantID/tenantIDFromContext
// before passing it here. Otherwise mixed contexts (some carrying "" legacy,
// some carrying tenant.LegacyDefault) would partition into separate registry
// slots and produce split-brain behaviour (CodeRabbit MAJOR PR #131).
func registryKey(tenantID, scope, name string) string {
	return tenantID + "|" + scope + "|" + name
}

// canonicalTenantID collapses tenant.LegacyDefault ("legacy-default") to "".
//
// The Swarm registry uses "" as the canonical legacy partition key. Callers
// that arrive with an explicit LegacyDefault TenantContext (e.g. emitted by
// pkg/server.DispatchMiddleware) and callers that arrive with no
// TenantContext at all (zero-value tc.TenantID == "") MUST share the same
// partition — otherwise the same logical legacy stream splits into two
// registry slots and produces ErrHandleNotFound for cross-path Get/Send.
//
// Real tenant IDs (anything other than "" or LegacyDefault) pass through
// unchanged.
func canonicalTenantID(id string) string {
	if id == tenant.LegacyDefault {
		return ""
	}
	return id
}

// tenantIDFromContext extracts the canonical tenantID from ctx.
// Equivalent to canonicalTenantID(tenant.FromContext(ctx).TenantID).
func tenantIDFromContext(ctx context.Context) string {
	tc, _ := tenant.FromContext(ctx)
	return canonicalTenantID(tc.TenantID)
}

// SpawnMode determines executor lifecycle policy for handles returned by Get.
type SpawnMode int

const (
	// Stateless creates a fresh executor for every Get call. The executor is
	// closed by the Swarm immediately after Send/SendStream returns.
	Stateless SpawnMode = iota

	// Stateful reuses an existing executor for the same name within a session.
	// A new executor is spawned when none exists or the existing one is dead.
	Stateful

	// Persistent keeps executors alive for the full daemon lifetime.
	// Identical to Stateful but handles survive Shutdown only when explicitly closed.
	//
	// Note: Persistent handles do NOT survive a daemon hot-swap (binary upgrade).
	// A hot-swap replaces the daemon process entirely; the new daemon starts with an
	// empty registry and re-spawns executors on the first Persistent Get call.
	// See NFR-Persistent-Honesty in the AIMUX-13 spec for rationale.
	Persistent
)

// String returns the human-readable spawn mode name.
func (m SpawnMode) String() string {
	switch m {
	case Stateless:
		return "stateless"
	case Stateful:
		return "stateful"
	case Persistent:
		return "persistent"
	default:
		return "unknown"
	}
}

// Handle is an opaque reference to a managed executor instance. Callers
// pass Handles to Swarm.Send / Swarm.SendStream — they never touch the
// executor directly.
type Handle struct {
	// ID is a unique identifier for this handle instance.
	ID string

	// TenantID identifies the tenant that owns this handle. Set once at spawn
	// time and never mutated (immutable after construction). Empty string means
	// legacy-default mode (no tenants.yaml). Cross-tenant Send/SendStream
	// attempts are rejected with ErrHandleNotFound (CHK079, FR-2).
	TenantID string

	// Name is the logical executor name (e.g., "codex", "claude").
	Name string

	// Mode is the spawn-mode this handle was created under.
	Mode SpawnMode

	executor   types.ExecutorV2
	startedAt  time.Time
	lastUsedAt time.Time
	mu         sync.Mutex // protects executor and lastUsedAt
}

// Swarm manages executor lifecycle: spawn, get, send, health check, restart,
// and shutdown. All fields after creation are protected by mu.
type Swarm struct {
	factoryFn func(name string) (types.ExecutorV2, error)
	auditLog  audit.AuditLog // receives spawn/close/restart/cross-tenant events

	mu       sync.RWMutex
	registry map[string][]*Handle // keyed by registryKey(tenantID, scope, name)
	nextID   uint64
}

// New creates a Swarm. factoryFn is called whenever a new ExecutorV2 is needed
// for the given name; it must be safe to call concurrently.
//
// auditLog receives executor lifecycle events (spawn, close, restart, cross-tenant
// block). Pass nil to use a no-op discard log — safe for tests and single-tenant
// deployments that do not need observability.
func New(factoryFn func(name string) (types.ExecutorV2, error), auditLog audit.AuditLog) *Swarm {
	al := auditLog
	if al == nil {
		al = audit.DiscardLog{}
	}
	return &Swarm{
		factoryFn: factoryFn,
		auditLog:  al,
		registry:  make(map[string][]*Handle),
	}
}

// isMultiTenant reports whether the swarm is operating in multi-tenant mode.
// It derives this from the tenant ID carried by the handle: if every tenant ID
// seen so far equals LegacyDefault or is empty the swarm is in legacy mode.
//
// For audit flood-prevention purposes (FR-4), spawn/close events are only
// emitted when the tenantID is not the legacy default. Cross-tenant block events
// are always emitted (they indicate a programming error or security violation).
func isMultiTenantID(tenantID string) bool {
	return tenantID != "" && tenantID != tenant.LegacyDefault
}

// Get returns a Handle for the named executor according to mode:
//   - Stateless: always creates a new executor and a fresh Handle.
//   - Stateful/Persistent: returns the first alive existing Handle, or spawns
//     a new one if none exist or all are dead.
//
// Pass WithScope(sessionID) to isolate handles per session (SEC-001). Without a
// scope the handle is global — two callers without scope share the same handle.
//
// The tenant identity is extracted from ctx via tenant.FromContext. An absent
// TenantContext falls back to an empty TenantID, which is treated as
// LegacyDefault (single-tenant mode, FR-4).
func (s *Swarm) Get(ctx context.Context, name string, mode SpawnMode, opts ...GetOption) (*Handle, error) {
	if name == "" {
		return nil, errors.New("swarm: executor name must not be empty")
	}

	tenantID := tenantIDFromContext(ctx)

	// Stateless always spawns fresh — no registry lookup needed.
	if mode == Stateless {
		h, err := s.spawn(ctx, name, mode)
		if err != nil {
			return nil, err
		}
		s.emitSpawn(h)
		return h, nil
	}

	var o getOpts
	for _, fn := range opts {
		fn(&o)
	}
	key := registryKey(tenantID, o.scope, name)

	// Stateful/Persistent: find-or-spawn under write lock to prevent TOCTOU
	// (BUG-003). Two concurrent goroutines must not both observe an empty
	// registry and both spawn separate handles for the same key.
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, h := range s.registry[key] {
		h.mu.Lock()
		alive := h.executor != nil && h.executor.IsAlive() == types.HealthAlive
		h.mu.Unlock()
		if alive {
			return h, nil
		}
	}

	// No alive handle — spawn a new one (still under write lock).
	h, err := s.spawnLocked(ctx, name, mode)
	if err != nil {
		return nil, err
	}
	s.registry[key] = append(s.registry[key], h)
	s.emitSpawn(h)
	return h, nil
}

// Send sends msg through h's executor and returns the complete response.
// If the executor is not alive, Swarm restarts it once and retries. If the
// restart also fails the original error is returned.
//
// Cross-tenant access check: if the TenantID embedded in h differs from the
// TenantID in ctx, Send returns ErrHandleNotFound and emits an audit event
// (FR-2, CHK079 defense-in-depth). The error message is intentionally generic
// so that a caller cannot infer whether the handle exists or belongs to another tenant.
func (s *Swarm) Send(ctx context.Context, h *Handle, msg types.Message) (*types.Response, error) {
	if err := s.checkTenant(ctx, h); err != nil {
		return nil, err
	}

	if err := s.ensureAlive(h); err != nil {
		return nil, err
	}

	h.mu.Lock()
	exec := h.executor
	h.mu.Unlock()

	resp, err := exec.Send(ctx, msg)
	if err == nil {
		h.mu.Lock()
		h.lastUsedAt = time.Now()
		h.mu.Unlock()
	}

	if h.Mode == Stateless {
		_ = s.closeHandle(h, "stateless-after-send")
	}

	return resp, err
}

// SendStream sends msg through h's executor, delivering incremental output via
// onChunk. Returns the complete aggregated response after the final chunk.
// If the executor is not alive, Swarm restarts it once before sending.
//
// Cross-tenant access check: see Send for behaviour and rationale.
func (s *Swarm) SendStream(ctx context.Context, h *Handle, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	if err := s.checkTenant(ctx, h); err != nil {
		return nil, err
	}

	if err := s.ensureAlive(h); err != nil {
		return nil, err
	}

	h.mu.Lock()
	exec := h.executor
	h.mu.Unlock()

	resp, err := exec.SendStream(ctx, msg, onChunk)
	if err == nil {
		h.mu.Lock()
		h.lastUsedAt = time.Now()
		h.mu.Unlock()
	}

	if h.Mode == Stateless {
		_ = s.closeHandle(h, "stateless-after-send")
	}

	return resp, err
}

// LegacyRun bridges Swarm lifecycle management with legacy SpawnArgs/Result callers.
// Strangler Fig pattern: callers continue using legacy types while benefiting from
// Swarm health check and restart. The handle's ExecutorV2 must implement
// types.LegacyAccessor to expose the underlying LegacyExecutor.
//
// Use this during M2 migration. Full migration to Send(Message)→Response in M3+.
func (s *Swarm) LegacyRun(ctx context.Context, h *Handle, args types.SpawnArgs) (*types.Result, error) {
	if err := s.checkTenant(ctx, h); err != nil {
		return nil, err
	}

	if err := s.ensureAlive(h); err != nil {
		return nil, err
	}

	h.mu.Lock()
	exec := h.executor
	h.mu.Unlock()

	accessor, ok := exec.(types.LegacyAccessor)
	if !ok {
		return nil, fmt.Errorf("swarm: handle %s (%s) does not support legacy access", h.ID, h.Name)
	}

	result, err := accessor.Legacy().Run(ctx, args)

	h.mu.Lock()
	h.lastUsedAt = time.Now()
	h.mu.Unlock()

	// For Stateless mode, close after use (consistent with Send behavior).
	if h.Mode == Stateless {
		_ = s.closeHandle(h, "stateless-after-send")
	}

	return result, err
}

// Health returns a snapshot of the health status of all registered executors.
// The returned map is keyed by executor name (h.Name); the value is the status
// of the first registered handle for that name (representative sample).
//
// Note: when multiple tenants hold handles for the same executor name the map
// carries only one status entry (last-write-wins across tenants). This is a
// coarse-grained health view — per-tenant health breakdown is out of scope.
func (s *Swarm) Health() map[string]types.HealthStatus {
	s.mu.RLock()
	snapshot := make([]*Handle, 0, len(s.registry))
	for _, handles := range s.registry {
		snapshot = append(snapshot, handles...)
	}
	s.mu.RUnlock()

	result := make(map[string]types.HealthStatus)
	for _, h := range snapshot {
		if _, seen := result[h.Name]; seen {
			continue // keep first entry per name
		}
		h.mu.Lock()
		var status types.HealthStatus
		if h.executor == nil {
			status = types.HealthUnknown
		} else {
			status = h.executor.IsAlive()
		}
		h.mu.Unlock()
		result[h.Name] = status
	}
	return result
}

// Shutdown closes all managed executors. It waits for each Close() to return
// but respects ctx for the overall deadline. Errors from individual closes are
// accumulated and returned as a single combined error.
func (s *Swarm) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	all := make([]*Handle, 0)
	for _, handles := range s.registry {
		all = append(all, handles...)
	}
	// Clear the registry so new Get calls after Shutdown start fresh.
	s.registry = make(map[string][]*Handle)
	s.mu.Unlock()

	var errs []error
	for _, h := range all {
		select {
		case <-ctx.Done():
			errs = append(errs, ctx.Err())
			return joinErrors(errs)
		default:
		}
		if err := s.closeHandle(h, "shutdown"); err != nil {
			errs = append(errs, fmt.Errorf("swarm: close %s (%s): %w", h.Name, h.ID, err))
		}
	}
	return joinErrors(errs)
}

// --- internal helpers ---

// checkTenant verifies that the tenant in ctx matches h.TenantID. Returns
// ErrHandleNotFound on mismatch and emits an audit cross_tenant_blocked event.
//
// An absent TenantContext in ctx falls back to an empty TenantID. A handle
// with an empty TenantID was spawned in legacy-default mode — it is accessible
// only by contexts that also resolve to an empty TenantID (FR-4).
func (s *Swarm) checkTenant(ctx context.Context, h *Handle) error {
	ctxTenantID := tenantIDFromContext(ctx)
	handleTenantID := canonicalTenantID(h.TenantID)

	if ctxTenantID == handleTenantID {
		return nil
	}

	// Emit cross-tenant blocked event regardless of mode (always an error condition).
	s.auditLog.Emit(audit.AuditEvent{
		Timestamp:  time.Now(),
		EventType:  audit.EventCrossTenantBlocked,
		TenantID:   ctxTenantID,
		ResourceID: h.ID,
		ToolName:   h.Name,
		Result:     "denied",
		Reason:     "cross-tenant handle access blocked",
	})

	return ErrHandleNotFound
}

// emitSpawn emits a swarm_spawn audit event for h, but only in multi-tenant
// mode (FR-4 anti-flood: legacy-default single-tenant mode does not emit).
func (s *Swarm) emitSpawn(h *Handle) {
	if !isMultiTenantID(h.TenantID) {
		return
	}
	s.auditLog.Emit(audit.AuditEvent{
		Timestamp:  time.Now(),
		EventType:  audit.EventSwarmSpawn,
		TenantID:   h.TenantID,
		ResourceID: h.ID,
		ToolName:   h.Name,
		Result:     "ok",
		Reason:     h.Mode.String(),
	})
}

// emitClose emits a swarm_close audit event for h with the given reason, but
// only in multi-tenant mode (FR-4 anti-flood).
func (s *Swarm) emitClose(h *Handle, reason string) {
	if !isMultiTenantID(h.TenantID) {
		return
	}
	s.auditLog.Emit(audit.AuditEvent{
		Timestamp:  time.Now(),
		EventType:  audit.EventSwarmClose,
		TenantID:   h.TenantID,
		ResourceID: h.ID,
		ToolName:   h.Name,
		Result:     "closed",
		Reason:     reason,
	})
}

// emitRestart emits a swarm_restart audit event. Unlike spawn/close, restart
// events are emitted regardless of multi-tenant mode — a restart indicates a
// health failure, which is always observability-relevant.
func (s *Swarm) emitRestart(h *Handle) {
	s.auditLog.Emit(audit.AuditEvent{
		Timestamp:  time.Now(),
		EventType:  audit.EventSwarmRestart,
		TenantID:   h.TenantID,
		ResourceID: h.ID,
		ToolName:   h.Name,
		Result:     "restarted",
		Reason:     "health-failure",
	})
}

// spawn creates a new executor via the factory and wraps it in a Handle.
// It does NOT register the handle in the registry (caller is responsible).
// It acquires s.mu briefly to generate the next ID — safe to call without
// holding s.mu (e.g., from the Stateless path in Get).
//
// TenantID is extracted from ctx and set once on the returned Handle; no
// subsequent code path mutates it (FR-1 immutability invariant).
func (s *Swarm) spawn(ctx context.Context, name string, mode SpawnMode) (*Handle, error) {
	exec, err := s.factoryFn(name)
	if err != nil {
		return nil, fmt.Errorf("swarm: factory(%s): %w", name, err)
	}

	tenantID := tenantIDFromContext(ctx)

	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("%s-%d", name, s.nextID)
	s.mu.Unlock()

	now := time.Now()
	return &Handle{
		ID:         id,
		TenantID:   tenantID,
		Name:       name,
		Mode:       mode,
		executor:   exec,
		startedAt:  now,
		lastUsedAt: now,
	}, nil
}

// spawnLocked is identical to spawn but must be called with s.mu already held
// (write lock). Used by Get to avoid a second lock acquisition while the
// find-or-spawn critical section is in progress (BUG-003 fix).
//
// TenantID is extracted from ctx and set once on the returned Handle (FR-1).
func (s *Swarm) spawnLocked(ctx context.Context, name string, mode SpawnMode) (*Handle, error) {
	// factoryFn must be safe to call without holding s.mu — documented in New.
	exec, err := s.factoryFn(name)
	if err != nil {
		return nil, fmt.Errorf("swarm: factory(%s): %w", name, err)
	}

	tenantID := tenantIDFromContext(ctx)

	s.nextID++
	id := fmt.Sprintf("%s-%d", name, s.nextID)

	now := time.Now()
	return &Handle{
		ID:         id,
		TenantID:   tenantID,
		Name:       name,
		Mode:       mode,
		executor:   exec,
		startedAt:  now,
		lastUsedAt: now,
	}, nil
}

// ensureAlive checks h.executor health and restarts once if not alive.
// Returns an error only if the restart also fails.
func (s *Swarm) ensureAlive(h *Handle) error {
	h.mu.Lock()
	status := h.executor.IsAlive()
	h.mu.Unlock()

	if status == types.HealthAlive || status == types.HealthDegraded {
		return nil
	}

	// Executor is dead or unknown — attempt a single restart.
	return s.restart(h)
}

// restart closes the current executor in h and replaces it with a fresh one
// from the factory. The handle's ID, TenantID, and registration slot are
// preserved — TenantID is never mutated (FR-1 immutability invariant).
func (s *Swarm) restart(h *Handle) error {
	h.mu.Lock()

	// Close the old executor; ignore close errors — it may already be dead.
	if h.executor != nil {
		_ = h.executor.Close()
		h.executor = nil
	}

	fresh, err := s.factoryFn(h.Name)
	if err != nil {
		h.mu.Unlock()
		return fmt.Errorf("swarm: restart(%s): %w", h.Name, err)
	}

	h.executor = fresh
	h.startedAt = time.Now()
	h.lastUsedAt = time.Now()
	h.mu.Unlock()

	// Emit restart event after releasing h.mu (audit.Emit is non-blocking but
	// emitting outside the lock keeps hot-path lock hold time minimal).
	s.emitRestart(h)

	return nil
}

// closeHandle calls Close on h's executor and nils the reference.
// reason is forwarded to the audit event (e.g. "shutdown", "stateless-after-send").
func (s *Swarm) closeHandle(h *Handle, reason string) error {
	h.mu.Lock()
	if h.executor == nil {
		h.mu.Unlock()
		return nil
	}
	err := h.executor.Close()
	h.executor = nil
	h.mu.Unlock()

	// Emit close event after releasing h.mu (audit.Emit is non-blocking but
	// emitting outside the lock keeps hot-path lock hold time minimal).
	s.emitClose(h, reason)

	return err
}

// joinErrors combines multiple errors into one preserving the unwrappable
// error chain. Returns nil if errs is empty. PRC v6 P3-1 — replaced manual
// string concat with errors.Join (Go 1.20+) so callers can use errors.Is /
// errors.As against individual errors.
func joinErrors(errs []error) error {
	return errors.Join(errs...)
}
