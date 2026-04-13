package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

const (
	DefaultMaxTurns  = 1
	CompletionSignal = "TASK_COMPLETE"
)

// RunConfig holds configuration for an agent run.
type RunConfig struct {
	Agent    *Agent                 // agent definition
	CLI      string                 // which CLI to use
	Prompt   string                 // user task
	CWD      string                 // working directory
	MaxTurns int                    // max conversation turns (default: 1)
	Timeout  int                    // per-turn timeout in seconds
	Model    string                 // model override (passed to CLI via profile model flag)
	Effort   string                 // reasoning effort override (passed to CLI via profile effort flag)
	Executor        types.Executor              // process executor
	Resolver        types.CLIResolver           // CLI resolver
	OnOutput        func(cli, line string)      // forwarded to SpawnArgs.OnOutput with resolved CLI context
	ModelFallback   []string                    // ordered model fallback chain (from profile)
	ModelFlag       string                      // CLI flag for model (e.g. "-m")
	CooldownTracker types.ModelCooldownTracker  // optional: cooldown tracker for rate-limited models
	CooldownSeconds int                         // cooldown duration after quota error
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

		// T011: use model fallback chain when the config carries fallback models and a tracker.
		var result *types.Result
		if cfg.CooldownTracker != nil && len(cfg.ModelFallback) > 0 && cfg.ModelFlag != "" {
			result, err = runWithModelFallbackAgent(ctx, cfg.Executor, cfg.CooldownTracker, cfg.CLI, cfg.ModelFlag, cfg.ModelFallback, cfg.CooldownSeconds, args)
		} else {
			result, err = cfg.Executor.Run(ctx, args)
		}
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

// runWithModelFallbackAgent iterates the model fallback chain for a single executor call.
// It uses the types.ModelCooldownTracker interface so the agents package does not
// depend on pkg/executor directly.
//
// On quota error: marks model cooled down, tries next model.
// On transient error: retries same model once, then applies full classification to retry result.
// On fatal error: returns immediately.
// On success: returns result.
// When all models are on cooldown the error contains "rate limit" so callers can
// distinguish it from a hard failure.
func runWithModelFallbackAgent(
	ctx context.Context,
	exec types.Executor,
	tracker types.ModelCooldownTracker,
	cli, modelFlag string,
	modelChain []string,
	cooldownSeconds int,
	baseArgs types.SpawnArgs,
) (*types.Result, error) {
	cooldownDuration := time.Duration(cooldownSeconds) * time.Second
	if cooldownDuration == 0 {
		cooldownDuration = 5 * time.Minute
	}

	available := tracker.FilterAvailable(cli, modelChain)
	if len(available) == 0 {
		// Include "rate limit" so callers can treat this as a retriable condition.
		return nil, fmt.Errorf("all models on cooldown (rate limit) for CLI %s", cli)
	}

	var lastErr error
	for _, model := range available {
		args := baseArgs
		args.Args = executor.ReplaceModelFlag(baseArgs.Args, modelFlag, model)

		result, err := exec.Run(ctx, args)

		var content, stderr string
		var exitCode int
		if result != nil {
			content = result.Content
			exitCode = result.ExitCode
		}
		if err != nil {
			stderr = err.Error()
		}

		errClass := executor.ClassifyError(content, stderr, exitCode)

		switch errClass {
		case executor.ErrorClassNone:
			return result, err

		case executor.ErrorClassQuota:
			tracker.MarkCooledDown(cli, model, cooldownDuration)
			lastErr = fmt.Errorf("quota exceeded for %s:%s", cli, model)
			continue

		case executor.ErrorClassTransient:
			// Retry same model once, then apply full classification to the retry result.
			result2, err2 := exec.Run(ctx, args)
			var c2, s2 string
			var ec2 int
			if result2 != nil {
				c2 = result2.Content
				ec2 = result2.ExitCode
			}
			if err2 != nil {
				s2 = err2.Error()
			}
			retryClass := executor.ClassifyError(c2, s2, ec2)
			switch retryClass {
			case executor.ErrorClassNone:
				return result2, err2
			case executor.ErrorClassQuota:
				tracker.MarkCooledDown(cli, model, cooldownDuration)
				lastErr = fmt.Errorf("quota exceeded for %s:%s (on transient retry)", cli, model)
				continue
			case executor.ErrorClassFatal:
				if err2 == nil {
					err2 = fmt.Errorf("fatal error detected in output")
				}
				return result2, fmt.Errorf("fatal error on %s:%s: %w", cli, model, err2)
			default:
				lastErr = fmt.Errorf("transient error on %s:%s after retry", cli, model)
				continue
			}

		case executor.ErrorClassFatal:
			if err == nil {
				err = fmt.Errorf("fatal error detected in output")
			}
			return result, fmt.Errorf("fatal error on %s:%s: %w", cli, model, err)
		}
	}

	return nil, fmt.Errorf("all models exhausted for CLI %s: %w", cli, lastErr)
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
				resolvedCLI := args.CLI
				args.OnOutput = func(line string) {
					cfg.OnOutput(resolvedCLI, line)
				}
			}
			return args, nil
		}
		// fall through to default on resolver error
	}

	var onOutput func(string)
	if cfg.OnOutput != nil {
		resolvedCLI := cfg.CLI
		onOutput = func(line string) {
			cfg.OnOutput(resolvedCLI, line)
		}
	}

	// Legacy fallback: cli + "-p" + prompt
	return types.SpawnArgs{
		CLI:            cfg.CLI,
		Command:        cfg.CLI,
		Args:           []string{"-p", prompt},
		CWD:            cfg.CWD,
		TimeoutSeconds: cfg.Timeout,
		OnOutput:       onOutput,
	}, nil
}

// isComplete checks whether the CLI has finished its work.
// Returns true when the response is empty (CLI exited with no output)
// or when it contains the TASK_COMPLETE signal for backward compatibility
// with agents that explicitly emit the completion marker.
func isComplete(response string) bool {
	return response == "" || strings.Contains(strings.ToUpper(response), strings.ToUpper(CompletionSignal))
}

