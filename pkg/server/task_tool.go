package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/executor/fallback"
	"github.com/thebtf/aimux/pkg/executor/picker"
	pipeExec "github.com/thebtf/aimux/pkg/executor/pipe"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/types"
)

// buildFallbackPicker constructs the FallbackPicker wired into the task tool.
// Returns nil when no CLIs are available — the task tool surfaces a clear error
// at call time rather than panicking during server startup.
func buildFallbackPicker(s *Server) *fallback.FallbackPicker {
	activeCLIs := s.registry.EnabledCLIs()
	if len(activeCLIs) == 0 {
		s.log.Warn("task tool: no CLIs available — FallbackPicker not initialized")
		return nil
	}

	pickerCfg := &s.cfg.Executor.Picker

	binaryResolver := func(cli string) string {
		profile, err := s.registry.Get(cli)
		if err != nil || profile == nil {
			return cli
		}
		if profile.ResolvedPath != "" {
			return profile.ResolvedPath
		}
		if profile.Binary != "" {
			return profile.Binary
		}
		return cli
	}

	capScore := picker.NewCapabilityScore(pickerCfg)
	health := picker.NewHealthChecker(pickerCfg, binaryResolver, activeCLIs, nil)
	p := picker.NewPicker(pickerCfg, capScore, health, activeCLIs)

	fbCfg := fallback.DefaultFallbackConfig()
	store := fallback.NewInMemoryScoreStore()

	fbCapScore := picker.NewCapabilityScore(pickerCfg)
	fbHealth := picker.NewHealthChecker(pickerCfg, binaryResolver, activeCLIs, nil)
	orderer := fallback.NewOrderer(fbCapScore, fbHealth, &fbCfg)
	classifier := fallback.NewFailureClassifier()
	translator := fallback.NewPassThroughTranslator()
	fb := fallback.NewFallback(classifier, orderer, translator, store, &fbCfg, activeCLIs)

	return fallback.NewFallbackPicker(p, fb, store, &fbCfg)
}

// registerTaskTool registers the generic `task` MCP tool (AIMUX-4 FR-10).
func (s *Server) registerTaskTool() {
	s.mcp.AddTool(
		mcp.NewTool("task",
			mcp.WithDescription("[delegate — multi-CLI, sync] Submit a task to the best available CLI. "+
				"Automatically selects the highest-scoring CLI via capability scoring, "+
				"and falls back to the next-best CLI if the primary fails with a retryable error "+
				"(rate limit, auth expiry, timeout, capability mismatch). "+
				"task_class controls capability routing: \"code\" prefers codex/claude, \"research\" prefers gemini. "+
				"Returns the CLI output content directly. "+
				"Use codex_task for async Codex-specific work with Loom persistence."),
			mcp.WithString("prompt",
				mcp.Required(),
				mcp.Description("Task prompt sent to the selected CLI."),
			),
			mcp.WithString("task_class",
				mcp.Description("Semantic task class for capability routing: code, review, research, write-task, task. Default: task."),
				mcp.Enum("code", "review", "research", "write-task", "task"),
				mcp.DefaultString("task"),
			),
			mcp.WithString("cli",
				mcp.Description("Override: force a specific CLI by name (e.g. \"claude\", \"gemini\"). Skips picker scoring when set."),
			),
			mcp.WithBoolean("fallback_enabled",
				mcp.Description("Whether to retry with alternative CLIs on eligible errors. Default: true."),
			),
			mcp.WithNumber("max_attempts",
				mcp.Description("Maximum number of CLIs to try (including the primary). 0 = use server default (2)."),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(false),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(false),
				OpenWorldHint:   mcp.ToBoolPtr(true),
			}),
		),
		s.handleTask,
	)
}

// handleTask is the MCP handler for the `task` tool.
func (s *Server) handleTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	prompt, err := req.RequireString("prompt")
	if err != nil || strings.TrimSpace(prompt) == "" {
		return mcp.NewToolResultError("task: prompt is required and must not be empty"), nil
	}

	taskClass := req.GetString("task_class", "task")
	cliOverride := req.GetString("cli", "")
	maxAttempts := req.GetInt("max_attempts", 0)

	// fallback_enabled: absent = nil (use config default), present = explicit override.
	// Use Params.Arguments to distinguish "not set" from "set to false".
	var fallbackEnabled *bool
	if args, ok := req.Params.Arguments.(map[string]any); ok {
		if _, present := args["fallback_enabled"]; present {
			b := req.GetBool("fallback_enabled", true)
			fallbackEnabled = &b
		}
	}

	if s.fallbackPicker == nil {
		return mcp.NewToolResultError("task: no CLIs available — FallbackPicker not initialized"), nil
	}

	spec := picker.TaskSpec{
		TaskClass: taskClass,
		Prompt:    prompt,
	}

	// When cli override is specified, dispatch directly without picker scoring.
	if cliOverride != "" {
		content, dispErr := s.taskDispatch(ctx, cliOverride, spec)
		if dispErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("task: cli %q failed: %v", cliOverride, dispErr)), nil
		}
		return mcp.NewToolResultText(content), nil
	}

	opts := fallback.RunOptions{
		FallbackEnabled: fallbackEnabled,
		MaxAttempts:     maxAttempts,
	}

	result, runErr := s.fallbackPicker.Run(ctx, spec, opts, s.taskDispatch)
	if runErr != nil {
		if fallback.IsExhausted(runErr) {
			var exErr *fallback.ErrAllFallbackExhausted
			if errors.As(runErr, &exErr) {
				return mcp.NewToolResultError(formatExhaustedError(exErr)), nil
			}
		}
		return mcp.NewToolResultError(fmt.Sprintf("task failed: %v", runErr)), nil
	}

	return mcp.NewToolResultText(result.Content), nil
}

// taskDispatch dispatches a single CLI call using the pipe executor.
// Implements fallback.DispatchFn — returns *extypes.CLIError on failure
// so the FailureClassifier can determine fallback eligibility.
func (s *Server) taskDispatch(ctx context.Context, cli string, spec picker.TaskSpec) (string, error) {
	profile, err := s.registry.Get(cli)
	if err != nil {
		return "", extypes.NewBinaryNotFound(
			fmt.Sprintf("CLI %q not configured: %v", cli, err), err)
	}
	if profile == nil {
		return "", extypes.NewBinaryNotFound(fmt.Sprintf("CLI %q profile is nil", cli), nil)
	}

	binaryPath := profile.ResolvedPath
	if binaryPath == "" {
		binaryPath = profile.Binary
	}
	if binaryPath == "" {
		return "", extypes.NewBinaryNotFound(fmt.Sprintf("CLI %q has no binary path", cli), nil)
	}

	spawnArgs := types.SpawnArgs{
		CLI:               cli,
		Command:           binaryPath,
		Args:              buildTaskArgs(profile, spec.Prompt),
		CWD:               taskCWD(),
		TimeoutSeconds:    profile.TimeoutSeconds,
		CompletionPattern: profile.CompletionPattern,
	}
	// For stdin-mode CLIs, deliver the prompt via stdin.
	if profile.PromptFlagType == "stdin" {
		spawnArgs.Stdin = spec.Prompt
	}

	exec := pipeExec.New()
	result, execErr := exec.Run(ctx, spawnArgs)
	if execErr != nil {
		return "", mapExecError(execErr)
	}
	if result == nil {
		return "", extypes.NewUnknown(fmt.Sprintf("CLI %q returned nil result", cli), nil)
	}

	// Typed errors embedded in the result (e.g., timeout, exit non-zero).
	if result.Error != nil {
		return result.Content, mapTypedError(result.Error)
	}
	if result.ExitCode != 0 && !result.Partial {
		msg := fmt.Sprintf("CLI %q exited with code %d", cli, result.ExitCode)
		if result.Stderr != "" {
			msg += ": " + result.Stderr
		}
		return result.Content, extypes.NewUnknown(msg, nil)
	}

	return result.Content, nil
}

// buildTaskArgs constructs the CLI argument list for a task prompt.
//
// Decision order:
//  1. PromptFlagType == "flag" → append [PromptFlag, prompt] (or just [prompt] if no flag).
//  2. PromptFlagType == "stdin" → append StdinSentinel if non-empty; prompt arrives via stdin.
//  3. Default (empty or unrecognized) → treat same as "flag".
//
// profile.Command.Base may contain subcommands (e.g., "run" for some CLIs); it is split
// on spaces and prepended. The result is never nil.
func buildTaskArgs(profile *config.CLIProfile, prompt string) []string {
	var args []string
	if profile.Command.Base != "" {
		args = strings.Fields(profile.Command.Base)
	}

	switch profile.PromptFlagType {
	case "stdin":
		// Prompt delivered via SpawnArgs.Stdin; only append sentinel if required.
		if profile.StdinSentinel != "" {
			args = append(args, profile.StdinSentinel)
		}
	default:
		// "flag" or empty: deliver prompt as a flag argument.
		if profile.PromptFlag != "" {
			args = append(args, profile.PromptFlag, prompt)
		} else {
			args = append(args, prompt)
		}
	}
	return args
}

// taskCWD returns the working directory for task dispatch.
// Reads AIMUX_CWD env var; empty string lets the pipe executor inherit the process CWD.
func taskCWD() string {
	return os.Getenv("AIMUX_CWD")
}

// mapExecError converts a generic executor error to a typed *extypes.CLIError so the
// FailureClassifier can determine fallback eligibility.
func mapExecError(err error) error {
	if err == nil {
		return nil
	}
	// Already typed — pass through unchanged.
	var cliErr *extypes.CLIError
	if errors.As(err, &cliErr) {
		return err
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "context canceled"):
		return extypes.NewCanceled(msg, err)
	case strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "timed out"):
		return extypes.NewTimeout(msg, err)
	case strings.Contains(lower, "not found") || strings.Contains(lower, "executable"):
		return extypes.NewBinaryNotFound(msg, err)
	default:
		return extypes.NewUnknown(msg, err)
	}
}

// mapTypedError converts a types.TypedError (embedded in types.Result.Error) to a *extypes.CLIError.
func mapTypedError(te *types.TypedError) *extypes.CLIError {
	if te == nil {
		return nil
	}
	switch te.Type {
	case types.ErrorTypeTimeout:
		return extypes.NewTimeout(te.Message, nil)
	default:
		return extypes.NewUnknown(te.Message, nil)
	}
}

// formatExhaustedError formats an ErrAllFallbackExhausted into a JSON string
// for MCP callers. Includes a per-CLI attempt breakdown.
func formatExhaustedError(e *fallback.ErrAllFallbackExhausted) string {
	if e == nil {
		return "task: all fallback CLIs exhausted"
	}
	type attempt struct {
		CLI     string `json:"cli"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	attempts := make([]attempt, len(e.Attempts))
	for i, a := range e.Attempts {
		attempts[i] = attempt{CLI: a.CLI, Code: a.Code, Message: a.Message}
	}
	payload := map[string]any{
		"error":    "all_fallback_exhausted",
		"message":  e.Error(),
		"attempts": attempts,
	}
	b, _ := json.Marshal(payload)
	return string(b)
}
