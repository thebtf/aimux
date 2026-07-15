package swarm

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/thebtf/aimux/pkg/types"
)

const maxSwarmRegistryGeneration = uint64(1<<63 - 1)

var (
	swarmRegistryGenerationSeed = newSwarmRegistryGenerationSeed()
	nextSwarmRegistryGeneration atomic.Uint64
)

func newSwarmRegistryGenerationSeed() uint64 {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic("swarm: registry generation seed: " + err.Error())
	}
	seed := binary.LittleEndian.Uint64(bytes[:]) & maxSwarmRegistryGeneration
	if seed == 0 {
		return 1
	}
	return seed
}

func newSwarmRegistryGeneration() uint64 {
	for {
		generation := (swarmRegistryGenerationSeed + nextSwarmRegistryGeneration.Add(1)) & maxSwarmRegistryGeneration
		if generation != 0 {
			return generation
		}
	}
}

// LiveSessionBinding is a provider-neutral, fenced view of a live Swarm handle.
// The private handle is retained solely for internal execution ownership.
type LiveSessionBinding struct {
	Scope              string
	HandleID           string
	HandleGeneration   uint64
	RegistryGeneration uint64
	ProviderSession    *types.SessionIdentity

	handle *Handle
}

// AcquireSessionBinding creates or resolves one exact live session binding.
func (s *Swarm) AcquireSessionBinding(ctx context.Context, name string, request types.SessionBindingRequest, opts ...GetOption) (LiveSessionBinding, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return LiveSessionBinding{}, errors.New("swarm: executor name must not be empty")
	}
	if err := request.Validate(); err != nil {
		return LiveSessionBinding{}, err
	}

	var o getOpts
	for _, fn := range opts {
		fn(&o)
	}

	switch request.Mode {
	case types.SessionBindingModeStateless:
		return s.acquireStatelessBinding(ctx, name, o.scope)
	case types.SessionBindingModeNew:
		return s.acquireNewBinding(ctx, name, o.scope, bindingArgs(o.sessionArgs))
	case types.SessionBindingModeExactResume:
		return s.acquireExactBinding(ctx, name, o.scope, *request.Expected)
	case types.SessionBindingModeFork:
		return s.acquireForkBinding(ctx, name, o.scope, *request.Parent, bindingArgs(o.sessionArgs))
	default:
		return LiveSessionBinding{}, errors.New("swarm: session binding mode is unknown")
	}
}

func bindingArgs(args *types.SpawnArgs) types.SpawnArgs {
	if args == nil {
		return types.SpawnArgs{}
	}
	return *args
}

func (s *Swarm) acquireStatelessBinding(ctx context.Context, name, scope string) (LiveSessionBinding, error) {
	h, err := s.spawn(ctx, name, Stateless, scope)
	if err != nil {
		return LiveSessionBinding{}, err
	}
	defer h.releaseOperation()

	binding, err := s.snapshotLiveBinding(h, false)
	if err != nil {
		_ = s.closeHandleLocked(h, "session-binding-snapshot-failed")
		return LiveSessionBinding{}, fmt.Errorf("swarm: stateless session binding: %w", err)
	}
	s.emitSpawn(h)
	return binding, nil
}

func (s *Swarm) acquireNewBinding(ctx context.Context, name, scope string, args types.SpawnArgs) (LiveSessionBinding, error) {
	key := registryKey(tenantIDFromContext(ctx), scope, name)
	keyMu := s.getKeyMutex(key)
	keyMu.Lock()
	defer keyMu.Unlock()

	h, err := s.spawnLocked(ctx, name, Stateful, scope)
	if err != nil {
		return LiveSessionBinding{}, err
	}
	defer h.releaseOperation()
	if err := s.strictBindSession(ctx, h, args); err != nil {
		_ = s.closeHandleLocked(h, "session-binding-new-failed")
		return LiveSessionBinding{}, fmt.Errorf("swarm: new session binding(%s): %w", name, err)
	}
	if err := s.registerBoundChild(h, key); err != nil {
		_ = s.closeHandleLocked(h, "session-binding-new-registration-failed")
		return LiveSessionBinding{}, err
	}

	binding, err := s.snapshotLiveBinding(h, true)
	if err != nil {
		_ = s.closeHandleLocked(h, "session-binding-new-snapshot-failed")
		return LiveSessionBinding{}, fmt.Errorf("swarm: new session binding(%s): %w", name, err)
	}
	s.emitSpawn(h)
	return binding, nil
}

func (s *Swarm) acquireExactBinding(ctx context.Context, name, scope string, expected types.SessionBindingIdentity) (LiveSessionBinding, error) {
	h, authority, err := s.exactBindingHandle(ctx, name, scope, expected)
	if err != nil {
		return LiveSessionBinding{}, err
	}
	defer h.releaseOperation()
	if authority.scope != scope || authority.generation != expected.HandleGeneration || authority.mode != Stateful {
		return LiveSessionBinding{}, ErrHandleNotFound
	}

	binding, err := s.snapshotLiveBinding(h, true)
	if err != nil {
		return LiveSessionBinding{}, fmt.Errorf("swarm: exact session binding: %w", err)
	}
	if binding.HandleID != expected.HandleID || binding.HandleGeneration != expected.HandleGeneration || binding.RegistryGeneration != expected.RegistryGeneration || binding.ProviderSession == nil || *binding.ProviderSession != expected.ProviderSession {
		return LiveSessionBinding{}, ErrHandleNotFound
	}
	return binding, nil
}

func (s *Swarm) acquireForkBinding(ctx context.Context, name, scope string, parent types.SessionBindingIdentity, args types.SpawnArgs) (LiveSessionBinding, error) {
	parentHandle, authority, err := s.exactBindingHandle(ctx, name, scope, parent)
	if err != nil {
		return LiveSessionBinding{}, err
	}
	defer parentHandle.releaseOperation()
	if authority.scope != scope || authority.generation != parent.HandleGeneration || authority.mode != Stateful {
		return LiveSessionBinding{}, ErrHandleNotFound
	}

	parentBinding, err := s.snapshotLiveBinding(parentHandle, true)
	if err != nil {
		return LiveSessionBinding{}, fmt.Errorf("swarm: fork session binding: %w", err)
	}
	if parentBinding.HandleID != parent.HandleID || parentBinding.HandleGeneration != parent.HandleGeneration || parentBinding.RegistryGeneration != parent.RegistryGeneration || parentBinding.ProviderSession == nil || *parentBinding.ProviderSession != parent.ProviderSession {
		return LiveSessionBinding{}, ErrHandleNotFound
	}

	parentHandle.mu.Lock()
	exec := parentHandle.executor
	parentHandle.mu.Unlock()
	forker, ok := exec.(types.SessionForker)
	if !ok {
		return LiveSessionBinding{}, errors.New("swarm: parent executor does not support session fork")
	}
	binder, ok := exec.(types.SessionBinder)
	if !ok {
		return LiveSessionBinding{}, errors.New("swarm: parent executor does not support session binding")
	}

	key := registryKey(tenantIDFromContext(ctx), scope, name)
	keyMu := s.getKeyMutex(key)
	keyMu.Lock()
	defer keyMu.Unlock()

	session, err := forker.ForkSession(ctx, parent.ProviderSession, args)
	if err != nil {
		if session != nil {
			_ = session.Close()
		}
		return LiveSessionBinding{}, fmt.Errorf("swarm: fork session binding(%s): %w", name, err)
	}
	if session == nil {
		return LiveSessionBinding{}, errors.New("swarm: fork session binding returned nil session")
	}
	bound := binder.WithSession(session)
	if bound == nil {
		_ = session.Close()
		return LiveSessionBinding{}, errors.New("swarm: fork session binding returned nil executor")
	}

	child, err := s.makeBoundChild(ctx, name, scope, bound, args)
	if err != nil {
		return LiveSessionBinding{}, err
	}
	defer child.releaseOperation()
	if err := s.registerBoundChild(child, key); err != nil {
		_ = s.closeHandleLocked(child, "session-binding-fork-registration-failed")
		return LiveSessionBinding{}, err
	}
	binding, err := s.snapshotLiveBinding(child, true)
	if err != nil {
		_ = s.closeHandleLocked(child, "session-binding-fork-snapshot-failed")
		return LiveSessionBinding{}, fmt.Errorf("swarm: fork session binding(%s): %w", name, err)
	}
	s.emitSpawn(child)
	return binding, nil
}

// exactBindingHandle finds only the requested tenant/scope/name slot, then
// holds the handle operation gate while its live authority is inspected.
func (s *Swarm) exactBindingHandle(ctx context.Context, name, scope string, identity types.SessionBindingIdentity) (*Handle, handleAuthority, error) {
	key := registryKey(tenantIDFromContext(ctx), scope, name)
	s.mu.RLock()
	var target *Handle
	for _, h := range s.registry[key] {
		if h.ID == identity.HandleID {
			target = h
			break
		}
	}
	s.mu.RUnlock()
	if target == nil {
		return nil, handleAuthority{}, ErrHandleNotFound
	}
	authority, err := s.beginHandleOperation(ctx, target)
	if err != nil {
		if errors.Is(err, ErrHandleNotFound) {
			return nil, handleAuthority{}, ErrHandleNotFound
		}
		return nil, handleAuthority{}, fmt.Errorf("swarm: session binding operation: %w", err)
	}
	return target, authority, nil
}

// strictBindSession deliberately does not use bindSession's stateless fallback:
// new and forked bindings require a real provider session and bound executor.
func (s *Swarm) strictBindSession(ctx context.Context, h *Handle, args types.SpawnArgs) error {
	h.mu.Lock()
	exec := h.executor
	h.mu.Unlock()
	if exec == nil {
		return ErrHandleNotFound
	}
	binder, ok := exec.(types.SessionBinder)
	if !ok {
		return errors.New("swarm: executor does not support session binding")
	}
	session, err := MaybeStartSession(ctx, exec, args)
	if err != nil {
		if session != nil {
			_ = session.Close()
		}
		return err
	}
	if session == nil {
		return errors.New("swarm: executor did not create a persistent session")
	}
	bound := binder.WithSession(session)
	if bound == nil {
		_ = session.Close()
		return errors.New("swarm: session binding returned nil executor")
	}
	h.mu.Lock()
	h.executor = bound
	h.sessionArgs = &args
	h.mu.Unlock()
	return nil
}

// makeBoundChild constructs a fork child without invoking the Swarm factory.
func (s *Swarm) makeBoundChild(ctx context.Context, name, scope string, exec types.ExecutorV2, args types.SpawnArgs) (*Handle, error) {
	s.lifecycleMu.RLock()
	if s.shuttingDown {
		s.lifecycleMu.RUnlock()
		_ = exec.Close()
		return nil, ErrSwarmShutdown
	}
	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("%s-%d", name, s.nextID)
	h := makeHandle(ctx, id, name, Stateful, scope, exec)
	h.sessionArgs = &args
	if !h.tryAcquireOperation() {
		s.mu.Unlock()
		s.lifecycleMu.RUnlock()
		_ = exec.Close()
		return nil, errors.New("swarm: failed to reserve fork handle operation")
	}
	s.live[h] = handleAuthority{tenantID: h.TenantID, scope: scope, generation: h.generation, mode: Stateful}
	s.mu.Unlock()
	s.lifecycleMu.RUnlock()
	return h, nil
}

// registerBoundChild appends a new handle while lifecycle admission remains open.
// Callers hold the per-key mutex; lock order remains key -> lifecycle -> Swarm.
func (s *Swarm) registerBoundChild(h *Handle, key string) error {
	s.lifecycleMu.RLock()
	defer s.lifecycleMu.RUnlock()
	if s.shuttingDown {
		return ErrSwarmShutdown
	}
	s.mu.Lock()
	s.registry[key] = append(s.registry[key], h)
	s.mu.Unlock()
	return nil
}

// snapshotLiveBinding copies currently held handle authority into public data.
// Stateless bindings deliberately omit provider session identity.
func (s *Swarm) snapshotLiveBinding(h *Handle, includeProvider bool) (LiveSessionBinding, error) {
	s.mu.RLock()
	registryGeneration := s.registryGeneration
	s.mu.RUnlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.executor == nil {
		return LiveSessionBinding{}, errors.New("swarm: cannot snapshot non-live handle")
	}
	if !h.executor.IsAlive().IsHealthy() {
		return LiveSessionBinding{}, errors.New("swarm: cannot snapshot non-live executor")
	}
	binding := LiveSessionBinding{
		Scope:              h.scope,
		HandleID:           h.ID,
		HandleGeneration:   h.generation,
		RegistryGeneration: registryGeneration,
		handle:             h,
	}
	if includeProvider {
		if identityProvider, ok := h.executor.(types.SessionIdentityProvider); ok {
			identity := identityProvider.SessionIdentity()
			if err := identity.Validate(); err != nil {
				return LiveSessionBinding{}, fmt.Errorf("swarm: invalid provider session identity: %w", err)
			}
			binding.ProviderSession = &identity
		}
	}
	return binding, nil
}
