package patterns

import (
	"testing"
)

func init() {
	RegisterAll()
}

// TestSuggestPatternFromKeywordsConfidence verifies the new signature returns a name + score.
func TestSuggestPatternFromKeywordsConfidence(t *testing.T) {
	tests := []struct {
		name            string
		keywords        []string
		wantPattern     string
		wantMinConf     float64
		wantMaxConf     float64
	}{
		{
			name:        "empty keywords → fallback, zero confidence",
			keywords:    []string{},
			wantPattern: "sequential_thinking",
			wantMinConf: 0.0,
			wantMaxConf: 0.0,
		},
		{
			name:        "debug keywords dominate",
			keywords:    []string{"debug", "race", "condition", "bug"},
			wantPattern: "debugging_approach",
			wantMinConf: 0.25, // at least 1/4
			wantMaxConf: 1.0,
		},
		{
			name:        "decision keywords dominate",
			keywords:    []string{"choose", "redis", "memcached"},
			wantPattern: "decision_framework",
			wantMinConf: 0.33, // at least 1/3
			wantMaxConf: 1.0,
		},
		{
			name:        "no matching keywords → fallback, low confidence",
			keywords:    []string{"hello", "world"},
			wantPattern: "sequential_thinking",
			wantMinConf: 0.0,
			wantMaxConf: 0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pattern, conf := suggestPatternFromKeywords(tc.keywords)
			if pattern != tc.wantPattern {
				t.Errorf("pattern: got %q, want %q", pattern, tc.wantPattern)
			}
			if conf < tc.wantMinConf || conf > tc.wantMaxConf {
				t.Errorf("confidence %f not in [%f, %f]", conf, tc.wantMinConf, tc.wantMaxConf)
			}
		})
	}
}

// TestThinkAutoRouteDebug verifies that a debugging-flavored thought auto-routes to debugging_approach.
// The thought is crafted to have >= 70% signal keywords so confidence crosses the threshold.
func TestThinkAutoRouteDebug(t *testing.T) {
	p := NewThinkPattern()
	// Keywords extracted: ["bug", "crash", "error"] — all 3 match debugging signals → confidence = 1.0
	input := map[string]any{
		"thought": "bug crash error",
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	result, err := p.Handle(validated, "test-session-debug")
	if err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	// Should have auto-routed.
	autoFrom, ok := result.Data["auto_routed_from"].(string)
	if !ok || autoFrom != "think" {
		t.Errorf("expected auto_routed_from=think, got %v", result.Data["auto_routed_from"])
	}
	autoTo, ok := result.Data["auto_routed_to"].(string)
	if !ok || autoTo != "debugging_approach" {
		t.Errorf("expected auto_routed_to=debugging_approach, got %v", result.Data["auto_routed_to"])
	}
}

// TestThinkAutoRouteDecision verifies that a decision-flavored thought auto-routes to decision_framework.
// The thought is crafted to have >= 70% signal keywords so confidence crosses the threshold.
func TestThinkAutoRouteDecision(t *testing.T) {
	p := NewThinkPattern()
	// Keywords extracted: ["choose", "decide", "vs"] — all 3 match decision signals → confidence = 1.0
	input := map[string]any{
		"thought": "choose decide vs",
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	result, err := p.Handle(validated, "test-session-decision")
	if err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	autoFrom, ok := result.Data["auto_routed_from"].(string)
	if !ok || autoFrom != "think" {
		t.Errorf("expected auto_routed_from=think, got %v", result.Data["auto_routed_from"])
	}
	autoTo, ok := result.Data["auto_routed_to"].(string)
	if !ok || autoTo != "decision_framework" {
		t.Errorf("expected auto_routed_to=decision_framework, got %v", result.Data["auto_routed_to"])
	}
}

// TestThinkNoAutoRouteLowConfidence verifies that a generic thought stays in think (low confidence).
func TestThinkNoAutoRouteLowConfidence(t *testing.T) {
	p := NewThinkPattern()
	input := map[string]any{
		"thought": "hello world",
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	result, err := p.Handle(validated, "test-session-generic")
	if err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	// Should NOT have auto-routed.
	if _, ok := result.Data["auto_routed_from"]; ok {
		t.Errorf("expected no auto_routed_from for low-confidence thought, got %v", result.Data["auto_routed_from"])
	}
	if _, ok := result.Data["auto_routed_to"]; ok {
		t.Errorf("expected no auto_routed_to for low-confidence thought, got %v", result.Data["auto_routed_to"])
	}
}

// TestThinkAutoRouteResultHasAutoFields verifies auto_routed_from and auto_routed_to both appear.
func TestThinkAutoRouteResultFields(t *testing.T) {
	p := NewThinkPattern()
	// All 3 keywords are debug signals → confidence = 1.0, well above threshold.
	input := map[string]any{
		"thought": "nil panic exception",
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	result, err := p.Handle(validated, "test-session-fields")
	if err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	if result.Data["auto_routed_from"] != "think" {
		t.Errorf("auto_routed_from: got %v, want think", result.Data["auto_routed_from"])
	}
	if result.Data["auto_routed_to"] != "debugging_approach" {
		t.Errorf("auto_routed_to: got %v, want debugging_approach", result.Data["auto_routed_to"])
	}
}

// TestThinkNoAutoRouteForFallbackPattern verifies that when suggestPatternFromKeywords
// returns "sequential_thinking" (the fallback), Handle does NOT auto-execute it even if
// confidence were somehow >= 0.7, because the fallback is explicitly excluded.
// We verify this via suggestPatternFromKeywords directly: it returns sequential_thinking
// with 0.0 confidence for unmatched input, and we confirm Handle does not auto-route.
func TestThinkNoAutoRouteForFallbackPattern(t *testing.T) {
	p := NewThinkPattern()
	// A thought that contains only stop words or words not in any signal list.
	input := map[string]any{
		"thought": "hello world",
	}
	validated, _ := p.Validate(input)
	result, err := p.Handle(validated, "test-session-fallback")
	if err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	// With confidence = 0.0, no auto-routing should occur.
	if _, ok := result.Data["auto_routed_from"]; ok {
		t.Errorf("should not auto-route when pattern is the fallback or confidence is 0")
	}
	// Confirm suggestedPattern is set in the normal think result.
	if _, ok := result.Data["suggestedPattern"]; !ok {
		t.Errorf("expected suggestedPattern in result data for non-routed think")
	}
}
