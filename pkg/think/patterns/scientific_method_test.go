package patterns

import (
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
)

// TestScientific_EntryChain verifies that hypothesis → prediction → experiment entries
// chain correctly: each entry gets the expected ID and linkedTo is preserved.
func TestScientific_EntryChain(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "sci-chain-1"

	// Step 1: add hypothesis
	inp1, _ := p.Validate(map[string]any{
		"stage": "hypothesis",
		"entry": map[string]any{"type": "hypothesis", "text": "plants grow faster with more light"},
	})
	r1, err := p.Handle(inp1, sid)
	if err != nil {
		t.Fatalf("hypothesis entry: %v", err)
	}
	h := r1.Data["entry"].(map[string]any)
	if h["id"] != "E-1" {
		t.Fatalf("expected hypothesis id=E-1, got %v", h["id"])
	}
	if h["type"] != "hypothesis" {
		t.Fatalf("expected type=hypothesis, got %v", h["type"])
	}

	// Step 2: add prediction linked to hypothesis
	inp2, _ := p.Validate(map[string]any{
		"stage": "hypothesis",
		"entry": map[string]any{"type": "prediction", "text": "plants in high-light will be 20% taller", "linkedTo": "E-1"},
	})
	r2, err := p.Handle(inp2, sid)
	if err != nil {
		t.Fatalf("prediction entry: %v", err)
	}
	pred := r2.Data["entry"].(map[string]any)
	if pred["id"] != "E-2" {
		t.Fatalf("expected prediction id=E-2, got %v", pred["id"])
	}
	if pred["linkedTo"] != "E-1" {
		t.Fatalf("expected prediction linkedTo=E-1, got %v", pred["linkedTo"])
	}

	// Step 3: add experiment linked to prediction
	inp3, _ := p.Validate(map[string]any{
		"stage": "experiment",
		"entry": map[string]any{"type": "experiment", "text": "grow plants under different light conditions", "linkedTo": "E-2"},
	})
	r3, err := p.Handle(inp3, sid)
	if err != nil {
		t.Fatalf("experiment entry: %v", err)
	}
	exp := r3.Data["entry"].(map[string]any)
	if exp["id"] != "E-3" {
		t.Fatalf("expected experiment id=E-3, got %v", exp["id"])
	}
	if exp["linkedTo"] != "E-2" {
		t.Fatalf("expected experiment linkedTo=E-2, got %v", exp["linkedTo"])
	}

	// Verify entryCount in last result
	count := r3.Data["entryCount"].(map[string]int)
	if count["hypothesis"] != 1 {
		t.Fatalf("expected 1 hypothesis, got %d", count["hypothesis"])
	}
	if count["prediction"] != 1 {
		t.Fatalf("expected 1 prediction, got %d", count["prediction"])
	}
	if count["experiment"] != 1 {
		t.Fatalf("expected 1 experiment, got %d", count["experiment"])
	}
}

// TestScientific_UntestedHypothesis verifies that a hypothesis with no linked prediction
// is detected and reported in untestedHypotheses.
func TestScientific_UntestedHypothesis(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "sci-untested-1"

	// Add hypothesis with no prediction
	inp1, _ := p.Validate(map[string]any{
		"stage": "hypothesis",
		"entry": map[string]any{"type": "hypothesis", "text": "untested idea"},
	})
	r1, err := p.Handle(inp1, sid)
	if err != nil {
		t.Fatalf("hypothesis entry: %v", err)
	}

	untested := r1.Data["untestedHypotheses"].([]string)
	if len(untested) != 1 {
		t.Fatalf("expected 1 untested hypothesis, got %d", len(untested))
	}

	// Add a prediction linked to the hypothesis — it should now be tested
	inp2, _ := p.Validate(map[string]any{
		"stage": "hypothesis",
		"entry": map[string]any{"type": "prediction", "text": "we predict X", "linkedTo": "E-1"},
	})
	r2, err := p.Handle(inp2, sid)
	if err != nil {
		t.Fatalf("prediction entry: %v", err)
	}

	untested2 := r2.Data["untestedHypotheses"].([]string)
	if len(untested2) != 0 {
		t.Fatalf("expected 0 untested hypotheses after adding prediction, got %d: %v", len(untested2), untested2)
	}

	// Add a second hypothesis with no prediction — should appear as untested again
	inp3, _ := p.Validate(map[string]any{
		"stage": "hypothesis",
		"entry": map[string]any{"type": "hypothesis", "text": "another unlinked hypothesis"},
	})
	r3, err := p.Handle(inp3, sid)
	if err != nil {
		t.Fatalf("second hypothesis entry: %v", err)
	}

	untested3 := r3.Data["untestedHypotheses"].([]string)
	if len(untested3) != 1 {
		t.Fatalf("expected 1 untested hypothesis for second entry, got %d: %v", len(untested3), untested3)
	}
}

// TestScientific_StageProgression verifies that stages advance correctly through the
// lifecycle and stageHistoryLen tracks all calls.
func TestScientific_StageProgression(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "sci-stages-1"

	lifecycle := []string{"observation", "question", "hypothesis", "experiment", "analysis", "conclusion"}

	for i, stage := range lifecycle {
		inp, _ := p.Validate(map[string]any{"stage": stage})
		r, err := p.Handle(inp, sid)
		if err != nil {
			t.Fatalf("stage %s: %v", stage, err)
		}
		if r.Data["stage"] != stage {
			t.Fatalf("step %d: expected stage=%s, got %v", i+1, stage, r.Data["stage"])
		}
		if r.Data["stageHistoryLen"] != i+1 {
			t.Fatalf("step %d: expected stageHistoryLen=%d, got %v", i+1, i+1, r.Data["stageHistoryLen"])
		}
		// All non-final stages should suggest continuing with scientific_method
		if stage != "iteration" && r.SuggestedNextPattern != "scientific_method" {
			t.Fatalf("step %d: expected suggestedNextPattern=scientific_method, got %q", i+1, r.SuggestedNextPattern)
		}
	}

	// Verify session persisted all stages
	sess := think.GetSession(sid)
	if sess == nil {
		t.Fatal("session not found after progression")
	}
	history, _ := sess.State["stageHistory"].([]any)
	if len(history) != len(lifecycle) {
		t.Fatalf("expected %d stages in stageHistory, got %d", len(lifecycle), len(history))
	}
	for i, stage := range lifecycle {
		if history[i] != stage {
			t.Fatalf("stageHistory[%d]: expected %s, got %v", i, stage, history[i])
		}
	}
}

// TestScientific_PredictionWithoutHypothesis: submitting a prediction entry with empty session → error.
func TestScientific_PredictionWithoutHypothesis(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "sci-pred-nohyp-1"

	// Try to submit a prediction entry without any prior hypothesis in the session.
	// The entry validation will catch missing linkedTo first — so we need to test
	// the session-level gate by bypassing the linkedTo rule would require a hypothesis.
	// Instead, directly validate a hypothesis entry without linkedTo but entry type=prediction
	// is rejected by Validate. We test the Handle-level gate via a mock or by first adding
	// a hypothesis entry and then trying to add prediction without linkedTo to a fresh session.
	//
	// Simplest valid path: pass a prediction entry that would link to a nonexistent hypothesis.
	// But validateEntryLink fires first. The session-level gate fires BEFORE validateEntryLink.
	//
	// Build validInput manually to bypass Validate — Handle receives already-validated input.
	validInput := map[string]any{
		"stage": "hypothesis",
		"entry": map[string]any{"type": "prediction", "text": "a prediction with no hypothesis in session"},
	}
	_, err := p.Handle(validInput, sid)
	if err == nil {
		t.Fatal("expected error when submitting prediction entry without a prior hypothesis in session")
	}
	if len(err.Error()) == 0 {
		t.Fatal("expected non-empty error message")
	}
}

// TestScientific_ExperimentWithoutPrediction: experiment entry with hypothesis but no prediction → error.
func TestScientific_ExperimentWithoutPrediction(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "sci-exp-nopred-1"

	// Add a hypothesis entry first (valid path through Validate+Handle).
	inp1, err := p.Validate(map[string]any{
		"stage": "hypothesis",
		"entry": map[string]any{"type": "hypothesis", "text": "some hypothesis"},
	})
	if err != nil {
		t.Fatalf("validate hypothesis: %v", err)
	}
	_, err = p.Handle(inp1, sid)
	if err != nil {
		t.Fatalf("hypothesis submission: %v", err)
	}

	// Now try to submit an experiment entry without any prediction in session.
	// Pass validInput directly to test the session-level gate.
	validInput := map[string]any{
		"stage": "experiment",
		"entry": map[string]any{"type": "experiment", "text": "run the test", "linkedTo": "E-1"},
	}
	_, err = p.Handle(validInput, sid)
	if err == nil {
		t.Fatal("expected error when submitting experiment entry without a prior prediction in session")
	}
}

// TestScientific_FlatEntry: entry_type="hypothesis" + entry_text → entry stored with auto-ID.
func TestScientific_FlatEntry(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "sci-flat-entry-1"

	inp, err := p.Validate(map[string]any{
		"stage":      "hypothesis",
		"entry_type": "hypothesis",
		"entry_text": "increased temperature accelerates reaction",
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	// Should have an entry map in validated input.
	entry, ok := inp["entry"].(map[string]any)
	if !ok {
		t.Fatal("expected entry map in validated input")
	}
	if entry["type"] != "hypothesis" {
		t.Fatalf("expected entry type=hypothesis, got %v", entry["type"])
	}
	if entry["text"] != "increased temperature accelerates reaction" {
		t.Fatalf("unexpected text: %v", entry["text"])
	}
	if _, hasAutoLink := entry["autoLink"]; !hasAutoLink {
		t.Fatal("expected autoLink sentinel in flat entry when no link_to provided")
	}

	r, err := p.Handle(inp, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	added, ok := r.Data["entry"].(map[string]any)
	if !ok {
		t.Fatal("expected entry in result data")
	}
	if added["id"] != "E-1" {
		t.Fatalf("expected id=E-1, got %v", added["id"])
	}
	if added["type"] != "hypothesis" {
		t.Fatalf("expected type=hypothesis, got %v", added["type"])
	}
}

// TestScientific_FlatAutoLink: entry_type="prediction" → auto-linked to last hypothesis.
func TestScientific_FlatAutoLink(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "sci-flat-autolink-1"

	// Add hypothesis first.
	inp1, _ := p.Validate(map[string]any{
		"stage":      "hypothesis",
		"entry_type": "hypothesis",
		"entry_text": "plants grow faster with blue light",
	})
	r1, err := p.Handle(inp1, sid)
	if err != nil {
		t.Fatalf("hypothesis: %v", err)
	}
	hypEntry := r1.Data["entry"].(map[string]any)
	hypID := hypEntry["id"].(string) // should be "E-1"

	// Add prediction without link_to → should auto-link to E-1.
	inp2, err := p.Validate(map[string]any{
		"stage":      "hypothesis",
		"entry_type": "prediction",
		"entry_text": "yield will be 30% higher under blue light",
	})
	if err != nil {
		t.Fatalf("validate prediction: %v", err)
	}
	r2, err := p.Handle(inp2, sid)
	if err != nil {
		t.Fatalf("prediction: %v", err)
	}
	predEntry := r2.Data["entry"].(map[string]any)
	if predEntry["linkedTo"] != hypID {
		t.Fatalf("expected prediction linked to %s, got %v", hypID, predEntry["linkedTo"])
	}
}

// TestScientific_FlatLifecycleGate: entry_type="prediction" without prior hypothesis → STOP.
func TestScientific_FlatLifecycleGate(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "sci-flat-gate-1"

	inp, err := p.Validate(map[string]any{
		"stage":      "hypothesis",
		"entry_type": "prediction",
		"entry_text": "some prediction with no hypothesis in session",
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	_, err = p.Handle(inp, sid)
	if err == nil {
		t.Fatal("expected error: prediction requires prior hypothesis in session")
	}
}

// TestScientificMethod_HypothesisSurvivalRate verifies that hypothesisSurvivalRate equals 1.0
// when a hypothesis has a complete hypothesis → prediction → experiment → result chain.
func TestScientificMethod_HypothesisSurvivalRate(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "test-survival"

	// Hypothesis (will be assigned id E-1)
	i1, err := p.Validate(map[string]any{
		"stage":      "hypothesis",
		"entry_type": "hypothesis",
		"entry_text": "Cache invalidation is wrong",
	})
	if err != nil {
		t.Fatalf("validate hypothesis: %v", err)
	}
	if _, err = p.Handle(i1, sid); err != nil {
		t.Fatalf("handle hypothesis: %v", err)
	}

	// Prediction linked to E-1 (will be assigned id E-2)
	i2, err := p.Validate(map[string]any{
		"stage":      "hypothesis",
		"entry_type": "prediction",
		"entry_text": "Clearing cache should fix 500s",
		"link_to":    "E-1",
	})
	if err != nil {
		t.Fatalf("validate prediction: %v", err)
	}
	if _, err = p.Handle(i2, sid); err != nil {
		t.Fatalf("handle prediction: %v", err)
	}

	// Experiment linked to E-2 (will be assigned id E-3)
	i3, err := p.Validate(map[string]any{
		"stage":      "experiment",
		"entry_type": "experiment",
		"entry_text": "Cleared cache, monitored errors",
		"link_to":    "E-2",
	})
	if err != nil {
		t.Fatalf("validate experiment: %v", err)
	}
	if _, err = p.Handle(i3, sid); err != nil {
		t.Fatalf("handle experiment: %v", err)
	}

	// Result linked to E-3 (will be assigned id E-4)
	i4, err := p.Validate(map[string]any{
		"stage":      "analysis",
		"entry_type": "result",
		"entry_text": "500s dropped to zero",
		"link_to":    "E-3",
	})
	if err != nil {
		t.Fatalf("validate result: %v", err)
	}
	result, err := p.Handle(i4, sid)
	if err != nil {
		t.Fatalf("handle result: %v", err)
	}

	rate, ok := result.Data["hypothesisSurvivalRate"].(float64)
	if !ok {
		t.Fatalf("hypothesisSurvivalRate not in data: %v", result.Data)
	}
	if rate != 1.0 {
		t.Errorf("hypothesisSurvivalRate = %v, want 1.0 (1 survived out of 1)", rate)
	}

	survived, ok := result.Data["survivedHypotheses"].([]string)
	if !ok {
		t.Fatalf("survivedHypotheses not in data: %v", result.Data)
	}
	if len(survived) != 1 || survived[0] != "E-1" {
		t.Errorf("survivedHypotheses = %v, want [E-1]", survived)
	}
}

// TestScientificMethod_HypothesisSurvivalRate_Partial verifies that rate reflects partial
// completion: one hypothesis survived, one did not.
func TestScientificMethod_HypothesisSurvivalRate_Partial(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "test-survival-partial"

	// Hypothesis 1 (E-1) — will get a full chain
	i1, _ := p.Validate(map[string]any{"stage": "hypothesis", "entry_type": "hypothesis", "entry_text": "H1"})
	p.Handle(i1, sid)

	// Hypothesis 2 (E-2) — will have no prediction
	i2, _ := p.Validate(map[string]any{"stage": "hypothesis", "entry_type": "hypothesis", "entry_text": "H2 untested"})
	p.Handle(i2, sid)

	// Prediction for H1 → E-1 (E-3)
	i3, _ := p.Validate(map[string]any{"stage": "hypothesis", "entry_type": "prediction", "entry_text": "P1", "link_to": "E-1"})
	p.Handle(i3, sid)

	// Experiment for prediction E-3 (E-4)
	i4, _ := p.Validate(map[string]any{"stage": "experiment", "entry_type": "experiment", "entry_text": "Exp1", "link_to": "E-3"})
	p.Handle(i4, sid)

	// Result for experiment E-4 (E-5)
	i5, _ := p.Validate(map[string]any{"stage": "analysis", "entry_type": "result", "entry_text": "R1", "link_to": "E-4"})
	result, err := p.Handle(i5, sid)
	if err != nil {
		t.Fatalf("handle result: %v", err)
	}

	rate, ok := result.Data["hypothesisSurvivalRate"].(float64)
	if !ok {
		t.Fatalf("hypothesisSurvivalRate not in data")
	}
	// 1 out of 2 hypotheses survived → 0.5
	if rate != 0.5 {
		t.Errorf("hypothesisSurvivalRate = %v, want 0.5", rate)
	}

	survived, ok := result.Data["survivedHypotheses"].([]string)
	if !ok {
		t.Fatalf("survivedHypotheses not in data")
	}
	if len(survived) != 1 || survived[0] != "E-1" {
		t.Errorf("survivedHypotheses = %v, want [E-1]", survived)
	}
}

// TestScientificMethod_HypothesisSurvivalRate_NoHypotheses verifies that rate is 0.0
// when there are no hypotheses in the session.
func TestScientificMethod_HypothesisSurvivalRate_NoHypotheses(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "test-survival-empty"

	i, _ := p.Validate(map[string]any{"stage": "observation"})
	result, err := p.Handle(i, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	rate, ok := result.Data["hypothesisSurvivalRate"].(float64)
	if !ok {
		t.Fatalf("hypothesisSurvivalRate not in data")
	}
	if rate != 0.0 {
		t.Errorf("hypothesisSurvivalRate = %v, want 0.0", rate)
	}

	survived, ok := result.Data["survivedHypotheses"].([]string)
	if !ok {
		// nil slice is also acceptable — check if field exists at all
		if _, exists := result.Data["survivedHypotheses"]; !exists {
			t.Fatal("survivedHypotheses missing from data")
		}
	}
	if len(survived) != 0 {
		t.Errorf("survivedHypotheses = %v, want empty", survived)
	}
}

// TestScientific_BackwardCompat: old nested entry map still works.
func TestScientific_BackwardCompat(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "sci-compat-1"

	inp, err := p.Validate(map[string]any{
		"stage": "hypothesis",
		"entry": map[string]any{
			"type": "hypothesis",
			"text": "old nested format hypothesis",
		},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	r, err := p.Handle(inp, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	added, ok := r.Data["entry"].(map[string]any)
	if !ok {
		t.Fatal("expected entry in result data")
	}
	if added["type"] != "hypothesis" {
		t.Fatalf("expected type=hypothesis, got %v", added["type"])
	}
	if added["text"] != "old nested format hypothesis" {
		t.Fatalf("unexpected text: %v", added["text"])
	}
}

// TestScientific_CorrectChain: hypothesis entry → prediction entry → no STOP (correct sequence).
func TestScientific_CorrectChain(t *testing.T) {
	think.ClearSessions()
	p := NewScientificMethodPattern()
	sid := "sci-chain-correct-1"

	// Step 1: add hypothesis entry.
	inp1, err := p.Validate(map[string]any{
		"stage": "hypothesis",
		"entry": map[string]any{"type": "hypothesis", "text": "plants grow faster with more light"},
	})
	if err != nil {
		t.Fatalf("validate hypothesis: %v", err)
	}
	_, err = p.Handle(inp1, sid)
	if err != nil {
		t.Fatalf("hypothesis: %v", err)
	}

	// Step 2: add prediction entry linked to hypothesis — should succeed.
	inp2, err := p.Validate(map[string]any{
		"stage": "hypothesis",
		"entry": map[string]any{"type": "prediction", "text": "plants will be 20% taller", "linkedTo": "E-1"},
	})
	if err != nil {
		t.Fatalf("validate prediction: %v", err)
	}
	_, err = p.Handle(inp2, sid)
	if err != nil {
		t.Fatalf("prediction: unexpected error: %v", err)
	}
}
