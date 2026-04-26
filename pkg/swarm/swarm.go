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

	"github.com/thebtf/aimux/pkg/types"
)

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

	mu       sync.RWMutex
	registry map[string][]*Handle // keyed by executor name
	nextID   uint64
}

// New creates a Swarm. factoryFn is called whenever a new ExecutorV2 is needed
// for the given name. It must be safe to call concurrently.
func New(factoryFn func(name string) (types.ExecutorV2, error)) *Swarm {
	return &Swarm{
		factoryFn: factoryFn,
		registry:  make(map[string][]*Handle),
	}
}

// Get returns a Handle for the named executor according to mode:
//   - Stateless: always creates a new executor and a fresh Handle.
//   - Stateful/Persistent: returns the first alive existing Handle, or spawns
//     a new one if none exist or all are dead.
func (s *Swarm) Get(ctx context.Context, name string, mode SpawnMode) (*Handle, error) {
	if name == "" {
		return nil, errors.New("swarm: executor name must not be empty")
	}

	if mode == Stateless {
		return s.spawn(name, mode)
	}

	// Stateful / Persistent: try to reuse an existing alive handle.
	s.mu.RLock()
	handles := s.registry[name]
	s.mu.RUnlock()

	for _, h := range handles {
		h.mu.Lock()
		alive := h.executor != nil && h.executor.IsAlive() == types.HealthAlive
		h.mu.Unlock()
		if alive {
			return h, nil
		}
	}

	// No alive handle — spawn a new one and register it.
	h, err := s.spawn(name, mode)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.registry[name] = append(s.registry[name], h)
	s.mu.Unlock()

	return h, nil
}

// Send sends msg through h's executor and returns the complete response.
// If the executor is not alive, Swarm restarts it once and retries. If the
// restart also fails the original error is returned.
func (s *Swarm) Send(ctx context.Context, h *Handle, msg types.Message) (*types.Response, error) {
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
		_ = s.closeHandle(h)
	}

	return resp, err
}

// SendStream sends msg through h's executor, delivering incremental output via
// onChunk. Returns the complete aggregated response after the final chunk.
// If the executor is not alive, Swarm restarts it once before sending.
func (s *Swarm) SendStream(ctx context.Context, h *Handle, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
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
		_ = s.closeHandle(h)
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
		_ = s.closeHandle(h)
	}

	return result, err
}

// Health returns a snapshot of the health status of all registered executors.
// The returned map is keyed by executor name; the value is the status of the
// first registered handle (representative sample).
func (s *Swarm) Health() map[string]types.HealthStatus {
	s.mu.RLock()
	snapshot := make(map[string][]*Handle, len(s.registry))
	for name, handles := range s.registry {
		snapshot[name] = handles
	}
	s.mu.RUnlock()

	result := make(map[string]types.HealthStatus, len(snapshot))
	for name, handles := range snapshot {
		if len(handles) == 0 {
			continue
		}
		h := handles[0]
		h.mu.Lock()
		var status types.HealthStatus
		if h.executor == nil {
			status = types.HealthUnknown
		} else {
			status = h.executor.IsAlive()
		}
		h.mu.Unlock()
		result[name] = status
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
		if err := s.closeHandle(h); err != nil {
			errs = append(errs, fmt.Errorf("swarm: close %s (%s): %w", h.Name, h.ID, err))
		}
	}
	return joinErrors(errs)
}

// --- internal helpers ---

// spawn creates a new executor via the factory and wraps it in a Handle.
// It does NOT register the handle in the registry (caller is responsible).
func (s *Swarm) spawn(name string, mode SpawnMode) (*Handle, error) {
	exec, err := s.factoryFn(name)
	if err != nil {
		return nil, fmt.Errorf("swarm: factory(%s): %w", name, err)
	}

	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("%s-%d", name, s.nextID)
	s.mu.Unlock()

	now := time.Now()
	return &Handle{
		ID:         id,
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
// from the factory. The handle's ID and registration slot are preserved.
func (s *Swarm) restart(h *Handle) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Close the old executor; ignore close errors — it may already be dead.
	if h.executor != nil {
		_ = h.executor.Close()
		h.executor = nil
	}

	fresh, err := s.factoryFn(h.Name)
	if err != nil {
		return fmt.Errorf("swarm: restart(%s): %w", h.Name, err)
	}

	h.executor = fresh
	h.startedAt = time.Now()
	h.lastUsedAt = time.Now()
	return nil
}

// closeHandle calls Close on h's executor and nils the reference.
func (s *Swarm) closeHandle(h *Handle) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.executor == nil {
		return nil
	}
	err := h.executor.Close()
	h.executor = nil
	return err
}

// joinErrors combines multiple errors into one. Returns nil if errs is empty.
func joinErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	msg := errs[0].Error()
	for _, e := range errs[1:] {
		msg += "; " + e.Error()
	}
	return errors.New(msg)
}
