// Package patterns provides the 23 thinking pattern implementations.
package patterns

import (
	"sync"

	think "github.com/thebtf/aimux/pkg/think"
)

var registerOnce sync.Once

// RegisterAll registers all 23 thinking patterns with the global registry.
// Safe to call multiple times — registration happens only once.
func RegisterAll() {
	registerOnce.Do(func() {
		registerAll()
	})
}

func registerAll() {
	register(NewCriticalThinkingPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: true,
	})
	register(NewSequentialThinkingPattern(), think.PatternMeta{
		IsStateful:      true,
		HasDialogConfig: false,
	})
	register(NewScientificMethodPattern(), think.PatternMeta{
		IsStateful:      true,
		HasDialogConfig: true,
	})
	register(NewDecisionFrameworkPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: true,
	})
	register(NewProblemDecompositionPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: true,
	})
	register(NewDebuggingApproachPattern(), think.PatternMeta{
		IsStateful:      true,
		HasDialogConfig: true,
	})
	register(NewMentalModelPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: true,
	})
	register(NewMetacognitiveMonitoringPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: true,
	})
	register(NewStructuredArgumentationPattern(), think.PatternMeta{
		IsStateful:      true,
		HasDialogConfig: true,
	})
	register(NewCollaborativeReasoningPattern(), think.PatternMeta{
		IsStateful:      true,
		HasDialogConfig: true,
	})
	register(NewRecursiveThinkingPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: false,
	})
	register(NewDomainModelingPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: true,
	})
	register(NewArchitectureAnalysisPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: true,
	})
	register(NewStochasticAlgorithmPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: false,
	})
	register(NewTemporalThinkingPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: true,
	})
	register(NewVisualReasoningPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: false,
	})
	register(NewSourceComparisonPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: false,
	})
	register(NewLiteratureReviewPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: false,
	})
	register(NewPeerReviewPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: false,
	})
	register(NewReplicationAnalysisPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: false,
	})
	register(NewExperimentalLoopPattern(), think.PatternMeta{
		IsStateful:      true,
		HasDialogConfig: false,
	})
	register(NewResearchSynthesisPattern(), think.PatternMeta{
		IsStateful:      false,
		HasDialogConfig: false,
	})
}

// register is a helper that registers both the handler and its metadata atomically.
func register(handler think.PatternHandler, meta think.PatternMeta) {
	think.RegisterPattern(handler)
	think.RegisterPatternMeta(handler.Name(), meta)
}
