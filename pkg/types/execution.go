package types

import "strings"

// ExecutionID identifies one execution attempt independently of provider and
// transport session identities.
type ExecutionID string

// Validate rejects an absent execution identity.
func (id ExecutionID) Validate() error {
	if strings.TrimSpace(string(id)) == "" {
		return NewValidationError("execution ID must not be blank")
	}
	return nil
}

// ExecutorEvent is a provider-neutral event emitted during an execution.
type ExecutorEvent struct {
	Channel   string `json:"channel"`
	Type      string `json:"type"`
	Content   []byte `json:"content,omitempty"`
	Terminal  bool   `json:"terminal"`
	Truncated bool   `json:"truncated"`
}

// ExecutorEventSink is the bounded, non-blocking admission boundary used by
// native event executors. Implementations MUST be safe for concurrent stdout
// and stderr calls and MUST return without waiting on durable I/O.
type ExecutorEventSink interface {
	TryAdmit(ExecutorEvent) bool
}

// ExecutorEventSinkFunc adapts a function to ExecutorEventSink.
type ExecutorEventSinkFunc func(ExecutorEvent) bool

func (fn ExecutorEventSinkFunc) TryAdmit(event ExecutorEvent) bool {
	if fn == nil {
		return false
	}
	return fn(event)
}

// ProcessIdentity identifies an OS process generation and its owned process
// tree without relying on a reusable PID alone.
type ProcessIdentity struct {
	PID              int    `json:"pid"`
	StartFingerprint string `json:"start_fingerprint"`
	TreeID           string `json:"tree_id"`
}

// Validate rejects incomplete or ambiguous process identities.
func (identity ProcessIdentity) Validate() error {
	if identity.PID <= 0 {
		return NewValidationError("process PID must be positive")
	}
	if strings.TrimSpace(identity.StartFingerprint) == "" {
		return NewValidationError("process start fingerprint must not be blank")
	}
	if strings.TrimSpace(identity.TreeID) == "" {
		return NewValidationError("process tree ID must not be blank")
	}
	return nil
}

// SessionIdentity identifies one exact provider-neutral session generation.
type SessionIdentity struct {
	Provider   string `json:"provider"`
	ID         string `json:"id"`
	Generation uint64 `json:"generation"`
}

// Validate rejects incomplete or unfenced session identities.
func (identity SessionIdentity) Validate() error {
	if strings.TrimSpace(identity.Provider) == "" {
		return NewValidationError("session provider must not be blank")
	}
	if strings.TrimSpace(identity.ID) == "" {
		return NewValidationError("session ID must not be blank")
	}
	if identity.Generation == 0 {
		return NewValidationError("session generation must be greater than zero")
	}
	return nil
}

// CancellationEvidence records whether a provider-native cancellation was
// acknowledged for an execution. A false acknowledgement remains truthful
// evidence and must not be promoted to confirmed cancellation.
type CancellationEvidence struct {
	ExecutionID        ExecutionID `json:"execution_id"`
	NativeAcknowledged bool        `json:"native_acknowledged"`
}

// Validate verifies the execution correlation carried by the evidence.
func (evidence CancellationEvidence) Validate() error {
	return evidence.ExecutionID.Validate()
}

// ProcessTreeEvidence records observed stop state for one exact process
// generation. Stopped=false is valid unconfirmed evidence.
type ProcessTreeEvidence struct {
	Process ProcessIdentity `json:"process"`
	Stopped bool            `json:"stopped"`
}

// Validate verifies the process generation carried by the evidence.
func (evidence ProcessTreeEvidence) Validate() error {
	return evidence.Process.Validate()
}
