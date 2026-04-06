// Package server implements the MCP server using mcp-go SDK.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/thebtf/aimux/pkg/agents"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/executor"
	conptyExec "github.com/thebtf/aimux/pkg/executor/conpty"
	pipeExec "github.com/thebtf/aimux/pkg/executor/pipe"
	ptyExec "github.com/thebtf/aimux/pkg/executor/pty"
	"github.com/thebtf/aimux/pkg/logger"
	orch "github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/prompt"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/tools/deepresearch"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

const serverVersion = "3.0.0-dev"

// Server holds all dependencies for the MCP server.
type Server struct {
	cfg          *config.Config
	log          *logger.Logger
	registry     *driver.Registry
	router       *routing.Router
	sessions     *session.Registry
	jobs         *session.JobManager
	breakers     *executor.BreakerRegistry
	executor     types.Executor
	mcp          *server.MCPServer
	orchestrator *orch.Orchestrator
	agentReg     *agents.Registry
	promptEng    *prompt.Engine
}

// New creates a new MCP server with all dependencies wired.
func New(cfg *config.Config, log *logger.Logger, reg *driver.Registry, router *routing.Router) *Server {
	s := &Server{
		cfg:      cfg,
		log:      log,
		registry: reg,
		router:   router,
		sessions: session.NewRegistry(),
		jobs:     session.NewJobManager(),
		breakers: executor.NewBreakerRegistry(executor.BreakerConfig{
			FailureThreshold: cfg.CircuitBreaker.FailureThreshold,
			CooldownSeconds:  cfg.CircuitBreaker.CooldownSeconds,
			HalfOpenMaxCalls: cfg.CircuitBreaker.HalfOpenMaxCalls,
		}),
		executor: selectBestExecutor(), // ConPTY > PTY > Pipe (Constitution P4)
	}

	// Initialize orchestrator with all strategies
	s.orchestrator = orch.New(log,
		orch.NewPairCoding(s.executor, s.executor),
		orch.NewSequentialDialog(s.executor),
		orch.NewParallelConsensus(s.executor),
		orch.NewStructuredDebate(s.executor),
		orch.NewAuditPipeline(s.executor),
	)

	// Initialize prompt engine with built-in and project prompts.d/
	builtInPrompts := filepath.Join(cfg.ConfigDir, "prompts.d")
	s.promptEng = prompt.NewEngine(builtInPrompts)
	if err := s.promptEng.Load(); err != nil {
		log.Warn("prompt engine load: %v", err)
	}

	// Initialize agent registry
	s.agentReg = agents.NewRegistry()
	// Discover agents from project and user directories
	if cwd, err := os.Getwd(); err == nil {
		home, _ := os.UserHomeDir()
		s.agentReg.Discover(cwd, home)
	}

	// Create MCP server with capabilities
	s.mcp = server.NewMCPServer(
		"aimux",
		serverVersion,
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true),
		server.WithPromptCapabilities(true),
		server.WithLogging(),
		server.WithRecovery(),
	)

	s.registerTools()
	s.registerResources()
	s.registerPrompts()

	return s
}

// ServeStdio starts the MCP server on stdio transport.
func (s *Server) ServeStdio() error {
	s.log.Info("MCP server starting on stdio (aimux v%s)", serverVersion)
	return server.ServeStdio(s.mcp)
}

// --- Tool Registration ---

func (s *Server) registerTools() {
	// exec tool
	s.mcp.AddTool(
		mcp.NewTool("exec",
			mcp.WithDescription("Execute a prompt via an AI coding CLI"),
			mcp.WithString("prompt",
				mcp.Required(),
				mcp.Description("The prompt to send to the CLI"),
			),
			mcp.WithString("cli",
				mcp.Description("CLI override (auto-resolved from role)"),
			),
			mcp.WithString("role",
				mcp.Description("Task type: coding, codereview, thinkdeep, secaudit, debug, planner, analyze, refactor, testgen, docgen"),
			),
			mcp.WithString("model",
				mcp.Description("Model override"),
			),
			mcp.WithString("reasoning_effort",
				mcp.Description("Reasoning effort: low/medium/high/xhigh"),
			),
			mcp.WithString("cwd",
				mcp.Description("Working directory for the CLI process"),
			),
			mcp.WithString("session_id",
				mcp.Description("Resume session by ID"),
			),
			mcp.WithBoolean("async",
				mcp.Description("Return job_id immediately for background execution"),
			),
			mcp.WithBoolean("read_only",
				mcp.Description("Execute in read-only sandbox mode"),
			),
			mcp.WithNumber("timeout_seconds",
				mcp.Description("Timeout override in seconds"),
			),
		),
		s.handleExec,
	)

	// status tool
	s.mcp.AddTool(
		mcp.NewTool("status",
			mcp.WithDescription("Check async job status"),
			mcp.WithString("job_id",
				mcp.Required(),
				mcp.Description("Job ID from async exec response"),
			),
		),
		s.handleStatus,
	)

	// sessions tool
	s.mcp.AddTool(
		mcp.NewTool("sessions",
			mcp.WithDescription("Manage sessions and jobs: list, info, health, cancel, gc"),
			mcp.WithString("action",
				mcp.Required(),
				mcp.Description("Action: list, info, kill, gc, health, cancel"),
				mcp.Enum("list", "info", "kill", "gc", "health", "cancel"),
			),
			mcp.WithString("session_id",
				mcp.Description("Session ID (required for info/kill)"),
			),
			mcp.WithString("job_id",
				mcp.Description("Job ID (required for cancel)"),
			),
			mcp.WithString("status",
				mcp.Description("Filter by status: active, completed, failed, all"),
			),
			mcp.WithNumber("limit",
				mcp.Description("Max results (default 10)"),
			),
		),
		s.handleSessions,
	)

	// audit tool
	s.mcp.AddTool(
		mcp.NewTool("audit",
			mcp.WithDescription("Run multi-agent codebase audit: scan→validate→investigate"),
			mcp.WithString("cwd",
				mcp.Description("Working directory to audit"),
			),
			mcp.WithString("mode",
				mcp.Description("Audit mode: quick (scan only), standard (scan+validate), deep (scan+validate+investigate)"),
				mcp.Enum("quick", "standard", "deep"),
			),
			mcp.WithString("scope",
				mcp.Description("Scope: full or changed"),
				mcp.Enum("full", "changed"),
			),
			mcp.WithBoolean("async",
				mcp.Description("Run in background"),
			),
		),
		s.handleAudit,
	)

	// think tool
	s.mcp.AddTool(
		mcp.NewTool("think",
			mcp.WithDescription("Structured thinking patterns for analysis and reasoning"),
			mcp.WithString("pattern",
				mcp.Required(),
				mcp.Description("Thinking pattern: critical_thinking, decision_framework, problem_decomposition, scientific_method, debugging_approach, sequential_thinking, etc."),
			),
			mcp.WithString("issue",
				mcp.Description("The issue or question to think about"),
			),
			mcp.WithString("topic",
				mcp.Description("Topic for analysis"),
			),
			mcp.WithString("session_id",
				mcp.Description("Session ID for stateful patterns"),
			),
		),
		s.handleThink,
	)

	// investigate tool
	s.mcp.AddTool(
		mcp.NewTool("investigate",
			mcp.WithDescription("Iterative convergent investigation with domain specialization"),
			mcp.WithString("action",
				mcp.Required(),
				mcp.Description("Action: start, finding, assess, report, status, list, recall"),
				mcp.Enum("start", "finding", "assess", "report", "status", "list", "recall"),
			),
			mcp.WithString("topic",
				mcp.Description("Investigation topic (required for start)"),
			),
			mcp.WithString("session_id",
				mcp.Description("Investigation session ID"),
			),
			mcp.WithString("domain",
				mcp.Description("Domain: generic, security, performance, code-quality, architecture, debugging"),
			),
		),
		s.handleInvestigate,
	)

	// consensus tool
	s.mcp.AddTool(
		mcp.NewTool("consensus",
			mcp.WithDescription("Multi-model blinded consensus with optional synthesis"),
			mcp.WithString("topic",
				mcp.Required(),
				mcp.Description("Topic for consensus"),
			),
			mcp.WithBoolean("synthesize",
				mcp.Description("Generate synthesis of opinions (default false)"),
			),
			mcp.WithBoolean("blinded",
				mcp.Description("Participants cannot see each other (default true)"),
			),
			mcp.WithNumber("max_turns",
				mcp.Description("Maximum turns"),
			),
			mcp.WithBoolean("async",
				mcp.Description("Run in background"),
			),
		),
		s.handleConsensus,
	)

	// debate tool
	s.mcp.AddTool(
		mcp.NewTool("debate",
			mcp.WithDescription("Structured adversarial debate with verdict synthesis"),
			mcp.WithString("topic",
				mcp.Required(),
				mcp.Description("Topic for debate"),
			),
			mcp.WithBoolean("synthesize",
				mcp.Description("Generate verdict (default true)"),
			),
			mcp.WithNumber("max_turns",
				mcp.Description("Maximum turns (default 6)"),
			),
			mcp.WithBoolean("async",
				mcp.Description("Run in background"),
			),
		),
		s.handleDebate,
	)

	// dialog tool
	s.mcp.AddTool(
		mcp.NewTool("dialog",
			mcp.WithDescription("Sequential multi-turn dialog between AI CLIs"),
			mcp.WithString("prompt",
				mcp.Required(),
				mcp.Description("Dialog topic or initial prompt"),
			),
			mcp.WithNumber("max_turns",
				mcp.Description("Maximum turns (default 6)"),
			),
			mcp.WithBoolean("async",
				mcp.Description("Run in background"),
			),
		),
		s.handleDialog,
	)

	// agents tool
	s.mcp.AddTool(
		mcp.NewTool("agents",
			mcp.WithDescription("Discover and run Loom Agents"),
			mcp.WithString("action",
				mcp.Required(),
				mcp.Description("Action: list, run, info, find"),
				mcp.Enum("list", "run", "info", "find"),
			),
			mcp.WithString("agent",
				mcp.Description("Agent name (required for run/info)"),
			),
			mcp.WithString("prompt",
				mcp.Description("Prompt for run, or search query for find"),
			),
		),
		s.handleAgents,
	)

	// deepresearch tool
	s.mcp.AddTool(
		mcp.NewTool("deepresearch",
			mcp.WithDescription("Deep research via Google Gemini API with file attachments and caching"),
			mcp.WithString("topic",
				mcp.Required(),
				mcp.Description("Research topic"),
			),
			mcp.WithString("output_format",
				mcp.Description("Output format hint (e.g., 'executive summary')"),
			),
			mcp.WithString("model",
				mcp.Description("Model override (default: gemini-2.0-flash)"),
			),
			mcp.WithBoolean("force",
				mcp.Description("Bypass cache"),
			),
		),
		s.handleDeepresearch,
	)
}

func (s *Server) registerResources() {
	// Job resource
	s.mcp.AddResource(
		mcp.NewResource(
			"aimux://health",
			"Server Health",
			mcp.WithResourceDescription("Current server health and running jobs"),
			mcp.WithMIMEType("application/json"),
		),
		s.handleHealthResource,
	)
}

func (s *Server) registerPrompts() {
	// Background execution protocol prompt
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-background",
			mcp.WithPromptDescription("Step-by-step orchestration for running aimux CLI tasks in background"),
			mcp.WithArgument("task_description",
				mcp.ArgumentDescription("Description of the task to execute"),
			),
		),
		s.handleBackgroundPrompt,
	)
}

// --- Tool Handlers ---

func (s *Server) handleExec(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	cwd := request.GetString("cwd", "")
	if cwd != "" {
		cwd = filepath.Clean(cwd)
		if info, err := os.Stat(cwd); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("cwd %q not found: %v", cwd, err)), nil
		} else if !info.IsDir() {
			return mcp.NewToolResultError(fmt.Sprintf("cwd %q is not a directory", cwd)), nil
		}
	}
	sessionID := request.GetString("session_id", "")
	async := request.GetBool("async", false)
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

	// Resolve CLI from role
	if cli == "" {
		pref, resolveErr := s.router.Resolve(role)
		if resolveErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("role resolution failed: %v", resolveErr)), nil
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
			Extra: map[string]any{
				"max_rounds": s.cfg.Server.Pair.MaxRounds,
				"complex":    request.GetBool("complex", false),
			},
		}

		sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
		job := s.jobs.Create(sess.ID, cli)
		s.log.Info("exec: PairCoding driver=%s reviewer=%s job=%s async=%v", cli, reviewerCLI, job.ID, async)

		if async {
			if err := s.checkConcurrencyLimit(); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			jobCtx, jobCancel := context.WithCancel(context.Background())
			s.jobs.RegisterCancel(job.ID, jobCancel)
			go s.executePairCoding(jobCtx, job.ID, sess.ID, pairParams, cb)
			result := map[string]any{
				"job_id":     job.ID,
				"session_id": sess.ID,
				"status":     "running",
			}
			data, _ := json.Marshal(result)
			return mcp.NewToolResultText(string(data)), nil
		}

		s.executePairCoding(ctx, job.ID, sess.ID, pairParams, cb)

		j := s.jobs.Get(job.ID)
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
			result["content"] = stratResult.Content
			result["turns"] = stratResult.Turns
			result["participants"] = stratResult.Participants
			if stratResult.ReviewReport != nil {
				result["review_report"] = stratResult.ReviewReport
			}
		} else {
			result["content"] = j.Content
		}
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}

	// Bootstrap prompt injection: prepend role-specific prompt from prompts.d/
	prompt = s.injectBootstrap(role, prompt)

	// Non-coding roles: direct execution
	sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
	job := s.jobs.Create(sess.ID, cli)

	args := types.SpawnArgs{
		CLI:            cli,
		Command:        commandBinary(profile.Command.Base),
		Args:           buildArgs(profile, model, effort, readOnly, prompt),
		CWD:            cwd,
		TimeoutSeconds: timeoutSec,
	}

	// Stdin piping for long prompts (Windows 8191 char limit)
	if profile.StdinThreshold > 0 && len(prompt) > profile.StdinThreshold {
		args.Stdin = prompt
		args.Args = buildArgs(profile, model, effort, readOnly, "") // empty prompt — piped via stdin
		s.log.Info("exec: stdin piping activated (prompt=%d chars, threshold=%d)", len(prompt), profile.StdinThreshold)
	}

	s.log.Info("exec: cli=%s role=%s job=%s async=%v", cli, role, job.ID, async)

	if async {
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		jobCtx, jobCancel := context.WithCancel(context.Background())
		s.jobs.RegisterCancel(job.ID, jobCancel)
		go s.executeJob(jobCtx, job.ID, sess.ID, args, cb)

		result := map[string]any{
			"job_id":     job.ID,
			"session_id": sess.ID,
			"status":     "running",
		}
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}

	s.executeJob(ctx, job.ID, sess.ID, args, cb)

	j := s.jobs.Get(job.ID)
	if j == nil {
		return mcp.NewToolResultError("job disappeared"), nil
	}

	if j.Status == types.JobStatusFailed && j.Error != nil {
		return mcp.NewToolResultError(j.Error.Error()), nil
	}

	result := map[string]any{
		"session_id": sess.ID,
		"status":     string(j.Status),
		"content":    j.Content,
	}
	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
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

// executeJob runs a CLI process and updates job/session state.
func (s *Server) executeJob(ctx context.Context, jobID, sessionID string, args types.SpawnArgs, cb *executor.CircuitBreaker) {
	s.jobs.StartJob(jobID, 0)
	s.sessions.Update(sessionID, func(sess *session.Session) {
		sess.Status = types.SessionStatusRunning
	})

	result, err := s.executor.Run(ctx, args)

	if err != nil {
		cb.RecordFailure(false)
		s.jobs.FailJob(jobID, types.NewExecutorError(err.Error(), err, ""))
		s.sessions.Update(sessionID, func(sess *session.Session) {
			sess.Status = types.SessionStatusFailed
		})
		s.log.Error("exec failed: job=%s error=%v", jobID, err)
		return
	}

	cb.RecordSuccess()

	if result.Error != nil {
		s.jobs.FailJob(jobID, result.Error)
		s.sessions.Update(sessionID, func(sess *session.Session) {
			sess.Status = types.SessionStatusFailed
		})
		s.log.Warn("exec partial: job=%s error=%v", jobID, result.Error)
		return
	}

	s.jobs.CompleteJob(jobID, result.Content, result.ExitCode)
	s.sessions.Update(sessionID, func(sess *session.Session) {
		sess.Status = types.SessionStatusCompleted
		sess.Turns++
	})
	s.log.Info("exec complete: job=%s exit=%d len=%d", jobID, result.ExitCode, len(result.Content))
}

func (s *Server) handleStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	jobID, err := request.RequireString("job_id")
	if err != nil {
		return mcp.NewToolResultError("job_id is required"), nil
	}

	j := s.jobs.Get(jobID)
	if j == nil {
		return mcp.NewToolResultError(fmt.Sprintf("job %q not found", jobID)), nil
	}

	pollCount := s.jobs.IncrementPoll(jobID)

	result := map[string]any{
		"job_id":     j.ID,
		"status":     string(j.Status),
		"progress":   j.Progress,
		"poll_count": pollCount,
		"session_id": j.SessionID,
	}

	if j.Status == types.JobStatusCompleted || j.Status == types.JobStatusFailed {
		result["content"] = j.Content
		if j.Error != nil {
			result["error"] = j.Error.Error()
		}
	}

	if pollCount >= 3 {
		result["warning"] = "Polling detected. Prefer background tasks over polling."
	}

	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleSessions(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action, err := request.RequireString("action")
	if err != nil {
		return mcp.NewToolResultError("action is required"), nil
	}

	switch action {
	case "list":
		statusFilter := request.GetString("status", "")
		sessions := s.sessions.List(types.SessionStatus(statusFilter))
		data, _ := json.Marshal(map[string]any{
			"sessions": sessions,
			"count":    len(sessions),
		})
		return mcp.NewToolResultText(string(data)), nil

	case "info":
		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for info"), nil
		}
		sess := s.sessions.Get(sessionID)
		if sess == nil {
			return mcp.NewToolResultError("session not found"), nil
		}
		jobs := s.jobs.ListBySession(sessionID)
		data, _ := json.Marshal(map[string]any{
			"session": sess,
			"jobs":    jobs,
		})
		return mcp.NewToolResultText(string(data)), nil

	case "health":
		running := s.jobs.ListRunning()
		data, _ := json.Marshal(map[string]any{
			"total_sessions": s.sessions.Count(),
			"running_jobs":   len(running),
		})
		return mcp.NewToolResultText(string(data)), nil

	case "cancel":
		jobID := request.GetString("job_id", "")
		if jobID == "" {
			return mcp.NewToolResultError("job_id required for cancel"), nil
		}
		if !s.jobs.CancelJob(jobID) {
			return mcp.NewToolResultError("job not found"), nil
		}
		return mcp.NewToolResultText(`{"status":"cancelled"}`), nil

	case "kill":
		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for kill"), nil
		}
		sess := s.sessions.Get(sessionID)
		if sess == nil {
			return mcp.NewToolResultError("session not found"), nil
		}
		// Fail all running jobs for this session
		for _, j := range s.jobs.ListBySession(sessionID) {
			if j.Status == types.JobStatusRunning || j.Status == types.JobStatusCreated {
				s.jobs.FailJob(j.ID, types.NewExecutorError("session killed", nil, ""))
			}
		}
		s.sessions.Delete(sessionID)
		return mcp.NewToolResultText(`{"status":"killed"}`), nil

	case "gc":
		// Garbage collect expired sessions (idle > 1 hour)
		collected := 0
		for _, sess := range s.sessions.List("") {
			if sess.Status == types.SessionStatusCompleted || sess.Status == types.SessionStatusFailed {
				s.sessions.Delete(sess.ID)
				collected++
			}
		}
		data, _ := json.Marshal(map[string]any{"collected": collected})
		return mcp.NewToolResultText(string(data)), nil

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action %q", action)), nil
	}
}

// --- Dialog Handler ---

func (s *Server) handleDialog(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	prompt, err := request.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError("prompt is required"), nil
	}

	enabled := s.registry.EnabledCLIs()
	if len(enabled) < 2 {
		return mcp.NewToolResultError("dialog requires at least 2 CLIs"), nil
	}

	params := types.StrategyParams{
		Prompt:   prompt,
		CLIs:     enabled[:2],
		MaxTurns: int(request.GetFloat("max_turns", 6)),
	}

	result, err := s.orchestrator.Execute(ctx, "dialog", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("dialog failed: %v", err)), nil
	}
	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

// --- Agents Handler ---

func (s *Server) handleAgents(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action, err := request.RequireString("action")
	if err != nil {
		return mcp.NewToolResultError("action is required"), nil
	}

	switch action {
	case "list":
		agentList := s.agentReg.List()
		data, _ := json.Marshal(map[string]any{"agents": agentList, "count": len(agentList)})
		return mcp.NewToolResultText(string(data)), nil

	case "find":
		query := request.GetString("prompt", "")
		if query == "" {
			return mcp.NewToolResultError("prompt required as search query for find"), nil
		}
		matches := s.agentReg.Find(query)
		data, _ := json.Marshal(map[string]any{"query": query, "matches": matches, "count": len(matches)})
		return mcp.NewToolResultText(string(data)), nil

	case "info":
		agentName := request.GetString("agent", "")
		if agentName == "" {
			return mcp.NewToolResultError("agent name required for info"), nil
		}
		agent, agentErr := s.agentReg.Get(agentName)
		if agentErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("agent %q not found", agentName)), nil
		}
		data, _ := json.Marshal(agent)
		return mcp.NewToolResultText(string(data)), nil

	case "run":
		agentName := request.GetString("agent", "")
		if agentName == "" {
			return mcp.NewToolResultError("agent name required for run"), nil
		}
		agent, agentErr := s.agentReg.Get(agentName)
		if agentErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("agent %q not found", agentName)), nil
		}
		prompt := request.GetString("prompt", "")
		fullPrompt := agent.Content + "\n\n" + prompt
		role := agent.Role
		if role == "" {
			role = "default"
		}
		cwd := request.GetString("cwd", "")

		// Resolve CLI from agent role
		cli := ""
		pref, resolveErr := s.router.Resolve(role)
		if resolveErr == nil {
			cli = pref.CLI
		}
		if cli == "" {
			cli = "codex" // default executor
		}

		profile, profileErr := s.registry.Get(cli)
		if profileErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("CLI %q not configured for agent role %q", cli, role)), nil
		}

		readOnly := routing.IsAdvisory(role)
		sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
		job := s.jobs.Create(sess.ID, cli)

		args := types.SpawnArgs{
			CLI:            cli,
			Command:        profile.Command.Base,
			Args:           buildArgs(profile, pref.Model, pref.ReasoningEffort, readOnly, fullPrompt),
			CWD:            cwd,
			TimeoutSeconds: profile.TimeoutSeconds,
		}

		s.log.Info("agents: run agent=%s cli=%s role=%s job=%s", agentName, cli, role, job.ID)

		cb := s.breakers.Get(cli)
		async := request.GetBool("async", false)

		if async {
			if err := s.checkConcurrencyLimit(); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			jobCtx, jobCancel := context.WithCancel(context.Background())
			s.jobs.RegisterCancel(job.ID, jobCancel)
			go s.executeJob(jobCtx, job.ID, sess.ID, args, cb)
			data, _ := json.Marshal(map[string]any{
				"agent":      agentName,
				"job_id":     job.ID,
				"session_id": sess.ID,
				"status":     "running",
			})
			return mcp.NewToolResultText(string(data)), nil
		}

		s.executeJob(ctx, job.ID, sess.ID, args, cb)

		j := s.jobs.Get(job.ID)
		if j == nil {
			return mcp.NewToolResultError("agent job disappeared"), nil
		}
		if j.Status == types.JobStatusFailed && j.Error != nil {
			return mcp.NewToolResultError(fmt.Sprintf("agent %q failed: %v", agentName, j.Error)), nil
		}

		data, _ := json.Marshal(map[string]any{
			"agent":      agentName,
			"session_id": sess.ID,
			"status":     string(j.Status),
			"content":    j.Content,
		})
		return mcp.NewToolResultText(string(data)), nil

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action %q", action)), nil
	}
}

// --- Audit Handler ---

func (s *Server) handleAudit(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cwd := request.GetString("cwd", "")
	mode := request.GetString("mode", "standard")
	async := request.GetBool("async", false)

	params := types.StrategyParams{
		Prompt: fmt.Sprintf("Audit codebase at %s", cwd),
		CWD:    cwd,
		Extra: map[string]any{
			"mode":              mode,
			"parallel_scanners": s.cfg.Server.Audit.ParallelScanners,
			"scanner_role":      s.cfg.Server.Audit.ScannerRole,
			"validator_role":    s.cfg.Server.Audit.ValidatorRole,
		},
	}

	if async {
		sess := s.sessions.Create("audit", types.SessionModeOnceStateless, cwd)
		job := s.jobs.Create(sess.ID, "audit")
		go func() {
			s.jobs.StartJob(job.ID, 0)
			result, stratErr := s.orchestrator.Execute(context.Background(), "audit", params)
			if stratErr != nil {
				s.jobs.FailJob(job.ID, types.NewExecutorError(stratErr.Error(), stratErr, ""))
				return
			}
			s.jobs.CompleteJob(job.ID, result.Content, 0)
		}()
		data, _ := json.Marshal(map[string]any{"job_id": job.ID, "status": "running"})
		return mcp.NewToolResultText(string(data)), nil
	}

	result, err := s.orchestrator.Execute(ctx, "audit", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("audit failed: %v", err)), nil
	}
	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

// --- Think Handler ---

func (s *Server) handleThink(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pattern, err := request.RequireString("pattern")
	if err != nil {
		return mcp.NewToolResultError("pattern is required"), nil
	}

	issue := request.GetString("issue", "")
	topic := request.GetString("topic", "")

	input := issue
	if input == "" {
		input = topic
	}
	if input == "" {
		input = pattern
	}

	// Think is in-process (solo mode) — no external CLI call
	result := map[string]any{
		"pattern": pattern,
		"input":   input,
		"output":  fmt.Sprintf("Thinking with pattern '%s' about: %s", pattern, input),
		"mode":    "solo",
	}

	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

// --- Investigate Handler ---

func (s *Server) handleInvestigate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action, err := request.RequireString("action")
	if err != nil {
		return mcp.NewToolResultError("action is required"), nil
	}

	switch action {
	case "start":
		topic := request.GetString("topic", "")
		if topic == "" {
			return mcp.NewToolResultError("topic required for start"), nil
		}
		domain := request.GetString("domain", "generic")

		sess := s.sessions.Create("investigate", types.SessionModeOnceStateful, "")
		result := map[string]any{
			"session_id": sess.ID,
			"topic":      topic,
			"domain":     domain,
			"status":     "started",
			"message":    "Investigation started. Use finding action to add findings, assess to check convergence.",
		}
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil

	case "status", "list", "recall":
		return mcp.NewToolResultText(fmt.Sprintf(`{"action":"%s","status":"ok"}`, action)), nil

	default:
		return mcp.NewToolResultText(fmt.Sprintf(`{"action":"%s","status":"acknowledged"}`, action)), nil
	}
}

// --- Consensus Handler ---

func (s *Server) handleConsensus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topic, err := request.RequireString("topic")
	if err != nil {
		return mcp.NewToolResultError("topic is required"), nil
	}

	synthesize := request.GetBool("synthesize", false)

	// Resolve participants from role preferences
	enabled := s.registry.EnabledCLIs()
	if len(enabled) < 2 {
		return mcp.NewToolResultError("consensus requires at least 2 CLIs"), nil
	}

	async := request.GetBool("async", false)

	params := types.StrategyParams{
		Prompt:   topic,
		CLIs:     enabled[:2], // First 2 enabled CLIs
		MaxTurns: int(request.GetFloat("max_turns", 0)),
		Extra:    map[string]any{"synthesize": synthesize},
	}

	if async {
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sess := s.sessions.Create("consensus", types.SessionModeOnceStateful, "")
		job := s.jobs.Create(sess.ID, "consensus")
		jobCtx, jobCancel := context.WithCancel(context.Background())
		s.jobs.RegisterCancel(job.ID, jobCancel)
		go s.executeStrategy(jobCtx, job.ID, sess.ID, "consensus", params)
		data, _ := json.Marshal(map[string]any{"job_id": job.ID, "session_id": sess.ID, "status": "running"})
		return mcp.NewToolResultText(string(data)), nil
	}

	result, err := s.orchestrator.Execute(ctx, "consensus", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("consensus failed: %v", err)), nil
	}
	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

// --- DeepResearch Handler ---

func (s *Server) handleDeepresearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topic, err := request.RequireString("topic")
	if err != nil {
		return mcp.NewToolResultError("topic is required"), nil
	}

	outputFormat := request.GetString("output_format", "")
	model := request.GetString("model", "")
	force := request.GetBool("force", false)

	// Try to create GenAI client
	client, clientErr := deepresearch.NewClient(model, 0)
	if clientErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("DeepResearch unavailable: %v. Set GOOGLE_API_KEY or GEMINI_API_KEY.", clientErr)), nil
	}

	content, cacheHit, researchErr := client.Research(ctx, topic, outputFormat, nil, force)
	if researchErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("DeepResearch failed: %v", researchErr)), nil
	}
	defer client.Close()

	result := map[string]any{
		"topic":   topic,
		"content": content,
		"cached":  cacheHit,
	}
	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

// --- Debate Handler ---

func (s *Server) handleDebate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topic, err := request.RequireString("topic")
	if err != nil {
		return mcp.NewToolResultError("topic is required"), nil
	}

	synthesize := request.GetBool("synthesize", true)

	enabled := s.registry.EnabledCLIs()
	if len(enabled) < 2 {
		return mcp.NewToolResultError("debate requires at least 2 CLIs"), nil
	}

	async := request.GetBool("async", false)

	params := types.StrategyParams{
		Prompt:   topic,
		CLIs:     enabled[:2],
		MaxTurns: int(request.GetFloat("max_turns", 6)),
		Extra:    map[string]any{"synthesize": synthesize},
	}

	if async {
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sess := s.sessions.Create("debate", types.SessionModeOnceStateful, "")
		job := s.jobs.Create(sess.ID, "debate")
		jobCtx, jobCancel := context.WithCancel(context.Background())
		s.jobs.RegisterCancel(job.ID, jobCancel)
		go s.executeStrategy(jobCtx, job.ID, sess.ID, "debate", params)
		data, _ := json.Marshal(map[string]any{"job_id": job.ID, "session_id": sess.ID, "status": "running"})
		return mcp.NewToolResultText(string(data)), nil
	}

	result, err := s.orchestrator.Execute(ctx, "debate", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("debate failed: %v", err)), nil
	}
	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

// --- Resource Handlers ---

func (s *Server) handleHealthResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	running := s.jobs.ListRunning()
	health := map[string]any{
		"version":        serverVersion,
		"total_sessions": s.sessions.Count(),
		"running_jobs":   len(running),
	}
	data, _ := json.Marshal(health)

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      request.Params.URI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

// --- Prompt Handlers ---

func (s *Server) handleBackgroundPrompt(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	taskDesc := ""
	if args := request.Params.Arguments; args != nil {
		if desc, exists := args["task_description"]; exists && desc != "" {
			taskDesc = desc
		}
	}

	instructions := "Use aimux exec with async=true for background execution. " +
		"Check status with the status tool using the returned job_id. " +
		"Prefer background tasks over synchronous calls for long-running operations."

	if taskDesc != "" {
		instructions = fmt.Sprintf("Task: %s\n\n%s", taskDesc, instructions)
	}

	return mcp.NewGetPromptResult(
		"Background execution protocol",
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(
				mcp.RoleAssistant,
				mcp.NewTextContent(instructions),
			),
		},
	), nil
}

// --- Helpers ---

// commandBinary extracts the binary name from command.base (first word).
// Supports multi-word command bases like "testcli codex --json" where only
// the first word is the binary and the rest are base args (handled by buildArgs).
func commandBinary(base string) string {
	if i := strings.IndexByte(base, ' '); i > 0 {
		return base[:i]
	}
	return base
}

// commandBaseArgs extracts extra args from command.base (all words after the first).
// For "testcli codex --json" returns ["codex", "--json"].
// For "echo" returns nil.
func commandBaseArgs(base string) []string {
	parts := strings.Fields(base)
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}

// buildArgs constructs CLI arguments from profile and parameters.
func buildArgs(profile *config.CLIProfile, model, effort string, readOnly bool, prompt string) []string {
	// Prepend any extra args from command.base (e.g., "testcli codex" → ["codex"])
	baseArgs := commandBaseArgs(profile.Command.Base)
	args := append([]string{}, baseArgs...)

	if profile.Features.Headless && profile.Name == "codex" {
		args = append(args, "--full-auto")
	}

	if readOnly && len(profile.ReadOnlyFlags) > 0 {
		args = append(args, profile.ReadOnlyFlags...)
	}

	if model != "" && profile.ModelFlag != "" {
		args = append(args, profile.ModelFlag, model)
	}

	if effort != "" && profile.Reasoning != nil {
		if profile.Reasoning.FlagValueTemplate != "" {
			val := strings.ReplaceAll(profile.Reasoning.FlagValueTemplate, "%s", effort)
			args = append(args, profile.Reasoning.Flag, val)
		} else {
			args = append(args, profile.Reasoning.Flag, effort)
		}
	}

	if prompt != "" {
		if profile.PromptFlag != "" {
			// Flag-based prompt: -p "prompt" (gemini, claude, qwen)
			args = append(args, profile.PromptFlag, prompt)
		} else {
			// Positional prompt: prompt as last argument (codex, echo)
			args = append(args, prompt)
		}
	}

	return args
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

// executeStrategy runs an orchestrator strategy in background and updates job/session state.
func (s *Server) executeStrategy(ctx context.Context, jobID, sessionID, strategyName string, params types.StrategyParams) {
	s.jobs.StartJob(jobID, 0)
	s.sessions.Update(sessionID, func(sess *session.Session) {
		sess.Status = types.SessionStatusRunning
	})

	result, err := s.orchestrator.Execute(ctx, strategyName, params)
	if err != nil {
		s.jobs.FailJob(jobID, types.NewExecutorError(err.Error(), err, ""))
		s.sessions.Update(sessionID, func(sess *session.Session) {
			sess.Status = types.SessionStatusFailed
		})
		return
	}

	data, _ := json.Marshal(result)
	s.jobs.CompleteJob(jobID, string(data), 0)
	s.sessions.Update(sessionID, func(sess *session.Session) {
		sess.Status = types.SessionStatusCompleted
		sess.Turns += result.Turns
	})
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
