package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/dialogue"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

// StructuredDebate implements adversarial multi-turn debate with synthesis.
// Participants have assigned stances (for/against) and see each other's arguments.
type StructuredDebate struct {
	executor types.Executor
	resolver types.CLIResolver
	// Strangler Fig (M3): optional dialogue path. Nil means legacy executor.Run path.
	dialogue *dialogue.Controller
	swarm    *swarm.Swarm
}

// NewStructuredDebate creates a debate strategy.
func NewStructuredDebate(executor types.Executor, resolver types.CLIResolver) *StructuredDebate {
	return &StructuredDebate{executor: executor, resolver: resolver}
}

// SetDialogue wires the dialogue controller and swarm for the M3 new path.
// When set, Execute() uses dialogue.ModeStance instead of the legacy executor.Run path.
func (d *StructuredDebate) SetDialogue(ctrl *dialogue.Controller, sw *swarm.Swarm) {
	d.dialogue = ctrl
	d.swarm = sw
}

// Name returns the strategy name.
func (d *StructuredDebate) Name() string { return "debate" }

// Execute runs a structured debate: participants alternate with adversarial stances,
// seeing all previous arguments. Final synthesis produces a verdict.
// When a dialogue controller is set (M3 Strangler Fig), uses dialogue.ModeStance.
func (d *StructuredDebate) Execute(ctx context.Context, params types.StrategyParams) (*types.StrategyResult, error) {
	maxTurns := params.MaxTurns
	if maxTurns == 0 {
		maxTurns = 6
	}

	participants := params.CLIs
	if len(participants) < 2 {
		return nil, fmt.Errorf("debate requires at least 2 participants, got %d", len(participants))
	}

	// debate defaults synthesize=true (verdict expected); consensus defaults false (raw opinions may suffice)
	synthesize := true
	if s, ok := params.Extra["synthesize"].(bool); ok {
		synthesize = s
	}

	// M3 Strangler Fig: new dialogue path when controller is wired.
	if d.dialogue != nil {
		return d.executeWithDialogue(ctx, params, maxTurns, synthesize)
	}
	return d.executeLegacy(ctx, params, maxTurns, synthesize)
}

type debateEntry struct {
	CLI     string
	Stance  string
	Content string
	Turn    int
}

// executeWithDialogue is the M3 new path: uses dialogue.ModeStance via SwarmParticipants.
func (d *StructuredDebate) executeWithDialogue(ctx context.Context, params types.StrategyParams, maxTurns int, synthesize bool) (*types.StrategyResult, error) {
	participants := params.CLIs

	// Build stances map: participant name → stance label.
	stancesMap := make(map[string]string, len(participants))
	stancesMap[participants[0]] = "for"
	stancesMap[participants[1]] = "against"
	for i := 2; i < len(participants); i++ {
		if i%2 == 0 {
			stancesMap[participants[i]] = "for"
		} else {
			stancesMap[participants[i]] = "against"
		}
	}

	var dlgParticipants []dialogue.Participant
	for _, cli := range participants {
		handle, err := d.swarm.Get(ctx, cli, swarm.Stateless)
		if err != nil {
			// Swarm unavailable — fall through to legacy path.
			return d.executeLegacy(ctx, params, maxTurns, synthesize)
		}
		dlgParticipants = append(dlgParticipants, dialogue.NewSwarmParticipant(d.swarm, handle, cli, stancesMap[cli]))
	}

	dlg, err := d.dialogue.NewDialogue(dialogue.DialogueConfig{
		Participants: dlgParticipants,
		Mode:         dialogue.ModeStance,
		MaxTurns:     maxTurns,
		Topic:        params.Prompt,
		Stances:      stancesMap,
		Synthesize:   synthesize,
	})
	if err != nil {
		return d.executeLegacy(ctx, params, maxTurns, synthesize)
	}

	// Drive turns until completion.
	for dlg.Status == dialogue.StatusActive {
		if _, err := d.dialogue.NextTurn(ctx, dlg); err != nil {
			return d.executeLegacy(ctx, params, maxTurns, synthesize)
		}
	}

	if synthesize {
		if _, err := d.dialogue.Synthesize(ctx, dlg); err != nil {
			// Non-fatal.
		}
	}
	_ = d.dialogue.Close(dlg)

	// Convert turns to content.
	var sb strings.Builder
	var participantNames []string
	seen := make(map[string]bool)
	for _, t := range dlg.Turns {
		sb.WriteString(fmt.Sprintf("## %s (%s, turn %d)\n\n%s\n\n", t.Participant, t.Stance, t.TurnNumber, CompactTurnContent(t.Content, 0)))
		if !seen[t.Participant] {
			participantNames = append(participantNames, t.Participant)
			seen[t.Participant] = true
		}
	}

	content := sb.String()
	if dlg.Synthesis != nil {
		content += "\n\n---\n\n## Verdict\n\n" + dlg.Synthesis.Content
	}

	return &types.StrategyResult{
		Content:      content,
		Status:       "completed",
		Turns:        len(dlg.Turns),
		Participants: participantNames,
	}, nil
}

// executeLegacy is the original debate logic preserved for Strangler Fig fallback.
func (d *StructuredDebate) executeLegacy(ctx context.Context, params types.StrategyParams, maxTurns int, synthesize bool) (*types.StrategyResult, error) {
	participants := params.CLIs

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
	qg := NewQualityGate(0)

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

		if action := qg.Evaluate(cli, result.Content, "", 0); action == QualityRetry {
			retryPrompt := buildDebatePrompt(params.Prompt, history, cli, stance) +
				"\n\nYour previous response was below quality threshold. Please provide a substantive argument."
			if retryResult, retryErr := d.executor.Run(ctx, resolveOrFallbackWithOpts(d.resolver, cli, retryPrompt, params.CWD, params.Timeout, params.Model, params.Effort)); retryErr == nil {
				result = retryResult
			}
		}

		history = append(history, debateEntry{
			CLI:     cli,
			Stance:  stance,
			Content: CompactTurnContent(result.Content, 0),
			Turn:    totalTurns,
		})
	}

	var sb strings.Builder
	for _, h := range history {
		sb.WriteString(fmt.Sprintf("## %s (%s, turn %d)\n\n%s\n\n", h.CLI, h.Stance, h.Turn, h.Content))
	}
	content := sb.String()

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
