package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/executor/fallback"
	"github.com/thebtf/aimux/pkg/executor/picker"
	pipeExec "github.com/thebtf/aimux/pkg/executor/pipe"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/recipes"
	"github.com/thebtf/aimux/pkg/server/classifier"
	"github.com/thebtf/aimux/pkg/types"
)

const (
	taskRouterLoomUnavailableMessage = "task router requires Loom; Loom is unavailable in this daemon. Remediation: restart aimux or check SQLite session store initialization."
	taskToolDetachedSubmitTimeout    = 30 * time.Second
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
	health.WarmAll(context.Background())
	p := picker.NewPicker(pickerCfg, capScore, health, activeCLIs)

	fbCfg := fallback.DefaultFallbackConfig()
	store := fallback.NewInMemoryScoreStore()

	fbCapScore := picker.NewCapabilityScore(pickerCfg)
	fbHealth := picker.NewHealthChecker(pickerCfg, binaryResolver, activeCLIs, nil)
	fbHealth.WarmAll(context.Background())
	orderer := fallback.NewOrderer(fbCapScore, fbHealth, &fbCfg)
	classifier := fallback.NewFailureClassifier()
	translator := fallback.NewPassThroughTranslator()
	fb := fallback.NewFallback(classifier, orderer, translator, store, &fbCfg, activeCLIs)

	return fallback.NewFallbackPicker(p, fb, store, &fbCfg)
}

// registerTaskTool registers the generic `task` MCP tool (AIMUX-4 FR-10).
func (s *Server) registerTaskTool() {
	s.registerContractedTool(
		toolContract{Name: "task", Classification: "async_mandatory", AdapterKind: "loom"},
		mcp.NewTool("task",
			mcp.WithDescription("[delegate — Loom routed, async] Submit a task through the v5.12 task meta-router. "+
				"Provide task_class to route directly to code, review, or spec. "+
				"Omit task_class or pass task to use the deterministic classifier. "+
				"Review/spec modes accept target; review mode accepts gate; code mode accepts sandbox and cli driver override. "+
				"Returns an accepted JSON TaskResult with task_id/job_id, status polling command, cancel command, and task resource URIs. "+
				"Poll status(job_id) for progress and include_content=true for terminal content."),
			mcp.WithString("prompt",
				mcp.Required(),
				mcp.Description("Task prompt routed through TaskRouter."),
			),
			mcp.WithString("task_class",
				mcp.Description("Explicit task class. Omit or use task to classify from prompt."),
				mcp.Enum("code", "review", "spec", "task"),
				mcp.DefaultString("task"),
			),
			mcp.WithString("recipe_id",
				mcp.Description("Compiled recipe ID. Resolves before Loom submit and routes through the existing task entry."),
			),
			mcp.WithString("cli",
				mcp.Description("Driver CLI override for code tasks."),
			),
			mcp.WithString("navigator",
				mcp.Description("Navigator CLI override for code tasks. Defaults to cross-family pick based on driver. "+
					"Pass \"none\" for solo mode: with sandbox=read-only driver returns unified diff to caller; "+
					"with sandbox=workspace-write|danger driver writes files directly."),
			),
			mcp.WithString("resume_id",
				mcp.Description("Loom root task_id to resume."),
			),
			mcp.WithString("target",
				mcp.Description("Review/spec target, such as HEAD, a diff, PR ref, feature slug, or spec artifact path."),
			),
			mcp.WithBoolean("gate",
				mcp.Description("Review sub-mode flag. Requires review routing and target."),
			),
			mcp.WithString("sandbox",
				mcp.Description("Code sandbox sub-mode. "+
					"read-only: driver returns diff without writing. "+
					"workspace-write: driver writes files within project. "+
					"danger: driver has full filesystem access."),
				mcp.Enum("read-only", "workspace-write", "danger"),
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

// registerReviewTool registers the dedicated `review` MCP facade over the task backbone.
func (s *Server) registerReviewTool() {
	s.registerContractedTool(
		toolContract{Name: "review", Classification: "async_mandatory", AdapterKind: "loom"},
		mcp.NewTool("review",
			mcp.WithDescription("[delegate — Loom routed, async] Submit code review work through the existing task/review backbone. "+
				"Standard review uses prompt+target; gate=true requests review-gate semantics. "+
				"Returns the same accepted JSON TaskResult, task_id/job_id, status polling command, cancel command, and task resource URIs as task."),
			mcp.WithString("prompt",
				mcp.Required(),
				mcp.Description("Review prompt routed through the review/task backbone."),
			),
			mcp.WithString("target",
				mcp.Required(),
				mcp.Description("Review target, such as HEAD, a diff, or a PR ref."),
			),
			mcp.WithBoolean("gate",
				mcp.Description("Use gate-oriented review semantics for merge/readiness decisions."),
			),
			mcp.WithString("recipe_id",
				mcp.Description("Optional read-only review recipe. Supported here: code-review. Use task(recipe_id=...) for recipes outside this facade."),
				mcp.Enum("code-review"),
			),
			mcp.WithString("resume_id",
				mcp.Description("Loom root task_id to resume."),
			),
			mcp.WithNumber("timeout_seconds",
				mcp.Description("Worker timeout in seconds, used by review-gate and long-running review workers."),
			),
			mcp.WithBoolean("fallback_enabled",
				mcp.Description("Worker fallback policy hint. Default: true."),
			),
			mcp.WithNumber("max_attempts",
				mcp.Description("Worker fallback attempt hint. 0 = worker default."),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(true),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(false),
				OpenWorldHint:   mcp.ToBoolPtr(true),
			}),
		),
		s.handleReview,
	)
}

// handleTask is the MCP handler for the `task` tool.
func (s *Server) handleTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dispatchCtx, cancel := taskSubmitContext(ctx)
	defer cancel()
	taskReq, parseErr := parseTaskToolRequest(dispatchCtx, req)
	if parseErr != nil {
		return taskToolError(TaskResult{}, parseErr)
	}
	return s.dispatchTaskRequest(dispatchCtx, taskReq)
}

// handleReview is the MCP handler for the dedicated `review` facade.
func (s *Server) handleReview(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dispatchCtx, cancel := taskSubmitContext(ctx)
	defer cancel()
	taskReq, parseErr := parseReviewToolRequest(dispatchCtx, req)
	if parseErr != nil {
		return taskToolError(TaskResult{}, parseErr)
	}
	return s.dispatchTaskRequest(dispatchCtx, taskReq)
}

func (s *Server) dispatchTaskRequest(ctx context.Context, taskReq TaskRequest) (*mcp.CallToolResult, error) {
	if policyErr := s.validateRecipeProviderPolicy(taskReq); policyErr != nil {
		return taskToolError(TaskResult{}, policyErr)
	}
	loomClient, loomErr := s.taskRouterLoom(ctx)
	if loomClient == nil {
		return taskToolError(TaskResult{}, taskRouterLoomUnavailableError(loomErr))
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

func taskSubmitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	dispatchCtx := ctx
	if dispatchCtx == nil {
		dispatchCtx = context.Background()
	}
	// mcp-go executes task-augmented regular tools on a background goroutine that
	// reuses the original request context. That context may already be canceled as
	// part of the normal async handoff before this handler begins, so ctx.Err()
	// cannot be treated as a reliable explicit tasks/cancel signal here. Detach
	// from the parent to keep the synchronous submit path alive across disconnects,
	// then bound the detached window so blocked routing or Loom access cannot hang
	// forever before the inner Loom task exists.
	detachedCtx := context.WithoutCancel(dispatchCtx)
	return context.WithTimeout(detachedCtx, taskToolDetachedSubmitTimeout)
}

func (s *Server) taskRouterLoom(ctx context.Context) (TaskRouterLoom, error) {
	if scoped, ok := TenantScopedLoomFromContext(ctx); ok && scoped != nil {
		return scoped, nil
	}
	if s == nil {
		return nil, nil
	}
	loomClient, err := s.ensureLoom(ctx)
	if err != nil {
		return nil, err
	}
	if scoped, ok, scopedErr := s.tenantScopedLoomForContext(ctx); scopedErr != nil {
		return nil, scopedErr
	} else if ok {
		return scoped, nil
	}
	return loomClient, nil
}

func parseReviewToolRequest(ctx context.Context, req mcp.CallToolRequest) (TaskRequest, error) {
	args := cloneToolArguments(req)
	args["task_class"] = classifier.TaskClassReview
	if strings.TrimSpace(req.GetString("cli", "")) != "" {
		return TaskRequest{}, extypes.NewUserInputError("review: cli override is not supported by review workers", nil)
	}
	if strings.TrimSpace(req.GetString("navigator", "")) != "" {
		return TaskRequest{}, extypes.NewUserInputError("review: navigator override is not supported by review workers", nil)
	}
	if strings.TrimSpace(req.GetString("sandbox", "")) != "" {
		return TaskRequest{}, extypes.NewUserInputError("review: sandbox is not supported by review workers", nil)
	}
	if recipeID := strings.TrimSpace(req.GetString("recipe_id", "")); recipeID != "" {
		switch recipeID {
		case "code-review":
		default:
			return TaskRequest{}, newUnsupportedReviewRecipeIDError(recipeID)
		}
	}
	req.Params.Arguments = args
	return parseTaskToolRequest(ctx, req)
}

func cloneToolArguments(req mcp.CallToolRequest) map[string]any {
	args := map[string]any{}
	if original, ok := req.Params.Arguments.(map[string]any); ok {
		for key, value := range original {
			args[key] = value
		}
	}
	return args
}

func parseTaskToolRequest(ctx context.Context, req mcp.CallToolRequest) (TaskRequest, error) {
	prompt, err := req.RequireString("prompt")
	if err != nil || strings.TrimSpace(prompt) == "" {
		return TaskRequest{}, extypes.NewUserInputError("task: prompt is required and must not be empty", err)
	}

	rawTaskClass := req.GetString("task_class", "")
	recipeID := strings.TrimSpace(req.GetString("recipe_id", ""))
	cliOverride := strings.TrimSpace(req.GetString("cli", ""))
	navigatorOverride := strings.TrimSpace(req.GetString("navigator", ""))
	resumeID := strings.TrimSpace(req.GetString("resume_id", ""))
	target := strings.TrimSpace(req.GetString("target", ""))
	gate := req.GetBool("gate", false)
	sandbox := strings.TrimSpace(req.GetString("sandbox", ""))
	mode := strings.TrimSpace(req.GetString("mode", ""))
	timeoutSeconds := req.GetInt("timeout_seconds", 0)
	maxAttempts := req.GetInt("max_attempts", 0)

	if mode != "" {
		return TaskRequest{}, extypes.NewUserInputError("task: mode param is not available in the Loom router", nil)
	}
	if timeoutSeconds < 0 {
		return TaskRequest{}, extypes.NewUserInputError("task: timeout_seconds must be >= 0", nil)
	}
	if maxAttempts < 0 {
		return TaskRequest{}, extypes.NewUserInputError("task: max_attempts must be >= 0", nil)
	}
	var recipe recipes.Recipe
	if recipeID != "" {
		resolvedRecipe, ok := recipes.Resolve(recipeID)
		if !ok {
			return TaskRequest{}, newUnsupportedRecipeIDError(recipeID)
		}
		if err := validateRecipeTaskClass(rawTaskClass, resolvedRecipe); err != nil {
			return TaskRequest{}, err
		}
		recipe = resolvedRecipe
		rawTaskClass = recipe.TaskClass
		if recipe.GateDefault && !taskArgumentPresent(req, "gate") {
			gate = true
		}
	}
	forcedReadOnlySandbox := false
	requestedSandbox := sandbox
	if recipe.WorkflowID != "" && recipe.ReadOnly && sandbox != "" {
		if err := validateSandbox(sandbox); err != nil {
			return TaskRequest{}, err
		}
		forcedReadOnlySandbox = true
		sandbox = ""
	}

	taskClass, classErr := normalizeTaskToolClass(rawTaskClass, target, gate, sandbox)
	if classErr != nil {
		return TaskRequest{}, classErr
	}

	metadata := map[string]any{}
	if recipe.ID != "" {
		metadata["recipe_id"] = recipe.ID
		metadata["recipe_title"] = recipe.Title
		metadata["recipe_read_only"] = recipe.ReadOnly
		metadata["recipe_phases"] = cloneRecipeStrings(recipe.Phases)
		metadata["recipe_output_resources"] = cloneRecipeStrings(recipe.OutputResources)
	}
	if recipe.WorkflowID != "" {
		metadata["recipe_workflow_id"] = recipe.WorkflowID
		metadata["recipe_workflow_source"] = recipe.WorkflowSource
		metadata["recipe_workflow_steps"] = cloneRecipeStrings(recipe.WorkflowSteps)
		prompt = workflowBackedRecipePrompt(recipe, prompt)
		metadata["recipe_workflow_worker_type"] = string(workflowRecipeWorkerType)
	}
	if sandbox != "" {
		metadata["sandbox"] = sandbox
	} else if forcedReadOnlySandbox {
		metadata["sandbox"] = "read-only"
		metadata["requested_sandbox"] = requestedSandbox
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
	if sessionKey, ok := worktreeSessionKeyFromContext(ctx); ok {
		metadata[worktreeSessionMetadataKey] = sessionKey
	}
	soloMode := strings.EqualFold(navigatorOverride, "none")
	if soloMode {
		metadata["solo_mode"] = true
		navigatorOverride = ""
	}

	return TaskRequest{
		Prompt:         prompt,
		TaskClass:      taskClass,
		WorkerType:     workflowRecipeWorkerTypeFromID(recipe.WorkflowID),
		ProjectID:      req.GetString("project_id", projectIDFromContext(ctx)),
		RequestID:      req.GetString("request_id", ""),
		CWD:            cwdFromRequestOrContext(req, ctx),
		Env:            sessionEnvFromContext(ctx),
		CLI:            cliOverride,
		Navigator:      navigatorOverride,
		Model:          req.GetString("model", ""),
		Effort:         req.GetString("effort", ""),
		TimeoutSeconds: timeoutSeconds,
		ResumeID:       resumeID,
		Target:         target,
		Gate:           gate,
		Metadata:       metadata,
	}, nil
}

func validateRecipeTaskClass(rawTaskClass string, recipe recipes.Recipe) error {
	taskClass := strings.ToLower(strings.TrimSpace(rawTaskClass))
	switch taskClass {
	case "", taskClassTask, recipe.TaskClass:
		return nil
	default:
		return extypes.NewUserInputError(fmt.Sprintf("task: recipe_id %q requires task_class %s", recipe.ID, recipe.TaskClass), nil)
	}
}

func taskArgumentPresent(req mcp.CallToolRequest, name string) bool {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return false
	}
	_, present := args[name]
	return present
}

func cloneRecipeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func workflowBackedRecipePrompt(recipe recipes.Recipe, prompt string) string {
	if recipe.WorkflowID == "" {
		return prompt
	}
	var b strings.Builder
	b.WriteString("Workflow-backed curated recipe: ")
	b.WriteString(recipe.ID)
	b.WriteString("\nCompiled workflow: ")
	b.WriteString(recipe.WorkflowID)
	if recipe.WorkflowSource != "" {
		b.WriteString(" (")
		b.WriteString(recipe.WorkflowSource)
		b.WriteString(")")
	}
	b.WriteString("\nRead-only execution boundary: use the following compiled workflow steps as the methodology; do not mutate source unless the caller opens a separate code task.\nSteps:")
	for i, step := range recipe.WorkflowSteps {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("%d. %s", i+1, step))
	}
	b.WriteString("\n\nCaller prompt:\n")
	b.WriteString(prompt)
	return b.String()
}

func normalizeTaskToolClass(raw string, target string, gate bool, sandbox string) (string, error) {
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
		if implied != "" {
			taskClass = implied
		}
	} else {
		if sandbox != "" {
			if err := validateSandbox(sandbox); err != nil {
				return "", err
			}
		}
	}

	if gate && taskClass != classifier.TaskClassReview {
		return "", extypes.NewUserInputError("task: gate param requires task_class review", nil)
	}
	if target != "" && taskClass != classifier.TaskClassReview && taskClass != classifier.TaskClassSpec {
		return "", extypes.NewUserInputError("task: target param requires task_class review or spec", nil)
	}
	if sandbox != "" && taskClass != classifier.TaskClassCode {
		return "", extypes.NewUserInputError("task: sandbox param requires task_class code", nil)
	}
	if taskClass == classifier.TaskClassReview && strings.TrimSpace(target) == "" {
		return "", extypes.NewUserInputError("task: target is required for review task_class", nil)
	}
	if taskClass == classifier.TaskClassSpec && strings.TrimSpace(target) == "" {
		return "", extypes.NewUserInputError("task: target is required for spec task_class", nil)
	}
	return taskClass, nil
}

func validTaskToolClass(taskClass string) bool {
	switch taskClass {
	case "", taskClassTask, classifier.TaskClassCode, classifier.TaskClassReview, classifier.TaskClassSpec:
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

func taskToolError(result TaskResult, err error) (*mcp.CallToolResult, error) {
	cliErr := taskCLIError(err)
	payload := map[string]any{
		"code":      cliErr.Code.String(),
		"message":   cliErr.Message,
		"retryable": cliErr.Retryable,
	}
	var recipeErr *taskRecipeInputError
	if errors.As(err, &recipeErr) {
		payload["available_recipes"] = cloneRecipeStrings(recipeErr.availableRecipes)
	}
	var policyErr *taskRecipePolicyError
	if errors.As(err, &policyErr) {
		result := policyErr.Result()
		payload["recipe_id"] = result.RecipeID
		payload["selected_cli"] = result.SelectedCLI
		payload["requested_policy"] = result.RequestedPolicy
		payload["missing_capabilities"] = result.MissingCapabilities
		payload["supported_capabilities"] = result.SupportedCapabilities
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

type taskRecipeInputError struct {
	err              *extypes.CLIError
	availableRecipes []string
}

func newUnsupportedRecipeIDError(recipeID string) error {
	return &taskRecipeInputError{
		err:              extypes.NewUserInputError(fmt.Sprintf("task: unsupported recipe_id %q", recipeID), nil),
		availableRecipes: recipes.AvailableIDs(),
	}
}

func newUnsupportedReviewRecipeIDError(recipeID string) error {
	return &taskRecipeInputError{
		err: extypes.NewUserInputError(
			fmt.Sprintf("review: unsupported recipe_id %q; use task(recipe_id=...) for recipes outside the public review facade", recipeID),
			nil,
		),
		availableRecipes: []string{"code-review"},
	}
}

func (e *taskRecipeInputError) Error() string {
	if e == nil || e.err == nil {
		return "task: recipe input error"
	}
	return e.err.Error()
}

func (e *taskRecipeInputError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func taskRouterLoomUnavailableError(cause error) *extypes.CLIError {
	err := extypes.NewCapabilityMismatch(formatLoomUnavailableError(cause), cause)
	err.Retryable = false
	return err
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
	if available, ok := s.registry.IsAvailable(cli); ok && !available {
		return "", extypes.NewCapabilityMismatch(
			fmt.Sprintf("CLI %q is runtime-unavailable (warmup failed or still unavailable); fail-fast instead of spawning", cli),
			nil,
		)
	}

	binaryPath := profile.ResolvedPath
	if binaryPath == "" {
		binaryPath = profile.Binary
	}
	if binaryPath == "" {
		return "", extypes.NewBinaryNotFound(fmt.Sprintf("CLI %q has no binary path", cli), nil)
	}

	spawnArgs := taskDispatchSpawnArgs(cli, binaryPath, profile, spec)
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

func taskDispatchSpawnArgs(cli string, binaryPath string, profile *config.CLIProfile, spec picker.TaskSpec) types.SpawnArgs {
	timeoutSeconds := profile.TimeoutSeconds
	if spec.TimeoutSeconds > 0 {
		timeoutSeconds = spec.TimeoutSeconds
	}
	return types.SpawnArgs{
		CLI:               cli,
		Command:           binaryPath,
		Args:              buildTaskArgs(profile, spec),
		CWD:               taskDispatchCWD(spec.CWD),
		Env:               cloneEnv(spec.Env),
		TimeoutSeconds:    timeoutSeconds,
		CompletionPattern: profile.CompletionPattern,
		OnOutput:          spec.OnOutput,
	}
}

// buildTaskArgs constructs the CLI argument list for a task prompt.
//
// Decision order:
//  1. command.base subcommands/flags.
//  2. command.args_template when present, with missing headless/read-only profile flags preserved.
//  3. Otherwise, headless/read-only/model/effort profile flags.
//  4. PromptFlagType == "stdin" → append StdinSentinel if non-empty; prompt arrives via stdin.
//  5. Default (flag, positional, empty, or unrecognized) → append prompt flag or positional prompt.
//
// profile.Command.Base may include the binary plus subcommands (e.g., "codex exec").
// taskDispatch supplies the binary separately, so the leading binary token is stripped
// and only subcommands/flags are prepended. The result is never nil.
func buildTaskArgs(profile *config.CLIProfile, spec picker.TaskSpec) []string {
	args := commandBaseArgs(profile)
	if args == nil {
		args = []string{}
	}
	// Work on a copy so callers can safely reuse config-owned slices.
	args = append([]string{}, args...)
	// MCP suppression (issue #359): applied before the template/default split so
	// it covers BOTH arg-building branches (codex uses args_template; claude uses
	// the default headless branch). Suppressing the child CLI's MCP table stops
	// it opening MCP clients back toward the spawning daemon on startup.
	if len(profile.MCPSuppressionFlags) > 0 {
		args = append(args, profile.MCPSuppressionFlags...)
	}
	if templateArgs, ok := commandArgsTemplateArgs(profile, spec); ok {
		args = appendMissingProfileExecutionFlags(args, profile, spec, templateArgs)
		return append(args, templateArgs...)
	}
	if profile.Features.Headless && len(profile.HeadlessFlags) > 0 {
		args = append(args, profile.HeadlessFlags...)
	}
	switch spec.Sandbox {
	case "read-only":
		if len(profile.ReadOnlyFlags) > 0 {
			args = append(args, profile.ReadOnlyFlags...)
		}
	case "workspace-write", "danger":
		if len(profile.WriteSandboxFlags) > 0 {
			args = append(args, profile.WriteSandboxFlags...)
		}
	}
	if model := taskModelForArgs(profile, spec); model != "" && profile.ModelFlag != "" {
		args = append(args, profile.ModelFlag, model)
	}
	if spec.Effort != "" && profile.Reasoning != nil && profile.Reasoning.Flag != "" {
		args = append(args, profile.Reasoning.Flag, reasoningFlagValue(profile.Reasoning, spec.Effort))
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
			args = append(args, profile.PromptFlag, spec.Prompt)
		} else {
			args = append(args, spec.Prompt)
		}
	}
	return args
}

type taskArgsTemplateData struct {
	Prompt          string
	Model           string
	ReasoningEffort string
	SessionID       string
	Sandbox         string
	Headless        bool
	ReadOnly        bool
	SessionResume   bool
	JSON            bool
}

func commandArgsTemplateArgs(profile *config.CLIProfile, spec picker.TaskSpec) ([]string, bool) {
	if profile == nil || strings.TrimSpace(profile.Command.ArgsTemplate) == "" {
		return nil, false
	}

	// Engram #242 (S1): validate every user-controlled value before it lands in
	// the rendered template. The args_template path tokenises the rendered
	// command line via splitCommandLine, so any unescaped whitespace inside an
	// unquoted template field (e.g. "--model {{.Model}}") injects extra argv
	// elements into the spawned CLI. Reject control characters in any value,
	// reject whitespace in identifier-shaped fields (Model/Effort/SessionID)
	// because those are always rendered unquoted, and reject "'" everywhere as
	// defence-in-depth for hypothetical single-quote-wrapped template segments.
	// On any rejection the caller falls back to the argv-based dispatch path
	// (buildTaskArgs default branch), which passes each value as a discrete
	// argv element and cannot be injected through splitCommandLine.
	prompt := spec.Prompt
	model := taskModelForArgs(profile, spec)
	effort := strings.TrimSpace(spec.Effort)
	sessionID := strings.TrimSpace(spec.SessionID)
	if err := validateCommandTemplateValue("prompt", prompt, templateValueModePrompt); err != nil {
		return nil, false
	}
	if err := validateCommandTemplateValue("model", model, templateValueModeIdentifier); err != nil {
		return nil, false
	}
	if err := validateCommandTemplateValue("effort", effort, templateValueModeIdentifier); err != nil {
		return nil, false
	}
	if err := validateCommandTemplateValue("session_id", sessionID, templateValueModeIdentifier); err != nil {
		return nil, false
	}

	tmpl, err := template.New("task_args").Option("missingkey=error").Parse(profile.Command.ArgsTemplate)
	if err != nil {
		return nil, false
	}
	data := taskArgsTemplateData{
		Prompt:          commandTemplateArgValue(prompt),
		Model:           commandTemplateArgValue(model),
		ReasoningEffort: commandTemplateArgValue(effort),
		SessionID:       commandTemplateArgValue(sessionID),
		Sandbox:         strings.TrimSpace(spec.Sandbox),
		Headless:        profile.Features.Headless,
		ReadOnly:        spec.Sandbox == "read-only",
		SessionResume:   spec.SessionResume || strings.TrimSpace(spec.SessionID) != "",
		JSON:            profile.Features.JSON || strings.EqualFold(profile.OutputFormat, "json"),
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, false
	}
	fields, err := splitCommandLine(buf.String())
	if err != nil {
		return nil, false
	}
	return fields, true
}

func commandTemplateArgValue(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
}

// templateValueMode controls which characters are forbidden inside a value
// destined for the args_template path. See validateCommandTemplateValue.
type templateValueMode int

const (
	// templateValueModePrompt: free-form text that normally lands inside a
	// "..."-quoted template segment. Spaces are allowed; control characters
	// (\n, \r, \t) and single quotes are not.
	templateValueModePrompt templateValueMode = iota
	// templateValueModeIdentifier: short identifier (model, effort, session_id)
	// that normally lands in an unquoted template position. No whitespace at
	// all, no single quotes.
	templateValueModeIdentifier
)

// validateCommandTemplateValue rejects user-controlled values that would
// break splitCommandLine tokenisation when rendered into args_template.
//
// Returns nil if the value is safe; a non-nil error otherwise. The caller is
// expected to fall back to the argv-based dispatch path on error.
func validateCommandTemplateValue(field, value string, mode templateValueMode) error {
	if strings.ContainsAny(value, "\n\r\t") {
		return fmt.Errorf("args_template field %q contains a control character", field)
	}
	if strings.Contains(value, "'") {
		return fmt.Errorf("args_template field %q contains a single quote", field)
	}
	if mode == templateValueModeIdentifier && strings.ContainsAny(value, " ") {
		return fmt.Errorf("args_template field %q contains whitespace", field)
	}
	return nil
}

func appendMissingProfileExecutionFlags(args []string, profile *config.CLIProfile, spec picker.TaskSpec, templateArgs []string) []string {
	if profile.Features.Headless && len(profile.HeadlessFlags) > 0 {
		args = appendMissingArgs(args, templateArgs, profile.HeadlessFlags)
	}
	if spec.Sandbox == "read-only" && len(profile.ReadOnlyFlags) > 0 {
		args = appendMissingArgs(args, templateArgs, profile.ReadOnlyFlags)
	}
	return args
}

func appendMissingArgs(args []string, existing []string, candidates []string) []string {
	for _, candidate := range candidates {
		if containsString(existing, candidate) || containsString(args, candidate) {
			continue
		}
		args = append(args, candidate)
	}
	return args
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func taskModelForArgs(profile *config.CLIProfile, spec picker.TaskSpec) string {
	if model := strings.TrimSpace(spec.Model); model != "" {
		return model
	}
	return strings.TrimSpace(profile.DefaultModel)
}

func reasoningFlagValue(reasoning *config.ReasoningConfig, effort string) string {
	effort = strings.TrimSpace(effort)
	if reasoning.FlagValueTemplate == "" {
		return effort
	}
	value := strings.ReplaceAll(reasoning.FlagValueTemplate, "{{.Level}}", effort)
	return strings.ReplaceAll(value, "{{.ReasoningEffort}}", effort)
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
	)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		fields = append(fields, current.String())
		current.Reset()
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\\' && quote == '"' {
			if i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
				i++
				current.WriteRune(runes[i])
				continue
			}
			current.WriteRune(r)
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
