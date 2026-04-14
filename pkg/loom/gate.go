package loom

import (
	"strings"
	"sync"
)

// GateDecision is the result of a quality gate evaluation.
type GateDecision struct {
	Accept bool   `json:"accept"`
	Reason string `json:"reason"` // "pass", "empty_output", "rate_limit_error", "thrashing"
	Retry  bool   `json:"retry"`  // if !Accept && Retry → status retrying, re-dispatch
}

// QualityGate validates worker results.
type QualityGate struct {
	threshold  float64 // Jaccard similarity threshold for thrashing detection
	windowSize int     // number of recent results to track
	mu         sync.Mutex
	history    map[string][]string // taskID → recent results
}

// NewQualityGate creates a quality gate with defaults (threshold=0.8, window=3).
func NewQualityGate() *QualityGate {
	return &QualityGate{
		threshold:  0.8,
		windowSize: 3,
		history:    make(map[string][]string),
	}
}

// QualityGateOption configures the quality gate.
type QualityGateOption func(*QualityGate)

// WithThreshold sets the Jaccard similarity threshold.
func WithThreshold(t float64) QualityGateOption {
	return func(g *QualityGate) { g.threshold = t }
}

// WithWindowSize sets the thrashing detection window.
func WithWindowSize(n int) QualityGateOption {
	return func(g *QualityGate) { g.windowSize = n }
}

// NewQualityGateWithOpts creates a quality gate with options.
func NewQualityGateWithOpts(opts ...QualityGateOption) *QualityGate {
	g := NewQualityGate()
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Check evaluates a worker result.
func (g *QualityGate) Check(task *Task, result *WorkerResult) GateDecision {
	content := strings.TrimSpace(result.Content)

	// 1. Empty output → reject, retry.
	if content == "" {
		return GateDecision{Accept: false, Reason: "empty_output", Retry: true}
	}

	// 2. Rate limit error detection.
	lower := strings.ToLower(content)
	if isRateLimitError(lower) {
		return GateDecision{Accept: false, Reason: "rate_limit_error", Retry: true}
	}

	// 3. Thrashing detection — if last N results are too similar.
	// Must happen BEFORE recordResult so history reflects only previously accepted results.
	if g.isThrashing(task.ID, content) {
		return GateDecision{Accept: false, Reason: "thrashing", Retry: false}
	}

	// 4. Content passes all checks — record and accept.
	g.recordResult(task.ID, content)
	return GateDecision{Accept: true, Reason: "pass", Retry: false}
}

// isRateLimitError checks for common rate limit patterns in content.
func isRateLimitError(lower string) bool {
	patterns := []string{
		"rate limit",
		"rate_limit",
		"too many requests",
		"429",
		"quota exceeded",
		"throttled",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// isThrashing detects whether the last windowSize results (including current)
// are ALL similar to each other. Requires at least (windowSize-1) prior results
// before activating, so the first windowSize-1 results are always accepted.
// Returns true only if ALL prior results in the window exceed the Jaccard
// similarity threshold compared to the current content.
func (g *QualityGate) isThrashing(taskID, content string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	history := g.history[taskID]
	if len(history) < g.windowSize-1 {
		// Not enough history to detect thrashing.
		return false
	}

	// Examine the last (windowSize-1) accepted results.
	recent := history
	if len(recent) > g.windowSize-1 {
		recent = recent[len(recent)-(g.windowSize-1):]
	}

	// ALL recent entries must be similar to current content → thrashing.
	for _, prev := range recent {
		if jaccardWordSimilarity(prev, content) <= g.threshold {
			return false
		}
	}
	return true
}

// recordResult adds content to the history for a task.
func (g *QualityGate) recordResult(taskID, content string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.history[taskID] = append(g.history[taskID], content)
	// Keep only the last windowSize entries.
	if len(g.history[taskID]) > g.windowSize {
		g.history[taskID] = g.history[taskID][len(g.history[taskID])-g.windowSize:]
	}
}

// jaccardWordSimilarity computes |intersection|/|union| of word sets.
// Returns 0.0 for completely different, 1.0 for identical word sets.
func jaccardWordSimilarity(a, b string) float64 {
	wordsA := wordSet(a)
	wordsB := wordSet(b)

	if len(wordsA) == 0 && len(wordsB) == 0 {
		return 1.0 // both empty = identical
	}
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0.0
	}

	intersection := 0
	for w := range wordsA {
		if wordsB[w] {
			intersection++
		}
	}

	union := len(wordsA)
	for w := range wordsB {
		if !wordsA[w] {
			union++
		}
	}

	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}

// wordSet splits text into whitespace-delimited lowercase words.
func wordSet(s string) map[string]bool {
	words := strings.Fields(strings.ToLower(s))
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}
