package server

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/guidance/policies"
)

// --- Consensus policy unit tests ---

func TestConsensusPolicy_StatePolling(t *testing.T) {
	pol := policies.NewConsensusPolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.ConsensusPolicyInput{
			Synthesize: false,
			Turns:      0,
			Status:     "running",
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State != "polling" {
		t.Errorf("state = %q, want polling", plan.State)
	}
}

func TestConsensusPolicy_StateSynthesizing(t *testing.T) {
	pol := policies.NewConsensusPolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.ConsensusPolicyInput{
			Synthesize: false,
			Turns:      2,
			Status:     "completed",
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State != "synthesizing" {
		t.Errorf("state = %q, want synthesizing", plan.State)
	}
}

func TestConsensusPolicy_StateComplete(t *testing.T) {
	pol := policies.NewConsensusPolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.ConsensusPolicyInput{
			Synthesize: true,
			Turns:      2,
			Status:     "completed",
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State != "complete" {
		t.Errorf("state = %q, want complete", plan.State)
	}
}

func TestConsensusPolicy_DoNotContainsSingleModelWarning(t *testing.T) {
	pol := policies.NewConsensusPolicy()
	plan, _ := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.ConsensusPolicyInput{Turns: 2, Status: "completed"},
	})
	found := false
	for _, d := range plan.DoNot {
		if len(d) > 0 && containsSubstring(d, "single model") {
			found = true
			break
		}
	}
	if !found {
		t.Error("consensus DoNot should warn against treating result as a single model opinion")
	}
}

func TestConsensusPolicy_NilSnapshot(t *testing.T) {
	pol := policies.NewConsensusPolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{StateSnapshot: nil})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State == "" {
		t.Error("nil snapshot must still produce a non-empty state")
	}
}

// --- Debate policy unit tests ---

func TestDebatePolicy_StateOpening(t *testing.T) {
	pol := policies.NewDebatePolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.DebatePolicyInput{Turns: 0, Status: "completed"},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State != "opening" {
		t.Errorf("state = %q, want opening", plan.State)
	}
}

func TestDebatePolicy_StateRebuttal(t *testing.T) {
	pol := policies.NewDebatePolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.DebatePolicyInput{Turns: 3, Status: "completed"},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State != "rebuttal_3" {
		t.Errorf("state = %q, want rebuttal_3", plan.State)
	}
}

func TestDebatePolicy_StateVerdict(t *testing.T) {
	pol := policies.NewDebatePolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.DebatePolicyInput{Turns: 4, Synthesize: true, Status: "completed"},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State != "verdict" {
		t.Errorf("state = %q, want verdict", plan.State)
	}
}

func TestDebatePolicy_HasTwoBranches(t *testing.T) {
	pol := policies.NewDebatePolicy()
	plan, _ := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.DebatePolicyInput{Turns: 2, Status: "completed"},
	})
	if len(plan.ChooseYourPath) < 2 {
		t.Errorf("debate must offer at least 2 branches (continue, verdict), got %d", len(plan.ChooseYourPath))
	}
}

func TestDebatePolicy_DoNotContainsDisagreementWarning(t *testing.T) {
	pol := policies.NewDebatePolicy()
	plan, _ := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.DebatePolicyInput{Turns: 2, Status: "completed"},
	})
	found := false
	for _, d := range plan.DoNot {
		if containsSubstring(d, "disagree") || containsSubstring(d, "agreement") {
			found = true
			break
		}
	}
	if !found {
		t.Error("debate DoNot should mention that debate surfaces disagreements")
	}
}

// --- Dialog policy unit tests ---

func TestDialogPolicy_StateTurn0(t *testing.T) {
	pol := policies.NewDialogPolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.DialogPolicyInput{Turns: 0, Status: "running"},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State != "turn_0" {
		t.Errorf("state = %q, want turn_0", plan.State)
	}
}

func TestDialogPolicy_StateTurnN(t *testing.T) {
	pol := policies.NewDialogPolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.DialogPolicyInput{Turns: 3, Status: "running"},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State != "turn_3" {
		t.Errorf("state = %q, want turn_3", plan.State)
	}
}

func TestDialogPolicy_StateComplete(t *testing.T) {
	pol := policies.NewDialogPolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.DialogPolicyInput{Turns: 4, Status: "completed"},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State != "complete" {
		t.Errorf("state = %q, want complete", plan.State)
	}
}

func TestDialogPolicy_YouAreHereContainsTurnInfo(t *testing.T) {
	pol := policies.NewDialogPolicy()
	plan, _ := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.DialogPolicyInput{
			SessionID: "sess-abc",
			Turns:     2,
			Status:    "running",
		},
	})
	if !containsSubstring(plan.YouAreHere, "2") {
		t.Errorf("you_are_here should mention the turn count, got: %q", plan.YouAreHere)
	}
}

func TestDialogPolicy_ChooseYourPathPresentWhenActive(t *testing.T) {
	pol := policies.NewDialogPolicy()
	plan, _ := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.DialogPolicyInput{
			SessionID: "sess-xyz",
			Turns:     2,
			Status:    "running",
		},
	})
	if len(plan.ChooseYourPath) == 0 {
		t.Error("active dialog must include choose_your_path for continuation")
	}
	selfBranch, hasSelf := plan.ChooseYourPath["self"]
	if !hasSelf {
		t.Fatal("choose_your_path must contain a self branch")
	}
	if !containsSubstring(selfBranch.NextCall, "sess-xyz") {
		t.Errorf("next_call should embed session_id, got: %q", selfBranch.NextCall)
	}
}

func TestDialogPolicy_NoChooseYourPathOnComplete(t *testing.T) {
	pol := policies.NewDialogPolicy()
	plan, _ := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.DialogPolicyInput{Turns: 4, Status: "completed"},
	})
	if len(plan.ChooseYourPath) != 0 {
		t.Errorf("complete dialog must not include choose_your_path, got: %v", plan.ChooseYourPath)
	}
}

// --- Workflow policy unit tests ---

func TestWorkflowPolicy_StateComplete(t *testing.T) {
	pol := policies.NewWorkflowPolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.WorkflowPolicyInput{
			TotalSteps:     3,
			CompletedSteps: 3,
			Status:         "completed",
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State != "complete" {
		t.Errorf("state = %q, want complete", plan.State)
	}
}

func TestWorkflowPolicy_StateStepNOfM(t *testing.T) {
	pol := policies.NewWorkflowPolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.WorkflowPolicyInput{
			TotalSteps:     5,
			CompletedSteps: 2,
			Status:         "partial",
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State != "step_2_of_5" {
		t.Errorf("state = %q, want step_2_of_5", plan.State)
	}
}

func TestWorkflowPolicy_StateFailedAtStep(t *testing.T) {
	pol := policies.NewWorkflowPolicy()
	plan, err := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.WorkflowPolicyInput{
			TotalSteps:     4,
			CompletedSteps: 2,
			FailedAtStep:   3,
			Status:         "failed",
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.State != "failed_at_step_3" {
		t.Errorf("state = %q, want failed_at_step_3", plan.State)
	}
}

func TestWorkflowPolicy_FailureHasThreeBranches(t *testing.T) {
	pol := policies.NewWorkflowPolicy()
	plan, _ := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.WorkflowPolicyInput{
			TotalSteps:   3,
			FailedAtStep: 2,
			Status:       "failed",
		},
	})
	if len(plan.ChooseYourPath) < 3 {
		t.Errorf("failed workflow must offer retry/skip/cancel branches, got %d branch(es)", len(plan.ChooseYourPath))
	}
	for _, name := range []string{"retry", "skip", "cancel"} {
		if _, ok := plan.ChooseYourPath[name]; !ok {
			t.Errorf("choose_your_path missing %q branch", name)
		}
	}
}

func TestWorkflowPolicy_DoNotMentionsStepDefinitions(t *testing.T) {
	pol := policies.NewWorkflowPolicy()
	plan, _ := pol.BuildPlan(policies.PolicyInput{
		StateSnapshot: &policies.WorkflowPolicyInput{TotalSteps: 3, Status: "completed"},
	})
	found := false
	for _, d := range plan.DoNot {
		if containsSubstring(d, "step") {
			found = true
			break
		}
	}
	if !found {
		t.Error("workflow DoNot should mention step definitions")
	}
}

// --- Handler integration tests: nested result structure ---

func TestConsensusHandler_GuidanceEnvelope(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("consensus", map[string]any{
		"topic": "Which approach is better?",
		"async": false,
	})

	result, err := srv.handleConsensus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleConsensus: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	data := parseResult(t, result)

	state, _ := data["state"].(string)
	if state == "" {
		t.Error("consensus response must include a non-empty state field")
	}
	if state == "guidance_not_implemented" {
		t.Errorf("consensus must have a real policy, got guidance_not_implemented")
	}

	// Result payload must be nested, not flattened.
	if _, ok := data["result"]; !ok {
		t.Error("consensus response must nest the raw payload under result key")
	}
}

func TestDebateHandler_GuidanceEnvelope(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("debate", map[string]any{
		"topic": "Is Go better than Rust?",
		"async": false,
	})

	result, err := srv.handleDebate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDebate: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	data := parseResult(t, result)

	state, _ := data["state"].(string)
	if state == "" {
		t.Error("debate response must include a non-empty state field")
	}
	if state == "guidance_not_implemented" {
		t.Errorf("debate must have a real policy, got guidance_not_implemented")
	}

	if _, ok := data["result"]; !ok {
		t.Error("debate response must nest the raw payload under result key")
	}
}

func TestDialogHandler_GuidanceEnvelope(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("dialog", map[string]any{
		"prompt":    "Discuss the tradeoffs of microservices",
		"max_turns": float64(2),
	})

	result, err := srv.handleDialog(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDialog: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	data := parseResult(t, result)

	state, _ := data["state"].(string)
	if state == "" {
		t.Error("dialog response must include a non-empty state field")
	}
	if state == "guidance_not_implemented" {
		t.Errorf("dialog must have a real policy, got guidance_not_implemented")
	}

	if _, ok := data["result"]; !ok {
		t.Error("dialog response must nest the raw payload under result key")
	}
}

func TestWorkflowHandler_GuidanceEnvelope(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("workflow", map[string]any{
		"name":  "test-workflow",
		"steps": `[{"id":"s1","tool":"exec","params":{"prompt":"hello","cli":"codex"}}]`,
		"async": false,
	})

	result, err := srv.handleWorkflow(context.Background(), req)
	if err != nil {
		t.Fatalf("handleWorkflow: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	data := parseResult(t, result)

	state, _ := data["state"].(string)
	if state == "" {
		t.Error("workflow response must include a non-empty state field")
	}
	if state == "guidance_not_implemented" {
		t.Errorf("workflow must have a real policy, got guidance_not_implemented")
	}

	if _, ok := data["result"]; !ok {
		t.Error("workflow response must nest the raw payload under result key")
	}
}

// TestConsensusHandler_AsyncDefault verifies that omitting the "async" field
// causes handleConsensus to run in the background and return job_id + status=running.
func TestConsensusHandler_AsyncDefault(t *testing.T) {
	srv := testServer(t)
	// No "async" key — default is true per P26 contract.
	req := makeRequest("consensus", map[string]any{
		"topic": "Which approach is better?",
	})

	result, err := srv.handleConsensus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleConsensus: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	data := parseResult(t, result)

	jobID, _ := data["job_id"].(string)
	if jobID == "" {
		t.Error("async-default consensus must return a non-empty job_id")
	}
	status, _ := data["status"].(string)
	if status != "running" {
		t.Errorf("async-default consensus must return status=running, got %q", status)
	}
}

// TestDebateHandler_AsyncDefault verifies that omitting the "async" field
// causes handleDebate to run in the background and return job_id + status=running.
func TestDebateHandler_AsyncDefault(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("debate", map[string]any{
		"topic": "Is Go better than Rust?",
	})

	result, err := srv.handleDebate(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDebate: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	data := parseResult(t, result)

	jobID, _ := data["job_id"].(string)
	if jobID == "" {
		t.Error("async-default debate must return a non-empty job_id")
	}
	status, _ := data["status"].(string)
	if status != "running" {
		t.Errorf("async-default debate must return status=running, got %q", status)
	}
}

// TestWorkflowHandler_AsyncDefault verifies that omitting the "async" field
// causes handleWorkflow to run in the background and return job_id + status=running.
func TestWorkflowHandler_AsyncDefault(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("workflow", map[string]any{
		"name":  "test-workflow",
		"steps": `[{"id":"s1","tool":"exec","params":{"prompt":"hello","cli":"codex"}}]`,
	})

	result, err := srv.handleWorkflow(context.Background(), req)
	if err != nil {
		t.Fatalf("handleWorkflow: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	data := parseResult(t, result)

	jobID, _ := data["job_id"].(string)
	if jobID == "" {
		t.Error("async-default workflow must return a non-empty job_id")
	}
	status, _ := data["status"].(string)
	if status != "running" {
		t.Errorf("async-default workflow must return status=running, got %q", status)
	}
}

// containsSubstring is a helper to avoid importing strings in this test file.
func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
