package policies

import (
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/guidance"
	inv "github.com/thebtf/aimux/pkg/investigate"
)

type InvestigatePolicy struct{}

func NewInvestigatePolicy() *InvestigatePolicy {
	return &InvestigatePolicy{}
}

func (p *InvestigatePolicy) ToolName() string {
	return "investigate"
}

func (p *InvestigatePolicy) BuildPlan(input guidance.PolicyInput) (guidance.NextActionPlan, error) {
	state, ok := input.StateSnapshot.(*inv.InvestigationState)
	if !ok || state == nil {
		return guidance.NextActionPlan{}, fmt.Errorf("investigate policy requires *investigate.InvestigationState")
	}

	gaps := uncheckedCoverageAreas(state)
	plan := guidance.NextActionPlan{
		State:            "notebook_ready",
		YouAreHere:       fmt.Sprintf("Iteration %d. %d findings. %d/%d areas covered.", state.Iteration, len(state.Findings), len(state.CoverageAreas)-len(gaps), len(state.CoverageAreas)),
		HowThisToolWorks: "This is a scratchpad for YOUR investigation. It does not research anything itself.",
		ChooseYourPath: map[string]guidance.PathBranch{
			guidance.BranchSelf: {
				When:     "Use this when you want to drive the investigation manually.",
				NextCall: `investigate(action="finding", session_id="<session_id>", description="...", source="...", severity="P2")`,
				Example:  `investigate(action="finding", session_id="<session_id>", description="Observed nil dereference in init()", source="main.go:42", severity="P0")`,
				Then:     "Add more findings, then call assess to check convergence and coverage.",
			},
			guidance.BranchDelegate: {
				When:     "Use this when you want a delegate to run the investigation loop for you.",
				NextCall: `investigate(action="auto", topic="<topic>")`,
				Example:  `investigate(action="auto", topic="server crash on startup")`,
				Then:     "Poll the delegated job until it completes, then inspect the resulting report.",
			},
		},
		Gaps:           gaps,
		StopConditions: "convergence >= 1.0 AND coverage >= 80%",
		DoNot: []string{
			"Do not assume this tool researches in the background.",
			"Do not treat start as completion — you must add findings or delegate the investigation.",
		},
	}

	if strings.TrimSpace(input.Action) != "start" {
		plan.HowThisToolWorks = ""
	}

	return plan, nil
}

func uncheckedCoverageAreas(state *inv.InvestigationState) []string {
	if state == nil {
		return nil
	}

	gaps := make([]string, 0)
	for _, area := range state.CoverageAreas {
		if !state.CoverageChecked[area] {
			gaps = append(gaps, area)
		}
	}
	return gaps
}
