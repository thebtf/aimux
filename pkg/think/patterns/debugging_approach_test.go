package patterns

import (
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
)

// submitHypothesis is a helper to validate and handle a hypothesis submission.
func submitHypothesis(t *testing.T, p think.PatternHandler, sid, id, text string, confidence float64) *think.ThinkResult {
	t.Helper()
	hyp := map[string]any{"id": id, "text": text}
	if confidence > 0 {
		hyp["confidence"] = confidence
	}
	inp, err := p.Validate(map[string]any{
		"issue":      "test issue",
		"hypothesis": hyp,
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	r, err := p.Handle(inp, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	return r
}

// TestDebugging_EvidenceGate: submit hypothesis with only 1 finding in session → STOP directive.
func TestDebugging_EvidenceGate(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	sid := "dbg-gate-1"

	// Submit first (and only) hypothesis — session has 1 finding, threshold is 3.
	r := submitHypothesis(t, p, sid, "h1", "memory leak in connection pool", 0)

	reflection, hasReflection := r.Data["reflection"]
	if !hasReflection {
		t.Fatal("expected reflection key in data when evidence is insufficient")
	}
	directive, ok := reflection.(*ReflectionDirective)
	if !ok {
		t.Fatalf("expected *ReflectionDirective, got %T", reflection)
	}
	if directive.Directive != "STOP" {
		t.Fatalf("expected directive=STOP, got %q", directive.Directive)
	}
}

// TestDebugging_NoGate: submit hypothesis with 4 findings → no reflection key.
func TestDebugging_NoGate(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	sid := "dbg-gate-2"

	// Submit 3 hypotheses first to build up session evidence.
	submitHypothesis(t, p, sid, "h1", "finding one", 0)
	submitHypothesis(t, p, sid, "h2", "finding two", 0)
	submitHypothesis(t, p, sid, "h3", "finding three", 0)

	// 4th submission: session now has 4 hypotheses — gate should pass.
	r := submitHypothesis(t, p, sid, "h4", "root cause identified", 0)

	if _, hasReflection := r.Data["reflection"]; hasReflection {
		t.Fatal("expected no reflection when evidence is sufficient")
	}
}

// TestDebugging_OverconfidenceWarning: confidence=0.95, 2 findings → VERIFY directive.
func TestDebugging_OverconfidenceWarning(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	sid := "dbg-overconf-1"

	// Submit two hypotheses to have >= 3 evidence... wait, we need < 3 to trigger gate,
	// but we need >= 3 to pass the gate and reach the overconfidence check.
	// Test: 2 findings total → evidence gate fires first (2 < 3), masking confidence check.
	// For overconfidence check: we need findingsCount >= 3 AND confidence > 0.8 AND evidenceCount < 5.
	// Submit exactly 3 hypotheses, then submit 4th with high confidence while count is still < 5.

	// Pre-populate 4 findings so gate passes (4 >= 3), then submit with confidence=0.95 (5th = count 5 → gate passes, confidence check: 5 < 5 = false).
	// We need count in range [3,4] after adding the hypothesis with confidence.
	// So pre-populate 2, then submit with confidence: after adding = 3 → gate passes (3>=3), confidence check: 3<5 → VERIFY.
	submitHypothesis(t, p, sid, "h1", "finding one", 0)
	submitHypothesis(t, p, sid, "h2", "finding two", 0)

	// 3rd submission with high confidence: count becomes 3, gate passes, overconfidence fires.
	r := submitHypothesis(t, p, sid, "h3", "definitely the root cause", 0.95)

	reflection, hasReflection := r.Data["reflection"]
	if !hasReflection {
		t.Fatal("expected reflection for overconfident hypothesis")
	}
	directive, ok := reflection.(*ReflectionDirective)
	if !ok {
		t.Fatalf("expected *ReflectionDirective, got %T", reflection)
	}
	if directive.Directive != "VERIFY" {
		t.Fatalf("expected directive=VERIFY, got %q", directive.Directive)
	}
}
