package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/thebtf/aimux/pkg/types"
)

// ParallelConsensus implements blinded parallel opinion gathering with synthesis.
// Each participant responds independently (cannot see others' responses).
// Optional synthesis turn combines all opinions.
type ParallelConsensus struct {
	executor types.Executor
	resolver types.CLIResolver
}

// NewParallelConsensus creates a consensus strategy.
func NewParallelConsensus(executor types.Executor, resolver types.CLIResolver) *ParallelConsensus {
	return &ParallelConsensus{executor: executor, resolver: resolver}
}

// Name returns the strategy name.
func (c *ParallelConsensus) Name() string { return "consensus" }

// Execute runs parallel blinded queries to all participants, then optionally synthesizes.
func (c *ParallelConsensus) Execute(ctx context.Context, params types.StrategyParams) (*types.StrategyResult, error) {
	participants := params.CLIs
	if len(participants) < 2 {
		return nil, fmt.Errorf("consensus requires at least 2 participants, got %d", len(participants))
	}

	synthesize := false
	if s, ok := params.Extra["synthesize"].(bool); ok {
		synthesize = s
	}

	// Phase 1: Parallel blinded opinions
	type opinionResult struct {
		CLI     string
		Content string
		Err     error
	}

	results := make([]opinionResult, len(participants))
	var wg sync.WaitGroup

	for i, cli := range participants {
		wg.Add(1)
		go func(idx int, cli string) {
			defer wg.Done()

			result, err := c.executor.Run(ctx, resolveOrFallback(c.resolver, cli, params.Prompt, params.CWD, params.Timeout))

			if err != nil {
				results[idx] = opinionResult{CLI: cli, Err: err}
			} else {
				results[idx] = opinionResult{CLI: cli, Content: result.Content}
			}
		}(i, cli)
	}

	wg.Wait()

	// Collect successful opinions with compaction
	var opinions []string
	var responseTexts []string
	var successCLIs []string
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		compacted := CompactTurnContent(r.Content, 0)
		opinions = append(opinions, fmt.Sprintf("## %s\n\n%s", r.CLI, compacted))
		responseTexts = append(responseTexts, compacted)
		successCLIs = append(successCLIs, r.CLI)
	}

	if len(opinions) == 0 {
		return nil, fmt.Errorf("all participants failed")
	}

	content := strings.Join(opinions, "\n\n---\n\n")
	turns := len(successCLIs)

	// Phase 2: Optional synthesis with budget-aware prompt
	if synthesize && len(successCLIs) > 1 {
		budget := ComputeDialogBudget(nil)
		synthPrompt := BuildSynthesisPrompt(params.Prompt, responseTexts, budget)

		synthResult, synthErr := c.executor.Run(ctx, resolveOrFallback(c.resolver, successCLIs[0], synthPrompt, params.CWD, params.Timeout))
		if synthErr == nil {
			content += "\n\n---\n\n## Synthesis\n\n" + synthResult.Content
			turns++
		} else {
			content += "\n\n---\n\n## Synthesis\n\n[synthesis failed: " + synthErr.Error() + "]"
		}
	}

	return &types.StrategyResult{
		Content:      content,
		Status:       "completed",
		Turns:        turns,
		Participants: successCLIs,
	}, nil
}
