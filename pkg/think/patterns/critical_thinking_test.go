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
