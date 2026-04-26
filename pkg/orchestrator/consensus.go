package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/thebtf/aimux/pkg/dialogue"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

// ParallelConsensus implements blinded parallel opinion gathering with synthesis.
// Each participant responds independently (cannot see others' responses).
// Optional synthesis turn combines all opinions.
type ParallelConsensus struct {
	executor types.Executor
	resolver types.CLIResolver
	// Strangler Fig (M3): optional dialogue path. Nil means legacy executor.Run path.
	dialogue *dialogue.Controller
	swarm    *swarm.Swarm
}

// NewParallelConsensus creates a consensus strategy.
func NewParallelConsensus(executor types.Executor, resolver types.CLIResolver) *ParallelConsensus {
	return &ParallelConsensus{executor: executor, resolver: resolver}
}

// SetDialogue wires the dialogue controller and swarm for the M3 new path.
// When set, Execute() uses dialogue.ModeParallel instead of the legacy executor.Run path.
func (c *ParallelConsensus) SetDialogue(ctrl *dialogue.Controller, sw *swarm.Swarm) {
	c.dialogue = ctrl
	c.swarm = sw
}

// Name returns the strategy name.
func (c *ParallelConsensus) Name() string { return "consensus" }

// Execute runs parallel blinded queries to all participants, then optionally synthesizes.
// When a dialogue controller is set (M3 Strangler Fig), uses dialogue.ModeParallel.
// Falls back to legacy executor.Run path when dialogue is nil.
func (c *ParallelConsensus) Execute(ctx context.Context, params types.StrategyParams) (*types.StrategyResult, error) {
	participants := params.CLIs
	if len(participants) < 2 {
		return nil, fmt.Errorf("consensus requires at least 2 participants, got %d", len(participants))
	}

	synthesize := false
	if s, ok := params.Extra["synthesize"].(bool); ok {
		synthesize = s
	}

	// M3 Strangler Fig: new dialogue path when controller is wired.
	if c.dialogue != nil {
		return c.executeWithDialogue(ctx, params, synthesize)
	}
	return c.executeLegacy(ctx, params, synthesize)
}

// executeWithDialogue is the M3 new path: uses dialogue.ModeParallel via SwarmParticipants.
func (c *ParallelConsensus) executeWithDialogue(ctx context.Context, params types.StrategyParams, synthesize bool) (*types.StrategyResult, error) {
	var dlgParticipants []dialogue.Participant
	for _, cli := range params.CLIs {
		handle, err := c.swarm.Get(ctx, cli, swarm.Stateless)
		if err != nil {
			// Swarm unavailable for this CLI — fall back to legacy path entirely.
			return c.executeLegacy(ctx, params, synthesize)
		}
		dlgParticipants = append(dlgParticipants, dialogue.NewSwarmParticipant(c.swarm, handle, cli, "participant"))
	}

	d, err := c.dialogue.NewDialogue(dialogue.DialogueConfig{
		Participants: dlgParticipants,
		Mode:         dialogue.ModeParallel,
		MaxTurns:     0, // one parallel round
		Topic:        params.Prompt,
		Synthesize:   synthesize,
	})
	if err != nil {
		return c.executeLegacy(ctx, params, synthesize)
	}

	_, err = c.dialogue.NextTurn(ctx, d)
	if err != nil {
		return c.executeLegacy(ctx, params, synthesize)
	}

	if synthesize {
		if _, err := c.dialogue.Synthesize(ctx, d); err != nil {
			// Non-fatal: continue with raw turns.
		}
	}
	_ = c.dialogue.Close(d)

	// Convert dialogue turns to StrategyResult.
	var opinions []string
	var participantNames []string
	for _, t := range d.Turns {
		compacted := CompactTurnContent(t.Content, 0)
		opinions = append(opinions, fmt.Sprintf("## %s\n\n%s", t.Participant, compacted))
		participantNames = append(participantNames, t.Participant)
	}

	content := strings.Join(opinions, "\n\n---\n\n")

	if d.Synthesis != nil {
		content += "\n\n---\n\n## Synthesis\n\n" + d.Synthesis.Content
	}

	return &types.StrategyResult{
		Content:      content,
		Status:       "completed",
		Turns:        len(d.Turns),
		Participants: participantNames,
	}, nil
}

// executeLegacy is the original consensus logic extracted for Strangler Fig fallback.
func (c *ParallelConsensus) executeLegacy(ctx context.Context, params types.StrategyParams, synthesize bool) (*types.StrategyResult, error) {
	participants := params.CLIs

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
			result, err := c.executor.Run(ctx, resolveOrFallbackWithOpts(c.resolver, cli, params.Prompt, params.CWD, params.Timeout, params.Model, params.Effort))
			if err != nil {
				results[idx] = opinionResult{CLI: cli, Err: err}
			} else {
				results[idx] = opinionResult{CLI: cli, Content: result.Content}
			}
		}(i, cli)
	}

	wg.Wait()

	var opinions []string
	var responseTexts []string
	var successCLIs []string
	var failedCLIs []string
	for _, r := range results {
		if r.Err != nil {
			failedCLIs = append(failedCLIs, r.CLI)
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

	status := "completed"
	var extra map[string]any
	if len(failedCLIs) > 0 {
		status = "partial"
		extra = map[string]any{"failed_clis": failedCLIs}
	}

	if synthesize && len(successCLIs) > 1 {
		budget := ComputeDialogBudget(nil)
		synthPrompt := BuildSynthesisPrompt(params.Prompt, responseTexts, budget)
		synthResult, synthErr := c.executor.Run(ctx, resolveOrFallbackWithOpts(c.resolver, successCLIs[0], synthPrompt, params.CWD, params.Timeout, params.Model, params.Effort))
		if synthErr == nil {
			content += "\n\n---\n\n## Synthesis\n\n" + synthResult.Content
			turns++
		} else {
			content += "\n\n---\n\n## Synthesis\n\n[synthesis failed: " + synthErr.Error() + "]"
		}
	}

	return &types.StrategyResult{
		Content:      content,
		Status:       status,
		Turns:        turns,
		Participants: successCLIs,
		Extra:        extra,
	}, nil
}
