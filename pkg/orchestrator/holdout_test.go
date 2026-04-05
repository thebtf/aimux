package orchestrator_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/orchestrator"
)

func TestHoldoutEvaluation_Split(t *testing.T) {
	h := orchestrator.NewHoldoutEvaluation()

	scenarios := []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9", "s10"}
	train, test := h.Split(scenarios)

	if len(train) != 8 {
		t.Errorf("train = %d, want 8 (80%%)", len(train))
	}
	if len(test) != 2 {
		t.Errorf("test = %d, want 2 (20%%)", len(test))
	}

	// Verify no duplicates
	seen := make(map[string]bool)
	for _, s := range train {
		seen[s] = true
	}
	for _, s := range test {
		if seen[s] {
			t.Errorf("duplicate scenario %q in both train and test", s)
		}
	}
}

func TestHoldoutEvaluation_SplitSmall(t *testing.T) {
	h := orchestrator.NewHoldoutEvaluation()

	train, test := h.Split([]string{"s1", "s2"})
	if len(test) < 1 {
		t.Error("should have at least 1 test scenario")
	}
	if len(train)+len(test) != 2 {
		t.Error("total should be 2")
	}
}

func TestHoldoutEvaluation_Evaluate(t *testing.T) {
	h := orchestrator.NewHoldoutEvaluation()

	results := map[string]bool{
		"s1": true,
		"s2": true,
		"s3": false,
		"s4": true,
	}

	score := h.Evaluate(results)
	if score.Passed != 3 {
		t.Errorf("Passed = %d, want 3", score.Passed)
	}
	if score.Failed != 1 {
		t.Errorf("Failed = %d, want 1", score.Failed)
	}
	if score.Percent != 75.0 {
		t.Errorf("Percent = %f, want 75", score.Percent)
	}
}
