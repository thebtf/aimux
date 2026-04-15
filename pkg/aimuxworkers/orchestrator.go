package aimuxworkers

import (
	"context"
	"fmt"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/types"
)

// OrchestratorWorker adapts orchestrator strategies to the loom.Worker interface.
type OrchestratorWorker struct {
	orch *orchestrator.Orchestrator
}

// NewOrchestratorWorker creates an OrchestratorWorker.
func NewOrchestratorWorker(orch *orchestrator.Orchestrator) *OrchestratorWorker {
	return &OrchestratorWorker{orch: orch}
}

func (w *OrchestratorWorker) Type() loom.WorkerType { return loom.WorkerTypeOrchestrator }

func (w *OrchestratorWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	start := time.Now()

	// Extract strategy name from metadata.
	strategy, _ := task.Metadata["strategy"].(string)
	if strategy == "" {
		return nil, fmt.Errorf("orchestrator worker: missing 'strategy' in metadata")
	}

	if w.orch == nil {
		return nil, fmt.Errorf("orchestrator worker: orchestrator not configured")
	}

	// Build StrategyParams from task.
	params := types.StrategyParams{
		Prompt: task.Prompt,
		CWD:    task.CWD,
		Model:  task.Model,
		Effort: task.Effort,
	}

	// Extract optional params from metadata.
	if clis, ok := task.Metadata["clis"].([]string); ok {
		params.CLIs = clis
	}
	// Handle []interface{} from JSON deserialization.
	if clisAny, ok := task.Metadata["clis"].([]interface{}); ok {
		for _, c := range clisAny {
			if s, ok := c.(string); ok {
				params.CLIs = append(params.CLIs, s)
			}
		}
	}
	if roles, ok := task.Metadata["roles"].([]string); ok {
		params.Roles = roles
	}
	if rolesAny, ok := task.Metadata["roles"].([]interface{}); ok {
		for _, r := range rolesAny {
			if s, ok := r.(string); ok {
				params.Roles = append(params.Roles, s)
			}
		}
	}
	switch v := task.Metadata["max_turns"].(type) {
	case int:
		params.MaxTurns = v
	case int64:
		params.MaxTurns = int(v)
	case float64:
		params.MaxTurns = int(v)
	}
	switch v := task.Metadata["timeout"].(type) {
	case int:
		params.Timeout = v
	case int64:
		params.Timeout = int(v)
	case float64:
		params.Timeout = int(v)
	}
	if task.Timeout > 0 && params.Timeout == 0 {
		params.Timeout = task.Timeout
	}
	if extra, ok := task.Metadata["extra"].(map[string]any); ok {
		params.Extra = extra
	}

	result, err := w.orch.Execute(ctx, strategy, params)
	if err != nil {
		return nil, fmt.Errorf("orchestrator worker: %s: %w", strategy, err)
	}

	duration := time.Since(start).Milliseconds()

	return &loom.WorkerResult{
		Content:    result.Content,
		Metadata:   map[string]any{"strategy": strategy, "turns": result.Turns, "status": result.Status},
		DurationMS: duration,
	}, nil
}
