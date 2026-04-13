package policies

import (
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/guidance"
)

// ConsensusPolicyInput carries context from the consensus handler to the policy.
type ConsensusPolicyInput struct {
	// Synthesize indicates whether the caller requested a synthesis pass.
	Synthesize bool
	// Turns is the number of opinion turns collected so far.
	Turns int
	// Status is the raw status string from the strategy result ("completed", "running", etc.).
	Status string
}

// ConsensusPolicy computes next-step guidance for the consensus tool.
type ConsensusPolicy struct{}

// NewConsensusPolicy creates a ConsensusPolicy.
func NewConsensusPolicy() *ConsensusPolicy {
	return &ConsensusPolicy{}
}

// ToolName implements guidance.ToolPolicy.
func (p *ConsensusPolicy) ToolName() string { return "consensus" }

// BuildPlan implements guidance.ToolPolicy.
// StateSnapshot must be a *ConsensusPolicyInput. Falls back to a safe default on nil or wrong type.
func (p *ConsensusPolicy) BuildPlan(input guidance.PolicyInput) (guidance.NextActionPlan, error) {
	cpi := extractConsensusInput(input.StateSnapshot)
	return buildConsensusPlan(cpi), nil
}

func extractConsensusInput(snapshot any) ConsensusPolicyInput {
	if snapshot == nil {
		return ConsensusPolicyInput{}
	}
	if cpi, ok := snapshot.(*ConsensusPolicyInput); ok && cpi != nil {
		return *cpi
	}
	if cpi, ok := snapshot.(ConsensusPolicyInput); ok {
		return cpi
	}
	return ConsensusPolicyInput{}
}

func buildConsensusPlan(input ConsensusPolicyInput) guidance.NextActionPlan {
	state := consensusState(input)
	return guidance.NextActionPlan{
		State:      state,
		YouAreHere: consensusYouAreHere(input, state),
		ChooseYourPath: map[string]guidance.PathBranch{
			guidance.BranchSelf: {
				When:     "Synthesize the opinions into a unified recommendation.",
				NextCall: `consensus(topic="<same_topic>", synthesize=true)`,
				Example:  `consensus(topic="Which approach is safer?", synthesize=true)`,
				Then:     "Read the synthesis section to extract the final recommendation.",
			},
		},
		StopConditions: "synthesize=true has been called and the synthesis section is present in the result",
		DoNot: []string{
			"Do not treat consensus as a single model opinion — each participant may reason differently.",
			"Do not skip synthesis if you need a definitive recommendation; raw opinions require your own judgment to reconcile.",
		},
	}
}

func consensusState(input ConsensusPolicyInput) string {
	status := strings.TrimSpace(strings.ToLower(input.Status))
	switch {
	case status == "running":
		return "polling"
	case input.Synthesize:
		return "complete"
	case status == "completed" && input.Turns > 0:
		return "synthesizing"
	default:
		return "polling"
	}
}

func consensusYouAreHere(input ConsensusPolicyInput, state string) string {
	switch state {
	case "polling":
		return fmt.Sprintf("Collected %d opinion turn(s). Opinions are ready — call consensus(synthesize=true) to merge them into a single recommendation.", input.Turns)
	case "synthesizing":
		return fmt.Sprintf("All %d opinion turns collected and synthesis was requested. Synthesis section should be present in the result.", input.Turns)
	case "complete":
		return "Consensus synthesis is complete. Use the synthesis section as the canonical recommendation."
	default:
		return "Consensus is in progress."
	}
}
