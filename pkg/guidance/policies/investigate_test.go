package policies_test

import (
	"strings"
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
	if plan.ChooseYourPath[guidance.BranchSelf].NextCall == "" {
		t.Fatal("self.next_call should be populated")
	}
	if _, ok := plan.ChooseYourPath[guidance.BranchDelegate]; ok {
		t.Fatal("delegate branch should not be exposed before action=auto exists")
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

func TestInvestigatePolicy_FindingKeepsRemainingCoverageGaps(t *testing.T) {
	policy := policies.NewInvestigatePolicy()
	state := &inv.InvestigationState{
		Topic:         "root cause",
		Domain:        "debugging",
		Iteration:     1,
		Findings:      []inv.Finding{{ID: "f1", CoverageArea: "assumptions"}},
		CoverageAreas: []string{"assumptions", "claims", "alternatives"},
		CoverageChecked: map[string]bool{
			"assumptions":  true,
			"claims":       false,
			"alternatives": false,
		},
		CreatedAt:      time.Now(),
		LastActivityAt: time.Now(),
	}

	plan, err := policy.BuildPlan(guidance.PolicyInput{Action: "finding", StateSnapshot: state})
	if err != nil {
		t.Fatalf("BuildPlan(finding): %v", err)
	}
	if len(plan.Gaps) != 2 {
		t.Fatalf("Gaps len = %d, want 2", len(plan.Gaps))
	}
	if plan.Gaps[0] != "claims" || plan.Gaps[1] != "alternatives" {
		t.Fatalf("Gaps = %#v, want [claims alternatives]", plan.Gaps)
	}
	if plan.ChooseYourPath[guidance.BranchSelf].NextCall == "" {
		t.Fatal("self.next_call should stay populated for finding state")
	}
}

func TestInvestigatePolicy_WhenCoverageCompleteSwitchesToAssessOrReport(t *testing.T) {
	policy := policies.NewInvestigatePolicy()
	state := &inv.InvestigationState{
		Topic:     "root cause",
		Domain:    "debugging",
		Iteration: 2,
		Findings: []inv.Finding{
			{ID: "f1", CoverageArea: "assumptions"},
			{ID: "f2", CoverageArea: "claims"},
		},
		CoverageAreas: []string{"assumptions", "claims"},
		CoverageChecked: map[string]bool{
			"assumptions": true,
			"claims":      true,
		},
		CreatedAt:      time.Now(),
		LastActivityAt: time.Now(),
	}

	plan, err := policy.BuildPlan(guidance.PolicyInput{Action: "finding", StateSnapshot: state})
	if err != nil {
		t.Fatalf("BuildPlan(finding): %v", err)
	}
	if len(plan.Gaps) != 0 {
		t.Fatalf("Gaps = %#v, want empty", plan.Gaps)
	}
	selfNext := plan.ChooseYourPath[guidance.BranchSelf].NextCall
	if selfNext != `investigate(action="assess", session_id="<session_id>")` && selfNext != `investigate(action="report", session_id="<session_id>")` {
		t.Fatalf("self.next_call = %q, want assess/report path", selfNext)
	}
}

func TestInvestigatePolicy_ReportWithoutFindingsReturnsCorrectiveGuidance(t *testing.T) {
	policy := policies.NewInvestigatePolicy()
	state := &inv.InvestigationState{
		Topic:           "root cause",
		Domain:          "debugging",
		Iteration:       0,
		CoverageAreas:   []string{"reproduction", "isolation"},
		CoverageChecked: map[string]bool{},
		CreatedAt:       time.Now(),
		LastActivityAt:  time.Now(),
	}

	plan, err := policy.BuildPlan(guidance.PolicyInput{Action: "report", StateSnapshot: state})
	if err != nil {
		t.Fatalf("BuildPlan(report): %v", err)
	}
	if plan.State != "report_blocked" {
		t.Fatalf("State = %q, want report_blocked", plan.State)
	}
	if plan.ChooseYourPath == nil {
		t.Fatal("ChooseYourPath should be populated")
	}
	self := plan.ChooseYourPath[guidance.BranchSelf]
	if self.NextCall != `investigate(action="finding", session_id="<session_id>", description="...", source="...", severity="P2")` {
		t.Fatalf("self.next_call = %q, want finding path", self.NextCall)
	}
	if self.Example != self.NextCall {
		t.Fatalf("self.example = %q, want %q", self.Example, self.NextCall)
	}
	if len(plan.Gaps) == 0 {
		t.Fatal("Gaps should remain populated when no findings exist")
	}
}

func TestInvestigatePolicy_ReportWithWeakEvidenceMarksPreliminaryAndListsGaps(t *testing.T) {
	policy := policies.NewInvestigatePolicy()
	state := &inv.InvestigationState{
		Topic:     "root cause",
		Domain:    "debugging",
		Iteration: 1,
		Findings: []inv.Finding{
			{ID: "f1", CoverageArea: "reproduction", Iteration: 1},
		},
		Corrections: []inv.Correction{{OriginalID: "f0", Iteration: 1}},
		CoverageAreas: []string{"reproduction", "isolation"},
		CoverageChecked: map[string]bool{
			"reproduction": true,
			"isolation":    true,
		},
		CreatedAt:      time.Now(),
		LastActivityAt: time.Now(),
	}

	plan, err := policy.BuildPlan(guidance.PolicyInput{Action: "report", StateSnapshot: state})
	if err != nil {
		t.Fatalf("BuildPlan(report): %v", err)
	}
	if plan.State != "report_preliminary" {
		t.Fatalf("State = %q, want report_preliminary", plan.State)
	}
	if len(plan.Gaps) == 0 {
		t.Fatal("Gaps should include remaining evidence gaps for preliminary report")
	}
	if !containsString(plan.Gaps, "convergence < 1.0") {
		t.Fatalf("Gaps = %#v, want convergence < 1.0 gap", plan.Gaps)
	}
	self := plan.ChooseYourPath[guidance.BranchSelf]
	if self.NextCall != `investigate(action="assess", session_id="<session_id>")` {
		t.Fatalf("self.next_call = %q, want assess path", self.NextCall)
	}
	if self.Example != self.NextCall {
		t.Fatalf("self.example = %q, want %q", self.Example, self.NextCall)
	}
	if !strings.Contains(strings.ToLower(self.Then), "preliminary") {
		t.Fatalf("self.then = %q, want PRELIMINARY guidance", self.Then)
	}
}

func TestInvestigatePolicy_ReportForceTrueStillMarksWeakEvidenceIncomplete(t *testing.T) {
	policy := policies.NewInvestigatePolicy()
	state := &inv.InvestigationState{
		Topic:     "root cause",
		Domain:    "debugging",
		Iteration: 1,
		Findings: []inv.Finding{
			{ID: "f1", CoverageArea: "reproduction", Iteration: 1},
		},
		Corrections: []inv.Correction{{OriginalID: "f0", Iteration: 1}},
		CoverageAreas: []string{"reproduction", "isolation"},
		CoverageChecked: map[string]bool{
			"reproduction": true,
			"isolation":    true,
		},
		CreatedAt:      time.Now(),
		LastActivityAt: time.Now(),
	}

	plan, err := policy.BuildPlan(guidance.PolicyInput{
		Action:        "report",
		StateSnapshot: state,
		RawResult: map[string]any{
			"force": true,
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan(report): %v", err)
	}
	if plan.State != "report_incomplete_forced" {
		t.Fatalf("State = %q, want report_incomplete_forced", plan.State)
	}
	if !containsString(plan.Gaps, "convergence < 1.0") {
		t.Fatalf("Gaps = %#v, want convergence < 1.0 gap", plan.Gaps)
	}
	if !strings.Contains(plan.YouAreHere, "force=true") {
		t.Fatalf("YouAreHere = %q, want force=true marker", plan.YouAreHere)
	}
}

func TestInvestigatePolicy_AutoRunningGuidesStatusAndCancel(t *testing.T) {
	policy := policies.NewInvestigatePolicy()
	state := &inv.InvestigationState{
		Topic:         "root cause",
		Domain:        "debugging",
		Iteration:     0,
		CoverageAreas: []string{"reproduction", "isolation"},
		CoverageChecked: map[string]bool{},
		CreatedAt:     time.Now(),
		LastActivityAt: time.Now(),
	}

	plan, err := policy.BuildPlan(guidance.PolicyInput{
		Action:        "auto",
		StateSnapshot: state,
		RawResult: map[string]any{
			"job_id":  "job-1",
			"status":  "running",
			"cli":     "codex",
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan(auto running): %v", err)
	}
	if plan.State != "delegate_running" {
		t.Fatalf("State = %q, want delegate_running", plan.State)
	}
	self := plan.ChooseYourPath[guidance.BranchSelf]
	if self.NextCall != `status(job_id="<job_id>")` {
		t.Fatalf("self.next_call = %q, want status path", self.NextCall)
	}
	if self.Example != self.NextCall {
		t.Fatalf("self.example = %q, want %q", self.Example, self.NextCall)
	}
	if !strings.Contains(strings.ToLower(self.Then), "cancel") {
		t.Fatalf("self.then = %q, want cancel guidance", self.Then)
	}
}

func TestInvestigatePolicy_AutoCompletedGuidesReport(t *testing.T) {
	policy := policies.NewInvestigatePolicy()
	state := &inv.InvestigationState{
		Topic:     "root cause",
		Domain:    "debugging",
		Iteration: 1,
		Findings: []inv.Finding{{ID: "f1", CoverageArea: "reproduction", Iteration: 1}},
		CoverageAreas: []string{"reproduction"},
		CoverageChecked: map[string]bool{"reproduction": true},
		CreatedAt: time.Now(),
		LastActivityAt: time.Now(),
	}

	plan, err := policy.BuildPlan(guidance.PolicyInput{
		Action:        "auto",
		StateSnapshot: state,
		RawResult: map[string]any{
			"job_id":  "job-1",
			"status":  "completed",
			"cli":     "codex",
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan(auto completed): %v", err)
	}
	if plan.State != "delegate_completed" {
		t.Fatalf("State = %q, want delegate_completed", plan.State)
	}
	self := plan.ChooseYourPath[guidance.BranchSelf]
	if self.NextCall != `investigate(action="report", session_id="<session_id>")` {
		t.Fatalf("self.next_call = %q, want report path", self.NextCall)
	}
	if self.Example != self.NextCall {
		t.Fatalf("self.example = %q, want %q", self.Example, self.NextCall)
	}
}

func TestInvestigatePolicy_AutoFailedOffersRedelegate(t *testing.T) {
	policy := policies.NewInvestigatePolicy()
	state := &inv.InvestigationState{
		Topic:     "root cause",
		Domain:    "debugging",
		Iteration: 0,
		CoverageAreas: []string{"reproduction"},
		CoverageChecked: map[string]bool{},
		CreatedAt: time.Now(),
		LastActivityAt: time.Now(),
	}

	plan, err := policy.BuildPlan(guidance.PolicyInput{
		Action:        "auto",
		StateSnapshot: state,
		RawResult: map[string]any{
			"job_id":  "job-1",
			"status":  "failed",
			"cli":     "codex",
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan(auto failed): %v", err)
	}
	if plan.State != "delegate_failed" {
		t.Fatalf("State = %q, want delegate_failed", plan.State)
	}
	self := plan.ChooseYourPath[guidance.BranchSelf]
	if self.NextCall != `investigate(action="auto", topic="<topic>", session_id="<session_id>")` {
		t.Fatalf("self.next_call = %q, want re-delegate path", self.NextCall)
	}
}

func TestInvestigatePolicy_AutoCancelledOffersResumeOrRedelegate(t *testing.T) {
	policy := policies.NewInvestigatePolicy()
	state := &inv.InvestigationState{
		Topic:     "root cause",
		Domain:    "debugging",
		Iteration: 0,
		CoverageAreas: []string{"reproduction"},
		CoverageChecked: map[string]bool{},
		CreatedAt: time.Now(),
		LastActivityAt: time.Now(),
	}

	plan, err := policy.BuildPlan(guidance.PolicyInput{
		Action:        "auto",
		StateSnapshot: state,
		RawResult: map[string]any{
			"job_id":  "job-1",
			"status":  "cancelled",
			"cli":     "codex",
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan(auto cancelled): %v", err)
	}
	if plan.State != "delegate_cancelled" {
		t.Fatalf("State = %q, want delegate_cancelled", plan.State)
	}
	self := plan.ChooseYourPath[guidance.BranchSelf]
	if self.NextCall != `investigate(action="auto", topic="<topic>", session_id="<session_id>")` {
		t.Fatalf("self.next_call = %q, want re-delegate path", self.NextCall)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
