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
}

// NewParallelConsensus creates a consensus strategy.
func NewParallelConsensus(executor types.Executor) *ParallelConsensus {
	return &ParallelConsensus{executor: executor}
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

			result, err := c.executor.Run(ctx, types.SpawnArgs{
				CLI:            cli,
				Command:        cli,
				Args:           []string{"-p", params.Prompt},
				CWD:            params.CWD,
				TimeoutSeconds: params.Timeout,
			})

			if err != nil {
				results[idx] = opinionResult{CLI: cli, Err: err}
			} else {
				results[idx] = opinionResult{CLI: cli, Content: result.Content}
			}
		}(i, cli)
	}

	wg.Wait()

	// Collect successful opinions
	var opinions []string
	var successCLIs []string
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		opinions = append(opinions, fmt.Sprintf("## %s\n\n%s", r.CLI, r.Content))
		successCLIs = append(successCLIs, r.CLI)
	}

	if len(opinions) == 0 {
		return nil, fmt.Errorf("all participants failed")
	}

	content := strings.Join(opinions, "\n\n---\n\n")
	turns := len(successCLIs)

	// Phase 2: Optional synthesis
	if synthesize && len(successCLIs) > 1 {
		synthPrompt := fmt.Sprintf(
			"You received the following independent opinions on: %s\n\n%s\n\n"+
				"Synthesize these into a consensus. Identify agreements, disagreements, and provide a final recommendation.",
			params.Prompt, content)

		synthResult, err := c.executor.Run(ctx, types.SpawnArgs{
			CLI:            successCLIs[0], // Use first participant as synthesizer
			Command:        successCLIs[0],
			Args:           []string{"-p", synthPrompt},
			CWD:            params.CWD,
			TimeoutSeconds: params.Timeout,
		})
		if err == nil {
			content += "\n\n---\n\n## Synthesis\n\n" + synthResult.Content
			turns++
		}
	}

	return &types.StrategyResult{
		Content:      content,
		Status:       "completed",
		Turns:        turns,
		Participants: successCLIs,
	}, nil
}
