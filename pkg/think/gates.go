package think

import "fmt"

// GateDecision is the result of an enforcement gate check.
type GateDecision struct {
	Status string // "complete" or "incomplete"
	Reason string
}

// patternGateConfig holds threshold parameters for a single pattern.
type patternGateConfig struct {
	// minSteps is the minimum number of state entries expected (0 = not checked).
	minSteps int
	// minEvidence is the minimum evidence count (0 = not checked).
	minEvidence int
	// requiredStages lists stages that must all appear in stageHistory (nil = not checked).
	requiredStages []string
	// requireAllCriteriaScored indicates every criterion must have a numeric score.
	requireAllCriteriaScored bool
	// minSources is the minimum number of distinct sources (0 = not checked).
	minSources int
	// minIterations is the minimum iteration count (0 = not checked).
	minIterations int
	// convergenceKey is the state key to check for convergence signal (empty = not checked).
	convergenceKey string
}

// gateConfigs maps pattern name to its enforcement config.
var gateConfigs = map[string]patternGateConfig{
	"debugging_approach": {
		minSteps:    3,
		minEvidence: 2,
	},
	"sequential_thinking": {
		convergenceKey: "convergence",
		minSteps:       1,
	},
	"scientific_method": {
		requiredStages: []string{
			"observation",
			"hypothesis",
			"prediction",
			"experiment",
			"analysis",
		},
	},
	"decision_framework": {
		requireAllCriteriaScored: true,
	},
	"source_comparison": {
		minSources: 3,
	},
	"structured_argumentation": {
		minSteps: 1,
	},
	"collaborative_reasoning": {
		minSteps: 2,
	},
	"experimental_loop": {
		minIterations: 1,
	},
	"literature_review": {
		minSources: 2,
	},
	"peer_review": {
		minSteps: 1,
	},
}

// EnforcementGate checks whether a session meets the completion thresholds for
// its current pattern.
type EnforcementGate struct{}

// NewEnforcementGate returns a new EnforcementGate.
func NewEnforcementGate() *EnforcementGate {
	return &EnforcementGate{}
}

// Check evaluates session state against the pattern's configured thresholds.
// Returns GateDecision{Status:"complete"} when all thresholds are met,
// GateDecision{Status:"incomplete", Reason: "..."} otherwise.
// Patterns without a config entry are always "complete".
func (g *EnforcementGate) Check(patternName string, session *ThinkSession) GateDecision {
	cfg, ok := gateConfigs[patternName]
	if !ok {
		return GateDecision{Status: "complete"}
	}

	if session == nil {
		return GateDecision{Status: "incomplete", Reason: "no active session"}
	}

	state := session.State

	// --- minSteps ---
	if cfg.minSteps > 0 {
		steps := countStateEntries(state, "steps", "thoughts", "entries", "hypotheses")
		if steps < cfg.minSteps {
			return GateDecision{
				Status: "incomplete",
				Reason: formatReason("need at least %d steps, have %d", cfg.minSteps, steps),
			}
		}
	}

	// --- minEvidence ---
	if cfg.minEvidence > 0 {
		ev := countStateEntries(state, "evidence", "findings", "observations")
		if ev < cfg.minEvidence {
			return GateDecision{
				Status: "incomplete",
				Reason: formatReason("need at least %d evidence items, have %d", cfg.minEvidence, ev),
			}
		}
	}

	// --- requiredStages ---
	if len(cfg.requiredStages) > 0 {
		history := stageHistorySet(state)
		missing := missingStages(cfg.requiredStages, history)
		if len(missing) > 0 {
			return GateDecision{
				Status: "incomplete",
				Reason: formatReason("missing required stages: %v", missing),
			}
		}
	}

	// --- requireAllCriteriaScored ---
	if cfg.requireAllCriteriaScored {
		if reason, ok := allCriteriaScored(state); !ok {
			return GateDecision{Status: "incomplete", Reason: reason}
		}
	}

	// --- minSources ---
	if cfg.minSources > 0 {
		src := countStateEntries(state, "sources", "references")
		if src < cfg.minSources {
			return GateDecision{
				Status: "incomplete",
				Reason: formatReason("need at least %d sources, have %d", cfg.minSources, src),
			}
		}
	}

	// --- minIterations ---
	if cfg.minIterations > 0 {
		it := countIterations(state)
		if it < cfg.minIterations {
			return GateDecision{
				Status: "incomplete",
				Reason: formatReason("need at least %d iterations, have %d", cfg.minIterations, it),
			}
		}
	}

	// --- convergenceKey ---
	if cfg.convergenceKey != "" {
		if !converged(state, cfg.convergenceKey) {
			return GateDecision{Status: "incomplete", Reason: "convergence not yet reached"}
		}
	}

	return GateDecision{Status: "complete"}
}

// --- helpers ---

// countStateEntries sums the lengths of any slice-typed values found under the
// given keys. Returns the maximum single-key count so callers get the most
// relevant field rather than a cross-key sum.
func countStateEntries(state map[string]any, keys ...string) int {
	max := 0
	for _, key := range keys {
		v, ok := state[key]
		if !ok {
			continue
		}
		switch val := v.(type) {
		case []any:
			if len(val) > max {
				max = len(val)
			}
		case int:
			if val > max {
				max = val
			}
		case float64:
			if int(val) > max {
				max = int(val)
			}
		}
	}
	return max
}

// stageHistorySet extracts the set of stage names from "stageHistory" in state.
func stageHistorySet(state map[string]any) map[string]bool {
	history := make(map[string]bool)
	v, ok := state["stageHistory"]
	if !ok {
		return history
	}
	slice, ok := v.([]any)
	if !ok {
		return history
	}
	for _, item := range slice {
		if s, ok := item.(string); ok {
			history[s] = true
		}
	}
	return history
}

// missingStages returns required stages absent from the set.
func missingStages(required []string, present map[string]bool) []string {
	var missing []string
	for _, s := range required {
		if !present[s] {
			missing = append(missing, s)
		}
	}
	return missing
}

// allCriteriaScored checks that every criterion in state["criteria"] has a
// numeric score. Returns (reason, false) if any are unscored.
func allCriteriaScored(state map[string]any) (string, bool) {
	v, ok := state["criteria"]
	if !ok {
		return "no criteria found", false
	}
	criteria, ok := v.([]any)
	if !ok {
		return "criteria is not a list", false
	}
	if len(criteria) == 0 {
		return "criteria list is empty", false
	}
	for i, c := range criteria {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if _, hasScore := m["score"]; !hasScore {
			return formatReason("criterion %d is missing a score", i), false
		}
	}
	return "", true
}

// countIterations reads the iteration count from state["iteration"] or
// falls back to counting entries in "iterations" slice.
func countIterations(state map[string]any) int {
	if v, ok := state["iteration"]; ok {
		switch n := v.(type) {
		case int:
			return n
		case float64:
			return int(n)
		}
	}
	if v, ok := state["iterations"]; ok {
		if sl, ok := v.([]any); ok {
			return len(sl)
		}
	}
	return 0
}

// converged returns true if state[key] is a truthy boolean or the string "true".
func converged(state map[string]any, key string) bool {
	v, ok := state[key]
	if !ok {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "true"
	}
	return false
}

// formatReason delegates to fmt.Sprintf so callers don't need an extra import.
func formatReason(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
