package patterns

import (
	"fmt"
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
)

// --- sequential_thinking ---

func TestSequentialThinking_ValidateSuccess(t *testing.T) {
	think.ClearSessions()
	p := NewSequentialThinkingPattern()
	out, err := p.Validate(map[string]any{"thought": "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["thought"] != "hello" {
		t.Fatalf("expected thought='hello', got %v", out["thought"])
	}
	if out["thoughtNumber"] != 1 {
		t.Fatalf("expected default thoughtNumber=1, got %v", out["thoughtNumber"])
	}
}

func TestSequentialThinking_ValidateFailure(t *testing.T) {
	think.ClearSessions()
	p := NewSequentialThinkingPattern()
	_, err := p.Validate(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing thought")
	}
	_, err = p.Validate(map[string]any{"thought": ""})
	if err == nil {
		t.Fatal("expected error for empty thought")
	}
}

func TestSequentialThinking_Accumulation(t *testing.T) {
	think.ClearSessions()
	p := NewSequentialThinkingPattern()
	sid := "seq-test-1"

	input1, _ := p.Validate(map[string]any{"thought": "first thought", "thoughtNumber": 1})
	r1, err := p.Handle(input1, sid)
	if err != nil {
		t.Fatalf("handle 1: %v", err)
	}
	if r1.Data["totalInSession"] != 1 {
		t.Fatalf("expected totalInSession=1, got %v", r1.Data["totalInSession"])
	}
	if r1.SuggestedNextPattern != "sequential_thinking" {
		t.Fatalf("expected suggestedNext=sequential_thinking, got %s", r1.SuggestedNextPattern)
	}

	input2, _ := p.Validate(map[string]any{"thought": "second thought", "thoughtNumber": 2})
	r2, err := p.Handle(input2, sid)
	if err != nil {
		t.Fatalf("handle 2: %v", err)
	}
	if r2.Data["totalInSession"] != 2 {
		t.Fatalf("expected totalInSession=2, got %v", r2.Data["totalInSession"])
	}
}

func TestSequentialThinking_Branching(t *testing.T) {
	think.ClearSessions()
	p := NewSequentialThinkingPattern()
	sid := "seq-branch-1"

	input, _ := p.Validate(map[string]any{
		"thought":           "branch thought",
		"branchId":          "alt-1",
		"branchFromThought": 1,
	})
	r, err := p.Handle(input, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["hasBranches"] != true {
		t.Fatal("expected hasBranches=true")
	}
}

// --- scientific_method ---

func TestScientificMethod_ValidateSuccess(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	out, err := p.Validate(map[string]any{"stage": "observation"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["stage"] != "observation" {
		t.Fatalf("expected stage='observation', got %v", out["stage"])
	}
}

func TestScientificMethod_ValidateFailure(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	_, err := p.Validate(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing stage")
	}
	_, err = p.Validate(map[string]any{"stage": "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid stage")
	}
}

func TestScientificMethod_StageProgression(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "sci-test-1"

	for _, stage := range []string{"observation", "question", "hypothesis"} {
		input, _ := p.Validate(map[string]any{"stage": stage})
		_, err := p.Handle(input, sid)
		if err != nil {
			t.Fatalf("handle stage %s: %v", stage, err)
		}
	}

	sess := think.GetSession(sid)
	history, _ := sess.State["stageHistory"].([]any)
	if len(history) != 3 {
		t.Fatalf("expected 3 stages in history, got %d", len(history))
	}
}

func TestScientificMethod_EntryLinking(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "sci-link-1"

	// hypothesis → prediction → experiment: valid chain
	input1, _ := p.Validate(map[string]any{
		"stage": "hypothesis",
		"entry": map[string]any{"type": "hypothesis", "text": "the sky is blue"},
	})
	r1, err := p.Handle(input1, sid)
	if err != nil {
		t.Fatalf("handle 1: %v", err)
	}
	entry1 := r1.Data["entry"].(map[string]any)
	if entry1["id"] != "E-1" {
		t.Fatalf("expected E-1, got %v", entry1["id"])
	}

	// prediction must link to hypothesis
	input2, _ := p.Validate(map[string]any{
		"stage": "hypothesis",
		"entry": map[string]any{"type": "prediction", "text": "sky will look blue at noon", "linkedTo": "E-1"},
	})
	r2, err := p.Handle(input2, sid)
	if err != nil {
		t.Fatalf("handle 2: %v", err)
	}
	entry2 := r2.Data["entry"].(map[string]any)
	if entry2["linkedTo"] != "E-1" {
		t.Fatalf("expected linkedTo=E-1, got %v", entry2["linkedTo"])
	}

	// experiment must link to prediction (E-2), not directly to hypothesis
	input3, _ := p.Validate(map[string]any{
		"stage": "experiment",
		"entry": map[string]any{"type": "experiment", "text": "observe sky at noon", "linkedTo": "E-2"},
	})
	r3, err := p.Handle(input3, sid)
	if err != nil {
		t.Fatalf("handle 3: %v", err)
	}
	entry3 := r3.Data["entry"].(map[string]any)
	if entry3["linkedTo"] != "E-2" {
		t.Fatalf("expected linkedTo=E-2, got %v", entry3["linkedTo"])
	}

	// Try linking to non-existent entry
	input4, _ := p.Validate(map[string]any{
		"stage": "analysis",
		"entry": map[string]any{"type": "result", "text": "result", "linkedTo": "E-999"},
	})
	_, err = p.Handle(input4, sid)
	if err == nil {
		t.Fatal("expected error for linking to non-existent entry")
	}
}

// --- debugging_approach ---

func TestDebuggingApproach_ValidateSuccess(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	out, err := p.Validate(map[string]any{"issue": "segfault"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["issue"] != "segfault" {
		t.Fatalf("expected issue='segfault', got %v", out["issue"])
	}
}

func TestDebuggingApproach_ValidateFailure(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	_, err := p.Validate(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing issue")
	}
}

func TestDebuggingApproach_HypothesisAddAndUpdate(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	sid := "debug-test-1"

	// Add hypothesis
	input1, _ := p.Validate(map[string]any{
		"issue":      "crash on startup",
		"hypothesis": map[string]any{"id": "H1", "text": "null pointer"},
	})
	r1, err := p.Handle(input1, sid)
	if err != nil {
		t.Fatalf("handle 1: %v", err)
	}
	if r1.Data["hypothesisCount"] != 1 {
		t.Fatalf("expected 1 hypothesis, got %v", r1.Data["hypothesisCount"])
	}

	// Update hypothesis status
	input2, _ := p.Validate(map[string]any{
		"issue":            "crash on startup",
		"hypothesisUpdate": map[string]any{"id": "H1", "status": "confirmed"},
	})
	r2, err := p.Handle(input2, sid)
	if err != nil {
		t.Fatalf("handle 2: %v", err)
	}
	if r2.Data["confirmedCount"] != 1 {
		t.Fatalf("expected confirmedCount=1, got %v", r2.Data["confirmedCount"])
	}

	// Update non-existent hypothesis
	input3, _ := p.Validate(map[string]any{
		"issue":            "crash on startup",
		"hypothesisUpdate": map[string]any{"id": "H999", "status": "refuted"},
	})
	_, err = p.Handle(input3, sid)
	if err == nil {
		t.Fatal("expected error for non-existent hypothesis update")
	}
}

func TestDebuggingApproach_KnownMethod(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	sid := "debug-method-1"

	input, _ := p.Validate(map[string]any{
		"issue":        "slow query",
		"approachName": "binary_search",
	})
	r, err := p.Handle(input, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["methodDescription"] != "Narrow the problem space by testing the midpoint" {
		t.Fatalf("unexpected method description: %v", r.Data["methodDescription"])
	}
}

func TestDebuggingApproach_CustomMethod(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	sid := "debug-custom-1"

	input, _ := p.Validate(map[string]any{
		"issue":        "flaky test",
		"approachName": "my_custom_method",
	})
	r, err := p.Handle(input, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["methodDescription"] != "custom approach" {
		t.Fatalf("expected 'custom approach', got %v", r.Data["methodDescription"])
	}
}

// --- structured_argumentation ---

func TestStructuredArgumentation_ValidateSuccess(t *testing.T) {
	think.ClearSessions()
	p := NewStructuredArgumentationPattern()
	out, err := p.Validate(map[string]any{"topic": "climate"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["topic"] != "climate" {
		t.Fatalf("expected topic='climate', got %v", out["topic"])
	}
}

func TestStructuredArgumentation_ValidateFailure(t *testing.T) {
	think.ClearSessions()
	p := NewStructuredArgumentationPattern()
	_, err := p.Validate(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing topic")
	}
}

func TestStructuredArgumentation_ClaimAndEvidence(t *testing.T) {
	think.ClearSessions()
	p := NewStructuredArgumentationPattern()
	sid := "arg-test-1"

	// Add claim
	input1, _ := p.Validate(map[string]any{
		"topic":    "testing",
		"argument": map[string]any{"type": "claim", "text": "tests improve quality"},
	})
	r1, err := p.Handle(input1, sid)
	if err != nil {
		t.Fatalf("handle 1: %v", err)
	}
	if r1.Data["claimCount"] != 1 {
		t.Fatalf("expected claimCount=1, got %v", r1.Data["claimCount"])
	}
	unsupported := r1.Data["unsupportedClaims"].([]map[string]any)
	if len(unsupported) != 1 {
		t.Fatalf("expected 1 unsupported claim, got %d", len(unsupported))
	}
	if unsupported[0]["id"] != "A-1" {
		t.Fatalf("expected unsupported claim id=A-1, got %v", unsupported[0]["id"])
	}

	// Add evidence supporting the claim
	input2, _ := p.Validate(map[string]any{
		"topic":    "testing",
		"argument": map[string]any{"type": "evidence", "text": "study shows 40% fewer bugs", "supportsClaimId": "A-1"},
	})
	r2, err := p.Handle(input2, sid)
	if err != nil {
		t.Fatalf("handle 2: %v", err)
	}
	if r2.Data["evidenceCount"] != 1 {
		t.Fatalf("expected evidenceCount=1, got %v", r2.Data["evidenceCount"])
	}
	unsupported2 := r2.Data["unsupportedClaims"].([]map[string]any)
	if len(unsupported2) != 0 {
		t.Fatalf("expected 0 unsupported claims after evidence, got %d", len(unsupported2))
	}
}

func TestStructuredArgumentation_InvalidReference(t *testing.T) {
	think.ClearSessions()
	p := NewStructuredArgumentationPattern()
	sid := "arg-ref-1"

	input, _ := p.Validate(map[string]any{
		"topic":    "testing",
		"argument": map[string]any{"type": "evidence", "text": "some evidence", "supportsClaimId": "A-999"},
	})
	_, err := p.Handle(input, sid)
	if err == nil {
		t.Fatal("expected error for invalid supportsClaimId reference")
	}
}

// --- collaborative_reasoning ---

func TestCollaborativeReasoning_ValidateSuccess(t *testing.T) {
	think.ClearSessions()
	p := NewCollaborativeReasoningPattern()
	out, err := p.Validate(map[string]any{"topic": "design"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["topic"] != "design" {
		t.Fatalf("expected topic='design', got %v", out["topic"])
	}
}

func TestCollaborativeReasoning_ValidateFailure(t *testing.T) {
	think.ClearSessions()
	p := NewCollaborativeReasoningPattern()
	_, err := p.Validate(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing topic")
	}
	_, err = p.Validate(map[string]any{"topic": "x", "stage": "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid stage")
	}
}

func TestCollaborativeReasoning_ContributionAndStage(t *testing.T) {
	think.ClearSessions()
	p := NewCollaborativeReasoningPattern()
	sid := "collab-test-1"

	// Add contribution
	input1, _ := p.Validate(map[string]any{
		"topic":        "architecture",
		"contribution": map[string]any{"type": "observation", "text": "the system is monolithic", "persona": "architect"},
	})
	r1, err := p.Handle(input1, sid)
	if err != nil {
		t.Fatalf("handle 1: %v", err)
	}
	if r1.Data["contributionCount"] != 1 {
		t.Fatalf("expected contributionCount=1, got %v", r1.Data["contributionCount"])
	}
	if r1.Data["currentStage"] != "problem-definition" {
		t.Fatalf("expected currentStage=problem-definition, got %v", r1.Data["currentStage"])
	}

	// Change stage and add another contribution
	input2, _ := p.Validate(map[string]any{
		"topic":        "architecture",
		"stage":        "ideation",
		"contribution": map[string]any{"type": "suggestion", "text": "split into microservices"},
	})
	r2, err := p.Handle(input2, sid)
	if err != nil {
		t.Fatalf("handle 2: %v", err)
	}
	if r2.Data["currentStage"] != "ideation" {
		t.Fatalf("expected currentStage=ideation, got %v", r2.Data["currentStage"])
	}
	if r2.Data["contributionCount"] != 2 {
		t.Fatalf("expected contributionCount=2, got %v", r2.Data["contributionCount"])
	}

	// Verify stage progress
	progress, ok := r2.Data["stageProgress"].(map[string]int)
	if !ok {
		t.Fatal("expected stageProgress to be map[string]int")
	}
	if progress["problem-definition"] != 1 {
		t.Fatalf("expected 1 contribution in problem-definition, got %d", progress["problem-definition"])
	}
	if progress["ideation"] != 1 {
		t.Fatalf("expected 1 contribution in ideation, got %d", progress["ideation"])
	}
}

// --- experimental_loop ---

func TestExperimentalLoop_MetricTrend(t *testing.T) {
	think.ClearSessions()
	p := NewExperimentalLoopPattern()
	sid := "test-trend"

	// 5 experiments with increasing metrics
	for i, m := range []float64{10, 20, 30, 40, 50} {
		input, _ := p.Validate(map[string]any{
			"hypothesis": fmt.Sprintf("Iteration %d", i+1),
			"metric":     m,
		})
		p.Handle(input, sid)
	}

	// Final call to get trend
	input, _ := p.Validate(map[string]any{
		"hypothesis": "Final check",
		"metric":     float64(60),
	})
	result, err := p.Handle(input, sid)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	slope, ok := result.Data["metricTrendSlope"].(float64)
	if !ok {
		t.Fatal("metricTrendSlope missing from data")
	}
	if slope <= 0 {
		t.Errorf("slope = %v, want > 0 for increasing metrics", slope)
	}
	dir, ok := result.Data["trendDirection"].(string)
	if !ok || dir != "improving" {
		t.Errorf("trendDirection = %v, want 'improving'", dir)
	}
}

// TestExperimentalLoop_MetricTrend_TooFew: fewer than 3 metrics → metricTrendSlope and trendDirection absent.
func TestExperimentalLoop_MetricTrend_TooFew(t *testing.T) {
	think.ClearSessions()
	p := NewExperimentalLoopPattern()
	sid := "test-trend-toofew"

	// Only 2 metric observations — not enough to compute slope
	for i, m := range []float64{10, 20} {
		input, _ := p.Validate(map[string]any{
			"hypothesis": fmt.Sprintf("Iteration %d", i+1),
			"metric":     m,
		})
		result, err := p.Handle(input, sid)
		if err != nil {
			t.Fatalf("Handle iteration %d: %v", i+1, err)
		}
		if _, present := result.Data["metricTrendSlope"]; present {
			t.Errorf("iteration %d: metricTrendSlope should be absent with < 3 metrics", i+1)
		}
		if _, present := result.Data["trendDirection"]; present {
			t.Errorf("iteration %d: trendDirection should be absent with < 3 metrics", i+1)
		}
	}
}
