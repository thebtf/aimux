package swarm

import (
	"context"

	"github.com/thebtf/aimux/pkg/types"
)

// ExecuteSessionBinding runs one execution against the live handle referenced
// by binding. It is the only public entry point that resolves a
// LiveSessionBinding into a live provider call; the private handle never
// leaves this package (CR-003).
//
// attempted reports whether every pre-provider admission gate passed and the
// provider was actually invoked. false means the run never reached the
// executor — the caller must treat that as a pre-provider rejection (safe to
// release the durable Run Binding reservation), never as a native return.
// true means the provider was invoked regardless of the returned error —
// callers must record a native return and must not release durable Run
// Binding authority for it here.
func (s *Swarm) ExecuteSessionBinding(ctx context.Context, binding LiveSessionBinding, id types.ExecutionID, msg types.Message, sink types.ExecutorEventSink) (response *types.Response, attempted bool, err error) {
	if binding.handle == nil {
		return nil, false, ErrHandleNotFound
	}
	response, err = s.executeAdmitted(ctx, binding.handle, binding.Scope, id, msg, sink,
		func() { attempted = true },
		func(authority handleAuthority, exec types.ExecutorV2) error {
			return s.validateLiveSessionBindingAuthority(binding, authority, exec)
		},
	)
	return response, attempted, err
}

// ReleaseSessionBinding closes the live handle referenced by binding under
// the handle's own operation gate. It exists for coordinators that acquired
// a binding but must abandon it before any Execute call — e.g. a durable Run
// Binding reservation failed after the live handle was already spawned — so
// a stateless/new/forked handle never leaks past its Loom-owned reservation
// attempt (CR-003).
//
// Only a binding this Swarm instance actually spawned/registered for the
// caller's own attempt should be released this way. A resolved exact_resume
// binding is shared live authority owned by the Worker Session across many
// turns; callers must never route it through ReleaseSessionBinding.
//
// binding is validated as an exact, unmutated capability token — same fence
// as ExecuteSessionBinding — before the handle is closed, so a stale or
// caller-mutated binding can never tear down a successor generation's live
// handle.
func (s *Swarm) ReleaseSessionBinding(ctx context.Context, binding LiveSessionBinding, reason string) error {
	h := binding.handle
	if h == nil {
		return ErrHandleNotFound
	}
	if err := h.acquireOperation(ctx); err != nil {
		return err
	}
	defer h.releaseOperation()
	authority, exec, err := s.executionAuthority(ctx, h, binding.Scope)
	if err != nil {
		return err
	}
	if err := s.validateLiveSessionBindingAuthority(binding, authority, exec); err != nil {
		return err
	}
	return s.closeHandleLocked(h, reason)
}

// validateLiveSessionBindingAuthority proves binding is an exact, unmutated
// capability token for the current live authority/executor pair that the
// caller already resolved under the handle's operation lease and (for
// Execute) Swarm's lifecycle fence: authority.scope/tenant already fences
// binding.Scope and the caller's tenant context (executionAuthority), so
// this only needs the fields unique to LiveSessionBinding — handle ID,
// handle generation, registry generation, and, when the current executor
// exposes provider session identity, the exact live ProviderSession. A
// caller-mutated nil ProviderSession never downgrades to an
// unauthenticated attempt, and an executor with no identity provider
// requires a nil captured ProviderSession. A stale or caller-mutated
// binding fails closed with ErrHandleNotFound rather than disclosing which
// field diverged (same non-disclosure posture as checkTenant).
func (s *Swarm) validateLiveSessionBindingAuthority(binding LiveSessionBinding, authority handleAuthority, exec types.ExecutorV2) error {
	if binding.handle == nil || binding.handle.ID != binding.HandleID {
		return ErrHandleNotFound
	}
	if authority.generation != binding.HandleGeneration {
		return ErrHandleNotFound
	}
	s.mu.RLock()
	registryGeneration := s.registryGeneration
	s.mu.RUnlock()
	if registryGeneration != binding.RegistryGeneration {
		return ErrHandleNotFound
	}
	if authority.mode == Stateless {
		if binding.ProviderSession != nil {
			return ErrHandleNotFound
		}
		return nil
	}
	identityProvider, hasIdentity := exec.(types.SessionIdentityProvider)
	switch {
	case hasIdentity && binding.ProviderSession == nil:
		return ErrHandleNotFound
	case hasIdentity:
		if identityProvider.SessionIdentity() != *binding.ProviderSession {
			return ErrHandleNotFound
		}
	case binding.ProviderSession != nil:
		return ErrHandleNotFound
	}
	return nil
}
