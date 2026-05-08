// Package code implements the Strong-Style pair-coding executor.
package code

import (
	"fmt"

	"github.com/thebtf/aimux/pkg/executor/types"
)

// State is a node in the code executor finite-state machine.
type State string

const (
	StatePrep      State = "PREP"
	StateDriver    State = "DRIVER"
	StateNavigator State = "NAVIGATOR"
	StateApply     State = "APPLY"
	StateRevise    State = "REVISE"
	StateRetry     State = "RETRY"
	StateEscalate  State = "ESCALATE"
	StateGate      State = "GATE"
	StateDone      State = "DONE"
	StateError     State = "ERROR"
)

const DefaultMaxRounds = 3

// Config controls Machine initialization.
type Config struct {
	InitialState  State
	InitialRounds int
	MaxRounds     int
	Metadata      map[string]any
}

// Machine tracks pair-coding state, retry rounds, and Loom metadata updates.
type Machine struct {
	state     State
	rounds    int
	maxRounds int
	metadata  map[string]any
}

// NewMachine creates a code FSM. Zero-value Config starts at PREP with default max rounds.
func NewMachine(cfg Config) (*Machine, *types.CLIError) {
	state := cfg.InitialState
	if state == "" {
		state = StatePrep
	}
	if !IsKnownState(state) {
		return nil, types.NewUserInputError(fmt.Sprintf("unknown code FSM state %q", state), nil)
	}
	maxRounds := cfg.MaxRounds
	if maxRounds == 0 {
		maxRounds = DefaultMaxRounds
	}
	if maxRounds < 0 {
		return nil, types.NewUserInputError("code FSM max rounds must be non-negative", nil)
	}
	if cfg.InitialRounds < 0 {
		return nil, types.NewUserInputError("code FSM initial rounds must be non-negative", nil)
	}
	if cfg.InitialRounds > maxRounds {
		return nil, types.NewUserInputError("code FSM initial rounds exceed max rounds", nil)
	}

	return &Machine{
		state:     state,
		rounds:    cfg.InitialRounds,
		maxRounds: maxRounds,
		metadata:  cloneMetadata(cfg.Metadata),
	}, nil
}

// State returns the current FSM state.
func (m *Machine) State() State {
	if m == nil {
		return ""
	}
	return m.state
}

// Rounds returns completed retry-to-driver loops.
func (m *Machine) Rounds() int {
	if m == nil {
		return 0
	}
	return m.rounds
}

// MaxRounds returns the configured retry loop limit.
func (m *Machine) MaxRounds() int {
	if m == nil {
		return 0
	}
	return m.maxRounds
}

// Metadata returns a defensive copy of the Loom metadata carried by this machine.
func (m *Machine) Metadata() map[string]any {
	if m == nil {
		return nil
	}
	return cloneMetadata(m.metadata)
}

// Advance validates and applies a transition. If a RETRY decision would exceed
// MaxRounds, the machine moves to ESCALATE and returns a CapabilityMismatch error
// describing the terminal outcome.
func (m *Machine) Advance(target State, reason string) *types.CLIError {
	if m == nil {
		return types.NewUnknown("code FSM machine is nil", nil)
	}

	actualTarget := target
	maxRoundErr := false
	if m.state == StateNavigator && target == StateRetry && m.rounds >= m.maxRounds {
		actualTarget = StateEscalate
		maxRoundErr = true
		if reason == "" {
			reason = "max rounds reached"
		}
	}

	if err := ValidateTransition(m.state, actualTarget, m.rounds, m.maxRounds); err != nil {
		return err
	}

	from := m.state
	nextRounds := m.rounds
	if from == StateRetry && actualTarget == StateDriver {
		nextRounds++
	}

	transition := Transition{
		From:   from,
		To:     actualTarget,
		Reason: reason,
		Round:  nextRounds,
	}
	m.state = actualTarget
	m.rounds = nextRounds
	m.metadata = RecordTransition(m.metadata, transition)

	if maxRoundErr {
		return types.NewCapabilityMismatch("code FSM max rounds reached", nil)
	}
	return nil
}

// IsKnownState reports whether s is part of the code FSM state vocabulary.
func IsKnownState(s State) bool {
	switch s {
	case StatePrep, StateDriver, StateNavigator, StateApply, StateRevise, StateRetry, StateEscalate, StateGate, StateDone, StateError:
		return true
	default:
		return false
	}
}

// IsTerminalState reports whether s ends FSM execution.
func IsTerminalState(s State) bool {
	switch s {
	case StateEscalate, StateDone, StateError:
		return true
	default:
		return false
	}
}

// IsActiveState reports whether s is a non-terminal FSM state.
func IsActiveState(s State) bool {
	return IsKnownState(s) && !IsTerminalState(s)
}
