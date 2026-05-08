package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/executor/fallback"
	"github.com/thebtf/aimux/pkg/executor/picker"
	pipeExec "github.com/thebtf/aimux/pkg/executor/pipe"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/server/classifier"
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
			mcp.WithDescription("[delegate — Loom routed, sync] Submit a task through the v5.11 task meta-router. "+
				"Provide task_class to route directly to code, review, or think. "+
				"Omit task_class or pass task to use the deterministic classifier. "+
				"Review mode accepts target and gate; code mode accepts sandbox and cli driver override. "+
				"Returns a JSON TaskResult with task_id, content, task_class, rounds, and confidence_score."),
			mcp.WithString("prompt",
				mcp.Required(),
				mcp.Description("Task prompt routed through TaskRouter."),
			),
			mcp.WithString("task_class",
				mcp.Description("Explicit task class. Omit or use task to classify from prompt."),
				mcp.Enum("code", "review", "think", "task"),
				mcp.DefaultString("task"),
			),
			mcp.WithString("cli",
				mcp.Description("Driver CLI override for code tasks; does not bypass cross-family navigator selection."),
			),
			mcp.WithString("resume_id",
				mcp.Description("Loom root task_id to resume."),
			),
			mcp.WithString("target",
				mcp.Description("Review target, such as HEAD, a diff, or a PR ref."),
			),
			mcp.WithBoolean("gate",
				mcp.Description("Review sub-mode flag. Requires review routing and target."),
			),
			mcp.WithString("sandbox",
				mcp.Description("Code sandbox sub-mode."),
				mcp.Enum("read-only", "workspace-write", "danger"),
			),
			mcp.WithString("mode",
				mcp.Description("Prompt sub-mode."),
				mcp.Enum("universal", "delegate", "review", "diagnose"),
			),
			mcp.WithNumber("timeout_seconds",
				mcp.Description("Worker timeout in seconds, used by review-gate and long-running workers."),
			),
			mcp.WithBoolean("fallback_enabled",
				mcp.Description("Worker fallback policy hint. Default: true."),
			),
			mcp.WithNumber("max_attempts",
				mcp.Description("Worker fallback attempt hint. 0 = worker default."),
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
	taskReq, parseErr := parseTaskToolRequest(ctx, req)
	if parseErr != nil {
		return taskToolError(TaskResult{}, parseErr)
	}
	loomClient := s.taskRouterLoom(ctx)
	if loomClient == nil {
		return taskToolError(TaskResult{}, extypes.NewCapabilityMismatch("task router requires Loom", nil))
	}
	router, err := NewTaskRouter(TaskRouterConfig{
		Loom:       loomClient,
		Classifier: classifier.New(),
	})
	if err != nil {
		return taskToolError(TaskResult{}, extypes.NewCapabilityMismatch(err.Error(), err))
	}
	result, err := router.Dispatch(ctx, taskReq)
	if err != nil {
		return taskToolError(result, err)
	}
	return marshalToolResult(result)
}

func (s *Server) taskRouterLoom(ctx context.Context) TaskRouterLoom {
	if scoped, ok := TenantScopedLoomFromContext(ctx); ok && scoped != nil {
		return scoped
	}
	if s == nil {
		return nil
	}
	if s.loom == nil {
		return nil
	}
	return s.loom
}

func parseTaskToolRequest(ctx context.Context, req mcp.CallToolRequest) (TaskRequest, error) {
	prompt, err := req.RequireString("prompt")
	if err != nil || strings.TrimSpace(prompt) == "" {
		return TaskRequest{}, extypes.NewUserInputError("task: prompt is required and must not be empty", err)
	}

	rawTaskClass := req.GetString("task_class", "")
	cliOverride := strings.TrimSpace(req.GetString("cli", ""))
	resumeID := strings.TrimSpace(req.GetString("resume_id", ""))
	target := strings.TrimSpace(req.GetString("target", ""))
	gate := req.GetBool("gate", false)
	sandbox := strings.TrimSpace(req.GetString("sandbox", ""))
	mode := strings.TrimSpace(req.GetString("mode", ""))
	timeoutSeconds := req.GetInt("timeout_seconds", 0)
	maxAttempts := req.GetInt("max_attempts", 0)

	if timeoutSeconds < 0 {
		return TaskRequest{}, extypes.NewUserInputError("task: timeout_seconds must be >= 0", nil)
	}
	if maxAttempts < 0 {
		return TaskRequest{}, extypes.NewUserInputError("task: max_attempts must be >= 0", nil)
	}
	taskClass, classErr := normalizeTaskToolClass(rawTaskClass, target, gate, sandbox, mode)
	if classErr != nil {
		return TaskRequest{}, classErr
	}

	metadata := map[string]any{}
	if sandbox != "" {
		metadata["sandbox"] = sandbox
	}
	if mode != "" {
		metadata["mode"] = mode
	}
	if timeoutSeconds > 0 {
		metadata["timeout_seconds"] = timeoutSeconds
	}
	if maxAttempts > 0 {
		metadata["max_attempts"] = maxAttempts
	}
	if args, ok := req.Params.Arguments.(map[string]any); ok {
		if _, present := args["fallback_enabled"]; present {
			metadata["fallback_enabled"] = req.GetBool("fallback_enabled", true)
		}
	}

	return TaskRequest{
		Prompt:         prompt,
		TaskClass:      taskClass,
		ProjectID:      req.GetString("project_id", projectIDFromContext(ctx)),
		RequestID:      req.GetString("request_id", ""),
		CWD:            cwdFromRequestOrContext(req, ctx),
		Env:            sessionEnvFromContext(ctx),
		CLI:            cliOverride,
		Model:          req.GetString("model", ""),
		Effort:         req.GetString("effort", ""),
		TimeoutSeconds: timeoutSeconds,
		ResumeID:       resumeID,
		Target:         target,
		Gate:           gate,
		Metadata:       metadata,
	}, nil
}

func normalizeTaskToolClass(raw string, target string, gate bool, sandbox string, mode string) (string, error) {
	taskClass := strings.ToLower(strings.TrimSpace(raw))
	if !validTaskToolClass(taskClass) {
		return "", extypes.NewUserInputError(fmt.Sprintf("task: unsupported task_class %q", raw), nil)
	}
	implied := ""
	setImplied := func(next string, reason string) error {
		if implied == "" || implied == next {
			implied = next
			return nil
		}
		return extypes.NewUserInputError(fmt.Sprintf("task: conflicting sub-mode params: %s implies %s but another param implies %s", reason, next, implied), nil)
	}
	if taskClass == "" || taskClass == taskClassTask {
		if target != "" || gate {
			if err := setImplied(classifier.TaskClassReview, "target/gate"); err != nil {
				return "", err
			}
		}
		if sandbox != "" {
			if err := validateSandbox(sandbox); err != nil {
				return "", err
			}
			if err := setImplied(classifier.TaskClassCode, "sandbox"); err != nil {
				return "", err
			}
		}
		if mode != "" {
			if err := validatePromptMode(mode); err != nil {
				return "", err
			}
			if err := setImplied(classifier.TaskClassPrompt, "mode"); err != nil {
				return "", err
			}
		}
		if implied != "" {
			taskClass = implied
		}
	} else {
		if sandbox != "" {
			if err := validateSandbox(sandbox); err != nil {
				return "", err
			}
		}
		if mode != "" {
			if err := validatePromptMode(mode); err != nil {
				return "", err
			}
		}
	}

	if (target != "" || gate) && taskClass != classifier.TaskClassReview {
		return "", extypes.NewUserInputError("task: target/gate params require task_class review", nil)
	}
	if sandbox != "" && taskClass != classifier.TaskClassCode {
		return "", extypes.NewUserInputError("task: sandbox param requires task_class code", nil)
	}
	if mode != "" && taskClass != classifier.TaskClassPrompt {
		return "", extypes.NewUserInputError("task: mode param requires task_class prompt", nil)
	}
	if taskClass == classifier.TaskClassPrompt {
		return "", extypes.NewUserInputError("task: prompt task_class is not available in the Loom router", nil)
	}
	if taskClass == classifier.TaskClassReview && strings.TrimSpace(target) == "" {
		return "", extypes.NewUserInputError("task: target is required for review task_class", nil)
	}
	return taskClass, nil
}

func validTaskToolClass(taskClass string) bool {
	switch taskClass {
	case "", taskClassTask, classifier.TaskClassCode, classifier.TaskClassReview, taskClassThink:
		return true
	default:
		return false
	}
}

func validateSandbox(sandbox string) error {
	switch sandbox {
	case "read-only", "workspace-write", "danger":
		return nil
	default:
		return extypes.NewUserInputError(fmt.Sprintf("task: invalid sandbox %q", sandbox), nil)
	}
}

func validatePromptMode(mode string) error {
	switch mode {
	case "universal", "delegate", "review", "diagnose":
		return nil
	default:
		return extypes.NewUserInputError(fmt.Sprintf("task: invalid mode %q", mode), nil)
	}
}

func taskToolError(result TaskResult, err error) (*mcp.CallToolResult, error) {
	cliErr := taskCLIError(err)
	payload := map[string]any{
		"code":      cliErr.Code.String(),
		"message":   cliErr.Message,
		"retryable": cliErr.Retryable,
	}
	if cliErr.CauseStr != "" {
		payload["cause"] = cliErr.CauseStr
	}
	if result.TaskID != "" {
		payload["task_id"] = result.TaskID
	}
	if result.TaskClass != "" {
		payload["task_class"] = result.TaskClass
	}
	if len(result.Candidates) > 0 {
		payload["candidates"] = result.Candidates
	}
	b, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("internal error: response serialization failed: %v", marshalErr)), nil
	}
	return mcp.NewToolResultError(string(b)), nil
}

func taskCLIError(err error) *extypes.CLIError {
	if err == nil {
		return extypes.NewUnknown("task failed", nil)
	}
	var cliErr *extypes.CLIError
	if errors.As(err, &cliErr) {
		return cliErr
	}
	return extypes.NewUnknown(err.Error(), err)
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
		CWD:               taskDispatchCWD(spec.CWD),
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
// profile.Command.Base may include the binary plus subcommands (e.g., "codex exec").
// taskDispatch supplies the binary separately, so the leading binary token is stripped
// and only subcommands/flags are prepended. The result is never nil.
func buildTaskArgs(profile *config.CLIProfile, prompt string) []string {
	args := commandBaseArgs(profile)
	if args == nil {
		args = []string{}
	}
	// Work on a copy so callers can safely reuse config-owned slices.
	args = append([]string{}, args...)

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

func commandBaseArgs(profile *config.CLIProfile) []string {
	if profile == nil {
		return nil
	}
	if profile.Command.Base != "" {
		fields, err := splitCommandLine(profile.Command.Base)
		if err != nil || len(fields) == 0 {
			return nil
		}
		if len(fields) > 0 && profileCommandStartsWithBinary(profile, fields[0]) {
			return fields[1:]
		}
		return fields
	}
	return nil
}

func profileCommandStartsWithBinary(profile *config.CLIProfile, token string) bool {
	tokenBase := filepath.Base(token)
	for _, candidate := range []string{profile.ResolvedPath, profile.Binary} {
		if candidate == "" {
			continue
		}
		if strings.EqualFold(tokenBase, filepath.Base(candidate)) {
			return true
		}
	}
	return false
}

func splitCommandLine(command string) ([]string, error) {
	var (
		fields  []string
		current strings.Builder
		quote   rune
		escaped bool
	)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		fields = append(fields, current.String())
		current.Reset()
	}
	for _, r := range command {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote == '"' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	flush()
	return fields, nil
}

// taskCWD returns the working directory for task dispatch.
// Reads AIMUX_CWD env var; empty string lets the pipe executor inherit the process CWD.
func taskCWD() string {
	return os.Getenv("AIMUX_CWD")
}

func taskDispatchCWD(specCWD string) string {
	if cwd := strings.TrimSpace(specCWD); cwd != "" {
		return cwd
	}
	return taskCWD()
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
