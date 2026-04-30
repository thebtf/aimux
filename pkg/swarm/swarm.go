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

// ErrNotSupported indicates a backend cannot create a persistent Session.
// Returned by SessionFactory.StartSession implementations when the backend
// lacks the capability (e.g. ConPTY on non-Windows). FR-1 C3: defensive
// guard for misuse, NOT a graceful fallback signal — callers MUST gate
// StartSession invocation on Info().Capabilities.PersistentSessions check.
var ErrNotSupported = errors.New("swarm: backend does not support persistent sessions")

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

	// keyLocks holds a *sync.Mutex per registry key (DEF-8 / FR-2).
	// Per-key locking allows concurrent Gets on distinct keys to run their
	// factoryFn in parallel while still serialising same-key Gets to prevent
	// TOCTOU double-spawn (BUG-003). The value type is *sync.Mutex; LoadOrStore
	// ensures exactly one mutex is created per key even under concurrent Gets.
	keyLocks sync.Map

	// statefulTTL is the idle reap budget for Stateful-mode handles. After
	// time.Since(h.lastUsedAt) > statefulTTL the reaper Closes the handle.
	// Persistent-mode handles ignore this — they are killed only by
	// Shutdown/Close/daemon hot-swap (US3 / NFR-5). 0 disables reaping.
	statefulTTL time.Duration

	// reaperStop is closed by Shutdown to terminate the reaper goroutine.
	reaperStop chan struct{}

	// reaperOnce ensures reaperStop is closed at most once even when
	// Shutdown is invoked from multiple goroutines or repeated.
	reaperOnce sync.Once
}

// Option customises Swarm behaviour at construction. Used to override
// non-default tuning knobs (TTL, reaper cadence) without breaking the
// minimal New signature used by all existing callers.
type Option func(*Swarm)

// WithStatefulTTL overrides the default 5-minute idle TTL applied to
// Stateful-mode handles by the background reaper. Persistent-mode handles
// are NEVER reaped — only explicit Close() or Shutdown() terminates them
// (US3 / NFR-5 / FR-4 contract). Pass d=0 to disable reaping entirely
// (useful for unit tests that manage handle lifecycle manually).
func WithStatefulTTL(d time.Duration) Option {
	return func(s *Swarm) { s.statefulTTL = d }
}

// defaultStatefulTTL — 5 minutes per FR-4 spec resolution Q-CLAR-2.
const defaultStatefulTTL = 5 * time.Minute

// New creates a Swarm. factoryFn is called whenever a new ExecutorV2 is needed
// for the given name; it must be safe to call concurrently.
//
// auditLog receives executor lifecycle events (spawn, close, restart, cross-tenant
// block). Pass nil to use a no-op discard log — safe for tests and single-tenant
// deployments that do not need observability.
//
// A background reaper goroutine starts automatically and scans every TTL/2
// interval for Stateful handles idle longer than statefulTTL. Persistent
// handles are exempt (US3 contract — survive idle reap, killed only by
// Shutdown/Close/daemon hot-swap).
func New(factoryFn func(name string) (types.ExecutorV2, error), auditLog audit.AuditLog, opts ...Option) *Swarm {
	al := auditLog
	if al == nil {
		al = audit.DiscardLog{}
	}
	s := &Swarm{
		factoryFn:   factoryFn,
		auditLog:    al,
		registry:    make(map[string][]*Handle),
		statefulTTL: defaultStatefulTTL,
		reaperStop:  make(chan struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.statefulTTL > 0 {
		go s.reapLoop()
	}
	return s
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

	// Stateful/Persistent: per-key mutex prevents TOCTOU double-spawn (BUG-003)
	// while allowing concurrent Gets on DISTINCT keys to run their factoryFn in
	// parallel (DEF-8 / FR-2 latency-bomb fix). Only same-key Gets are serialised.
	//
	// Lock topology (order must be respected everywhere to avoid deadlock):
	//   1. per-key mutex (keyLocks)  — held for the full find-or-spawn sequence
	//   2. s.mu (RLock or Lock)      — held only for registry read / write
	//
	// PRC v7 BUG-004: dead handles are pruned inline (under the per-key mutex
	// + Swarm write lock) to prevent unbounded slice growth.
	mu := s.getKeyMutex(key)
	mu.Lock()
	defer mu.Unlock()

	// Step 1: check registry for an existing alive handle (read lock only).
	s.mu.RLock()
	existing := s.registry[key]
	// Build alive slice without mutating the registry while holding RLock.
	var alive []*Handle
	for _, h := range existing {
		h.mu.Lock()
		isAlive := h.executor != nil && h.executor.IsAlive() == types.HealthAlive
		h.mu.Unlock()
		if isAlive {
			alive = append(alive, h)
		}
	}
	s.mu.RUnlock()

	if len(alive) > 0 {
		// Prune dead handles from the registry if any were found dead.
		if len(alive) != len(existing) {
			s.mu.Lock()
			s.registry[key] = alive
			s.mu.Unlock()
		}
		return alive[0], nil
	}

	// Step 2: no alive handle — run factoryFn OUTSIDE s.mu (DEF-8 fix).
	// The per-key mutex above serialises concurrent same-key Gets, so exactly
	// one goroutine reaches here per key at a time (TOCTOU prevention preserved).
	h, err := s.spawnLocked(ctx, name, mode)
	if err != nil {
		return nil, err
	}

	// Step 3: re-acquire write lock only for registry insertion.
	// Use alive (the already-pruned slice) as the base, not s.registry[key],
	// so dead handles that were pruned in Step 1 are not re-added here.
	s.mu.Lock()
	s.registry[key] = append(alive, h)
	s.mu.Unlock()

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
	// Bump lastUsedAt at Send ENTRY so a long-running Send (e.g. multi-turn
	// dialog с slow CLI) does not let the reaper see a stale timestamp and
	// kill the executor mid-flight (PR #134 review — coderabbit major).
	h.lastUsedAt = time.Now()
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
	// Bump lastUsedAt at SendStream ENTRY so a long-running stream does not
	// let the reaper see a stale timestamp and kill the executor mid-flight
	// (PR #134 review — coderabbit major).
	h.lastUsedAt = time.Now()
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
// of the FIRST registered handle for that name (first-write-wins).
//
// Note: when multiple tenants hold handles for the same executor name the map
// carries only one status entry — first-write-wins across tenants, NOT
// last-write-wins. The loop short-circuits on the first hit per name. This is
// a coarse-grained health view — per-tenant health breakdown is out of scope.
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
	// Stop the reaper goroutine before walking the registry so it does
	// not race with the shutdown drain.
	s.reaperOnce.Do(func() {
		if s.reaperStop != nil {
			close(s.reaperStop)
		}
	})

	s.mu.Lock()
	all := make([]*Handle, 0)
	for _, handles := range s.registry {
		all = append(all, handles...)
	}
	// Clear the registry so new Get calls after Shutdown start fresh.
	s.registry = make(map[string][]*Handle)
	s.mu.Unlock()

	// Release all per-key mutexes so they can be GC'd. This prevents the
	// keyLocks sync.Map from accumulating unbounded entries across repeated
	// Shutdown+reinit cycles (DEF-8 follow-up — keyLocks memory leak fix).
	s.keyLocks.Range(func(k, _ any) bool {
		s.keyLocks.Delete(k)
		return true
	})

	var errs []error
	for _, h := range all {
		select {
		case <-ctx.Done():
			errs = append(errs, ctx.Err())
			return errors.Join(errs...)
		default:
		}
		if err := s.closeHandle(h, "shutdown"); err != nil {
			errs = append(errs, fmt.Errorf("swarm: close %s (%s): %w", h.Name, h.ID, err))
		}
	}
	return errors.Join(errs...)
}

// reapLoop ticks every statefulTTL/2 and reaps idle Stateful handles
// (US3 / FR-4). Stops when reaperStop is closed by Shutdown.
func (s *Swarm) reapLoop() {
	interval := s.statefulTTL / 2
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.reaperStop:
			return
		case <-ticker.C:
			s.reapStaleStateful()
		}
	}
}

// reapStaleStateful walks the registry once and Closes any Stateful-mode
// handle whose lastUsedAt exceeded statefulTTL. Persistent-mode handles
// are SKIPPED — US3 contract (Persistent survives idle reap; killed only
// by Close / Shutdown / daemon hot-swap).
func (s *Swarm) reapStaleStateful() {
	now := time.Now()
	var stale []*Handle

	s.mu.Lock()
	for key, handles := range s.registry {
		kept := handles[:0]
		for _, h := range handles {
			h.mu.Lock()
			idle := now.Sub(h.lastUsedAt)
			h.mu.Unlock()
			if h.Mode == Stateful && idle > s.statefulTTL {
				stale = append(stale, h)
				continue
			}
			kept = append(kept, h)
		}
		if len(kept) == 0 {
			delete(s.registry, key)
		} else {
			s.registry[key] = kept
		}
	}
	s.mu.Unlock()

	for _, h := range stale {
		_ = s.closeHandle(h, "stateful-ttl-idle")
	}
}

// MaybeStartSession invokes ex.StartSession when (a) ex satisfies SessionFactory
// AND (b) ex.Info().Capabilities.PersistentSessions == true. Returns the started
// Session or (nil, nil) when the capability is not present — callers fall back to
// stateless dispatch. Returns an error only when StartSession is called and fails
// (FR-1 C3 defensive guard — ErrNotSupported propagated to caller, not swallowed).
//
// The capability gate is the primary contract: callers MUST check
// Info().Capabilities.PersistentSessions before calling StartSession directly;
// this helper encapsulates the check to prevent misuse.
//
// Pattern (FR-4 / DEF-8 latency-bomb fix preserved): capability check and factory
// invocation run outside Swarm.mu to avoid contention on concurrent Gets.
func MaybeStartSession(ctx context.Context, ex types.ExecutorV2, args types.SpawnArgs) (types.Session, error) {
	if !ex.Info().Capabilities.PersistentSessions {
		return nil, nil
	}
	// Direct path: ex itself satisfies SessionFactory (concrete backend
	// types — pipe.Executor / conpty.Executor / pty.Executor — implement it).
	if sf, ok := ex.(types.SessionFactory); ok {
		return sf.StartSession(ctx, args)
	}
	// Adapter path: ex is a CLI{Pipe,ConPTY,PTY}Adapter wrapping a
	// LegacyExecutor whose concrete type is the backend Executor satisfying
	// SessionFactory. Probe the underlying via LegacyAccessor — this lets
	// orchestrators construct the Swarm с adapter factories WITHOUT losing
	// persistent-session capability (PR #134 review — gemini high).
	if la, ok := ex.(types.LegacyAccessor); ok {
		if sf, ok := la.Legacy().(types.SessionFactory); ok {
			return sf.StartSession(ctx, args)
		}
	}
	return nil, nil
}

// --- internal helpers ---

// getKeyMutex returns the per-key *sync.Mutex for the given registry key,
// creating it on first access. LoadOrStore guarantees exactly one mutex per
// key even when multiple goroutines race on first access (DEF-8 / FR-2).
func (s *Swarm) getKeyMutex(key string) *sync.Mutex {
	actual, _ := s.keyLocks.LoadOrStore(key, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

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
		Reason:     "swarm: cross-tenant handle access blocked at executor boundary",
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
		TenantID:   canonicalTenantID(h.TenantID),
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
		TenantID:   canonicalTenantID(h.TenantID),
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
		TenantID:   canonicalTenantID(h.TenantID),
		ResourceID: h.ID,
		ToolName:   h.Name,
		Result:     "restarted",
		Reason:     "health-failure",
	})
}

// makeHandle constructs a Handle from (exec, ctx, id, name, mode). Pure factory
// — no lock acquisition, no registry side-effect. Both spawn and spawnLocked
// delegate to this helper to eliminate field-list duplication (PRC v7 HIGH-3).
//
// TenantID is canonicalized once at construction; FR-1 immutability holds for
// the lifetime of the returned Handle.
func makeHandle(ctx context.Context, id, name string, mode SpawnMode, exec types.ExecutorV2) *Handle {
	now := time.Now()
	return &Handle{
		ID:         id,
		TenantID:   tenantIDFromContext(ctx),
		Name:       name,
		Mode:       mode,
		executor:   exec,
		startedAt:  now,
		lastUsedAt: now,
	}
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

	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("%s-%d", name, s.nextID)
	s.mu.Unlock()

	return makeHandle(ctx, id, name, mode, exec), nil
}

// spawnLocked creates a new executor via the factory and wraps it in a Handle.
// It does NOT register the handle in the registry (caller is responsible).
// It must be called while holding the per-key mutex (s.keyLocks entry) but NOT
// s.mu — factoryFn runs outside s.mu to resolve DEF-8 (FR-2 latency bomb).
// s.mu is acquired briefly only to increment nextID.
//
// TenantID is extracted from ctx and set once on the returned Handle (FR-1).
func (s *Swarm) spawnLocked(ctx context.Context, name string, mode SpawnMode) (*Handle, error) {
	// factoryFn is called without holding s.mu (DEF-8 fix). The per-key mutex
	// held by the caller serialises same-key spawns; distinct keys run concurrently.
	exec, err := s.factoryFn(name)
	if err != nil {
		return nil, fmt.Errorf("swarm: factory(%s): %w", name, err)
	}

	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("%s-%d", name, s.nextID)
	s.mu.Unlock()

	return makeHandle(ctx, id, name, mode, exec), nil
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

