// Package orchestrator provides the unified orchestration engine.
// All multi-step workflows (pair, dialog, consensus, debate, audit) share
// a single Orchestrator that delegates to Strategy implementations.
package orchestrator

import (
	"context"
	"fmt"

	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/types"
)

// Orchestrator executes strategies with shared infrastructure:
// turn management, retry with backoff, context propagation, progress, abort.
type Orchestrator struct {
	strategies map[string]types.Strategy
	log        *logger.Logger
}

// New creates an orchestrator with registered strategies.
func New(log *logger.Logger, strategies ...types.Strategy) *Orchestrator {
	m := make(map[string]types.Strategy, len(strategies))
	for _, s := range strategies {
		m[s.Name()] = s
	}
	return &Orchestrator{
		strategies: m,
		log:        log,
	}
}

// Execute runs a named strategy with the given parameters.
func (o *Orchestrator) Execute(ctx context.Context, strategyName string, params types.StrategyParams) (*types.StrategyResult, error) {
	s, ok := o.strategies[strategyName]
	if !ok {
		return nil, fmt.Errorf("strategy %q not registered", strategyName)
	}

	o.log.Info("orchestrator: starting %s", strategyName)

	result, err := s.Execute(ctx, params)
	if err != nil {
		o.log.Error("orchestrator: %s failed: %v", strategyName, err)
		return nil, err
	}

	o.log.Info("orchestrator: %s completed (turns=%d)", strategyName, result.Turns)
	return result, nil
}

// Register adds a strategy to the orchestrator.
func (o *Orchestrator) Register(s types.Strategy) {
	o.strategies[s.Name()] = s
}
