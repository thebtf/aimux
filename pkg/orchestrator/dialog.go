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

	budget := ComputeDialogBudget(nil) // default context window
	remainingTurns := maxTurns * len(participants)
	responseHint := budget / max(remainingTurns, 1)

	var history []turnEntry
	totalTurns := 0

	for turn := 0; turn < maxTurns; turn++ {
		for _, cli := range participants {
			totalTurns++
			remainingTurns--

			dialogCtx := BuildDialogContext(history, budget)
			prompt := buildDialogPromptWithContext(params.Prompt, dialogCtx, cli, responseHint)

			result, err := d.executor.Run(ctx, resolveOrFallback(d.resolver, cli, prompt, params.CWD, params.Timeout))
			if err != nil {
				// Return partial results on failure instead of bare error
				if len(history) > 0 {
					var sb strings.Builder
					for _, h := range history {
						sb.WriteString(fmt.Sprintf("## %s (turn %d)\n\n%s\n\n", h.CLI, h.Turn, h.Content))
					}
					sb.WriteString(fmt.Sprintf("## Error at turn %d (%s)\n\n%s\n", totalTurns, cli, err.Error()))
					return &types.StrategyResult{
						Content:      sb.String(),
						Status:       "partial",
						Turns:        totalTurns - 1,
						Participants: participants,
					}, nil
				}
				return nil, fmt.Errorf("dialog turn %d (%s) failed: %w", totalTurns, cli, err)
			}

			history = append(history, turnEntry{
				CLI:     cli,
				Content: CompactTurnContent(result.Content, 0),
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
	return buildDialogPromptWithContext(topic, BuildDialogContext(history, 0), currentCLI, 0)
}

func buildDialogPromptWithContext(topic, dialogContext, currentCLI string, responseHint int) string {
	if dialogContext == "" {
		if responseHint > 0 {
			return fmt.Sprintf("%s\n\nKeep your response under %d characters.", topic, responseHint)
		}
		return topic
	}

	var sb strings.Builder
	sb.WriteString("Topic: " + topic + "\n\nPrevious discussion:\n\n")
	sb.WriteString(dialogContext)
	sb.WriteString(fmt.Sprintf("\nYou are %s. Continue the discussion.", currentCLI))
	if responseHint > 0 {
		sb.WriteString(fmt.Sprintf(" Keep your response under %d characters.", responseHint))
	}
	return sb.String()
}
