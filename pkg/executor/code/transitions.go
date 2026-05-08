package code

import (
	"fmt"

	"github.com/thebtf/aimux/pkg/executor/types"
)

const MetadataTransitionsKey = "code_fsm_transitions"

// Transition is the serializable audit record stored in Loom Task.Metadata.
type Transition struct {
	From   State  `json:"from"`
	To     State  `json:"to"`
	Reason string `json:"reason,omitempty"`
	Round  int    `json:"round"`
}

// ValidateTransition rejects unknown, terminal-origin, and illegal FSM moves.
func ValidateTransition(from, to State, rounds, maxRounds int) *types.CLIError {
	if !IsKnownState(from) {
		return types.NewUserInputError(fmt.Sprintf("unknown code FSM source state %q", from), nil)
	}
	if !IsKnownState(to) {
		return types.NewUserInputError(fmt.Sprintf("unknown code FSM target state %q", to), nil)
	}
	if rounds < 0 {
		return types.NewUserInputError("code FSM rounds must be non-negative", nil)
	}
	if maxRounds < 0 {
		return types.NewUserInputError("code FSM max rounds must be non-negative", nil)
	}
	if IsTerminalState(from) {
		return types.NewUserInputError(fmt.Sprintf("terminal code FSM state %s cannot transition", from), nil)
	}
	if !isAllowedTransition(from, to) {
		return types.NewUserInputError(fmt.Sprintf("illegal code FSM transition %s -> %s", from, to), nil)
	}
	return nil
}

// RecordTransition returns a copy of metadata with transition appended to the FSM log.
func RecordTransition(metadata map[string]any, transition Transition) map[string]any {
	next := cloneMetadata(metadata)
	log := appendTransitionLog(next[MetadataTransitionsKey], transition)
	next[MetadataTransitionsKey] = log
	return next
}

func isAllowedTransition(from, to State) bool {
	switch from {
	case StatePrep:
		return to == StateDriver
	case StateDriver:
		return to == StateNavigator
	case StateNavigator:
		return to == StateApply || to == StateRevise || to == StateRetry || to == StateEscalate
	case StateApply, StateRevise:
		return to == StateGate
	case StateRetry:
		return to == StateDriver
	case StateGate:
		return to == StateDone || to == StateError
	default:
		return false
	}
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	cp := make(map[string]any, len(metadata))
	for key, value := range metadata {
		if key == MetadataTransitionsKey {
			cp[key] = cloneTransitionLog(value)
			continue
		}
		cp[key] = value
	}
	return cp
}

func appendTransitionLog(existing any, transition Transition) []any {
	log := cloneTransitionLog(existing)
	return append(log, map[string]any{
		"from":   string(transition.From),
		"to":     string(transition.To),
		"reason": transition.Reason,
		"round":  transition.Round,
	})
}

func cloneTransitionLog(existing any) []any {
	switch entries := existing.(type) {
	case nil:
		return []any{}
	case []any:
		cp := make([]any, 0, len(entries))
		for _, entry := range entries {
			if entryMap, ok := entry.(map[string]any); ok {
				cp = append(cp, cloneMetadata(entryMap))
				continue
			}
			cp = append(cp, entry)
		}
		return cp
	case []map[string]any:
		cp := make([]any, 0, len(entries))
		for _, entry := range entries {
			cp = append(cp, cloneMetadata(entry))
		}
		return cp
	case []Transition:
		cp := make([]any, 0, len(entries))
		for _, entry := range entries {
			cp = append(cp, map[string]any{
				"from":   string(entry.From),
				"to":     string(entry.To),
				"reason": entry.Reason,
				"round":  entry.Round,
			})
		}
		return cp
	default:
		return []any{}
	}
}
