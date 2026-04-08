package patterns

import (
	"testing"
)

func TestRecursiveThinking_DepthRemainingAndPercentage(t *testing.T) {
	p := NewRecursiveThinkingPattern()

	input := map[string]any{
		"problem":          "sort a list",
		"currentDepth":     4.0,
		"maxDepth":         10.0,
		"convergenceCheck": "list length decreases each call",
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(validated, "s1")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	remaining, ok := result.Data["depthRemaining"].(float64)
	if !ok {
		t.Fatalf("depthRemaining missing or wrong type: %T", result.Data["depthRemaining"])
	}
	if remaining != 6.0 {
		t.Errorf("depthRemaining = %.1f, want 6.0", remaining)
	}

	pct, ok := result.Data["depthPercentage"].(float64)
	if !ok {
		t.Fatalf("depthPercentage missing or wrong type: %T", result.Data["depthPercentage"])
	}
	const wantPct = 40.0
	const epsilon = 0.0001
	if pct < wantPct-epsilon || pct > wantPct+epsilon {
		t.Errorf("depthPercentage = %.2f, want %.2f", pct, wantPct)
	}

	// Different depths produce different depthRemaining values.
	input2 := map[string]any{
		"problem":          "sort a list",
		"currentDepth":     8.0,
		"maxDepth":         10.0,
		"convergenceCheck": "list length decreases each call",
	}
	validated2, _ := p.Validate(input2)
	result2, _ := p.Handle(validated2, "s1")
	remaining2 := result2.Data["depthRemaining"].(float64)
	if remaining2 >= remaining {
		t.Errorf("deeper call should have less remaining: %.1f >= %.1f", remaining2, remaining)
	}
}

func TestRecursiveThinking_IsBaseCaseAtMaxDepth(t *testing.T) {
	p := NewRecursiveThinkingPattern()

	// At max depth → isBaseCase = true
	atMax := map[string]any{
		"problem":          "fibonacci",
		"currentDepth":     5.0,
		"maxDepth":         5.0,
		"convergenceCheck": "n approaches 0",
	}
	validated, _ := p.Validate(atMax)
	result, _ := p.Handle(validated, "s1")
	isBaseCase, ok := result.Data["isBaseCase"].(bool)
	if !ok || !isBaseCase {
		t.Errorf("expected isBaseCase=true at maxDepth, got %v", result.Data["isBaseCase"])
	}

	// Below max depth → isBaseCase = false
	belowMax := map[string]any{
		"problem":          "fibonacci",
		"currentDepth":     3.0,
		"maxDepth":         5.0,
		"convergenceCheck": "n approaches 0",
	}
	validated2, _ := p.Validate(belowMax)
	result2, _ := p.Handle(validated2, "s1")
	isBaseCase2, ok2 := result2.Data["isBaseCase"].(bool)
	if !ok2 || isBaseCase2 {
		t.Errorf("expected isBaseCase=false below maxDepth, got %v", result2.Data["isBaseCase"])
	}
}

func TestRecursiveThinking_ConvergenceWarningAtDepthBeyond3(t *testing.T) {
	p := NewRecursiveThinkingPattern()

	// Depth > 3 and no convergenceCheck → warning about depth > 3
	deepNoCheck := map[string]any{
		"problem":      "deeply nested computation",
		"currentDepth": 5.0,
		"maxDepth":     10.0,
	}
	validated, _ := p.Validate(deepNoCheck)
	result, _ := p.Handle(validated, "s1")
	warning, ok := result.Data["convergenceWarning"].(string)
	if !ok || warning == "" {
		t.Fatalf("expected convergenceWarning at depth>3 without convergenceCheck, got %v", result.Data["convergenceWarning"])
	}

	// Depth <= 3, no convergenceCheck → generic warning (not the depth>3 message)
	shallowNoCheck := map[string]any{
		"problem":      "shallow computation",
		"currentDepth": 2.0,
		"maxDepth":     10.0,
	}
	validated2, _ := p.Validate(shallowNoCheck)
	result2, _ := p.Handle(validated2, "s1")
	warning2 := result2.Data["convergenceWarning"].(string)
	if warning2 == warning {
		t.Errorf("shallow warning should differ from deep warning, both = %q", warning)
	}

	// With convergenceCheck → no warning
	withCheck := map[string]any{
		"problem":          "deep computation with check",
		"currentDepth":     6.0,
		"maxDepth":         10.0,
		"convergenceCheck": "value approaches fixed point",
	}
	validated3, _ := p.Validate(withCheck)
	result3, _ := p.Handle(validated3, "s1")
	warning3 := result3.Data["convergenceWarning"].(string)
	if warning3 != "" {
		t.Errorf("expected no convergenceWarning when check is provided, got %q", warning3)
	}
}
