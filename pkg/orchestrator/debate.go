package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/types"
)

// StructuredDebate implements adversarial multi-turn debate with synthesis.
// Participants have assigned stances (for/against) and see each other's arguments.
type StructuredDebate struct {
	executor types.Executor
	resolver types.CLIResolver
}

// NewStructuredDebate creates a debate strategy.
func NewStructuredDebate(executor types.Executor, resolver types.CLIResolver) *StructuredDebate {
	return &StructuredDebate{executor: executor, resolver: resolver}
}

// Name returns the strategy name.
func (d *StructuredDebate) Name() string { return "debate" }

// Execute runs a structured debate: participants alternate with adversarial stances,
// seeing all previous arguments. Final synthesis produces a verdict.
func (d *StructuredDebate) Execute(ctx context.Context, params types.StrategyParams) (*types.StrategyResult, error) {
	maxTurns := params.MaxTurns
	if maxTurns == 0 {
		maxTurns = 6
	}

	participants := params.CLIs
	if len(participants) < 2 {
		return nil, fmt.Errorf("debate requires at least 2 participants, got %d", len(participants))
	}

	synthesize := true
	if s, ok := params.Extra["synthesize"].(bool); ok {
		synthesize = s
	}

	// Assign stances
	stances := make([]string, len(participants))
	stances[0] = "for"
	stances[1] = "against"
	for i := 2; i < len(participants); i++ {
		if i%2 == 0 {
			stances[i] = "for"
		} else {
			stances[i] = "against"
		}
	}

	var history []debateEntry
	totalTurns := 0

	for turn := 0; turn < maxTurns; turn++ {
		participantIdx := turn % len(participants)
		cli := participants[participantIdx]
		stance := stances[participantIdx]
		totalTurns++

		prompt := buildDebatePrompt(params.Prompt, history, cli, stance)

		result, err := d.executor.Run(ctx, resolveOrFallbackWithOpts(d.resolver, cli, prompt, params.CWD, params.Timeout, params.Model, params.Effort))
		if err != nil {
			return nil, fmt.Errorf("debate turn %d (%s) failed: %w", totalTurns, cli, err)
		}

		history = append(history, debateEntry{
			CLI:     cli,
			Stance:  stance,
			Content: CompactTurnContent(result.Content, 0),
			Turn:    totalTurns,
		})
	}

	// Build result
	var sb strings.Builder
	for _, h := range history {
		sb.WriteString(fmt.Sprintf("## %s (%s, turn %d)\n\n%s\n\n", h.CLI, h.Stance, h.Turn, h.Content))
	}

	content := sb.String()

	// Synthesis: final verdict with budget-aware prompt
	if synthesize && len(history) > 0 {
		var responses []string
		for _, h := range history {
			responses = append(responses, fmt.Sprintf("[%s (%s)]: %s", h.CLI, h.Stance, h.Content))
		}
		budget := ComputeDialogBudget(nil)
		synthPrompt := BuildSynthesisPrompt(
			params.Prompt+"\n\nProvide a final verdict: which side presented stronger arguments? Summarize key points from each side and give your recommendation.",
			responses, budget)

		synthResult, synthErr := d.executor.Run(ctx, resolveOrFallbackWithOpts(d.resolver, participants[0], synthPrompt, params.CWD, params.Timeout, params.Model, params.Effort))
		if synthErr == nil {
			content += "\n\n---\n\n## Verdict\n\n" + synthResult.Content
			totalTurns++
		} else {
			content += "\n\n---\n\n## Verdict\n\n[synthesis failed: " + synthErr.Error() + "]"
		}
	}

	return &types.StrategyResult{
		Content:      content,
		Status:       "completed",
		Turns:        totalTurns,
		Participants: participants,
	}, nil
}

type debateEntry struct {
	CLI     string
	Stance  string
	Content string
	Turn    int
}

func buildDebatePrompt(topic string, history []debateEntry, currentCLI, stance string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Debate topic: %s\nYour stance: %s\n\n", topic, strings.ToUpper(stance)))

	if len(history) > 0 {
		sb.WriteString("Previous arguments:\n\n")
		for _, h := range history {
			sb.WriteString(fmt.Sprintf("[%s (%s)]: %s\n\n", h.CLI, h.Stance, h.Content))
		}
	}

	sb.WriteString(fmt.Sprintf("You are %s arguing %s. Present your argument.", currentCLI, strings.ToUpper(stance)))
	return sb.String()
}
