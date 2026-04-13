package policies

import (
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/guidance"
)

// DebatePolicyInput carries context from the debate handler to the policy.
type DebatePolicyInput struct {
	// Turns is the number of rebuttal turns completed so far.
	Turns int
	// MaxTurns is the configured turn limit (0 = use default).
	MaxTurns int
	// Synthesize indicates whether a verdict synthesis was requested.
	Synthesize bool
	// Status is the raw status string from the strategy result.
	Status string
}

// DebatePolicy computes next-step guidance for the debate tool.
type DebatePolicy struct{}

// NewDebatePolicy creates a DebatePolicy.
func NewDebatePolicy() *DebatePolicy {
	return &DebatePolicy{}
}

// ToolName implements guidance.ToolPolicy.
func (p *DebatePolicy) ToolName() string { return "debate" }

// BuildPlan implements guidance.ToolPolicy.
// StateSnapshot must be a *DebatePolicyInput. Falls back to a safe default on nil or wrong type.
func (p *DebatePolicy) BuildPlan(input guidance.PolicyInput) (guidance.NextActionPlan, error) {
	dpi := extractDebateInput(input.StateSnapshot)
	return buildDebatePlan(dpi), nil
}

func extractDebateInput(snapshot any) DebatePolicyInput {
	return extractInput[DebatePolicyInput](snapshot)
}

func buildDebatePlan(input DebatePolicyInput) guidance.NextActionPlan {
	state := debateState(input)
	return guidance.NextActionPlan{
		State:      state,
		YouAreHere: debateYouAreHere(input, state),
		ChooseYourPath: map[string]guidance.PathBranch{
			"continue": {
				When:     "More rebuttal turns are available and the disagreement is unresolved.",
				NextCall: `debate(topic="<same_topic>", max_turns=<N+2>)`,
				Example:  `debate(topic="Is approach A safer than B?", max_turns=8)`,
				Then:     "Review whether positions converged before requesting a verdict.",
			},
			"verdict": {
				When:     "Rebuttals are exhausted or the disagreement is sufficiently surfaced.",
				NextCall: `debate(topic="<same_topic>", synthesize=true)`,
				Example:  `debate(topic="Is approach A safer than B?", synthesize=true)`,
				Then:     "Read the verdict section for the synthesized outcome.",
			},
		},
		StopConditions: "synthesize=true returned and a verdict section is present in the result",
		DoNot: []string{
			"Do not treat debate as agreement — its purpose is to surface disagreements, not eliminate them.",
			"Do not interpret the absence of consensus as a failure; divergent positions are valid outcomes.",
		},
	}
}

func debateState(input DebatePolicyInput) string {
	status := strings.TrimSpace(strings.ToLower(input.Status))
	switch {
	case status == "running":
		return "opening"
	case input.Synthesize && status == "completed":
		return "verdict"
	case input.Turns == 0:
		return "opening"
	default:
		return fmt.Sprintf("rebuttal_%d", input.Turns)
	}
}

func debateYouAreHere(input DebatePolicyInput, state string) string {
	switch state {
	case "opening":
		return "Opening arguments are being collected. No rebuttal turns have been recorded yet."
	case "verdict":
		return fmt.Sprintf("Verdict reached after %d turn(s). Read the verdict section for the synthesized outcome.", input.Turns)
	default:
		maxNote := ""
		if input.MaxTurns > 0 {
			maxNote = fmt.Sprintf(" of %d", input.MaxTurns)
		}
		return fmt.Sprintf("Rebuttal turn %d%s completed. Positions are active — continue or request a verdict.", input.Turns, maxNote)
	}
}
