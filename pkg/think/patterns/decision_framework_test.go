package patterns

import (
	"errors"
	"testing"
)

// TestDecisionFramework_FullInput verifies that when criteria and options are
// both supplied, weighted scoring runs and rankedOptions is present (backward compat).
func TestDecisionFramework_FullInput(t *testing.T) {
	p := NewDecisionFrameworkPattern()

	input := map[string]any{
		"decision": "choose a database",
		"criteria": []any{
			map[string]any{"name": "performance", "weight": 0.5},
			map[string]any{"name": "cost", "weight": 0.5},
		},
		"options": []any{
			map[string]any{
				"name":   "Postgres",
				"scores": map[string]any{"performance": 8.0, "cost": 7.0},
			},
			map[string]any{
				"name":   "MySQL",
				"scores": map[string]any{"performance": 7.0, "cost": 8.0},
			},
		},
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-full")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	ranked, ok := result.Data["rankedOptions"].([]any)
	if !ok || len(ranked) == 0 {
		t.Fatalf("expected non-empty rankedOptions, got %v", result.Data["rankedOptions"])
	}
	// guidance must be present.
	if _, ok := result.Data["guidance"]; !ok {
		t.Error("expected guidance field")
	}
	// suggestedCriteria must NOT be present in full mode.
	if _, ok := result.Data["suggestedCriteria"]; ok {
		t.Error("suggestedCriteria must not appear when criteria+options are provided")
	}
}

// TestDecisionFramework_AutoMode_NoCriteria verifies that when criteria are absent,
// Validate does not error and Handle returns suggestedCriteria + optionTemplate + guidance.
func TestDecisionFramework_AutoMode_NoCriteria(t *testing.T) {
	p := NewDecisionFrameworkPattern()

	input := map[string]any{
		"decision": "choose a security framework",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate must not error when criteria missing, got: %v", err)
	}
	result, err := p.Handle(validated, "test-auto-security")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	// suggestedCriteria must be present and non-empty.
	sc, ok := result.Data["suggestedCriteria"].([]string)
	if !ok || len(sc) == 0 {
		t.Errorf("expected non-empty suggestedCriteria, got %v (%T)", result.Data["suggestedCriteria"], result.Data["suggestedCriteria"])
	}

	// optionTemplate must be present.
	if _, ok := result.Data["optionTemplate"]; !ok {
		t.Error("expected optionTemplate field")
	}

	// guidance must be present.
	if _, ok := result.Data["guidance"]; !ok {
		t.Error("expected guidance field")
	}

	// rankedOptions must NOT be present (no scoring without actual options).
	if _, ok := result.Data["rankedOptions"]; ok {
		t.Error("rankedOptions must not appear in auto-mode")
	}
}

// TestDecisionFramework_AutoMode_NoOptions verifies that when options are absent,
// auto-mode activates even when criteria are present.
func TestDecisionFramework_AutoMode_NoOptions(t *testing.T) {
	p := NewDecisionFrameworkPattern()

	input := map[string]any{
		"decision": "pick a deployment strategy",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate must not error when options missing, got: %v", err)
	}
	result, err := p.Handle(validated, "test-auto-deploy")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	if _, ok := result.Data["suggestedCriteria"]; !ok {
		t.Error("expected suggestedCriteria in auto-mode")
	}
	if _, ok := result.Data["guidance"]; !ok {
		t.Error("expected guidance in auto-mode")
	}
}

// TestDecisionFramework_AutoMode_DomainTemplate verifies that when the decision text
// matches a known domain template, its criteria are used as suggestions.
func TestDecisionFramework_AutoMode_DomainTemplate(t *testing.T) {
	p := NewDecisionFrameworkPattern()

	// "security" matches the security domain template which has domain-specific criteria.
	input := map[string]any{
		"decision": "evaluate a security vulnerability",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-auto-tmpl")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	aa, ok := result.Data["autoAnalysis"].(map[string]any)
	if !ok {
		t.Fatalf("expected autoAnalysis map, got %T", result.Data["autoAnalysis"])
	}
	if aa["source"] != "domain-template" {
		t.Errorf("expected source=domain-template, got %v", aa["source"])
	}
}

// TestDecisionFramework_AutoMode_GenericFallback verifies that when no template matches,
// generic default criteria are returned.
func TestDecisionFramework_AutoMode_GenericFallback(t *testing.T) {
	p := NewDecisionFrameworkPattern()

	input := map[string]any{
		"decision": "pick a color for my office chair",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-auto-generic")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	sc, ok := result.Data["suggestedCriteria"].([]string)
	if !ok || len(sc) == 0 {
		t.Fatalf("expected suggestedCriteria, got %v", result.Data["suggestedCriteria"])
	}
	// Generic defaults must include "cost" and "performance".
	found := map[string]bool{}
	for _, c := range sc {
		found[c] = true
	}
	for _, want := range []string{"performance", "cost"} {
		if !found[want] {
			t.Errorf("expected generic criteria to include %q, got %v", want, sc)
		}
	}
}

// TestDecision_SamplingSuggestsCriteria verifies that in auto-mode with no domain template
// match, sampling is used to suggest context-aware criteria. The mock returns structured
// JSON and the result must reflect those criteria with source="sampling".
func TestDecision_SamplingSuggestsCriteria(t *testing.T) {
	samplingResp := `{
		"suggestedCriteria": [
			{"name": "latency", "weight": 0.4, "rationale": "critical for user experience"},
			{"name": "throughput", "weight": 0.3, "rationale": "handles peak load"},
			{"name": "cost", "weight": 0.3, "rationale": "within budget constraints"}
		],
		"suggestedOptions": ["Redis", "Memcached", "Hazelcast"]
	}`

	p := &decisionFrameworkPattern{}
	p.SetSampling(&mockSamplingProvider{response: samplingResp})

	// Use a novel domain that won't match any template.
	input := map[string]any{
		"decision": "choose a caching layer for a unicorn breeding application",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-sampling-criteria")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	sc, ok := result.Data["suggestedCriteria"].([]string)
	if !ok || len(sc) == 0 {
		t.Fatalf("expected non-empty suggestedCriteria, got %v (%T)", result.Data["suggestedCriteria"], result.Data["suggestedCriteria"])
	}
	// Verify LLM-suggested criteria are in the result.
	found := map[string]bool{}
	for _, c := range sc {
		found[c] = true
	}
	for _, want := range []string{"latency", "throughput", "cost"} {
		if !found[want] {
			t.Errorf("expected sampling criterion %q in suggestedCriteria, got %v", want, sc)
		}
	}

	// autoAnalysis source must be "sampling".
	aa, ok := result.Data["autoAnalysis"].(map[string]any)
	if !ok {
		t.Fatalf("expected autoAnalysis map, got %T", result.Data["autoAnalysis"])
	}
	if aa["source"] != "sampling" {
		t.Errorf("expected autoAnalysis.source=sampling, got %v", aa["source"])
	}
}

// TestDecision_FallbackWithoutSampling verifies that when a domain template matches,
// its criteria are used regardless of whether a sampling provider is available.
func TestDecision_FallbackWithoutSampling(t *testing.T) {
	p := &decisionFrameworkPattern{} // no sampling

	// "security" matches a known domain template.
	input := map[string]any{
		"decision": "evaluate a security vulnerability scanner",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-domain-fallback")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	aa, ok := result.Data["autoAnalysis"].(map[string]any)
	if !ok {
		t.Fatalf("expected autoAnalysis map, got %T", result.Data["autoAnalysis"])
	}
	// Domain template should win even without sampling.
	if aa["source"] != "domain-template" {
		t.Errorf("expected source=domain-template, got %v", aa["source"])
	}
}

// TestDecision_SamplingFailureFallbackToGeneric verifies that when sampling fails
// and no domain template matches, generic default criteria are used gracefully.
func TestDecision_SamplingFailureFallbackToGeneric(t *testing.T) {
	p := &decisionFrameworkPattern{}
	p.SetSampling(&mockSamplingProvider{err: errors.New("sampling unavailable")})

	input := map[string]any{
		"decision": "pick a color for my office chair",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-sampling-fail")
	if err != nil {
		t.Fatalf("Handle must not return error on sampling failure, got: %v", err)
	}

	sc, ok := result.Data["suggestedCriteria"].([]string)
	if !ok || len(sc) == 0 {
		t.Fatalf("expected generic suggestedCriteria on sampling failure, got %v", result.Data["suggestedCriteria"])
	}
}
