package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/executor"
	conptyExec "github.com/thebtf/aimux/pkg/executor/conpty"
	pipeExec "github.com/thebtf/aimux/pkg/executor/pipe"
	ptyExec "github.com/thebtf/aimux/pkg/executor/pty"
	"github.com/thebtf/aimux/pkg/parser"
	"github.com/thebtf/aimux/pkg/resolve"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/server/budget"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

func (s *Server) handleExec(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	bp, budgetErr := budget.ParseBudgetParams(request)
	if budgetErr != nil {
		return mcp.NewToolResultError(budgetErr.Error()), nil
	}

	prompt, err := request.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError("prompt is required"), nil
	}

	maxPrompt := s.cfg.Server.MaxPromptBytes
	if maxPrompt > 0 && len(prompt) > maxPrompt {
		return mcp.NewToolResultError(fmt.Sprintf("prompt too large (%d bytes, max %d)", len(prompt), maxPrompt)), nil
	}

	cli := request.GetString("cli", "")
	role := request.GetString("role", "default")

	model := request.GetString("model", "")
	effort := request.GetString("reasoning_effort", "")
	cwd := cwdFromRequestOrContext(request, ctx)
	if cwd != "" {
		cwd = filepath.Clean(cwd)
		if info, err := os.Stat(cwd); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("cwd %q not found: %v", cwd, err)), nil
		} else if !info.IsDir() {
			return mcp.NewToolResultError(fmt.Sprintf("cwd %q is not a directory", cwd)), nil
		}
	}
	sessionID := request.GetString("session_id", "")
	async := request.GetBool("async", true)
	readOnly := request.GetBool("read_only", false)
	timeoutSec := int(request.GetFloat("timeout_seconds", 0))

	// Session resume: if session_id provided, lookup existing session and inherit CLI
	if sessionID != "" {
		existing := s.sessions.Get(sessionID)
		if existing == nil {
			return mcp.NewToolResultError(fmt.Sprintf("session %q not found", sessionID)), nil
		}
		if existing.Status == types.SessionStatusFailed || existing.Status == types.SessionStatusCompleted {
			return mcp.NewToolResultError(fmt.Sprintf("session %q is %s — create a new session instead", sessionID, existing.Status)), nil
		}
		if cli == "" {
			cli = existing.CLI
		} else if cli != existing.CLI {
			return mcp.NewToolResultError(fmt.Sprintf("session %q belongs to CLI %q, not %q", sessionID, existing.CLI, cli)), nil
		}
		if cwd == "" {
			cwd = existing.CWD
		}
		s.log.Info("exec: resuming session=%s cli=%s turns=%d", sessionID, cli, existing.Turns)
	}

	// Resolve CLI from role — validates the role name and applies capability-aware
	// routing. Unknown role names (neither in defaults nor in any CLI's capabilities)
	// return an error immediately. Skip when cli= is explicitly provided (role is
	// advisory only) or when session resume already set cli.
	if cli == "" {
		pref, resolveErr := s.router.Resolve(role)
		if resolveErr != nil {
			// Log full error (includes capable CLI names) server-side for debugging.
			// Return a sanitized message to avoid leaking internal routing topology.
			s.log.Warn("exec: role resolution failed role=%q: %v", role, resolveErr)
			return mcp.NewToolResultError(fmt.Sprintf("unknown role %q: no CLI available", role)), nil
		}
		cli = pref.CLI
		if model == "" {
			model = pref.Model
		}
		if effort == "" {
			effort = pref.ReasoningEffort
		}
	}

	// Check circuit breaker
	cb := s.breakers.Get(cli)
	if !cb.Allow() {
		return mcp.NewToolResultError(types.NewCircuitOpenError(cli).Error()), nil
	}

	// Get CLI profile
	profile, profileErr := s.registry.Get(cli)
	if profileErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("CLI %q not configured", cli)), nil
	}

	// Resolve read_only from role
	if routing.IsAdvisory(role) {
		readOnly = true
	}

	// Resolve timeout
	if timeoutSec == 0 {
		timeoutSec = profile.TimeoutSeconds
	}
	// Ensure minimum timeout for reasoning tasks — high/xhigh need more time
	if effort == "high" || effort == "xhigh" {
		minTimeout := 120
		if timeoutSec < minTimeout {
			timeoutSec = minTimeout
		}
	}

	// Constitution P2: Solo coding prohibited — route coding role through PairCoding
	if role == "coding" {
		s.log.Info("exec: role=coding → PairCoding strategy (cli=%s)", cli)

		// Resolve reviewer CLI (different from driver)
		reviewerCLI := "claude"
		reviewerPref, _ := s.router.Resolve("codereview")
		if reviewerPref.CLI != "" && reviewerPref.CLI != cli {
			reviewerCLI = reviewerPref.CLI
		}

		pairParams := types.StrategyParams{
			Prompt:  prompt,
			CLIs:    []string{cli, reviewerCLI},
			CWD:     cwd,
			Timeout: timeoutSec,
			Model:   model,
			Effort:  effort,
			Extra: map[string]any{
				"max_rounds": s.cfg.Server.Pair.MaxRounds,
				"complex":    request.GetBool("complex", false),
			},
		}

		if async && s.loom != nil {
			// Route through LoomEngine — task survives disconnect.
			taskID, err := s.loom.Submit(ctx, loom.TaskRequest{
				WorkerType: loom.WorkerTypeOrchestrator,
				ProjectID:  projectIDFromContext(ctx),
				Prompt:     prompt,
				CWD:        cwd,
				Env:        FilterSensitive(sessionEnvFromContext(ctx)),
				CLI:        cli,
				Model:      model,
				Effort:     effort,
				Timeout:    timeoutSec,
				Metadata: map[string]any{
					"strategy": "pair_coding",
					"clis":     pairParams.CLIs,
					"extra":    pairParams.Extra,
				},
			})
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("loom submit: %v", err)), nil
			}
			result := map[string]any{"job_id": taskID, "status": "running"}
			return marshalToolResult(result)
		}

		// Legacy path (when s.loom == nil or sync):
		if async {
			if err := s.checkConcurrencyLimit(); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
			job := s.jobs.Create(sess.ID, cli)
			s.log.Info("exec: PairCoding driver=%s reviewer=%s job=%s async=%v", cli, reviewerCLI, job.ID, async)
			jobCtx, jobCancel := context.WithCancel(context.Background())
			s.jobs.RegisterCancel(job.ID, jobCancel)
			s.sendBusy(job.ID, "pair_coding", pairParams.Timeout*1000)
			go func() {
				defer s.sendIdle(job.ID)
				s.executePairCoding(jobCtx, job.ID, sess.ID, pairParams, cb)
			}()
			result := map[string]any{
				"job_id":     job.ID,
				"session_id": sess.ID,
				"status":     "running",
			}
			return marshalToolResult(result)
		}

		sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
		job := s.jobs.Create(sess.ID, cli)
		s.log.Info("exec: PairCoding driver=%s reviewer=%s job=%s async=%v", cli, reviewerCLI, job.ID, async)
		s.executePairCoding(ctx, job.ID, sess.ID, pairParams, cb)

		j := s.jobs.GetSnapshot(job.ID)
		if j == nil {
			return mcp.NewToolResultError("job disappeared"), nil
		}
		if j.Status == types.JobStatusFailed && j.Error != nil {
			return mcp.NewToolResultError(j.Error.Error()), nil
		}

		// j.Content is JSON-encoded StrategyResult with ReviewReport
		result := map[string]any{
			"session_id": sess.ID,
			"status":     string(j.Status),
		}
		// Parse strategy result to include fields at top level
		var stratResult types.StrategyResult
		if json.Unmarshal([]byte(j.Content), &stratResult) == nil {
			contentLen := len(stratResult.Content)
			if bp.Tail > 0 {
				tail := stratResult.Content
				if len(tail) > bp.Tail {
					tail = tail[len(tail)-bp.Tail:]
				}
				result["content_tail"] = tail
				result["content_length"] = contentLen
				meta := budget.BuildTruncationMeta(nil, contentLen, "Use exec with include_content=true for full output.")
				if meta.Truncated {
					result["truncated"] = meta.Truncated
					result["hint"] = meta.Hint
				}
			} else if bp.IncludeContent {
				result["content"] = stratResult.Content
			} else {
				result["content_length"] = contentLen
				meta := budget.BuildTruncationMeta(nil, contentLen, "Use exec with include_content=true for full output.")
				if meta.Truncated {
					result["truncated"] = meta.Truncated
					result["hint"] = meta.Hint
				}
			}
			result["turns"] = stratResult.Turns
			result["participants"] = stratResult.Participants
			if stratResult.ReviewReport != nil {
				result["review_report"] = stratResult.ReviewReport
			}
		} else {
			contentLen := len(j.Content)
			if bp.Tail > 0 {
				tail := j.Content
				if len(tail) > bp.Tail {
					tail = tail[len(tail)-bp.Tail:]
				}
				result["content_tail"] = tail
				result["content_length"] = contentLen
				meta := budget.BuildTruncationMeta(nil, contentLen, "Use exec with include_content=true for full output.")
				if meta.Truncated {
					result["truncated"] = meta.Truncated
					result["hint"] = meta.Hint
				}
			} else if bp.IncludeContent {
				result["content"] = j.Content
			} else {
				result["content_length"] = contentLen
				meta := budget.BuildTruncationMeta(nil, contentLen, "Use exec with include_content=true for full output.")
				if meta.Truncated {
					result["truncated"] = meta.Truncated
					result["hint"] = meta.Hint
				}
			}
		}
		whitelist := budget.FieldWhitelist["exec"]
		filtered, _, applyErr := budget.ApplyFields(result, bp.Fields, whitelist)
		if applyErr != nil {
			return mcp.NewToolResultError(applyErr.Error()), nil
		}
		return marshalToolResult(filtered)
	}

	// Bootstrap prompt injection: prepend role-specific prompt from prompts.d/
	prompt = s.injectBootstrap(role, prompt)

	// Inject per-session environment (API keys etc.) from ProjectContext.
	var sessionEnv map[string]string
	if pc, ok := ProjectContextFromContext(ctx); ok {
		sessionEnv = pc.Env
	}

	// Non-coding roles: direct execution
	args := types.SpawnArgs{
		CLI:            cli,
		Command:        resolve.CommandBinary(profile.Command.Base),
		Args:           resolve.BuildPromptArgs(profile, model, effort, readOnly, prompt),
		CWD:            cwd,
		EnvList:        resolve.BuildEnv(profile, sessionEnv),
		TimeoutSeconds: timeoutSec,
	}

	// Stdin piping for long prompts (Windows 8191 char limit)
	if profile.StdinThreshold > 0 && len(prompt) > profile.StdinThreshold {
		args.Stdin = prompt
		args.Args = resolve.BuildPromptArgs(profile, model, effort, readOnly, "") // empty prompt — piped via stdin
		s.log.Info("exec: stdin piping activated (prompt=%d chars, threshold=%d)", len(prompt), profile.StdinThreshold)
	}

	if async && s.loom != nil {
		// Route through LoomEngine — task survives disconnect.
		taskID, err := s.loom.Submit(ctx, loom.TaskRequest{
			WorkerType: loom.WorkerTypeCLI,
			ProjectID:  projectIDFromContext(ctx),
			Prompt:     prompt,
			CWD:        cwd,
			Env:        FilterSensitive(sessionEnvFromContext(ctx)),
			CLI:        cli,
			Role:       role,
			Model:      model,
			Effort:     effort,
			Timeout:    timeoutSec,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("loom submit: %v", err)), nil
		}
		result := map[string]any{"job_id": taskID, "status": "running"}
		return marshalToolResult(result)
	}

	// Legacy path (when s.loom == nil or sync):
	if async {
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
		job := s.jobs.Create(sess.ID, cli)
		s.log.Info("exec: cli=%s role=%s job=%s async=%v", cli, role, job.ID, async)
		jobCtx, jobCancel := context.WithCancel(context.Background())
		s.jobs.RegisterCancel(job.ID, jobCancel)
		go s.executeJob(jobCtx, job.ID, sess.ID, role, args, cb, profile.OutputFormat)

		result := map[string]any{
			"job_id":     job.ID,
			"session_id": sess.ID,
			"status":     "running",
		}
		return marshalToolResult(result)
	}

	sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
	job := s.jobs.Create(sess.ID, cli)
	s.log.Info("exec: cli=%s role=%s job=%s async=%v", cli, role, job.ID, async)
	s.executeJob(ctx, job.ID, sess.ID, role, args, cb, profile.OutputFormat)

	j := s.jobs.GetSnapshot(job.ID)
	if j == nil {
		return mcp.NewToolResultError("job disappeared"), nil
	}

	if j.Status == types.JobStatusFailed && j.Error != nil {
		return mcp.NewToolResultError(j.Error.Error()), nil
	}

	result := map[string]any{
		"session_id": sess.ID,
		"status":     string(j.Status),
	}
	contentLen := len(j.Content)
	if bp.Tail > 0 {
		tail := j.Content
		if len(tail) > bp.Tail {
			tail = tail[len(tail)-bp.Tail:]
		}
		result["content_tail"] = tail
		result["content_length"] = contentLen
		meta := budget.BuildTruncationMeta(nil, contentLen, "Use exec with include_content=true for full output.")
		if meta.Truncated {
			result["truncated"] = meta.Truncated
			result["hint"] = meta.Hint
		}
	} else if bp.IncludeContent {
		result["content"] = j.Content
	} else {
		result["content_length"] = contentLen
		meta := budget.BuildTruncationMeta(nil, contentLen, "Use exec with include_content=true for full output.")
		if meta.Truncated {
			result["truncated"] = meta.Truncated
			result["hint"] = meta.Hint
		}
	}
	whitelist := budget.FieldWhitelist["exec"]
	filtered, _, applyErr := budget.ApplyFields(result, bp.Fields, whitelist)
	if applyErr != nil {
		return mcp.NewToolResultError(applyErr.Error()), nil
	}
	return marshalToolResult(filtered)
}

// executePairCoding runs a pair coding pipeline via orchestrator and updates job/session state.
func (s *Server) executePairCoding(ctx context.Context, jobID, sessionID string, params types.StrategyParams, cb *executor.CircuitBreaker) {
	s.jobs.StartJob(jobID, 0)
	s.sessions.Update(sessionID, func(sess *session.Session) {
		sess.Status = types.SessionStatusRunning
	})

	stratResult, err := s.orchestrator.Execute(ctx, "pair_coding", params)
	if err != nil {
		cb.RecordFailure(false)
		s.jobs.FailJob(jobID, types.NewExecutorError(err.Error(), err, ""))
		s.sessions.Update(sessionID, func(sess *session.Session) {
			sess.Status = types.SessionStatusFailed
		})
		s.log.Error("pair_coding failed: job=%s error=%v", jobID, err)
		return
	}

	cb.RecordSuccess()

	// Encode full strategy result as JSON content (includes ReviewReport)
	data, _ := json.Marshal(stratResult)
	s.jobs.CompleteJob(jobID, string(data), 0)
	s.sessions.Update(sessionID, func(sess *session.Session) {
		sess.Status = types.SessionStatusCompleted
		sess.Turns += stratResult.Turns
	})
	s.log.Info("pair_coding complete: job=%s turns=%d", jobID, stratResult.Turns)
}

// executeJob runs a CLI process, parses output, and updates job/session state.
// When role is non-empty the router's fallback list is consulted on transient
// failures (rate limit, auth error, connection error) — up to 2 additional CLIs
// are tried before giving up.
func (s *Server) executeJob(ctx context.Context, jobID, sessionID, role string, args types.SpawnArgs, cb *executor.CircuitBreaker, outputFormat string) {
	s.jobs.StartJob(jobID, 0)
	s.sessions.Update(sessionID, func(sess *session.Session) {
		sess.Status = types.SessionStatusRunning
	})

	// Extract projectID for per-project metrics (empty string if not in session mode).
	projectID := ""
	if pc, ok := ProjectContextFromContext(ctx); ok {
		projectID = pc.ID
	}

	// Declare busy to mcp-mux so the idle reaper does not evict this upstream
	// while the background goroutine runs. The deferred sendIdle covers every
	// return path below (success, failure, fallback exhaustion). See P26.
	s.sendBusy(jobID, "exec:"+args.CLI, args.TimeoutSeconds*1000)
	defer s.sendIdle(jobID)

	// Build the ordered list of CLIs to try. The primary (args.CLI) is always
	// first; fallbacks from the router follow (max 2 additional attempts).
	candidates := s.buildFallbackCandidates(role, args.CLI, cb)

	var (
		lastErr        *types.TypedError
		lastValidation *executor.TurnValidation
	)

	for attempt, cand := range candidates {
		currentArgs := args
		currentArgs.CLI = cand.CLI
		currentCB := s.breakers.Get(cand.CLI)

		// Resolve profile for the candidate CLI to get correct output format.
		currentFormat := outputFormat
		if cand.CLI != args.CLI {
			if p, err := s.registry.Get(cand.CLI); err == nil {
				currentFormat = p.OutputFormat
				// Rebuild args with the candidate profile so flags/binary are correct.
				currentArgs = s.rebuildArgsForCLI(args, p)
			}
		}
		currentArgs.OnOutput = s.progressSink(jobID, currentFormat)

		// Model fallback: if current CLI profile has a fallback chain, try all
		// models before moving to the next CLI.
		var result *types.Result
		var err error
		candProfile, profileErr := s.registry.Get(cand.CLI)
		if profileErr == nil && (len(candProfile.ModelFallback) > 0 || len(candProfile.FallbackSuffixStrip) > 0) && candProfile.ModelFlag != "" {
			result, err = s.runWithModelFallback(ctx, s.executor, candProfile, currentArgs)
		} else {
			result, err = s.executor.Run(ctx, currentArgs)
		}

		if err != nil {
			currentCB.RecordFailure(false)
			s.metrics.RecordRequest(cand.CLI, projectID, 0, true)
			lastErr = types.NewExecutorError(err.Error(), err, "")
			s.log.Warn("exec failed: job=%s cli=%s attempt=%d error=%v", jobID, cand.CLI, attempt+1, err)
			if attempt < len(candidates)-1 && isRetriableError(err.Error()) {
				s.log.Info("exec: cli=%s failed (retriable), trying fallback", cand.CLI)
				continue
			}
			s.jobs.FailJob(jobID, lastErr)
			s.sessions.Update(sessionID, func(sess *session.Session) {
				sess.Status = types.SessionStatusFailed
			})
			s.log.Error("exec failed (no more fallbacks): job=%s error=%v", jobID, err)
			return
		}

		currentCB.RecordSuccess()

		if result.Error != nil {
			s.metrics.RecordRequest(cand.CLI, projectID, 0, true)
			lastErr = result.Error
			s.log.Warn("exec partial: job=%s cli=%s attempt=%d error=%v", jobID, cand.CLI, attempt+1, result.Error)
			if attempt < len(candidates)-1 && isRetriableError(result.Error.Error()) {
				currentCB.RecordFailure(false)
				s.log.Info("exec: cli=%s partial error (retriable), trying fallback", cand.CLI)
				continue
			}
			s.jobs.FailJob(jobID, result.Error)
			s.sessions.Update(sessionID, func(sess *session.Session) {
				sess.Status = types.SessionStatusFailed
			})
			return
		}

		// Parse CLI output according to profile format (FR-1, FR-2, FR-3)
		parsed, cliSessionID := parser.ParseContent(result.Content, currentFormat)
		if cliSessionID != "" {
			result.CLISessionID = cliSessionID
		}

		// Validate turn content quality
		validation := executor.ValidateTurnContent(parsed, "", result.ExitCode)
		if !validation.Valid {
			s.metrics.RecordRequest(cand.CLI, projectID, 0, true)
			lastValidation = &validation
			s.log.Warn("exec validation failed: job=%s cli=%s attempt=%d errors=%v", jobID, cand.CLI, attempt+1, validation.Errors)
			if attempt < len(candidates)-1 && isRetriableValidationError(validation.Errors) {
				currentCB.RecordFailure(false)
				s.log.Info("exec: cli=%s validation failed (retriable), trying fallback", cand.CLI)
				continue
			}
			s.jobs.FailJob(jobID, types.NewExecutorError(validation.Errors[0], nil, "validation"))
			s.sessions.Update(sessionID, func(sess *session.Session) {
				sess.Status = types.SessionStatusFailed
			})
			return
		}

		if len(validation.Warnings) > 0 {
			s.log.Warn("exec warnings: job=%s cli=%s warnings=%v", jobID, cand.CLI, validation.Warnings)
		}

		s.metrics.RecordRequest(cand.CLI, projectID, result.DurationMS, false)
		s.jobs.CompleteJob(jobID, parsed, result.ExitCode)
		s.sessions.Update(sessionID, func(sess *session.Session) {
			sess.Status = types.SessionStatusCompleted
			sess.Turns++
		})
		s.log.Info("exec complete: job=%s cli=%s attempt=%d exit=%d raw=%d parsed=%d",
			jobID, cand.CLI, attempt+1, result.ExitCode, len(result.Content), len(parsed))
		return
	}

	// All candidates exhausted — surface the last known error.
	finalErr := lastErr
	if finalErr == nil && lastValidation != nil {
		finalErr = types.NewExecutorError(lastValidation.Errors[0], nil, "validation")
	}
	if finalErr == nil {
		finalErr = types.NewExecutorError("all fallback CLIs exhausted", nil, "fallback")
	}
	s.jobs.FailJob(jobID, finalErr)
	s.sessions.Update(sessionID, func(sess *session.Session) {
		sess.Status = types.SessionStatusFailed
	})
	s.log.Error("exec: all fallbacks failed: job=%s error=%v", jobID, finalErr)
}

// buildFallbackCandidates returns an ordered list of CLIs to try for a job.
// The primary is always first. When role is non-empty, up to 2 additional
// capable CLIs are appended (those whose circuit breakers allow requests).
func (s *Server) buildFallbackCandidates(role, primaryCLI string, primaryCB *executor.CircuitBreaker) []types.RolePreference {
	const maxFallbacks = 2

	primary := types.RolePreference{CLI: primaryCLI}

	if role == "" {
		return []types.RolePreference{primary}
	}

	all := s.router.ResolveWithFallback(role)

	// Ensure primary is always first even if router returned a different order.
	ordered := make([]types.RolePreference, 0, 1+maxFallbacks)
	ordered = append(ordered, primary)

	added := 0
	for _, pref := range all {
		if pref.CLI == primaryCLI || added >= maxFallbacks {
			continue
		}
		// Only include CLIs whose breakers allow requests.
		if s.breakers.Get(pref.CLI).Allow() {
			ordered = append(ordered, pref)
			added++
		}
	}

	return ordered
}

// rebuildArgsForCLI creates a new SpawnArgs using the fallback CLI's profile
// binary and command — preserving prompt, CWD, timeout, and stdin from the
// original args. Flags are rebuilt via resolve so they match the new profile.
func (s *Server) rebuildArgsForCLI(orig types.SpawnArgs, profile *config.CLIProfile) types.SpawnArgs {
	rebuilt := types.SpawnArgs{
		CLI:            profile.Name,
		Command:        resolve.CommandBinary(profile.Command.Base),
		Args:           resolve.BuildPromptArgs(profile, "", "", false, orig.Stdin),
		CWD:            orig.CWD,
		Env:            orig.Env,
		TimeoutSeconds: orig.TimeoutSeconds,
		Stdin:          orig.Stdin,
	}
	// Extract the prompt from original args: if Stdin was used as the prompt
	// (stdin piping mode), keep it; otherwise try to recover from Args.
	if orig.Stdin == "" {
		// Prompt was embedded in args — use the last positional arg as prompt.
		prompt := extractPromptFromArgs(orig.Args)
		rebuilt.Args = resolve.BuildPromptArgs(profile, "", "", false, prompt)
	}
	return rebuilt
}

// extractPromptFromArgs returns the last non-flag argument from an args slice,
// which is where positional prompts are placed by resolve.BuildPromptArgs.
func extractPromptFromArgs(args []string) string {
	for i := len(args) - 1; i >= 0; i-- {
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
	}
	return ""
}

// isRetriableError returns true for transient infrastructure errors where a
// different CLI might succeed (rate limits, auth failures, connection errors).
func isRetriableError(msg string) bool {
	lower := strings.ToLower(msg)
	retriable := []string{
		"rate limit",
		"rate_limit",
		"quota exceeded",
		"quota_exceeded",
		"429",
		"authentication",
		"auth",
		"connection refused",
		"connection timeout",
		"etimedout",
		"econnrefused",
		"enotfound",
		"dns resolution",
	}
	for _, pattern := range retriable {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// isQuotaError returns true if the error indicates a model-level quota/rate limit.
// Used to trigger model fallback (try next model on same CLI) before CLI fallback.
func isQuotaError(content, stderr string, exitCode int) bool {
	return executor.ClassifyError(content, stderr, exitCode) == executor.ErrorClassQuota
}

// runWithModelFallback tries each model in the profile's ModelFallback chain.
// Delegates to executor.RunWithModelFallback for the canonical state machine.
func (s *Server) runWithModelFallback(
	ctx context.Context,
	exec types.Executor,
	profile *config.CLIProfile,
	baseArgs types.SpawnArgs,
) (*types.Result, error) {
	cooldownDuration := time.Duration(profile.CooldownSeconds) * time.Second
	// Detect current model from args to feed into suffix-strip chain.
	currentModel := executor.DetectModelFromArgs(baseArgs.Args, profile.ModelFlag)
	models := executor.BuildModelChain(currentModel, profile.ModelFallback, profile.FallbackSuffixStrip)
	return executor.RunWithModelFallback(
		ctx,
		exec,
		baseArgs,
		models,
		profile.ModelFlag,
		s.cooldownTracker,
		cooldownDuration,
		func(format string, args ...any) { s.log.Warn(format, args...) },
	)
}

// isRetriableValidationError returns true when validation errors indicate a
// transient infrastructure problem rather than a permanent content issue.
func isRetriableValidationError(errors []string) bool {
	for _, e := range errors {
		if isRetriableError(e) {
			return true
		}
	}
	return false
}

// injectBootstrap prepends role-specific prompt from prompts.d/ if available.
// Falls back to original prompt if no template found for the role.
func (s *Server) injectBootstrap(role, userPrompt string) string {
	if s.promptEng == nil {
		return userPrompt
	}

	// Try role-specific template (e.g., "coding-rules", "review-checklist")
	tmpl, err := s.promptEng.Resolve(role, nil)
	if err != nil {
		return userPrompt // no template for this role — use prompt as-is
	}

	return tmpl + "\n\n" + userPrompt
}

// contains reports whether s appears in the slice.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// checkConcurrencyLimit returns an error if the maximum concurrent job limit is reached.
func (s *Server) checkConcurrencyLimit() error {
	max := s.cfg.Server.MaxConcurrentJobs
	if max <= 0 {
		return nil // no limit configured
	}
	running := s.jobs.CountRunning()
	if running >= max {
		return fmt.Errorf("max concurrent jobs reached (%d/%d) — wait for running jobs to complete", running, max)
	}
	return nil
}

// selectBestExecutor returns the best available executor for the current platform.
// Priority: ConPTY (Windows) > PTY (Linux/Mac) > Pipe (everywhere).
func selectBestExecutor() types.Executor {
	sel := executor.NewSelector(
		conptyExec.New(), // ConPTY: Windows 10 1809+
		ptyExec.New(),    // PTY: Linux, macOS
		pipeExec.New(),   // Pipe: everywhere (fallback)
	)
	return sel.Select()
}

// projectIDFromContext extracts the ProjectContext.ID for Loom task scoping.
func projectIDFromContext(ctx context.Context) string {
	if pc, ok := ProjectContextFromContext(ctx); ok {
		return pc.ID
	}
	return ""
}

// sessionEnvFromContext extracts the per-session environment (API keys).
func sessionEnvFromContext(ctx context.Context) map[string]string {
	if pc, ok := ProjectContextFromContext(ctx); ok {
		return pc.Env
	}
	return nil
}
