package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

const (
	DefaultMaxTurns  = 10
	CompletionSignal = "TASK_COMPLETE"
)

// RunConfig holds configuration for an agent run.
type RunConfig struct {
	Agent    *Agent          // agent definition
	CLI      string          // which CLI to use
	Prompt   string          // user task
	CWD      string          // working directory
	MaxTurns int             // max conversation turns (default 10)
	Timeout  int             // per-turn timeout in seconds
	Executor types.Executor  // process executor
	Resolver types.CLIResolver // CLI resolver
}

// RunResult holds the outcome of an agent run.
type RunResult struct {
	Content    string   `json:"content"`
	Turns      int      `json:"turns"`
	Status     string   `json:"status"` // "completed", "max_turns", "error"
	TurnLog    []string `json:"turn_log"`
	DurationMS int64    `json:"duration_ms"`
}

// RunAgent executes a multi-turn agent session through a CLI.
// It builds a system prompt, executes turns until TASK_COMPLETE is detected
// or MaxTurns is reached.
func RunAgent(ctx context.Context, cfg RunConfig) (*RunResult, error) {
	if cfg.Agent == nil {
		return nil, fmt.Errorf("RunConfig.Agent is required")
	}
	if cfg.Executor == nil {
		return nil, fmt.Errorf("RunConfig.Executor is required")
	}

	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}

	systemPrompt := buildSystemPrompt(cfg.Agent, cfg.Prompt)

	start := time.Now()
	var turnLog []string
	var lastResponse string
	var finalContent strings.Builder

	for turn := 1; turn <= maxTurns; turn++ {
		var currentPrompt string
		if turn == 1 {
			currentPrompt = systemPrompt
		} else {
			currentPrompt = fmt.Sprintf(
				"Previous response:\n%s\n\nContinue the task. If done, say TASK_COMPLETE.",
				lastResponse,
			)
		}

		args, err := resolveArgs(cfg, currentPrompt)
		if err != nil {
			return &RunResult{
				Content:    finalContent.String(),
				Turns:      turn - 1,
				Status:     "error",
				TurnLog:    turnLog,
				DurationMS: time.Since(start).Milliseconds(),
			}, fmt.Errorf("turn %d: resolve args: %w", turn, err)
		}

		result, err := cfg.Executor.Run(ctx, args)
		if err != nil {
			return &RunResult{
				Content:    finalContent.String(),
				Turns:      turn - 1,
				Status:     "error",
				TurnLog:    turnLog,
				DurationMS: time.Since(start).Milliseconds(),
			}, fmt.Errorf("turn %d: executor: %w", turn, err)
		}

		lastResponse = result.Content
		turnLog = append(turnLog, fmt.Sprintf("turn=%d content_len=%d", turn, len(result.Content)))

		if finalContent.Len() > 0 {
			finalContent.WriteString("\n\n")
		}
		finalContent.WriteString(result.Content)

		if isComplete(result.Content) {
			return &RunResult{
				Content:    finalContent.String(),
				Turns:      turn,
				Status:     "completed",
				TurnLog:    turnLog,
				DurationMS: time.Since(start).Milliseconds(),
			}, nil
		}
	}

	return &RunResult{
		Content:    finalContent.String(),
		Turns:      maxTurns,
		Status:     "max_turns",
		TurnLog:    turnLog,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// buildSystemPrompt assembles the full first-turn prompt for the agent.
func buildSystemPrompt(agent *Agent, userTask string) string {
	var sb strings.Builder

	sb.WriteString("You are ")
	sb.WriteString(agent.Name)
	sb.WriteString(".")
	if agent.Description != "" {
		sb.WriteString(" ")
		sb.WriteString(agent.Description)
	}
	if agent.Content != "" {
		sb.WriteString("\n\n")
		sb.WriteString(agent.Content)
	}
	sb.WriteString("\n\nIMPORTANT: When the task is complete, include '")
	sb.WriteString(CompletionSignal)
	sb.WriteString("' in your response.")
	sb.WriteString("\n\nTask: ")
	sb.WriteString(userTask)

	return sb.String()
}

// resolveArgs resolves SpawnArgs from the resolver or falls back to a simple default.
func resolveArgs(cfg RunConfig, prompt string) (types.SpawnArgs, error) {
	if cfg.Resolver != nil {
		args, err := cfg.Resolver.ResolveSpawnArgs(cfg.CLI, prompt)
		if err == nil {
			args.CWD = cfg.CWD
			if cfg.Timeout > 0 {
				args.TimeoutSeconds = cfg.Timeout
			}
			return args, nil
		}
		// fall through to default on resolver error
	}

	// Legacy fallback: cli + "-p" + prompt
	return types.SpawnArgs{
		CLI:            cfg.CLI,
		Command:        cfg.CLI,
		Args:           []string{"-p", prompt},
		CWD:            cfg.CWD,
		TimeoutSeconds: cfg.Timeout,
	}, nil
}

// isComplete checks whether the response contains the completion signal.
func isComplete(response string) bool {
	return strings.Contains(strings.ToUpper(response), strings.ToUpper(CompletionSignal))
}
