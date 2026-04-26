package patterns

import (
	"testing"
)

func TestMetacognitiveMonitoring_CalibratedConfidenceReducedByPenalties(t *testing.T) {
	p := NewMetacognitiveMonitoringPattern()

	// 4 uncertainties → penalty 0.20; 2 biases → penalty 0.20; raw=0.9 → calibrated=0.50
	input := map[string]any{
		"task":          "evaluate architecture",
		"confidence":    0.9,
		"uncertainties": []any{"u1", "u2", "u3", "u4"},
		"biases":        []any{"b1", "b2"},
		"claims":        []any{"c1", "c2", "c3"},
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(validated, "s1")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	calibrated, ok := result.Data["calibratedConfidence"].(float64)
	if !ok {
		t.Fatalf("calibratedConfidence missing or wrong type: %T", result.Data["calibratedConfidence"])
	}
	// raw(0.9) - uncertainty(0.20) - bias(0.20) = 0.50
	const want = 0.50
	const epsilon = 0.0001
	if calibrated < want-epsilon || calibrated > want+epsilon {
		t.Errorf("calibratedConfidence = %.4f, want %.4f", calibrated, want)
	}
}

func TestMetacognitiveMonitoring_OverconfidentFlag(t *testing.T) {
	p := NewMetacognitiveMonitoringPattern()

	// No biases, no uncertainties → calibrated = raw = 0.9; claims < 3 → overconfident
	overconfidentInput := map[string]any{
		"task":       "quick check",
		"confidence": 0.9,
		"claims":     []any{"only one"},
	}
	validated, _ := p.Validate(overconfidentInput)
	result, _ := p.Handle(validated, "s1")

	overconfident, ok := result.Data["overconfident"].(bool)
	if !ok || !overconfident {
		t.Errorf("expected overconfident=true, got %v", result.Data["overconfident"])
	}

	// Same confidence but 3 claims → NOT overconfident
	notOverconfidentInput := map[string]any{
		"task":       "thorough check",
		"confidence": 0.9,
		"claims":     []any{"c1", "c2", "c3"},
	}
	validated2, _ := p.Validate(notOverconfidentInput)
	result2, _ := p.Handle(validated2, "s1")
	overconfident2, ok2 := result2.Data["overconfident"].(bool)
	if !ok2 || overconfident2 {
		t.Errorf("expected overconfident=false for 3 claims, got %v", result2.Data["overconfident"])
	}
}

func TestMetacognitiveMonitoring_CanonicalFieldNames(t *testing.T) {
	p := NewMetacognitiveMonitoringPattern()

	input := map[string]any{
		"task":          "test canonical names",
		"confidence":    0.7,
		"claims":        []any{"c1", "c2"},
		"biases":        []any{"b1"},
		"uncertainties": []any{"u1", "u2", "u3"},
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(validated, "s1")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// canonical singular names must be present
	if _, ok := result.Data["claimCount"]; !ok {
		t.Error("claimCount missing (expected canonical singular field)")
	}
	if _, ok := result.Data["biasCount"]; !ok {
		t.Error("biasCount missing (expected canonical singular field)")
	}
	if _, ok := result.Data["uncertaintyCount"]; !ok {
		t.Error("uncertaintyCount missing (expected canonical singular field)")
	}

	// values must be correct
	if got := result.Data["claimCount"].(int); got != 2 {
		t.Errorf("claimCount = %d, want 2", got)
	}
	if got := result.Data["biasCount"].(int); got != 1 {
		t.Errorf("biasCount = %d, want 1", got)
	}
	if got := result.Data["uncertaintyCount"].(int); got != 3 {
		t.Errorf("uncertaintyCount = %d, want 3", got)
	}
}

func TestMetacognitiveMonitoring_SuggestedNextPattern(t *testing.T) {
	p := NewMetacognitiveMonitoringPattern()

	input := map[string]any{
		"task":       "check suggested next",
		"confidence": 0.6,
	}
	validated, _ := p.Validate(input)
	result, err := p.Handle(validated, "s1")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if result.SuggestedNextPattern != "decision_framework" {
		t.Errorf("SuggestedNextPattern = %q, want %q", result.SuggestedNextPattern, "decision_framework")
	}
}

func TestMetacognitiveMonitoring_InputGuidanceWhenNoConfidence(t *testing.T) {
	p := NewMetacognitiveMonitoringPattern()

	// Without confidence — inputGuidance must be present
	inputNoConf := map[string]any{
		"task":   "test without confidence",
		"claims": []any{"c1"},
	}
	validated, _ := p.Validate(inputNoConf)
	result, err := p.Handle(validated, "s1")
	if err != nil {
		t.Fatalf("Handle (no confidence): %v", err)
	}
	guidance, ok := result.Data["inputGuidance"].(string)
	if !ok || guidance == "" {
		t.Errorf("inputGuidance missing or empty when confidence not provided: %v", result.Data["inputGuidance"])
	}

	// With confidence — inputGuidance must NOT be present
	inputWithConf := map[string]any{
		"task":       "test with confidence",
		"confidence": 0.5,
		"claims":     []any{"c1"},
	}
	validated2, _ := p.Validate(inputWithConf)
	result2, err := p.Handle(validated2, "s1")
	if err != nil {
		t.Fatalf("Handle (with confidence): %v", err)
	}
	if _, present := result2.Data["inputGuidance"]; present {
		t.Error("inputGuidance should not be set when confidence is provided")
	}
}

func TestMetacognitiveMonitoring_AdjustmentReasonDescribesPenalties(t *testing.T) {
	p := NewMetacognitiveMonitoringPattern()

	// 2 uncertainties (penalty=0.10), 1 bias (penalty=0.10)
	input := map[string]any{
		"task":          "review",
		"confidence":    0.7,
		"uncertainties": []any{"u1", "u2"},
		"biases":        []any{"b1"},
		"claims":        []any{"c1", "c2", "c3"},
	}
	validated, _ := p.Validate(input)
	result, _ := p.Handle(validated, "s1")

	reason, ok := result.Data["adjustmentReason"].(string)
	if !ok || reason == "" {
		t.Fatalf("adjustmentReason missing or empty: %v", result.Data["adjustmentReason"])
	}
	// Should mention both uncertainty and bias penalties
	if reason == "no adjustments applied" {
		t.Error("expected penalties in adjustmentReason, got 'no adjustments applied'")
	}

	// With zero penalties the reason should be "no adjustments applied"
	inputClean := map[string]any{
		"task":       "review",
		"confidence": 0.5,
		"claims":     []any{"c1", "c2", "c3"},
	}
	validated2, _ := p.Validate(inputClean)
	result2, _ := p.Handle(validated2, "s1")
	reason2 := result2.Data["adjustmentReason"].(string)
	if reason2 != "no adjustments applied" {
		t.Errorf("expected 'no adjustments applied', got %q", reason2)
	}
}
