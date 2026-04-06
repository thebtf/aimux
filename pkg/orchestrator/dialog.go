package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/types"
)

// SequentialDialog implements sequential multi-turn dialog between CLIs.
// Each participant sees all previous responses (non-blinded).
type SequentialDialog struct {
	executor types.Executor
	resolver types.CLIResolver
}

// NewSequentialDialog creates a dialog strategy.
func NewSequentialDialog(executor types.Executor, resolver types.CLIResolver) *SequentialDialog {
	return &SequentialDialog{executor: executor, resolver: resolver}
}

// Name returns the strategy name.
func (d *SequentialDialog) Name() string { return "dialog" }

// Execute runs a sequential dialog: each participant responds in turn,
// seeing all previous responses. Continues until max_turns or convergence.
func (d *SequentialDialog) Execute(ctx context.Context, params types.StrategyParams) (*types.StrategyResult, error) {
	maxTurns := params.MaxTurns
	if maxTurns == 0 {
		maxTurns = 6
	}

	participants := params.CLIs
	if len(participants) < 2 {
		return nil, fmt.Errorf("dialog requires at least 2 participants, got %d", len(participants))
	}

	var history []turnEntry
	totalTurns := 0

	for turn := 0; turn < maxTurns; turn++ {
		for _, cli := range participants {
			totalTurns++

			prompt := buildDialogPrompt(params.Prompt, history, cli)

			result, err := d.executor.Run(ctx, resolveOrFallback(d.resolver, cli, prompt, params.CWD, params.Timeout))
			if err != nil {
				return nil, fmt.Errorf("dialog turn %d (%s) failed: %w", totalTurns, cli, err)
			}

			history = append(history, turnEntry{
				CLI:     cli,
				Content: result.Content,
				Turn:    totalTurns,
			})
		}
	}

	// Combine all responses
	var sb strings.Builder
	for _, h := range history {
		sb.WriteString(fmt.Sprintf("## %s (turn %d)\n\n%s\n\n", h.CLI, h.Turn, h.Content))
	}

	return &types.StrategyResult{
		Content:      sb.String(),
		Status:       "completed",
		Turns:        totalTurns,
		Participants: participants,
	}, nil
}

type turnEntry struct {
	CLI     string
	Content string
	Turn    int
}

func buildDialogPrompt(topic string, history []turnEntry, currentCLI string) string {
	if len(history) == 0 {
		return topic
	}

	var sb strings.Builder
	sb.WriteString("Topic: " + topic + "\n\nPrevious discussion:\n\n")

	for _, h := range history {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", h.CLI, h.Content))
	}

	sb.WriteString(fmt.Sprintf("You are %s. Continue the discussion.", currentCLI))
	return sb.String()
}
