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
