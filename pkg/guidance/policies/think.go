package policies

import (
	"fmt"

	"github.com/thebtf/aimux/pkg/guidance"
)

// statefulPatterns is the authoritative set of think patterns that maintain session state
// across multiple calls. All other patterns are one-shot.
var statefulPatterns = map[string]bool{
	"sequential_thinking":     true,
	"scientific_method":       true,
	"debugging_approach":      true,
	"experimental_loop":       true,
	"structured_argumentation": true,
	"collaborative_reasoning": true,
}

// ThinkPolicyInput carries context from the think handler to the policy.
type ThinkPolicyInput struct {
	// Pattern is the think pattern name (e.g. "critical_thinking", "sequential_thinking").
	Pattern string
	// SessionID is non-empty when a stateful session is active or was just created.
	SessionID string
	// IsStateful indicates whether Pattern is in the stateful set.
	IsStateful bool
	// StepNumber is the current step for sequential/stateful patterns (0 = initial).
	StepNumber int
}

// ThinkPolicy computes next-step guidance for the think tool.
type ThinkPolicy struct{}

// NewThinkPolicy creates a ThinkPolicy.
func NewThinkPolicy() *ThinkPolicy {
	return &ThinkPolicy{}
}

// ToolName implements guidance.ToolPolicy.
func (p *ThinkPolicy) ToolName() string {
	return "think"
}

// BuildPlan implements guidance.ToolPolicy via the shared PolicyInput contract.
// StateSnapshot must be a *ThinkPolicyInput. If it is nil or the wrong type,
// the plan falls back to a one-shot complete state so callers are never blocked.
func (p *ThinkPolicy) BuildPlan(input guidance.PolicyInput) (guidance.NextActionPlan, error) {
	tpi := extractThinkInput(input.StateSnapshot)
	return buildThinkPlan(tpi), nil
}

// BuildPlanTyped is the strongly-typed entry point used by callers that already
// have a ThinkPolicyInput and want to avoid an interface round-trip.
func (p *ThinkPolicy) BuildPlanTyped(input ThinkPolicyInput) *guidance.NextActionPlan {
	plan := buildThinkPlan(input)
	return &plan
}

// IsStatefulPattern reports whether the named pattern maintains session state.
func IsStatefulPattern(pattern string) bool {
	return statefulPatterns[pattern]
}

// extractThinkInput converts the opaque StateSnapshot to a ThinkPolicyInput.
// Returns a safe default if the snapshot is absent or has an unexpected type.
func extractThinkInput(snapshot any) ThinkPolicyInput {
	if snapshot == nil {
		return ThinkPolicyInput{}
	}
	if tpi, ok := snapshot.(*ThinkPolicyInput); ok && tpi != nil {
		return *tpi
	}
	if tpi, ok := snapshot.(ThinkPolicyInput); ok {
		return tpi
	}
	return ThinkPolicyInput{}
}

// buildThinkPlan is the pure policy function — no I/O, no side effects.
func buildThinkPlan(input ThinkPolicyInput) guidance.NextActionPlan {
	if !input.IsStateful || input.Pattern == "" {
		return oneShotPlan(input.Pattern)
	}
	return statefulPlan(input)
}

// oneShotPlan returns guidance for patterns that complete in a single call.
func oneShotPlan(pattern string) guidance.NextActionPlan {
	what := "one-shot pattern"
	if pattern != "" {
		what = fmt.Sprintf("%s (one-shot pattern)", pattern)
	}
	return guidance.NextActionPlan{
		State:      "complete",
		YouAreHere: fmt.Sprintf("Pattern %s completed. No session state is maintained.", what),
		HowThisToolWorks: "This pattern produces its full output in a single call. " +
			"Pass a different pattern name to start a new analysis. " +
			"Use a stateful pattern (sequential_thinking, scientific_method, debugging_approach, " +
			"experimental_loop, structured_argumentation, collaborative_reasoning) " +
			"when you need multi-step iteration with session continuity.",
		StopConditions: "Output is complete — no further calls needed for this analysis.",
		DoNot: []string{
			"Do not pass session_id to a one-shot pattern — it has no effect.",
			"Do not call this pattern again expecting incremental results; each call is independent.",
		},
	}
}

// statefulPlan returns guidance for multi-step patterns that carry session state.
func statefulPlan(input ThinkPolicyInput) guidance.NextActionPlan {
	state := deriveStatefulState(input)
	youAreHere := deriveYouAreHere(input, state)
	nextCall := deriveNextCall(input)
	then := deriveThen(input, state)

	return guidance.NextActionPlan{
		State:      state,
		YouAreHere: youAreHere,
		ChooseYourPath: map[string]guidance.PathBranch{
			guidance.BranchSelf: {
				When:     "Continue the current session to advance the reasoning chain.",
				NextCall: nextCall,
				Example:  nextCallExample(input),
				Then:     then,
			},
		},
		StopConditions: statefulStopCondition(input.Pattern),
		DoNot: []string{
			"Do not omit session_id on follow-up calls — you will start a new session instead of continuing.",
			"Do not skip steps; patterns enforce their lifecycle and will return an error for out-of-order entries.",
		},
	}
}

// deriveStatefulState maps the current step context to a descriptive state string.
func deriveStatefulState(input ThinkPolicyInput) string {
	switch input.Pattern {
	case "sequential_thinking":
		if input.StepNumber == 0 {
			return "session_started"
		}
		return fmt.Sprintf("step_%d", input.StepNumber)

	case "scientific_method":
		return "observation_phase"

	case "debugging_approach":
		if input.StepNumber == 0 {
			return "reproducing"
		}
		return "diagnosing"

	case "experimental_loop":
		return "loop_active"

	case "structured_argumentation":
		return "argument_forming"

	case "collaborative_reasoning":
		return "reasoning_in_progress"

	default:
		return "session_active"
	}
}

// deriveYouAreHere produces a human-readable progress description.
func deriveYouAreHere(input ThinkPolicyInput, state string) string {
	sessionHint := ""
	if input.SessionID != "" {
		sessionHint = fmt.Sprintf(" Session %s.", input.SessionID)
	}

	switch input.Pattern {
	case "sequential_thinking":
		if input.StepNumber == 0 {
			return fmt.Sprintf("Sequential thinking session started.%s Record the first thought to advance.", sessionHint)
		}
		return fmt.Sprintf("Sequential thinking step %d recorded.%s Continue adding thoughts until next_step_needed=false.", input.StepNumber, sessionHint)

	case "scientific_method":
		return fmt.Sprintf("Scientific method session active.%s Advance through observation → hypothesis → experiment → analysis → conclusion.", sessionHint)

	case "debugging_approach":
		if input.StepNumber == 0 {
			return fmt.Sprintf("Debugging session started.%s Reproduce the issue first before isolating the cause.", sessionHint)
		}
		return fmt.Sprintf("Debugging step %d reached.%s Continue narrowing the fault domain.", input.StepNumber, sessionHint)

	case "experimental_loop":
		return fmt.Sprintf("Experimental loop running.%s Each call adds one iteration; loop until convergence.", sessionHint)

	case "structured_argumentation":
		return fmt.Sprintf("Structured argumentation in progress.%s Add claims, rebuttals, and supporting evidence.", sessionHint)

	case "collaborative_reasoning":
		return fmt.Sprintf("Collaborative reasoning session active.%s Add contributions per persona until synthesis is ready.", sessionHint)

	default:
		return fmt.Sprintf("Stateful pattern %q — step %d.%s", input.Pattern, input.StepNumber, sessionHint)
	}
}

// deriveNextCall produces the canonical next-call template.
func deriveNextCall(input ThinkPolicyInput) string {
	if input.SessionID != "" {
		return fmt.Sprintf(`think(pattern=%q, session_id=%q, ...)`, input.Pattern, input.SessionID)
	}
	return fmt.Sprintf(`think(pattern=%q, session_id="<session_id>", ...)`, input.Pattern)
}

// nextCallExample produces a pattern-specific concrete example.
func nextCallExample(input ThinkPolicyInput) string {
	sid := input.SessionID
	if sid == "" {
		sid = "<session_id>"
	}
	switch input.Pattern {
	case "sequential_thinking":
		return fmt.Sprintf(`think(pattern="sequential_thinking", session_id=%q, thought="Next reasoning step", thoughtNumber=%d, totalThoughts=5, next_step_needed=true)`, sid, input.StepNumber+1)
	case "scientific_method":
		return fmt.Sprintf(`think(pattern="scientific_method", session_id=%q, entry_type="hypothesis", entry_text="Proposed explanation")`, sid)
	case "debugging_approach":
		return fmt.Sprintf(`think(pattern="debugging_approach", session_id=%q, observation="Observed symptom", question="Why does X happen?")`, sid)
	case "experimental_loop":
		return fmt.Sprintf(`think(pattern="experimental_loop", session_id=%q, hypothesis="Expected outcome", experiment="Test description", result="Observed result")`, sid)
	case "structured_argumentation":
		return fmt.Sprintf(`think(pattern="structured_argumentation", session_id=%q, argument_type="claim", argument_text="Core assertion")`, sid)
	case "collaborative_reasoning":
		return fmt.Sprintf(`think(pattern="collaborative_reasoning", session_id=%q, contribution_type="analysis", contribution_text="Persona perspective")`, sid)
	default:
		return fmt.Sprintf(`think(pattern=%q, session_id=%q)`, input.Pattern, sid)
	}
}

// deriveThen provides guidance on what to do after the next call.
func deriveThen(input ThinkPolicyInput, state string) string {
	switch input.Pattern {
	case "sequential_thinking":
		return "Keep adding thoughts with incrementing thoughtNumber. Set next_step_needed=false on the final thought to signal completion."
	case "scientific_method":
		return "Follow the lifecycle: observation → question → hypothesis → prediction → experiment → result → analysis → conclusion. Do not skip stages."
	case "debugging_approach":
		return "After reproducing, isolate variables one at a time. Once the fault is pinpointed, fix at root cause — not symptom."
	case "experimental_loop":
		return "Iterate until the hypothesis is confirmed or refuted. Record each result honestly to avoid confirmation bias."
	case "structured_argumentation":
		return "Add rebuttals and supporting evidence to strengthen or rebut the claim. Seek the strongest opposing argument before concluding."
	case "collaborative_reasoning":
		return "Cycle through each persona's perspective before synthesizing a unified conclusion."
	default:
		return "Continue the session with the same pattern and session_id until the analysis is complete."
	}
}

// statefulStopCondition returns the completion criterion for a pattern.
func statefulStopCondition(pattern string) string {
	switch pattern {
	case "sequential_thinking":
		return "next_step_needed=false on the final thought"
	case "scientific_method":
		return "conclusion stage reached with evidence from all prior stages"
	case "debugging_approach":
		return "root cause identified and fix verified"
	case "experimental_loop":
		return "hypothesis confirmed or refuted with supporting results"
	case "structured_argumentation":
		return "all major rebuttals addressed and a verdict recorded"
	case "collaborative_reasoning":
		return "all personas contributed and a synthesis is produced"
	default:
		return "pattern-specific completion signal returned"
	}
}
