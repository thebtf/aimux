// Package patterns provides the 17 thinking pattern implementations.
package patterns

import (
	"sync"

	think "github.com/thebtf/aimux/pkg/think"
)

var registerOnce sync.Once

// RegisterAll registers all 17 thinking patterns with the global registry.
// Safe to call multiple times — registration happens only once.
func RegisterAll() {
	registerOnce.Do(func() {
		registerAll()
	})
}

func registerAll() {
	think.RegisterPattern(NewThinkPattern())
	think.RegisterPattern(NewCriticalThinkingPattern())
	think.RegisterPattern(NewSequentialThinkingPattern())
	think.RegisterPattern(NewScientificMethodPattern())
	think.RegisterPattern(NewDecisionFrameworkPattern())
	think.RegisterPattern(NewProblemDecompositionPattern())
	think.RegisterPattern(NewDebuggingApproachPattern())
	think.RegisterPattern(NewMentalModelPattern())
	think.RegisterPattern(NewMetacognitiveMonitoringPattern())
	think.RegisterPattern(NewStructuredArgumentationPattern())
	think.RegisterPattern(NewCollaborativeReasoningPattern())
	think.RegisterPattern(NewRecursiveThinkingPattern())
	think.RegisterPattern(NewDomainModelingPattern())
	think.RegisterPattern(NewArchitectureAnalysisPattern())
	think.RegisterPattern(NewStochasticAlgorithmPattern())
	think.RegisterPattern(NewTemporalThinkingPattern())
	think.RegisterPattern(NewVisualReasoningPattern())
}
