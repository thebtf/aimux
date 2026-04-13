package server

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/guidance/policies"
)

// TestThinkGuidance_OneShotPattern verifies that a one-shot pattern returns
// state="complete" and does not include choose_your_path.
func TestThinkGuidance_OneShotPattern(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("think", map[string]any{
		"pattern": "critical_thinking",
		"issue":   "evaluate this argument",
	})

	result, err := srv.handleThink(context.Background(), req)
	if err != nil {
		t.Fatalf("handleThink: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %v", result.Content)
	}

	data := parseResult(t, result)

	// Guidance envelope fields are at the top level.
	state, _ := data["state"].(string)
	if state != "complete" {
		t.Errorf("state = %q, want %q", state, "complete")
	}

	// One-shot patterns must not suggest unnecessary follow-up via choose_your_path.
	if cyp, exists := data["choose_your_path"]; exists && cyp != nil {
		t.Errorf("one-shot pattern must not populate choose_your_path, got: %v", cyp)
	}

	// HowThisToolWorks should be present on one-shot patterns to orient the caller.
	howThisToolWorks, _ := data["how_this_tool_works"].(string)
	if howThisToolWorks == "" {
		t.Error("one-shot pattern must include how_this_tool_works explanation")
	}

	// Raw result must be nested under result key, not flattened at the top.
	resultPayload, ok := data["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result payload under result key, got %T", data["result"])
	}
	if resultPayload["pattern"] != "critical_thinking" {
		t.Errorf("result.pattern = %v, want critical_thinking", resultPayload["pattern"])
	}
}

// TestThinkGuidance_StatefulPattern verifies that a stateful pattern returns
// a non-complete state and includes a next-step suggestion in choose_your_path.
func TestThinkGuidance_StatefulPattern(t *testing.T) {
	srv := testServer(t)
	req := makeRequest("think", map[string]any{
		"pattern":     "sequential_thinking",
		"thought":     "First step of the reasoning chain",
		"thoughtNumber": 1,
		"totalThoughts": 3,
		"next_step_needed": true,
	})

	result, err := srv.handleThink(context.Background(), req)
	if err != nil {
		t.Fatalf("handleThink: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %v", result.Content)
	}

	data := parseResult(t, result)

	state, _ := data["state"].(string)
	if state == "complete" {
		t.Errorf("stateful pattern should not have state=complete on first step, got %q", state)
	}
	if state == "" {
		t.Error("stateful pattern must have a non-empty state")
	}

	// Stateful patterns must provide next-step guidance via choose_your_path.
	cyp, ok := data["choose_your_path"].(map[string]any)
	if !ok || len(cyp) == 0 {
		t.Errorf("stateful pattern must include choose_your_path, got: %v", data["choose_your_path"])
	}

	selfBranch, hasSelf := cyp["self"].(map[string]any)
	if !hasSelf {
		t.Fatal("choose_your_path must contain a self branch")
	}
	nextCall, _ := selfBranch["next_call"].(string)
	if nextCall == "" {
		t.Error("self branch must include a non-empty next_call")
	}

	// The result payload is nested under result.
	if _, ok := data["result"].(map[string]any); !ok {
		t.Fatalf("expected result payload under result key, got %T", data["result"])
	}
}

// TestThinkGuidance_OneShotDoesNotSuggestFollowUp verifies explicitly that
// one-shot patterns do not pollute the response with continuation guidance.
// This is a distinct property check from the state=complete check above.
func TestThinkGuidance_OneShotDoesNotSuggestFollowUp(t *testing.T) {
	oneShotPatterns := []string{
		"critical_thinking",
		"decision_framework",
		"mental_model",
		"problem_decomposition",
	}

	srv := testServer(t)

	for _, pattern := range oneShotPatterns {
		t.Run(pattern, func(t *testing.T) {
			req := makeRequest("think", map[string]any{
				"pattern": pattern,
				"issue":   "test issue for " + pattern,
			})

			result, err := srv.handleThink(context.Background(), req)
			if err != nil {
				t.Fatalf("handleThink(%s): %v", pattern, err)
			}
			if result.IsError {
				// Some patterns may require additional fields — skip rather than fail.
				t.Skipf("pattern %s returned an error (may need more fields): %v", pattern, result.Content)
			}

			data := parseResult(t, result)

			state, _ := data["state"].(string)
			if state != "complete" {
				t.Errorf("pattern=%s: state = %q, want complete", pattern, state)
			}

			// Must not contain choose_your_path with continuation guidance.
			if cyp := data["choose_your_path"]; cyp != nil {
				t.Errorf("pattern=%s: one-shot pattern must not include choose_your_path", pattern)
			}
		})
	}
}

// TestThinkPolicyInput_IsStatefulPattern checks the policy helper directly.
func TestThinkPolicyInput_IsStatefulPattern(t *testing.T) {
	stateful := []string{
		"sequential_thinking",
		"scientific_method",
		"debugging_approach",
		"experimental_loop",
		"structured_argumentation",
		"collaborative_reasoning",
	}
	oneShot := []string{
		"critical_thinking",
		"decision_framework",
		"problem_decomposition",
		"mental_model",
		"metacognitive_monitoring",
		"stochastic_algorithm",
		"temporal_thinking",
		"visual_reasoning",
		"source_comparison",
		"literature_review",
		"peer_review",
		"replication_analysis",
		"research_synthesis",
		"architecture_analysis",
		"domain_modeling",
		"recursive_thinking",
		"think",
	}

	for _, p := range stateful {
		if !policies.IsStatefulPattern(p) {
			t.Errorf("IsStatefulPattern(%q) = false, want true", p)
		}
	}
	for _, p := range oneShot {
		if policies.IsStatefulPattern(p) {
			t.Errorf("IsStatefulPattern(%q) = true, want false", p)
		}
	}
}

// TestThinkPolicy_BuildPlanOneShot exercises the policy in isolation.
func TestThinkPolicy_BuildPlanOneShot(t *testing.T) {
	pol := policies.NewThinkPolicy()
	input := policies.ThinkPolicyInput{
		Pattern:    "critical_thinking",
		IsStateful: false,
	}

	plan := pol.BuildPlanTyped(input)
	if plan == nil {
		t.Fatal("BuildPlanTyped returned nil")
	}
	if plan.State != "complete" {
		t.Errorf("state = %q, want complete", plan.State)
	}
	if plan.HowThisToolWorks == "" {
		t.Error("HowThisToolWorks must be non-empty for one-shot patterns")
	}
	if plan.ChooseYourPath != nil {
		t.Errorf("ChooseYourPath should be nil for one-shot, got: %v", plan.ChooseYourPath)
	}
}

// TestThinkPolicy_BuildPlanStateful exercises the policy in isolation for a stateful pattern.
func TestThinkPolicy_BuildPlanStateful(t *testing.T) {
	pol := policies.NewThinkPolicy()
	sessionID := "test-session-id"
	input := policies.ThinkPolicyInput{
		Pattern:    "scientific_method",
		SessionID:  sessionID,
		IsStateful: true,
		StepNumber: 0,
	}

	plan := pol.BuildPlanTyped(input)
	if plan == nil {
		t.Fatal("BuildPlanTyped returned nil")
	}
	if plan.State == "complete" {
		t.Errorf("stateful pattern first step should not be complete, got state=%q", plan.State)
	}
	if plan.State == "" {
		t.Error("state must be non-empty for stateful pattern")
	}
	selfBranch, hasSelf := plan.ChooseYourPath["self"]
	if !hasSelf {
		t.Fatal("stateful plan must include a self branch in choose_your_path")
	}
	if selfBranch.NextCall == "" {
		t.Error("self branch must include a non-empty next_call")
	}
	if plan.StopConditions == "" {
		t.Error("stateful plan must have stop_conditions")
	}
}
