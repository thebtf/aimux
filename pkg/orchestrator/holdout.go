package orchestrator

import (
	"math/rand/v2"
)

// HoldoutEvaluation implements 80/20 scenario split for autonomous pipeline testing.
// ADR-014 Decision 20: blind test after implementation, weighted scoring.
type HoldoutEvaluation struct {
	TrainRatio float64 // default 0.8
}

// NewHoldoutEvaluation creates an evaluator with default 80/20 split.
func NewHoldoutEvaluation() *HoldoutEvaluation {
	return &HoldoutEvaluation{TrainRatio: 0.8}
}

// Split divides scenarios into train (implement) and test (holdout) sets.
func (h *HoldoutEvaluation) Split(scenarios []string) (train, test []string) {
	// Shuffle for random split
	shuffled := make([]string, len(scenarios))
	copy(shuffled, scenarios)
	rand.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	splitIdx := int(float64(len(shuffled)) * h.TrainRatio)
	if splitIdx == len(shuffled) && len(shuffled) > 1 {
		splitIdx-- // ensure at least 1 test scenario
	}

	return shuffled[:splitIdx], shuffled[splitIdx:]
}

// Score calculates a weighted score for holdout evaluation results.
type Score struct {
	Passed  int     `json:"passed"`
	Failed  int     `json:"failed"`
	Total   int     `json:"total"`
	Percent float64 `json:"percent"`
}

// Evaluate scores the holdout test results.
func (h *HoldoutEvaluation) Evaluate(results map[string]bool) Score {
	s := Score{Total: len(results)}
	for _, passed := range results {
		if passed {
			s.Passed++
		} else {
			s.Failed++
		}
	}
	if s.Total > 0 {
		s.Percent = float64(s.Passed) / float64(s.Total) * 100
	}
	return s
}
