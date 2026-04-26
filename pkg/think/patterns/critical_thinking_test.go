package patterns

import (
	"errors"
	"testing"
)

// TestCritical_SamplingDetectsMoreBiases verifies that when a SamplingProvider is set,
// LLM-detected biases not in the keyword catalog are merged into detectedBiases
// and tagged with source="sampling".
func TestCritical_SamplingDetectsMoreBiases(t *testing.T) {
	samplingResp := `{
		"biases": [
			{"type": "planning_fallacy", "evidence": "assumed ideal conditions throughout", "severity": "high"},
			{"type": "optimism_bias", "evidence": "no contingency plans mentioned", "severity": "medium"}
		],
		"fallacies": [],
		"unsupportedAssumptions": []
	}`

	p := &criticalThinkingPattern{}
	p.SetSampling(&mockSamplingProvider{response: samplingResp})

	// Issue text that does NOT trigger keyword-based biases.
	input := map[string]any{
		"issue": "We will deliver the project in three weeks assuming the team works at full capacity.",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-sampling-biases")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	biases, ok := result.Data["detectedBiases"].([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any for detectedBiases, got %T", result.Data["detectedBiases"])
	}

	// At least the two sampling-detected biases must be present.
	samplingCount := 0
	foundTypes := map[string]bool{}
	for _, b := range biases {
		if b["source"] == "sampling" {
			samplingCount++
			foundTypes[b["bias"].(string)] = true
		}
	}
	if samplingCount < 2 {
		t.Errorf("expected at least 2 sampling-tagged biases, got %d (biases: %v)", samplingCount, biases)
	}
	for _, want := range []string{"planning_fallacy", "optimism_bias"} {
		if !foundTypes[want] {
			t.Errorf("expected sampling bias %q, not found in %v", want, biases)
		}
	}

	// biasCount must reflect all detected biases including sampling.
	biasCount, _ := result.Data["biasCount"].(int)
	if biasCount < 2 {
		t.Errorf("expected biasCount >= 2, got %d", biasCount)
	}
}

// TestCritical_NoDuplicates verifies that when keyword-based detection and sampling
// both detect the same bias type, the result contains no duplicates.
func TestCritical_NoDuplicates(t *testing.T) {
	// "as expected" triggers confirmation_bias keyword detection.
	// Sampling also returns confirmation_bias — should not duplicate.
	samplingResp := `{
		"biases": [
			{"type": "confirmation_bias", "evidence": "as expected is a strong signal", "severity": "high"},
			{"type": "planning_fallacy", "evidence": "no buffer time allocated", "severity": "medium"}
		],
		"fallacies": [],
		"unsupportedAssumptions": []
	}`

	p := &criticalThinkingPattern{}
	p.SetSampling(&mockSamplingProvider{response: samplingResp})

	input := map[string]any{
		"issue": "As expected, the results confirm our hypothesis perfectly.",
	}

	validated, _ := p.Validate(input)
	result, err := p.Handle(validated, "test-dedup")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	biases, ok := result.Data["detectedBiases"].([]map[string]any)
	if !ok {
		t.Fatalf("expected detectedBiases map slice, got %T", result.Data["detectedBiases"])
	}

	// Count occurrences of confirmation_bias.
	count := 0
	for _, b := range biases {
		if b["bias"] == "confirmation_bias" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 confirmation_bias entry (deduplication), got %d (biases: %v)", count, biases)
	}

	// planning_fallacy from sampling should still be present.
	foundPlanningFallacy := false
	for _, b := range biases {
		if b["bias"] == "planning_fallacy" {
			foundPlanningFallacy = true
		}
	}
	if !foundPlanningFallacy {
		t.Error("expected planning_fallacy from sampling to be present")
	}
}

// TestCritical_NoSampling verifies that when no SamplingProvider is set,
// the existing keyword-based behavior is preserved unchanged.
func TestCritical_NoSampling(t *testing.T) {
	p := &criticalThinkingPattern{} // no SetSampling call

	input := map[string]any{
		"issue": "Everyone thinks this is the best approach and it confirms our original assumption.",
	}

	validated, _ := p.Validate(input)
	result, err := p.Handle(validated, "test-no-sampling")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	biases, ok := result.Data["detectedBiases"].([]map[string]any)
	if !ok {
		t.Fatalf("expected detectedBiases, got %T", result.Data["detectedBiases"])
	}

	// Keyword matches should fire (bandwagon + confirmation_bias).
	if len(biases) == 0 {
		t.Error("expected keyword-detected biases, got none")
	}

	// None should be tagged as sampling.
	for _, b := range biases {
		if b["source"] == "sampling" {
			t.Errorf("unexpected sampling-tagged bias when no provider set: %v", b)
		}
	}
}

// TestCritical_StructuredFields verifies that assumptions, alternatives, evidence,
// and conclusion are accepted by Validate and echoed through Handle output (TS v1 parity).
func TestCritical_StructuredFields(t *testing.T) {
	p := &criticalThinkingPattern{}

	input := map[string]any{
		"issue":        "We should proceed because it confirms our original assumption.",
		"assumptions":  []any{"team is available", "budget is fixed"},
		"alternatives": []any{"delay launch", "reduce scope"},
		"evidence":     []any{"Q3 data shows 20% growth", "competitor launched last week"},
		"conclusion":   "Launch in Q4 is the safest option.",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate rejected structured fields: %v", err)
	}

	// All 5 fields must survive Validate.
	for _, field := range []string{"issue", "assumptions", "alternatives", "evidence", "conclusion"} {
		if _, ok := validated[field]; !ok {
			t.Errorf("Validate dropped field %q", field)
		}
	}

	result, err := p.Handle(validated, "test-structured-fields")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	// All structured fields must be echoed in output data.
	assumptions, ok := result.Data["assumptions"].([]any)
	if !ok || len(assumptions) != 2 {
		t.Errorf("expected assumptions echoed as []any len=2, got %T %v", result.Data["assumptions"], result.Data["assumptions"])
	}
	alternatives, ok := result.Data["alternatives"].([]any)
	if !ok || len(alternatives) != 2 {
		t.Errorf("expected alternatives echoed as []any len=2, got %T %v", result.Data["alternatives"], result.Data["alternatives"])
	}
	evidence, ok := result.Data["evidence"].([]any)
	if !ok || len(evidence) != 2 {
		t.Errorf("expected evidence echoed as []any len=2, got %T %v", result.Data["evidence"], result.Data["evidence"])
	}
	conclusion, ok := result.Data["conclusion"].(string)
	if !ok || conclusion == "" {
		t.Errorf("expected conclusion echoed as non-empty string, got %T %v", result.Data["conclusion"], result.Data["conclusion"])
	}

	// Count fields must match slice lengths.
	if result.Data["assumptionCount"] != 2 {
		t.Errorf("expected assumptionCount=2, got %v", result.Data["assumptionCount"])
	}
	if result.Data["alternativeCount"] != 2 {
		t.Errorf("expected alternativeCount=2, got %v", result.Data["alternativeCount"])
	}
	if result.Data["evidenceCount"] != 2 {
		t.Errorf("expected evidenceCount=2, got %v", result.Data["evidenceCount"])
	}
	if result.Data["hasConclusion"] != true {
		t.Errorf("expected hasConclusion=true, got %v", result.Data["hasConclusion"])
	}
}

// TestCritical_StructuredFieldsAbsent verifies that omitting the optional fields
// does not cause errors and leaves them absent from output data.
func TestCritical_StructuredFieldsAbsent(t *testing.T) {
	p := &criticalThinkingPattern{}

	input := map[string]any{
		"issue": "How hard can it be, obviously this will work.",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	result, err := p.Handle(validated, "test-fields-absent")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	for _, field := range []string{"assumptions", "alternatives", "evidence", "conclusion"} {
		if _, present := result.Data[field]; present {
			t.Errorf("expected field %q absent when not provided, but found in output", field)
		}
	}
}

func TestCriticalThinking_StructuralBiasDetection(t *testing.T) {
	p := NewCriticalThinkingPattern()

	tests := []struct {
		name      string
		issue     string
		wantBias  string
		wantCount int // minimum bias count
	}{
		{
			name:      "planning_fallacy",
			issue:     "The CTO estimates 6 months for the rewrite of the entire platform",
			wantBias:  "planning_fallacy",
			wantCount: 1,
		},
		{
			name:      "silver_bullet",
			issue:     "We should rewrite our monolith in microservices",
			wantBias:  "silver_bullet",
			wantCount: 1,
		},
		{
			name:      "overconfidence",
			issue:     "This new framework will solve all our performance problems",
			wantBias:  "overconfidence",
			wantCount: 1,
		},
		{
			name:      "correlation_not_causation",
			issue:     "We need microservices because the team has grown to 30 engineers",
			wantBias:  "correlation_not_causation",
			wantCount: 1,
		},
		{
			name:      "combined_monolith_scenario",
			issue:     "We should rewrite our monolith in microservices because our team has grown to 30 engineers. The CTO estimates 6 months for the rewrite.",
			wantBias:  "", // check count only
			wantCount: 2,  // at least planning_fallacy + silver_bullet or correlation
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := map[string]any{"issue": tt.issue}
			validated, err := p.Validate(input)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			result, err := p.Handle(validated, "")
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			biasCount, _ := result.Data["biasCount"].(int)
			if biasCount < tt.wantCount {
				t.Errorf("biasCount = %d, want >= %d (biases: %v)", biasCount, tt.wantCount, result.Data["detectedBiases"])
			}
			if tt.wantBias != "" {
				biases, _ := result.Data["detectedBiases"].([]map[string]any)
				found := false
				for _, b := range biases {
					if b["bias"] == tt.wantBias {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected bias %q not found in %v", tt.wantBias, biases)
				}
			}
		})
	}
}

func TestCriticalThinking_AssumptionCrossReference(t *testing.T) {
	p := NewCriticalThinkingPattern()
	input := map[string]any{
		"issue": "We should rewrite our monolith in microservices because our team has grown to 30 engineers and deployment conflicts are increasing. The CTO estimates 6 months for the rewrite.",
		"assumptions": []any{
			"Microservices will solve our deployment conflicts",
			"6 months is enough for a full rewrite",
			"The weather is nice today",
		},
	}
	validated, _ := p.Validate(input)
	result, err := p.Handle(validated, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	contradicted, ok := result.Data["contradictedAssumptions"].([]map[string]any)
	if !ok || len(contradicted) == 0 {
		t.Error("expected contradicted assumptions for certainty-laden assumptions about contested topic")
	}
}

// TestCritical_SamplingFailureFallback verifies that when the SamplingProvider
// returns an error, Handle returns keyword-based results without error.
func TestCritical_SamplingFailureFallback(t *testing.T) {
	p := &criticalThinkingPattern{}
	p.SetSampling(&mockSamplingProvider{err: errors.New("sampling unavailable")})

	input := map[string]any{
		"issue": "As expected, everyone thinks we are right.",
	}

	validated, _ := p.Validate(input)
	result, err := p.Handle(validated, "test-sampling-fail")
	if err != nil {
		t.Fatalf("Handle must not return error on sampling failure, got: %v", err)
	}

	// Keyword-based biases must still be present.
	biases, ok := result.Data["detectedBiases"].([]map[string]any)
	if !ok || len(biases) == 0 {
		t.Fatal("expected keyword-detected biases on sampling failure")
	}

	// No sampling source tags.
	for _, b := range biases {
		if b["source"] == "sampling" {
			t.Errorf("unexpected sampling source tag on fallback: %v", b)
		}
	}
}
