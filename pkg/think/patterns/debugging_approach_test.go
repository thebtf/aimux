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

// TestDebugging_FlatHypothesis: hypothesis_text + confidence="medium" → hypothesis tracked in session.
func TestDebugging_FlatHypothesis(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	sid := "dbg-flat-hyp-1"

	inp, err := p.Validate(map[string]any{
		"issue":            "SQL injection in login endpoint",
		"hypothesis_text":  "SQL injection",
		"confidence":       "medium",
		"step_number":      float64(1),
		"next_step_needed": true,
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	// Verify flat params were translated.
	hyp, ok := inp["hypothesis"].(map[string]any)
	if !ok {
		t.Fatal("expected hypothesis map in validated input")
	}
	if hyp["text"] != "SQL injection" {
		t.Fatalf("expected hypothesis text=SQL injection, got %v", hyp["text"])
	}
	if hyp["confidence"] != 0.5 {
		t.Fatalf("expected confidence=0.5 (medium), got %v", hyp["confidence"])
	}
	if inp["step_number"] != 1 {
		t.Fatalf("expected step_number=1, got %v", inp["step_number"])
	}

	r, err := p.Handle(inp, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["step_number"] != 1 {
		t.Fatalf("expected step_number=1 in output, got %v", r.Data["step_number"])
	}
	if r.Data["next_step_needed"] != true {
		t.Fatalf("expected next_step_needed=true in output")
	}
	// Session should have 1 hypothesis.
	sess := think.GetSession(sid)
	hypotheses, _ := sess.State["hypotheses"].([]any)
	if len(hypotheses) != 1 {
		t.Fatalf("expected 1 hypothesis in session, got %d", len(hypotheses))
	}
}

// TestDebugging_FlatRefute: hypothesis_action="refute" → last hypothesis refuted.
func TestDebugging_FlatRefute(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	sid := "dbg-flat-refute-1"

	// First add a hypothesis via flat format.
	inp1, err := p.Validate(map[string]any{
		"issue":           "memory leak",
		"hypothesis_text": "connection pool not releasing",
		"confidence":      "low",
	})
	if err != nil {
		t.Fatalf("validate hypothesis: %v", err)
	}
	r1, err := p.Handle(inp1, sid)
	if err != nil {
		t.Fatalf("handle hypothesis: %v", err)
	}
	_ = r1

	// Retrieve the added hypothesis ID from session.
	sess := think.GetSession(sid)
	hypotheses, _ := sess.State["hypotheses"].([]any)
	if len(hypotheses) != 1 {
		t.Fatalf("expected 1 hypothesis before refute, got %d", len(hypotheses))
	}

	// Now refute via flat format.
	inp2, err := p.Validate(map[string]any{
		"issue":             "memory leak",
		"hypothesis_action": "refute",
	})
	if err != nil {
		t.Fatalf("validate refute: %v", err)
	}
	hu, ok := inp2["hypothesisUpdate"].(map[string]any)
	if !ok {
		t.Fatal("expected hypothesisUpdate in validated input")
	}
	if hu["id"] != "__last__" {
		t.Fatalf("expected __last__ sentinel, got %v", hu["id"])
	}
	if hu["status"] != "refuted" {
		t.Fatalf("expected status=refuted, got %v", hu["status"])
	}

	r2, err := p.Handle(inp2, sid)
	if err != nil {
		t.Fatalf("handle refute: %v", err)
	}
	if r2.Data["refutedCount"] != 1 {
		t.Fatalf("expected refutedCount=1, got %v", r2.Data["refutedCount"])
	}
}

// TestDebugging_FlatStepProgression: step_number=1 without findings → gate fires; step with 3 prior hypotheses → passes.
func TestDebugging_FlatStepProgression(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	sid := "dbg-flat-step-1"

	// Step 1: no prior findings → evidence gate should fire (STOP).
	inp1, err := p.Validate(map[string]any{
		"issue":           "crash on startup",
		"hypothesis_text": "nil pointer dereference",
		"confidence":      "exploring",
		"step_number":     float64(1),
	})
	if err != nil {
		t.Fatalf("validate step1: %v", err)
	}
	r1, err := p.Handle(inp1, sid)
	if err != nil {
		t.Fatalf("handle step1: %v", err)
	}
	if _, hasReflection := r1.Data["reflection"]; !hasReflection {
		t.Fatal("expected reflection (STOP) on step 1 without prior findings")
	}

	// Add 2 more hypotheses to reach count=3 → gate passes on 3rd.
	inp2, _ := p.Validate(map[string]any{"issue": "crash on startup", "hypothesis_text": "stack overflow", "confidence": "low"})
	p.Handle(inp2, sid) //nolint:errcheck

	// Step 3: 3 hypotheses total → gate should pass, no STOP.
	inp3, err := p.Validate(map[string]any{
		"issue":           "crash on startup",
		"hypothesis_text": "config parsing error",
		"confidence":      "medium",
		"step_number":     float64(3),
		"findings_text":   "found null dereference at line 42",
	})
	if err != nil {
		t.Fatalf("validate step3: %v", err)
	}
	r3, err := p.Handle(inp3, sid)
	if err != nil {
		t.Fatalf("handle step3: %v", err)
	}
	if r3.Data["findings_text"] != "found null dereference at line 42" {
		t.Fatalf("expected findings_text in output, got %v", r3.Data["findings_text"])
	}
	if r3.Data["step_number"] != 3 {
		t.Fatalf("expected step_number=3, got %v", r3.Data["step_number"])
	}
}

// TestDebugging_BackwardCompat: old nested hypothesis map still works.
func TestDebugging_BackwardCompat(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	sid := "dbg-compat-1"

	inp, err := p.Validate(map[string]any{
		"issue": "login fails for admin",
		"hypothesis": map[string]any{
			"id":         "h-legacy-1",
			"text":       "session token expired",
			"confidence": 0.7,
		},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	hyp, ok := inp["hypothesis"].(map[string]any)
	if !ok {
		t.Fatal("expected hypothesis in validated input")
	}
	if hyp["id"] != "h-legacy-1" {
		t.Fatalf("expected id=h-legacy-1, got %v", hyp["id"])
	}

	r, err := p.Handle(inp, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["hypothesisCount"] != 1 {
		t.Fatalf("expected hypothesisCount=1, got %v", r.Data["hypothesisCount"])
	}
}

// TestDebugging_ConfidenceEnumMapping: all 6 enum values map to correct floats.
func TestDebugging_ConfidenceEnumMapping(t *testing.T) {
	cases := []struct {
		enum     string
		expected float64
	}{
		{"exploring", 0.1},
		{"low", 0.2},
		{"medium", 0.5},
		{"high", 0.7},
		{"very_high", 0.85},
		{"certain", 0.95},
	}
	for _, tc := range cases {
		got := confidenceEnumToFloat(tc.enum)
		if got != tc.expected {
			t.Errorf("confidenceEnumToFloat(%q) = %v, want %v", tc.enum, got, tc.expected)
		}
	}
}

// TestDebuggingApproach_MethodEfficiency: verify per-approach efficiency scoring.
func TestDebuggingApproach_MethodEfficiency(t *testing.T) {
	think.ClearSessions()
	p := NewDebuggingApproachPattern()
	sid := "test-method-eff"

	// Step 1: binary_search, propose hypothesis
	input1, _ := p.Validate(map[string]any{
		"issue":             "API returns 500",
		"approachName":     "binary_search",
		"hypothesis_text":   "Database connection pool exhausted",
		"hypothesis_action": "propose",
	})
	p.Handle(input1, sid) //nolint:errcheck

	// Step 2: binary_search, confirm hypothesis
	input2, _ := p.Validate(map[string]any{
		"issue":             "API returns 500",
		"approachName":     "binary_search",
		"hypothesis_action": "confirm",
	})
	p.Handle(input2, sid) //nolint:errcheck

	// Step 3: trace, propose hypothesis
	input3, _ := p.Validate(map[string]any{
		"issue":             "API returns 500",
		"approachName":     "trace",
		"hypothesis_text":   "Middleware timeout",
		"hypothesis_action": "propose",
	})
	p.Handle(input3, sid) //nolint:errcheck

	// Step 4: trace, refute hypothesis
	input4, _ := p.Validate(map[string]any{
		"issue":             "API returns 500",
		"approachName":     "trace",
		"hypothesis_action": "refute",
	})
	result, err := p.Handle(input4, sid)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	eff, ok := result.Data["methodEfficiency"].(map[string]float64)
	if !ok {
		t.Fatalf("methodEfficiency not found or wrong type: %v (%T)", result.Data["methodEfficiency"], result.Data["methodEfficiency"])
	}
	// binary_search: 1 confirmed, 0 refuted → 1.0
	if eff["binary_search"] != 1.0 {
		t.Errorf("binary_search efficiency = %v, want 1.0", eff["binary_search"])
	}
	// trace: 0 confirmed, 1 refuted → 0.0
	if eff["trace"] != 0.0 {
		t.Errorf("trace efficiency = %v, want 0.0", eff["trace"])
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
