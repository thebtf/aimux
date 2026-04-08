package patterns

import (
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
