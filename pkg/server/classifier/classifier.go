// Package classifier provides deterministic task-class routing for the task MCP entry.
package classifier

import (
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/executor/types"
)

// Candidate is one ranked task_class option returned by the classifier.
type Candidate struct {
	TaskClass string  `json:"task_class"`
	Score     float64 `json:"score"`
}

// Classifier scores prompts against task classes.
type Classifier struct {
	threshold float64
}

// New constructs a classifier with the default confidence threshold.
func New() *Classifier {
	return &Classifier{threshold: DefaultThreshold}
}

// NewWithThreshold constructs a classifier with a custom confidence threshold.
func NewWithThreshold(threshold float64) *Classifier {
	return &Classifier{threshold: clamp01(threshold)}
}

// Classify scores prompt and returns ranked candidates plus the top score as confidence.
func Classify(prompt string) ([]Candidate, float64, error) {
	return New().Classify(prompt)
}

// Classify scores prompt and returns ranked candidates plus the top score as confidence.
func (c *Classifier) Classify(prompt string) ([]Candidate, float64, error) {
	candidates := score(prompt)
	if len(candidates) == 0 {
		return nil, 0, types.NewClassificationAmbiguous("classification ambiguous: no candidates", nil)
	}

	confidence := candidates[0].Score
	if confidence < c.threshold {
		top := topCandidates(candidates, 3)
		return top, confidence, types.NewClassificationAmbiguous(ambiguousMessage(top), nil)
	}

	return candidates, confidence, nil
}

func topCandidates(candidates []Candidate, n int) []Candidate {
	if n > len(candidates) {
		n = len(candidates)
	}
	out := make([]Candidate, n)
	copy(out, candidates[:n])
	return out
}

func ambiguousMessage(candidates []Candidate) string {
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, fmt.Sprintf("%s=%.2f", candidate.TaskClass, candidate.Score))
	}
	return "classification ambiguous; pass explicit task_class; top candidates: " + strings.Join(parts, ", ")
}
