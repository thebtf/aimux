package types

import (
	"context"
	"strings"
)

// SessionBindingMode selects how a request binds to a session.
type SessionBindingMode string

const (
	SessionBindingModeStateless   SessionBindingMode = "stateless"
	SessionBindingModeNew         SessionBindingMode = "new"
	SessionBindingModeExactResume SessionBindingMode = "exact_resume"
	SessionBindingModeFork        SessionBindingMode = "fork"
)

// SessionBindingIdentity fences a live session binding to its exact registry
// and provider session generations.
type SessionBindingIdentity struct {
	HandleID           string          `json:"handle_id"`
	HandleGeneration   uint64          `json:"handle_generation"`
	RegistryGeneration uint64          `json:"registry_generation"`
	ProviderSession    SessionIdentity `json:"provider_session"`
}

// Validate rejects incomplete or unfenced session binding identities.
func (identity SessionBindingIdentity) Validate() error {
	if strings.TrimSpace(identity.HandleID) == "" {
		return NewValidationError("session binding handle ID must not be blank")
	}
	if identity.HandleGeneration == 0 {
		return NewValidationError("session binding handle generation must be greater than zero")
	}
	if identity.RegistryGeneration == 0 {
		return NewValidationError("session binding registry generation must be greater than zero")
	}
	return identity.ProviderSession.Validate()
}

// SessionBindingRequest describes a provider-neutral session binding request.
type SessionBindingRequest struct {
	Mode     SessionBindingMode      `json:"mode"`
	Expected *SessionBindingIdentity `json:"expected,omitempty"`
	Parent   *SessionBindingIdentity `json:"parent,omitempty"`
}

// Validate rejects modes with missing, invalid, or extraneous identities.
func (request SessionBindingRequest) Validate() error {
	switch request.Mode {
	case SessionBindingModeStateless, SessionBindingModeNew:
		if request.Expected != nil || request.Parent != nil {
			return NewValidationError("stateless and new session bindings must not include identities")
		}
		return nil
	case SessionBindingModeExactResume:
		if request.Expected == nil {
			return NewValidationError("exact resume session binding requires an expected identity")
		}
		if request.Parent != nil {
			return NewValidationError("exact resume session binding must not include a parent identity")
		}
		return request.Expected.Validate()
	case SessionBindingModeFork:
		if request.Parent == nil {
			return NewValidationError("fork session binding requires a parent identity")
		}
		if request.Expected != nil {
			return NewValidationError("fork session binding must not include an expected identity")
		}
		return request.Parent.Validate()
	default:
		return NewValidationError("session binding mode is unknown")
	}
}

// SessionForker is an optional executor capability for forking a session.
type SessionForker interface {
	ForkSession(context.Context, SessionIdentity, SpawnArgs) (Session, error)
}
