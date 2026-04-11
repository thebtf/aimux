package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

const (
	DefaultMaxTurns  = 1
	CompletionSignal = "TASK_COMPLETE"
)

// RunConfig holds configuration for an agent run.
type RunConfig struct {
	Agent    *Agent            // agent definition
	CLI      string            // which CLI to use
	Prompt   string            // user task
	CWD      string            // working directory
	MaxTurns int               // max conversation turns (default: 1)
	Timeout  int               // per-turn timeout in seconds
	Model    string            // model override (passed to CLI via profile model flag)
	Effort   string            // reasoning effort override (passed to CLI via profile effort flag)
	Executor types.Executor    // process executor
	Resolver types.CLIResolver // CLI resolver
	OnOutput func(line string) // forwarded to SpawnArgs.OnOutput for live progress
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

	systemPrompt := buildSystemPrompt(cfg.Agent, cfg.Prompt, cfg.CWD)

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
// If the agent has a Content body, that IS the system prompt. Otherwise
// a minimal fallback prompt is generated from the agent name and description.
func buildSystemPrompt(agent *Agent, userTask string, cwd string) string {
	var sb strings.Builder

	if agent.Content != "" {
		// Agent .md body IS the system prompt
		sb.WriteString(agent.Content)
	} else {
		// Fallback for agents without content
		sb.WriteString(fmt.Sprintf("You are %s.", agent.Name))
		if agent.Description != "" {
			sb.WriteString(" ")
			sb.WriteString(agent.Description)
		}
	}

	sb.WriteString("\n\n## Task\n")
	sb.WriteString(userTask)

	if cwd != "" {
		sb.WriteString("\n\n## Context\n")
		sb.WriteString(fmt.Sprintf("Working directory: %s\n", cwd))
	}

	return sb.String()
}

// resolveArgs resolves SpawnArgs from the resolver or falls back to a simple default.
// When Model or Effort are set on RunConfig, they are forwarded to the resolver if
// it implements types.ModelledCLIResolver; otherwise the base interface is used.
func resolveArgs(cfg RunConfig, prompt string) (types.SpawnArgs, error) {
	if cfg.Resolver != nil {
		var (
			args types.SpawnArgs
			err  error
		)
		if mr, ok := cfg.Resolver.(types.ModelledCLIResolver); ok && (cfg.Model != "" || cfg.Effort != "") {
			args, err = mr.ResolveSpawnArgsWithOpts(cfg.CLI, prompt, cfg.Model, cfg.Effort)
		} else {
			args, err = cfg.Resolver.ResolveSpawnArgs(cfg.CLI, prompt)
		}
		if err == nil {
			args.CWD = cfg.CWD
			if cfg.Timeout > 0 {
				args.TimeoutSeconds = cfg.Timeout
			}
			if cfg.OnOutput != nil {
				args.OnOutput = cfg.OnOutput
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
		OnOutput:       cfg.OnOutput,
	}, nil
}

// isComplete checks whether the CLI has finished its work.
// Returns true when the response is empty (CLI exited with no output)
// or when it contains the TASK_COMPLETE signal for backward compatibility
// with agents that explicitly emit the completion marker.
func isComplete(response string) bool {
	return response == "" || strings.Contains(strings.ToUpper(response), strings.ToUpper(CompletionSignal))
}
