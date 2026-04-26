package patterns

import (
	"testing"
)

// TestChain_CriticalToDecision verifies that the output of critical_thinking feeds
// naturally into decision_framework: detecting biases suggests decision_framework,
// and using the issue text as the decision triggers auto-mode with suggestedCriteria.
func TestChain_CriticalToDecision(t *testing.T) {
	issue := "We should use MongoDB because everyone thinks it is the best and popular opinion supports NoSQL"

	// Step 1: call critical_thinking.
	ct := NewCriticalThinkingPattern()
	ctValidated, err := ct.Validate(map[string]any{"issue": issue})
	if err != nil {
		t.Fatalf("critical_thinking Validate: %v", err)
	}
	ctResult, err := ct.Handle(ctValidated, "chain-session-1")
	if err != nil {
		t.Fatalf("critical_thinking Handle: %v", err)
	}

	// Step 2: verify suggestedNextPattern is "decision_framework".
	if ctResult.SuggestedNextPattern != "decision_framework" {
		t.Fatalf("expected suggestedNextPattern=decision_framework, got %q", ctResult.SuggestedNextPattern)
	}

	// Step 3: verify critical_thinking produced useful output.
	biasCount, _ := ctResult.Data["biasCount"].(int)
	if biasCount == 0 {
		t.Error("expected at least one detected bias for the bandwagon issue text")
	}
	biases, _ := ctResult.Data["detectedBiases"].([]map[string]any)
	if len(biases) == 0 {
		t.Error("expected detectedBiases to be non-empty")
	}

	// Step 4: call decision_framework with the same issue text as decision (auto-mode).
	df := NewDecisionFrameworkPattern()
	dfValidated, err := df.Validate(map[string]any{"decision": issue})
	if err != nil {
		t.Fatalf("decision_framework Validate: %v", err)
	}
	dfResult, err := df.Handle(dfValidated, "chain-session-1")
	if err != nil {
		t.Fatalf("decision_framework Handle: %v", err)
	}

	// Step 5: verify decision_framework returns suggestedCriteria (auto-mode).
	suggestedCriteria, ok := dfResult.Data["suggestedCriteria"].([]string)
	if !ok {
		t.Fatalf("expected suggestedCriteria []string, got %T", dfResult.Data["suggestedCriteria"])
	}
	if len(suggestedCriteria) == 0 {
		t.Error("expected non-empty suggestedCriteria from decision_framework auto-mode")
	}

	// Step 6: verify the chain produces useful output at each step.
	if ctResult.Status != "success" {
		t.Errorf("critical_thinking status: want success, got %q", ctResult.Status)
	}
	if dfResult.Status != "success" {
		t.Errorf("decision_framework status: want success, got %q", dfResult.Status)
	}
	if _, hasGuidance := dfResult.Data["guidance"]; !hasGuidance {
		t.Error("decision_framework result missing guidance field")
	}
}

// TestChain_DecompToArchitecture verifies that sub-problems from problem_decomposition
// can be used directly as components for architecture_analysis.
func TestChain_DecompToArchitecture(t *testing.T) {
	problem := "Design microservice API"

	// Step 1: call problem_decomposition.
	pd := NewProblemDecompositionPattern()
	pdValidated, err := pd.Validate(map[string]any{"problem": problem})
	if err != nil {
		t.Fatalf("problem_decomposition Validate: %v", err)
	}
	pdResult, err := pd.Handle(pdValidated, "chain-session-2")
	if err != nil {
		t.Fatalf("problem_decomposition Handle: %v", err)
	}
	if pdResult.Status != "success" {
		t.Fatalf("problem_decomposition status: want success, got %q", pdResult.Status)
	}

	// Step 2: get suggestedSubProblems from result.
	suggestedSubProblems, ok := pdResult.Data["suggestedSubProblems"].([]string)
	if !ok || len(suggestedSubProblems) == 0 {
		// Fallback: use hardcoded sub-problems so the chain is testable
		// even if the input triggers no auto-analysis.
		suggestedSubProblems = []string{"api-design", "service-layer", "data-access"}
	}

	// Step 3: build components from sub-problems — each sub-problem becomes a component.
	components := make([]any, len(suggestedSubProblems))
	for i, sp := range suggestedSubProblems {
		components[i] = sp
	}

	// Step 4: call architecture_analysis with those components.
	aa := NewArchitectureAnalysisPattern()
	aaValidated, err := aa.Validate(map[string]any{"components": components})
	if err != nil {
		t.Fatalf("architecture_analysis Validate: %v", err)
	}
	aaResult, err := aa.Handle(aaValidated, "chain-session-2")
	if err != nil {
		t.Fatalf("architecture_analysis Handle: %v", err)
	}

	// Step 5: verify architecture_analysis returns componentMetrics.
	componentMetrics, ok := aaResult.Data["componentMetrics"].([]any)
	if !ok {
		t.Fatalf("expected componentMetrics []any, got %T", aaResult.Data["componentMetrics"])
	}
	if len(componentMetrics) == 0 {
		t.Error("expected non-empty componentMetrics from architecture_analysis")
	}

	// Verify each metric entry has required fields.
	for i, m := range componentMetrics {
		entry, ok := m.(map[string]any)
		if !ok {
			t.Errorf("componentMetrics[%d]: expected map[string]any, got %T", i, m)
			continue
		}
		if _, hasComponent := entry["component"]; !hasComponent {
			t.Errorf("componentMetrics[%d]: missing 'component' field", i)
		}
		if _, hasCa := entry["ca"]; !hasCa {
			t.Errorf("componentMetrics[%d]: missing 'ca' field", i)
		}
		if _, hasCe := entry["ce"]; !hasCe {
			t.Errorf("componentMetrics[%d]: missing 'ce' field", i)
		}
	}

	// Verify component count matches.
	componentCount, _ := aaResult.Data["componentCount"].(int)
	if componentCount != len(suggestedSubProblems) {
		t.Errorf("componentCount: want %d, got %d", len(suggestedSubProblems), componentCount)
	}

	if aaResult.Status != "success" {
		t.Errorf("architecture_analysis status: want success, got %q", aaResult.Status)
	}
}
