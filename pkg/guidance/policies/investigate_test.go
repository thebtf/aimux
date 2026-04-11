package policies_test

import (
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/guidance"
	"github.com/thebtf/aimux/pkg/guidance/policies"
	inv "github.com/thebtf/aimux/pkg/investigate"
)

func TestInvestigatePolicy_StartBuildsNotebookReadyGuidance(t *testing.T) {
	policy := policies.NewInvestigatePolicy()
	state := &inv.InvestigationState{
		Topic:              "root cause",
		Domain:             "debugging",
		Iteration:          0,
		Findings:           nil,
		Corrections:        nil,
		CoverageAreas:      []string{"assumptions", "claims", "alternatives", "blind_spots", "ranking"},
		CoverageChecked:    map[string]bool{},
		ConvergenceHistory: nil,
		CreatedAt:          time.Now(),
		LastActivityAt:     time.Now(),
	}

	plan, err := policy.BuildPlan(guidance.PolicyInput{
		Action:        "start",
		StateSnapshot: state,
		RawResult: map[string]any{
			"session_id":     "s1",
			"iteration":      0,
			"findings_count": 0,
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan(start): %v", err)
	}
	if plan.State != "notebook_ready" {
		t.Fatalf("State = %q, want notebook_ready", plan.State)
	}
	if plan.HowThisToolWorks == "" {
		t.Fatal("HowThisToolWorks should be populated for start")
	}
	if len(plan.DoNot) == 0 {
		t.Fatal("DoNot should contain at least one anti-pattern")
	}
	if plan.ChooseYourPath == nil {
		t.Fatal("ChooseYourPath should be populated")
	}
	if _, ok := plan.ChooseYourPath[guidance.BranchSelf]; !ok {
		t.Fatal("self branch missing")
	}
	if _, ok := plan.ChooseYourPath[guidance.BranchDelegate]; !ok {
		t.Fatal("delegate branch missing")
	}
	if plan.ChooseYourPath[guidance.BranchSelf].NextCall == "" {
		t.Fatal("self.next_call should be populated")
	}
	if plan.ChooseYourPath[guidance.BranchDelegate].NextCall == "" {
		t.Fatal("delegate.next_call should be populated")
	}
}

func TestInvestigatePolicy_StartIncludesCoverageGapsAndStopConditions(t *testing.T) {
	policy := policies.NewInvestigatePolicy()
	state := &inv.InvestigationState{
		Topic:           "root cause",
		Domain:          "debugging",
		Iteration:       0,
		CoverageAreas:   []string{"assumptions", "claims"},
		CoverageChecked: map[string]bool{"claims": false},
		CreatedAt:       time.Now(),
		LastActivityAt:  time.Now(),
	}

	plan, err := policy.BuildPlan(guidance.PolicyInput{Action: "start", StateSnapshot: state})
	if err != nil {
		t.Fatalf("BuildPlan(start): %v", err)
	}
	if len(plan.Gaps) == 0 {
		t.Fatal("Gaps should be populated")
	}
	if plan.StopConditions == "" {
		t.Fatal("StopConditions should be populated")
	}
	if plan.YouAreHere == "" {
		t.Fatal("YouAreHere should be populated")
	}
}
