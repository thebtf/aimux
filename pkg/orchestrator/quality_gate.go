package orchestrator

import (
	"sync"

	"github.com/thebtf/aimux/pkg/executor"
)

const defaultMaxRetries = 2

// QualityAction is the result of a quality gate evaluation.
type QualityAction string

const (
	QualityContinue QualityAction = "continue"
	QualityRetry    QualityAction = "retry"
	QualityEscalate QualityAction = "escalate"
	QualityHalt     QualityAction = "halt"
)

// QualityGate evaluates CLI output quality and decides whether to continue,
// retry, escalate, or halt. Tracks per-participant retry counts.
type QualityGate struct {
	maxRetries int
	mu         sync.Mutex
	retries    map[string]int // cli → retry count
}

// NewQualityGate creates a quality gate with the given max retries per participant.
func NewQualityGate(maxRetries int) *QualityGate {
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	return &QualityGate{
		maxRetries: maxRetries,
		retries:    make(map[string]int),
	}
}

// Evaluate checks the output quality for a CLI and returns the appropriate action.
func (qg *QualityGate) Evaluate(cli, content, stderr string, exitCode int) QualityAction {
	validation := executor.ValidateTurnContent(content, stderr, exitCode)

	if validation.Valid && len(validation.Warnings) == 0 {
		return QualityContinue
	}

	// Check for refusal (warning-level)
	for _, w := range validation.Warnings {
		if w == "possible refusal detected" {
			return QualityEscalate
		}
	}

	// Valid with non-refusal warnings → continue
	if validation.Valid {
		return QualityContinue
	}

	// Invalid → check retry budget
	qg.mu.Lock()
	defer qg.mu.Unlock()

	qg.retries[cli]++
	if qg.retries[cli] > qg.maxRetries {
		return QualityHalt
	}

	return QualityRetry
}

// Reset clears retry counters for all participants.
func (qg *QualityGate) Reset() {
	qg.mu.Lock()
	defer qg.mu.Unlock()
	qg.retries = make(map[string]int)
}
