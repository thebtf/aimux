package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/dialogue"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

// SequentialDialog implements sequential multi-turn dialog between CLIs.
// Each participant sees all previous responses (non-blinded).
type SequentialDialog struct {
	executor types.Executor
	resolver types.CLIResolver
	// Strangler Fig (M3): optional dialogue path. Nil means legacy executor.Run path.
	dialogue *dialogue.Controller
	swarm    *swarm.Swarm
}

// NewSequentialDialog creates a dialog strategy.
func NewSequentialDialog(executor types.Executor, resolver types.CLIResolver) *SequentialDialog {
	return &SequentialDialog{executor: executor, resolver: resolver}
}

// SetDialogue wires the dialogue controller and swarm for the M3 new path.
// When set, Execute() uses dialogue.ModeSequential instead of the legacy executor.Run path.
func (d *SequentialDialog) SetDialogue(ctrl *dialogue.Controller, sw *swarm.Swarm) {
	d.dialogue = ctrl
	d.swarm = sw
}

// Name returns the strategy name.
func (d *SequentialDialog) Name() string { return "dialog" }

// Execute runs a sequential dialog: each participant responds in turn,
// seeing all previous responses. Continues until max_turns or convergence.
// When a dialogue controller is set (M3 Strangler Fig), uses dialogue.ModeSequential.
func (d *SequentialDialog) Execute(ctx context.Context, params types.StrategyParams) (*types.StrategyResult, error) {
	// maxTurns is NEW turns only; prior turn count from history does not reduce the budget
	maxTurns := params.MaxTurns
	if maxTurns == 0 {
		maxTurns = 6
	}

	participants := params.CLIs
	if len(participants) < 2 {
		return nil, fmt.Errorf("dialog requires at least 2 participants, got %d", len(participants))
	}

	// M3 Strangler Fig: new dialogue path when controller is wired.
	if d.dialogue != nil {
		return d.executeWithDialogue(ctx, params, maxTurns)
	}
	return d.executeLegacy(ctx, params, maxTurns)
}

type turnEntry struct {
	CLI     string
	Content string
	Turn    int
}

func buildDialogPrompt(topic string, history []turnEntry, currentCLI string) string {
	return buildDialogPromptWithContext(topic, BuildDialogContext(history, 0), currentCLI, 0)
}

// executeWithDialogue is the M3 new path: uses dialogue.ModeSequential via SwarmParticipants.
// It does NOT support session resume (prior_turns) — that path stays in the legacy executor.
func (d *SequentialDialog) executeWithDialogue(ctx context.Context, params types.StrategyParams, maxTurns int) (*types.StrategyResult, error) {
	participants := params.CLIs

	var dlgParticipants []dialogue.Participant
	for _, cli := range participants {
		handle, err := d.swarm.Get(ctx, cli, swarm.Stateless)
		if err != nil {
			return d.executeLegacy(ctx, params, maxTurns)
		}
		dlgParticipants = append(dlgParticipants, dialogue.NewSwarmParticipant(d.swarm, handle, cli, "participant"))
	}

	dlg, err := d.dialogue.NewDialogue(dialogue.DialogueConfig{
		Participants: dlgParticipants,
		Mode:         dialogue.ModeSequential,
		MaxTurns:     maxTurns * len(participants),
		Topic:        params.Prompt,
	})
	if err != nil {
		return d.executeLegacy(ctx, params, maxTurns)
	}

	for dlg.Status == dialogue.StatusActive {
		if _, err := d.dialogue.NextTurn(ctx, dlg); err != nil {
			// Return partial results rather than bare error.
			if len(dlg.Turns) > 0 {
				return d.turnsToPartialResult(dlg, participants), nil
			}
			return d.executeLegacy(ctx, params, maxTurns)
		}
	}
	_ = d.dialogue.Close(dlg)

	var sb strings.Builder
	var participantNames []string
	seen := make(map[string]bool)
	for _, t := range dlg.Turns {
		sb.WriteString(fmt.Sprintf("## %s (turn %d)\n\n%s\n\n", t.Participant, t.TurnNumber, CompactTurnContent(t.Content, 0)))
		if !seen[t.Participant] {
			participantNames = append(participantNames, t.Participant)
			seen[t.Participant] = true
		}
	}

	// Encode turns as TurnHistory for session resume compatibility.
	var history []turnEntry
	for _, t := range dlg.Turns {
		history = append(history, turnEntry{CLI: t.Participant, Content: t.Content, Turn: t.TurnNumber})
	}
	historyJSON, _ := json.Marshal(history)

	return &types.StrategyResult{
		Content:      sb.String(),
		Status:       "completed",
		Turns:        len(dlg.Turns),
		Participants: participantNames,
		TurnHistory:  historyJSON,
	}, nil
}

// turnsToPartialResult converts accumulated dialogue turns to a partial StrategyResult.
func (d *SequentialDialog) turnsToPartialResult(dlg *dialogue.Dialogue, participants []string) *types.StrategyResult {
	var sb strings.Builder
	var history []turnEntry
	for _, t := range dlg.Turns {
		sb.WriteString(fmt.Sprintf("## %s (turn %d)\n\n%s\n\n", t.Participant, t.TurnNumber, t.Content))
		history = append(history, turnEntry{CLI: t.Participant, Content: t.Content, Turn: t.TurnNumber})
	}
	historyJSON, _ := json.Marshal(history)
	return &types.StrategyResult{
		Content:      sb.String(),
		Status:       "partial",
		Turns:        len(dlg.Turns),
		Participants: participants,
		TurnHistory:  historyJSON,
	}
}

// executeLegacy is the original dialog logic preserved for Strangler Fig fallback.
func (d *SequentialDialog) executeLegacy(ctx context.Context, params types.StrategyParams, maxTurns int) (*types.StrategyResult, error) {
	participants := params.CLIs
	budget := ComputeDialogBudget(nil)
	remainingTurns := maxTurns * len(participants)
	responseHint := budget / max(remainingTurns, 1)

	var history []turnEntry
	if raw, ok := params.Extra["prior_turns"]; ok {
		switch v := raw.(type) {
		case []byte:
			_ = json.Unmarshal(v, &history)
		case string:
			_ = json.Unmarshal([]byte(v), &history)
		}
	}
	totalTurns := len(history)
	qg := NewQualityGate(0)

	for turn := 0; turn < maxTurns; turn++ {
		for _, cli := range participants {
			totalTurns++
			remainingTurns--

			dialogCtx := BuildDialogContext(history, budget)
			prompt := buildDialogPromptWithContext(params.Prompt, dialogCtx, cli, responseHint)

			result, err := d.executor.Run(ctx, resolveOrFallbackWithOpts(d.resolver, cli, prompt, params.CWD, params.Timeout, params.Model, params.Effort))
			if err != nil {
				if len(history) > 0 {
					var sb strings.Builder
					for _, h := range history {
						sb.WriteString(fmt.Sprintf("## %s (turn %d)\n\n%s\n\n", h.CLI, h.Turn, h.Content))
					}
					sb.WriteString(fmt.Sprintf("## Error at turn %d (%s)\n\n%s\n", totalTurns, cli, err.Error()))
					historyJSON, _ := json.Marshal(history)
					return &types.StrategyResult{
						Content:      sb.String(),
						Status:       "partial",
						Turns:        totalTurns - 1,
						Participants: participants,
						TurnHistory:  historyJSON,
					}, nil
				}
				return nil, fmt.Errorf("dialog turn %d (%s) failed: %w", totalTurns, cli, err)
			}

			if action := qg.Evaluate(cli, result.Content, "", 0); action == QualityRetry {
				retryPrompt := buildDialogPromptWithContext(
					params.Prompt+"\n\nYour previous response was below quality threshold. Please provide a substantive response.",
					dialogCtx, cli, responseHint)
				if retryResult, retryErr := d.executor.Run(ctx, resolveOrFallbackWithOpts(d.resolver, cli, retryPrompt, params.CWD, params.Timeout, params.Model, params.Effort)); retryErr == nil {
					result = retryResult
				}
			}

			history = append(history, turnEntry{
				CLI:     cli,
				Content: CompactTurnContent(result.Content, 0),
				Turn:    totalTurns,
			})
		}
	}

	var sb strings.Builder
	for _, h := range history {
		sb.WriteString(fmt.Sprintf("## %s (turn %d)\n\n%s\n\n", h.CLI, h.Turn, h.Content))
	}

	historyJSON, _ := json.Marshal(history)
	return &types.StrategyResult{
		Content:      sb.String(),
		Status:       "completed",
		Turns:        totalTurns,
		Participants: participants,
		TurnHistory:  historyJSON,
	}, nil
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
