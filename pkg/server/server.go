// Package server implements the MCP server using mcp-go SDK.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/thebtf/aimux/pkg/agents"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/executor"
	conptyExec "github.com/thebtf/aimux/pkg/executor/conpty"
	pipeExec "github.com/thebtf/aimux/pkg/executor/pipe"
	ptyExec "github.com/thebtf/aimux/pkg/executor/pty"
	"github.com/thebtf/aimux/pkg/hooks"
	inv "github.com/thebtf/aimux/pkg/investigate"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/metrics"
	orch "github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/parser"
	"github.com/thebtf/aimux/pkg/prompt"
	"github.com/thebtf/aimux/pkg/ratelimit"
	"github.com/thebtf/aimux/pkg/resolve"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/skills"
	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
	"github.com/thebtf/aimux/pkg/tools/deepresearch"
	"github.com/thebtf/aimux/pkg/types"
)

const serverVersion = "3.0.0-dev"

// aimuxInstructions is delivered to every MCP client on connect via server.WithInstructions().
// This replaces the need for an external SKILL.md file — the server documents itself.
const aimuxInstructions = `aimux — AI CLI Multiplexer (13 MCP tools, 12 CLIs, 23 think patterns)

One MCP server that routes prompts to 12 AI coding CLIs with role-based routing,
multi-model orchestration, structured reasoning, and deep investigation.

## Skill-Based Workflows (MCP Prompts)

aimux provides deep workflow skills as MCP prompts. Each skill is a multi-phase
orchestration guide with hard gates, exact tool parameters, and cross-skill routing.

| Skill Prompt | Purpose |
|---|---|
| debug | 5-phase debug: reproduce → investigate → root-cause → fix → verify |
| review | Code review with CLI-adaptive consensus/peer_review fallback |
| audit | Codebase audit with P0-P3 triage routing to debug/security/review |
| security | 10-category security checklist with investigate integration |
| research | 4-phase pipeline: literature → comparison → adversarial → synthesis |
| consensus | Multi-model consensus with "consensus ≠ correctness" warning |
| investigate | Investigation protocol with domain auto-detect and convergence |
| delegate | Delegation decision tree: task size → routing (direct/exec/agent) |
| tdd | TDD workflow: RED gate → GREEN gate → IMPROVE → coverage |
| workflow | Declarative multi-step pipeline builder |
| agent-exec | Agent-first execution: match task → agent, exec as fallback |
| guide | Complete reference: tools, roles, patterns |
| background | Background async execution with role routing |

Use these prompts for structured guidance. Each injects live data (your CLIs, metrics,
past reports) and adapts to your environment.

## Tool Selection — "I need to..."

| I need to... | Tool | Key params |
|---|---|---|
| Run a prompt on an AI CLI | exec | prompt, role, cli, async |
| Get consensus from multiple models | consensus | topic, synthesize |
| Have models debate a decision | debate | topic, max_turns |
| Multi-turn discussion between CLIs | dialog | prompt, max_turns |
| Structured reasoning/analysis | think | pattern (23 options) |
| Deep investigation with tracking | investigate | action, topic, domain |
| Run a codebase audit | audit | cwd, mode (quick/standard/deep) |
| Execute a project agent | agent | agent (name), prompt |
| Chain multiple steps declaratively | workflow | steps (JSON), input |
| Check async job status | status | job_id |
| Manage sessions | sessions | action (list/health/gc/cancel) |
| Discover available agents | agents | action (list/find) |
| Deep research via Gemini | deepresearch | topic |

## Roles (exec tool) — don't pick CLI manually, use role=
coding → codex | codereview → gemini | debug → codex | secaudit → codex
analyze → gemini | refactor → codex | testgen → codex | planner → codex
If a CLI fails (rate limit, timeout), aimux auto-retries with the next capable CLI.

## Anti-Patterns
- Don't specify cli= when role= is enough — let routing pick the best CLI
- Don't use sync exec for tasks >30s — use async=true
- Don't skip investigate for complex bugs — jumping to fix wastes time
- Don't run consensus with 1 CLI — needs 2+ for comparison
- Don't call exec for tasks an agent can handle — use agent-exec first`

// marshalToolResult marshals data to JSON and returns an MCP tool result.
// Returns an error result if marshaling fails instead of silently returning empty.
func marshalToolResult(data any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("internal error: response serialization failed: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

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
	hooks        *hooks.Registry
	metrics      *metrics.Collector
	store        *session.Store
	gcCancel     context.CancelFunc
	skillEngine  *skills.Engine
	rateLimiter  *ratelimit.Limiter
	authToken    string
	projectDir   string // directory used for initial agent discovery
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
	s.metrics = metrics.New()

	// Initialize rate limiter — per-tool token bucket.
	s.rateLimiter = ratelimit.New(cfg.Server.RateLimitRPS, cfg.Server.RateLimitBurst)

	// Initialize auth token — config takes precedence over env var.
	s.authToken = cfg.Server.AuthToken
	if s.authToken == "" {
		s.authToken = os.Getenv("AIMUX_AUTH_TOKEN")
	}

	// Initialize SQLite persistence and WAL recovery
	dbPath := config.ExpandPath(cfg.Server.DBPath)
	if dbPath != "" {
		store, err := session.NewStore(dbPath)
		if err != nil {
			log.Warn("SQLite persistence unavailable: %v (continuing in-memory only)", err)
		} else {
			s.store = store
			log.Info("SQLite persistence enabled: %s", dbPath)

			// Recover state from WAL if exists
			walPath := dbPath + ".wal.log"
			if err := session.RecoverFromWAL(walPath, s.sessions, s.jobs); err != nil {
				log.Warn("WAL recovery: %v", err)
			}

			// Restore jobs from SQLite (survive process restarts)
			if n, err := s.store.RestoreJobs(s.jobs); err != nil {
				log.Warn("job restore failed: %v", err)
			} else if n > 0 {
				log.Info("restored %d jobs from SQLite", n)
			}

			// Enable immediate persistence — jobs written to SQLite on create/complete/fail.
			// Survives process restart between 30s snapshot intervals.
			s.jobs.SetStore(s.store)

			// snapshot loop started below after gcCtx is created
		}
	}

	// Start GC reaper for expired sessions
	gcCtx, gcCancel := context.WithCancel(context.Background())
	s.gcCancel = gcCancel

	// Start periodic snapshot (uses gcCtx for graceful shutdown)
	if s.store != nil {
		go s.runSnapshotLoop(gcCtx, s.store)
	}
	ttl := cfg.Server.SessionTTLHours
	if ttl <= 0 {
		ttl = 24
	}
	gcInterval := cfg.Server.GCIntervalSeconds
	if gcInterval <= 0 {
		gcInterval = 300
	}
	gc := session.NewGCReaper(s.sessions, s.jobs, log, ttl)
	go gc.Run(gcCtx, time.Duration(gcInterval)*time.Second)

	// Think session GC: clean up stale think pattern sessions alongside main GC
	go func() {
		ticker := time.NewTicker(time.Duration(gcInterval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-gcCtx.Done():
				return
			case <-ticker.C:
				thinkTTL := time.Duration(ttl) * time.Hour
				if removed := think.GCSessions(thinkTTL); removed > 0 {
					log.Info("think session GC: removed %d expired sessions", removed)
				}
			}
		}
	}()

	// Initialize CLI resolver for profile-aware command resolution
	cliResolver := resolve.NewProfileResolver(cfg.CLIProfiles)

	// Initialize orchestrator with all strategies
	s.orchestrator = orch.New(log,
		orch.NewPairCoding(s.executor, s.executor, cliResolver),
		orch.NewSequentialDialog(s.executor, cliResolver),
		orch.NewParallelConsensus(s.executor, cliResolver),
		orch.NewStructuredDebate(s.executor, cliResolver),
		orch.NewAuditPipeline(s.executor, cliResolver),
		orch.NewWorkflowStrategy(s.executor, cliResolver),
	)

	// Initialize prompt engine with built-in and project prompts.d/
	builtInPrompts := filepath.Join(cfg.ConfigDir, "prompts.d")
	s.promptEng = prompt.NewEngine(builtInPrompts)
	if err := s.promptEng.Load(); err != nil {
		log.Warn("prompt engine load: %v", err)
	}

	// Initialize think patterns
	patterns.RegisterAll()

	// Initialize hooks registry with built-in telemetry
	s.hooks = hooks.NewRegistry()
	s.hooks.RegisterAfter("builtin:telemetry", hooks.NewTelemetryHook())

	// Initialize agent registry
	s.agentReg = agents.NewRegistry()
	// Discover agents from project and user directories
	if cwd, err := os.Getwd(); err == nil {
		s.projectDir = cwd
		home, _ := os.UserHomeDir()
		s.agentReg.Discover(cwd, home)
	}
	// Register built-in generic agents (shadowed by project/user agents with same name)
	agents.RegisterBuiltins(s.agentReg)

	// Initialize skill engine — embedded skills from config/skills.d, with optional disk overlay.
	s.skillEngine = skills.NewEngine()
	diskSkillsDir := filepath.Join(cfg.ConfigDir, "skills.d")
	if err := s.skillEngine.Load(skillsEmbedFS, diskSkillsDir); err != nil {
		log.Warn("skill engine load: %v", err)
		s.skillEngine = nil
	} else {
		log.Info("skill engine loaded: %d skills", len(s.skillEngine.Skills()))
		// Validate skill graph map if present.
		if warnings := s.skillEngine.ValidateMap(); len(warnings) > 0 {
			for _, w := range warnings {
				log.Warn("skill map: %s", w)
			}
		}
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
		server.WithInstructions(aimuxInstructions),
	)

	// Enable sampling capability — allows think patterns to request LLM calls from the client.
	s.mcp.EnableSampling()

	s.registerTools()
	s.registerResources()
	s.registerPrompts()
	s.registerSkillPrompts()

	return s
}

// ServeStdio starts the MCP server on stdio transport.
func (s *Server) ServeStdio() error {
	s.log.Info("MCP server starting on stdio (aimux v%s)", serverVersion)
	return server.ServeStdio(s.mcp)
}

// ServeSSE starts the MCP server with Server-Sent Events transport.
// If authToken is configured, all requests must carry a valid Bearer token.
func (s *Server) ServeSSE(addr string) error {
	addr = ensureLocalhostBinding(addr)
	s.log.Info("MCP server starting on SSE at %s (aimux v%s)", addr, serverVersion)
	if !isLocalhostAddr(addr) {
		s.log.Warn("SSE transport bound to non-localhost address %s", addr)
	}
	sseServer := server.NewSSEServer(s.mcp)
	if s.authToken == "" {
		return sseServer.Start(addr)
	}
	s.log.Info("SSE transport: bearer token authentication enabled")
	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      bearerAuthMiddleware(s.authToken, sseServer),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return server.NewSSEServer(s.mcp, server.WithHTTPServer(httpSrv)).Start(addr)
}

// ServeHTTP starts the MCP server with StreamableHTTP transport.
// If authToken is configured, all requests must carry a valid Bearer token.
func (s *Server) ServeHTTP(addr string, opts ...server.StreamableHTTPOption) error {
	addr = ensureLocalhostBinding(addr)
	s.log.Info("MCP server starting on HTTP at %s (aimux v%s)", addr, serverVersion)
	if !isLocalhostAddr(addr) {
		s.log.Warn("HTTP transport bound to non-localhost address %s", addr)
	}
	httpMCPServer := server.NewStreamableHTTPServer(s.mcp, opts...)
	if s.authToken == "" {
		return httpMCPServer.Start(addr)
	}
	s.log.Info("HTTP transport: bearer token authentication enabled")
	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      bearerAuthMiddleware(s.authToken, httpMCPServer),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	authOpts := append(opts, server.WithStreamableHTTPServer(httpSrv))
	return server.NewStreamableHTTPServer(s.mcp, authOpts...).Start(addr)
}

// ensureLocalhostBinding rewrites bare port specs and 0.0.0.0 to 127.0.0.1 to
// prevent accidental exposure on all interfaces.
//   - ":8080"         → "127.0.0.1:8080"
//   - "0.0.0.0:8080"  → "127.0.0.1:8080"
func ensureLocalhostBinding(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "127.0.0.1" + addr
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "127.0.0.1" + addr[len("0.0.0.0"):]
	}
	return addr
}

// isLocalhostAddr checks if the address is bound to localhost/127.0.0.1.
func isLocalhostAddr(addr string) bool {
	return strings.HasPrefix(addr, "127.0.0.1") || strings.HasPrefix(addr, "localhost") || strings.HasPrefix(addr, "[::1]")
}

// bearerAuthMiddleware returns an http.Handler that enforces Bearer token authentication.
// Requests missing or presenting an incorrect token receive 401 Unauthorized.
// When token is empty the original handler is returned unchanged (backward-compatible).
func bearerAuthMiddleware(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	expected := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != expected {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// runSnapshotLoop periodically saves in-memory state to SQLite.
// Stops gracefully when ctx is cancelled.
func (s *Server) runSnapshotLoop(ctx context.Context, store *session.Store) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := store.SnapshotAll(s.sessions, s.jobs); err != nil {
				s.log.Warn("snapshot failed: %v", err)
			}
		}
	}
}

// Shutdown stops background services (GC reaper, snapshot) and closes persistence.
// Graceful: waits up to 5s for running CLI processes to finish before killing.
func (s *Server) Shutdown() {
	// Graceful drain: give running CLI processes time to finish.
	// mcp-mux gives us ~8s grace after stdin close — use 5s for drain, rest for cleanup.
	graceful := executor.SharedPM.GracefulShutdown(5 * time.Second)
	if graceful > 0 && s.log != nil {
		s.log.Info("graceful shutdown: %d processes finished naturally", graceful)
	}

	// Kill remaining session processes (persistent sessions don't need grace — they're idle).
	if pm := pipeExec.SessionProcessManager(); pm != nil {
		pm.Shutdown()
	}

	// Mark any still-running jobs as interrupted with partial output.
	if s.jobs != nil {
		for _, job := range s.jobs.ListRunning() {
			s.jobs.FailJob(job.ID, types.NewExecutorError("interrupted: upstream shutdown", nil, job.Progress))
		}
	}

	if s.gcCancel != nil {
		s.gcCancel()
	}
	if s.store != nil {
		// Final snapshot before close
		if err := s.store.SnapshotAll(s.sessions, s.jobs); err != nil {
			s.log.Warn("final snapshot failed: %v", err)
		}
		s.store.Close()
	}
}

// --- Tool Registration ---

func (s *Server) registerTools() {
	// exec tool
	s.mcp.AddTool(
		mcp.NewTool("exec",
			mcp.WithDescription("Execute a raw prompt on a specific CLI. "+
				"Use agent tool instead for task-based work — it auto-selects the best agent. "+
				"Use exec only when you need a specific CLI or low-level control. "+
				"Use role= for CLI routing (coding→codex, codereview→gemini, debug→codex, secaudit→codex, analyze→gemini, refactor→codex, testgen→codex, docgen→codex, planner→codex, thinkdeep→codex). "+
				"Use async=true for long tasks (>30s) — returns job_id immediately, poll with status tool."),
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
			mcp.WithDescription("23 reasoning patterns. "+
				"Top patterns: decision_framework (structured tradeoff analysis), problem_decomposition (break down complexity), "+
				"debugging_approach (systematic trace + fix), peer_review (simulate review with objections), "+
				"research_synthesis (group findings, assess evidence). "+
				"Stateless: think, critical_thinking, decision_framework, problem_decomposition, mental_model, metacognitive_monitoring, recursive_thinking, "+
				"domain_modeling, architecture_analysis, stochastic_algorithm, temporal_thinking, visual_reasoning, "+
				"source_comparison, literature_review, peer_review, replication_analysis, experimental_loop, research_synthesis. "+
				"Stateful (pass session_id): sequential_thinking, scientific_method, debugging_approach, "+
				"structured_argumentation, collaborative_reasoning."),
			mcp.WithString("pattern",
				mcp.Required(),
				mcp.Description("Pattern name"),
				mcp.Enum("think", "critical_thinking", "sequential_thinking", "scientific_method",
					"decision_framework", "problem_decomposition", "debugging_approach", "mental_model",
					"metacognitive_monitoring", "structured_argumentation", "collaborative_reasoning",
					"recursive_thinking", "domain_modeling", "architecture_analysis", "stochastic_algorithm",
					"temporal_thinking", "visual_reasoning",
					"source_comparison", "literature_review", "peer_review",
					"replication_analysis", "experimental_loop", "research_synthesis"),
			),
			mcp.WithString("issue", mcp.Description("Issue to analyze. Describe: the problem, symptoms observed, what you've already tried, and your current understanding. Be specific — vague descriptions produce vague analysis. (Used by: critical_thinking, debugging_approach)")),
			mcp.WithString("topic", mcp.Description("Central question or area to explore. Include context and what you already know. (Used by: structured_argumentation, collaborative_reasoning)")),
			mcp.WithString("thought", mcp.Description("Your current thinking. Be specific — the tool will analyze structure and suggest next steps. (Used by: think, sequential_thinking)")),
			mcp.WithString("session_id", mcp.Description("Session ID for stateful patterns")),
			mcp.WithString("decision", mcp.Description("Frame as a choice: 'Choose between X and Y given constraints Z'. (Used by: decision_framework)")),
			mcp.WithString("problem", mcp.Description("State the problem clearly. Include: scope, constraints, what success looks like. (Used by: problem_decomposition, mental_model, recursive_thinking)")),
			mcp.WithString("task", mcp.Description("Task for metacognitive monitoring")),
			mcp.WithString("modelName", mcp.Description("Mental model name (mental_model)")),
			mcp.WithString("approachName", mcp.Description("Debugging method name (debugging_approach)")),
			mcp.WithString("domainName", mcp.Description("Domain name (domain_modeling)")),
			mcp.WithString("timeFrame", mcp.Description("Time frame (temporal_thinking)")),
			mcp.WithString("operation", mcp.Description("Visual operation (visual_reasoning)")),
			mcp.WithString("algorithmType", mcp.Description("Algorithm type: mdp, mcts, bandit, bayesian, hmm")),
			mcp.WithString("problemDefinition", mcp.Description("Problem definition (stochastic_algorithm)")),
			mcp.WithString("stage", mcp.Description("Stage (scientific_method, collaborative_reasoning)")),
			mcp.WithString("artifact", mcp.Description("The code, design, or document to review. Include relevant context. (Used by: peer_review)")),
			mcp.WithString("claim", mcp.Description("Claim to analyze (replication_analysis)")),
			mcp.WithString("hypothesis", mcp.Description("Concrete root cause theory based on evidence. Rate confidence 0-1. (Used by: experimental_loop)")),
			mcp.WithString("criteria",
				mcp.Description("JSON array of criteria objects [{\"name\":\"x\",\"weight\":0.3}] for decision_framework pattern"),
			),
			mcp.WithString("options",
				mcp.Description("JSON array of option objects [{\"name\":\"x\",\"scores\":{...}}] for decision_framework pattern"),
			),
			mcp.WithString("components",
				mcp.Description("JSON array of component objects [{\"name\":\"x\",\"dependencies\":[...]}] for architecture_analysis pattern"),
			),
			mcp.WithString("sources",
				mcp.Description("JSON array of source strings for source_comparison pattern"),
			),
			mcp.WithString("findings",
				mcp.Description("JSON array of finding strings for research_synthesis pattern"),
			),
			// Flat params for stateful patterns (step progression, pal-mcp-server style)
			mcp.WithString("hypothesis_text",
				mcp.Description("Your root cause theory. Be specific: what exactly is broken and why. Base on evidence, not assumptions. (Used by: debugging_approach)"),
			),
			mcp.WithString("confidence",
				mcp.Description("Your confidence: exploring (just started), low (early idea), medium (some evidence), high (strong evidence), very_high (very strong), certain (confirmed). (Used by: debugging_approach, experimental_loop)"),
			),
			mcp.WithString("findings_text",
				mcp.Description("What you discovered this step. Include: file paths, line numbers, error messages, observations. (Used by: debugging_approach)"),
			),
			mcp.WithString("hypothesis_action",
				mcp.Description("Action on hypothesis: propose (new theory), confirm (verified), refute (disproven). (Used by: debugging_approach)"),
			),
			mcp.WithString("entry_type",
				mcp.Description("Scientific method entry type: observation, hypothesis, prediction, experiment, analysis, conclusion. (Used by: scientific_method)"),
			),
			mcp.WithString("entry_text",
				mcp.Description("Content of the scientific method entry. (Used by: scientific_method)"),
			),
			mcp.WithString("link_to",
				mcp.Description("ID of entry to link to. Optional — auto-links by type sequence if omitted. (Used by: scientific_method)"),
			),
			mcp.WithString("contribution_type",
				mcp.Description("Contribution type: observation, question, insight, concern, suggestion, challenge, synthesis. (Used by: collaborative_reasoning)"),
			),
			mcp.WithString("contribution_text",
				mcp.Description("Content of the contribution. (Used by: collaborative_reasoning)"),
			),
			mcp.WithString("persona_id",
				mcp.Description("ID of the persona making this contribution. (Used by: collaborative_reasoning)"),
			),
			mcp.WithString("argument_type",
				mcp.Description("Argument type: claim, evidence, rebuttal. (Used by: structured_argumentation)"),
			),
			mcp.WithString("argument_text",
				mcp.Description("Content of the argument. (Used by: structured_argumentation)"),
			),
			mcp.WithString("supports_claim_id",
				mcp.Description("ID of claim this evidence or rebuttal targets. (Used by: structured_argumentation)"),
			),
			mcp.WithNumber("step_number",
				mcp.Description("Current investigation step (1, 2, 3...). Tracks progression. (Used by: debugging_approach, scientific_method)"),
			),
			mcp.WithNumber("contribution_confidence",
				mcp.Description("Confidence in contribution (0-1). (Used by: collaborative_reasoning)"),
			),
			mcp.WithBoolean("next_step_needed",
				mcp.Description("True if you plan to continue with another step. (Used by: debugging_approach, scientific_method)"),
			),
		),
		s.handleThink,
	)

	// investigate tool
	s.mcp.AddTool(
		mcp.NewTool("investigate",
			mcp.WithDescription("Structured deep investigation — catches wrong assumptions before they become wrong decisions. "+
				"Auto-detects domain (security/performance/architecture/debugging/research) from topic keywords if not specified. "+
				"Flow: start(domain?) → (finding + assess) × N → report. "+
				"Stops only when BOTH: convergence ≥ 1.0 AND coverage ≥ 80%."),
			mcp.WithString("action",
				mcp.Required(),
				mcp.Description("Action: start, finding, assess, report, status, list, recall"),
				mcp.Enum("start", "finding", "assess", "report", "status", "list", "recall"),
			),
			mcp.WithString("topic",
				mcp.Description("Investigation topic (required for start, recall)"),
			),
			mcp.WithString("session_id",
				mcp.Description("Investigation session ID (required for finding, assess, report, status)"),
			),
			mcp.WithString("domain",
				mcp.Description("Domain: generic, debugging. Loads domain-specific coverage areas and methods."),
			),
			mcp.WithString("description",
				mcp.Description("Finding description (required for action=finding)"),
			),
			mcp.WithString("source",
				mcp.Description("Finding source — tool + location (required for action=finding)"),
			),
			mcp.WithString("severity",
				mcp.Description("Finding severity (required for action=finding)"),
				mcp.Enum("P0", "P1", "P2", "P3"),
			),
			mcp.WithString("confidence",
				mcp.Description("Finding confidence level (optional, default VERIFIED)"),
				mcp.Enum("VERIFIED", "INFERRED", "STALE", "BLOCKED", "UNKNOWN"),
			),
			mcp.WithString("coverage_area",
				mcp.Description("Which coverage area this finding covers (optional for action=finding)"),
			),
			mcp.WithString("corrects",
				mcp.Description("Finding ID this corrects — creates a Correction chain (optional for action=finding)"),
			),
			mcp.WithString("cwd",
				mcp.Description("Working directory for report file save (optional for action=report)"),
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
			mcp.WithDescription("PRIMARY tool for task execution. "+
				"Routes tasks to the best agent automatically. "+
				"Just provide a prompt — agent selection, CLI routing, and model configuration happen automatically. "+
				"Actions: run (execute task), list (show agents), find (search agents)."),
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

	// agent tool — runs a discovered agent through any CLI
	s.mcp.AddTool(
		mcp.NewTool("agent",
			mcp.WithDescription("Run a project agent through any CLI. Loads agent definition, "+
				"injects system prompt, delegates to CLI in autonomous mode. "+
				"The CLI IS the agent — it reads files, runs commands, edits code."),
			mcp.WithString("agent",
				mcp.Required(),
				mcp.Description("Agent name from registry"),
			),
			mcp.WithString("prompt",
				mcp.Required(),
				mcp.Description("Task for the agent"),
			),
			mcp.WithString("cli",
				mcp.Description("CLI override (default: from agent definition or role routing)"),
			),
			mcp.WithString("cwd",
				mcp.Description("Working directory"),
			),
			mcp.WithBoolean("async",
				mcp.Description("Run in background"),
			),
			mcp.WithNumber("timeout_seconds",
				mcp.Description("Timeout override in seconds"),
			),
		),
		s.handleAgentRun,
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

	// workflow tool
	s.mcp.AddTool(
		mcp.NewTool("workflow",
			mcp.WithDescription("Execute a declarative multi-step pipeline. Each step can call exec, think, or investigate. Steps can reference previous step outputs via {{step_id.content}} templates."),
			mcp.WithString("name",
				mcp.Description("Workflow name (for logging)"),
			),
			mcp.WithString("steps",
				mcp.Required(),
				mcp.Description("JSON array of step definitions: [{id, tool, params, condition?, on_error?}]"),
			),
			mcp.WithString("input",
				mcp.Description("Initial input text (available as {{input}} in templates)"),
			),
			mcp.WithBoolean("async",
				mcp.Description("Run in background"),
			),
		),
		s.handleWorkflow,
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

	s.mcp.AddResource(
		mcp.NewResource(
			"aimux://metrics",
			"Request Metrics",
			mcp.WithResourceDescription("Per-CLI request counts, error rates, and latency metrics"),
			mcp.WithMIMEType("application/json"),
		),
		s.handleMetricsResource,
	)
}

// --- Tool Handlers ---

func (s *Server) handleExec(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("exec") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
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

		sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
		job := s.jobs.Create(sess.ID, cli)
		s.log.Info("exec: PairCoding driver=%s reviewer=%s job=%s async=%v", cli, reviewerCLI, job.ID, async)

		if async {
			if err := s.checkConcurrencyLimit(); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
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
			result["content"] = stratResult.Content
			result["turns"] = stratResult.Turns
			result["participants"] = stratResult.Participants
			if stratResult.ReviewReport != nil {
				result["review_report"] = stratResult.ReviewReport
			}
		} else {
			result["content"] = j.Content
		}
		return marshalToolResult(result)
	}

	// Bootstrap prompt injection: prepend role-specific prompt from prompts.d/
	prompt = s.injectBootstrap(role, prompt)

	// Non-coding roles: direct execution
	sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
	job := s.jobs.Create(sess.ID, cli)

	args := types.SpawnArgs{
		CLI:            cli,
		Command:        resolve.CommandBinary(profile.Command.Base),
		Args:           resolve.BuildPromptArgs(profile, model, effort, readOnly, prompt),
		CWD:            cwd,
		TimeoutSeconds: timeoutSec,
	}

	// Stdin piping for long prompts (Windows 8191 char limit)
	if profile.StdinThreshold > 0 && len(prompt) > profile.StdinThreshold {
		args.Stdin = prompt
		args.Args = resolve.BuildPromptArgs(profile, model, effort, readOnly, "") // empty prompt — piped via stdin
		s.log.Info("exec: stdin piping activated (prompt=%d chars, threshold=%d)", len(prompt), profile.StdinThreshold)
	}

	s.log.Info("exec: cli=%s role=%s job=%s async=%v", cli, role, job.ID, async)

	if async {
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
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
		"content":    j.Content,
	}
	return marshalToolResult(result)
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

		result, err := s.executor.Run(ctx, currentArgs)

		if err != nil {
			currentCB.RecordFailure(false)
			s.metrics.RecordRequest(cand.CLI, 0, true)
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
			s.metrics.RecordRequest(cand.CLI, 0, true)
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
			s.metrics.RecordRequest(cand.CLI, 0, true)
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

		s.metrics.RecordRequest(cand.CLI, result.DurationMS, false)
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

func (s *Server) handleStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	jobID, err := request.RequireString("job_id")
	if err != nil {
		return mcp.NewToolResultError("job_id is required"), nil
	}

	j := s.jobs.GetSnapshot(jobID)
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

	return marshalToolResult(result)
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
		return marshalToolResult(map[string]any{
			"sessions": sessions,
			"count":    len(sessions),
		})

	case "info":
		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for info"), nil
		}
		sess := s.sessions.Get(sessionID)
		if sess == nil {
			return mcp.NewToolResultError("session not found"), nil
		}
		jobs := s.jobs.ListBySessionSnapshot(sessionID)
		return marshalToolResult(map[string]any{
			"session": sess,
			"jobs":    jobs,
		})

	case "health":
		running := s.jobs.ListRunning()
		return marshalToolResult(map[string]any{
			"total_sessions": s.sessions.Count(),
			"running_jobs":   len(running),
		})

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
		// Atomically fail only still-active jobs for this session.
		for _, j := range s.jobs.ListBySessionSnapshot(sessionID) {
			s.jobs.FailJobIfActive(j.ID, types.NewExecutorError("session killed", nil, ""))
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
		return marshalToolResult(map[string]any{"collected": collected})

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action %q", action)), nil
	}
}

// --- Dialog Handler ---

func (s *Server) handleDialog(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("dialog") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
	prompt, err := request.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError("prompt is required"), nil
	}

	enabled := s.registry.EnabledCLIs()
	if len(enabled) < 2 {
		return mcp.NewToolResultError("dialog requires at least 2 CLIs"), nil
	}

	sessionID := request.GetString("session_id", "")
	cwd := request.GetString("cwd", "")

	params := types.StrategyParams{
		Prompt:   prompt,
		CLIs:     enabled[:2],
		MaxTurns: int(request.GetFloat("max_turns", 6)),
		CWD:      cwd,
	}

	// Session resume: load prior turn history from existing session job.
	if sessionID != "" {
		existing := s.sessions.Get(sessionID)
		if existing == nil {
			return mcp.NewToolResultError(fmt.Sprintf("session %q not found", sessionID)), nil
		}
		if cwd == "" {
			params.CWD = existing.CWD
		}
		// Find prior turn history from the most recent completed job for this session.
		priorTurns := s.findDialogTurnHistory(existing.ID)
		if len(priorTurns) > 0 {
			params.Extra = map[string]any{"prior_turns": priorTurns}
		}
		s.log.Info("dialog: resuming session=%s with %d prior bytes of turn history", sessionID, len(priorTurns))
	}

	// Create or reuse session for persistence.
	var sess *session.Session
	if sessionID != "" {
		sess = s.sessions.Get(sessionID)
	}
	if sess == nil {
		sess = s.sessions.Create("dialog", types.SessionModeOnceStateful, params.CWD)
		s.sessions.Update(sess.ID, func(ss *session.Session) {
			ss.Status = types.SessionStatusRunning
		})
	}

	result, err := s.orchestrator.Execute(ctx, "dialog", params)
	if err != nil {
		s.sessions.Update(sess.ID, func(ss *session.Session) {
			ss.Status = types.SessionStatusFailed
		})
		return mcp.NewToolResultError(fmt.Sprintf("dialog failed: %v", err)), nil
	}

	// Persist turn history in a job so it can be recalled on resume.
	// Job content stores the JSON turn history; full dialog text is returned directly.
	job := s.jobs.Create(sess.ID, "dialog")
	s.jobs.StartJob(job.ID, 0)
	turnContent := ""
	if len(result.TurnHistory) > 0 {
		turnContent = string(result.TurnHistory)
	}
	s.jobs.CompleteJob(job.ID, turnContent, 0)

	s.sessions.Update(sess.ID, func(ss *session.Session) {
		ss.Status = types.SessionStatusCompleted
		ss.Turns = result.Turns
	})

	return marshalToolResult(map[string]any{
		"session_id":   sess.ID,
		"status":       result.Status,
		"turns":        result.Turns,
		"content":      result.Content,
		"participants": result.Participants,
	})
}

// findDialogTurnHistory scans jobs for the most recent dialog turn history
// stored as JSON in job.Content for the given session.
func (s *Server) findDialogTurnHistory(sessionID string) []byte {
	jobs := s.jobs.ListBySession(sessionID)
	// Walk in reverse to find the most recent completed dialog job with content.
	for i := len(jobs) - 1; i >= 0; i-- {
		j := jobs[i]
		if j.Status == types.JobStatusCompleted && j.Content != "" {
			return []byte(j.Content)
		}
	}
	return nil
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
		// Return summaries without full content (content can be 500KB+ total)
		summaries := make([]map[string]any, len(agentList))
		for i, a := range agentList {
			summaries[i] = map[string]any{
				"name":        a.Name,
				"description": a.Description,
				"role":        a.Role,
				"domain":      a.Domain,
				"source":      a.Source,
				"tools":       a.Tools,
			}
		}
		return marshalToolResult(map[string]any{"agents": summaries, "count": len(summaries)})

	case "find":
		query := request.GetString("prompt", "")
		if query == "" {
			return mcp.NewToolResultError("prompt required as search query for find"), nil
		}
		matches := s.agentReg.Find(query)
		return marshalToolResult(map[string]any{"query": query, "matches": matches, "count": len(matches)})

	case "info":
		agentName := request.GetString("agent", "")
		if agentName == "" {
			return mcp.NewToolResultError("agent name required for info"), nil
		}
		agent, agentErr := s.agentReg.Get(agentName)
		if agentErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("agent %q not found", agentName)), nil
		}
		return marshalToolResult(agent)

	case "run":
		prompt := request.GetString("prompt", "")
		if prompt == "" {
			return mcp.NewToolResultError("prompt is required for run"), nil
		}
		cwd := request.GetString("cwd", "")

		// Auto-discover agents from the caller's project directory if it differs
		// from the initial discovery path. Additive — does not remove existing agents.
		if cwd != "" && cwd != s.projectDir {
			s.agentReg.Discover(cwd, "")
		}

		agentName := request.GetString("agent", "")
		var agent *agents.Agent
		if agentName == "" {
			// Auto-select: score all registered agents against prompt keywords.
			var score int
			agent, score = agents.AutoSelectAgent(s.agentReg, prompt)
			if agent == nil {
				return mcp.NewToolResultError("no agents registered and no fallback available"), nil
			}
			agentName = agent.Name
			keywords := agents.ExtractKeywords(prompt)
			s.log.Info("agent auto-selected: %s (score: %d, keywords: %v)", agent.Name, score, keywords)
		} else {
			var agentErr error
			agent, agentErr = s.agentReg.Get(agentName)
			if agentErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("agent %q not found", agentName)), nil
			}
		}

		fullPrompt := agent.Content + "\n\n" + prompt
		role := agent.Role
		if role == "" {
			role = "default"
		}

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
			Command:        resolve.CommandBinary(profile.Command.Base),
			Args:           resolve.BuildPromptArgs(profile, pref.Model, pref.ReasoningEffort, readOnly, fullPrompt),
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
			go s.executeJob(jobCtx, job.ID, sess.ID, role, args, cb, profile.OutputFormat)
			return marshalToolResult(map[string]any{
				"agent":      agentName,
				"job_id":     job.ID,
				"session_id": sess.ID,
				"status":     "running",
			})
		}

		s.executeJob(ctx, job.ID, sess.ID, role, args, cb, profile.OutputFormat)

		j := s.jobs.GetSnapshot(job.ID)
		if j == nil {
			return mcp.NewToolResultError("agent job disappeared"), nil
		}
		if j.Status == types.JobStatusFailed && j.Error != nil {
			return mcp.NewToolResultError(fmt.Sprintf("agent %q failed: %v", agentName, j.Error)), nil
		}

		return marshalToolResult(map[string]any{
			"agent":      agentName,
			"session_id": sess.ID,
			"status":     string(j.Status),
			"content":    j.Content,
		})

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action %q", action)), nil
	}
}

// --- Audit Handler ---

func (s *Server) handleAudit(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("audit") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
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
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sess := s.sessions.Create("audit", types.SessionModeOnceStateless, cwd)
		job := s.jobs.Create(sess.ID, "audit")
		s.sendBusy(job.ID, "audit", 0)
		go func() {
			defer s.sendIdle(job.ID)
			s.jobs.StartJob(job.ID, 0)
			result, stratErr := s.orchestrator.Execute(context.Background(), "audit", params)
			if stratErr != nil {
				s.jobs.FailJob(job.ID, types.NewExecutorError(stratErr.Error(), stratErr, ""))
				return
			}
			s.jobs.CompleteJob(job.ID, result.Content, 0)
		}()
		return marshalToolResult(map[string]any{"job_id": job.ID, "status": "running"})
	}

	result, err := s.orchestrator.Execute(ctx, "audit", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("audit failed: %v", err)), nil
	}
	return marshalToolResult(result)
}

// --- Think Handler ---

func (s *Server) handleThink(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	patternName, err := request.RequireString("pattern")
	if err != nil {
		return mcp.NewToolResultError("pattern is required"), nil
	}

	handler := think.GetPattern(patternName)
	if handler == nil {
		return mcp.NewToolResultError(fmt.Sprintf("unknown pattern %q; available: %v", patternName, think.GetAllPatterns())), nil
	}

	// Build input map from all optional MCP params
	input := make(map[string]any)
	optionalStrings := []string{
		"issue", "topic", "thought", "decision", "problem", "task",
		"modelName", "approachName", "domainName", "timeFrame", "operation",
		"observation", "question", "hypothesis", "experiment", "analysis",
		"conclusion", "algorithmType", "problemDefinition", "baseCase",
		"recursiveCase", "convergenceCheck", "diagramType", "description",
		"methodology", "knowledgeAssessment", "result", "stage", "branchId",
		"artifact", "claim",
		"hypothesis_text", "confidence", "findings_text", "hypothesis_action",
		"entry_type", "entry_text", "link_to",
		"contribution_type", "contribution_text", "persona_id",
		"argument_type", "argument_text", "supports_claim_id",
	}
	for _, key := range optionalStrings {
		if v := request.GetString(key, ""); v != "" {
			input[key] = v
		}
	}

	sessionID := request.GetString("session_id", "")

	// Pass through structured, numeric, and boolean params from the raw arguments
	if args, ok := request.Params.Arguments.(map[string]any); ok {
		forwardKeys := []string{
			// Structured
			"criteria", "options", "components", "sources", "findings",
			"subProblems", "dependencies",
			"risks", "stakeholders", "entities", "relationships", "rules",
			"constraints", "states", "events", "transitions", "transformations",
			"elements", "claims", "biases", "uncertainties", "cognitiveProcesses",
			"parameters", "argument", "contribution", "entry", "hypothesisUpdate",
			// Numeric
			"confidence", "thoughtNumber", "totalThoughts", "currentDepth",
			"maxDepth", "iterations", "revisesThought", "branchFromThought",
			"step_number", "contribution_confidence",
			// Boolean
			"isRevision",
			"next_step_needed",
		}
		for _, key := range forwardKeys {
			if v, exists := args[key]; exists {
				input[key] = v
			}
		}
	}

	// Validate input
	validInput, err := handler.Validate(input)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("validation error: %v", err)), nil
	}

	// Execute pattern handler
	thinkResult, err := handler.Handle(validInput, sessionID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("pattern error: %v", err)), nil
	}

	// Compute complexity for mode recommendation
	complexity := think.CalculateComplexity(patternName, input, 60)

	// Build response with mode indicator
	response := map[string]any{
		"pattern":   thinkResult.Pattern,
		"status":    thinkResult.Status,
		"timestamp": thinkResult.Timestamp,
		"data":      thinkResult.Data,
		"mode":      complexity.Recommendation,
		"complexity": map[string]any{
			"total":     complexity.Total,
			"threshold": complexity.Threshold,
			"components": map[string]any{
				"textLength":      complexity.TextLength,
				"subItemCount":    complexity.SubItemCount,
				"structuralDepth": complexity.StructuralDepth,
				"patternBias":     complexity.PatternBias,
			},
		},
	}
	if thinkResult.SessionID != "" {
		response["session_id"] = thinkResult.SessionID
	}
	if thinkResult.SuggestedNextPattern != "" {
		response["suggestedNextPattern"] = thinkResult.SuggestedNextPattern
	}
	if thinkResult.Metadata != nil {
		response["metadata"] = thinkResult.Metadata
	}
	if len(thinkResult.ComputedFields) > 0 {
		response["computed_fields"] = thinkResult.ComputedFields
	}

	data, err := json.Marshal(response)
	if err != nil {
		return mcp.NewToolResultError("internal error: failed to marshal response"), nil
	}
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
		domainName := request.GetString("domain", "")
		if domainName == "" {
			domainName = inv.AutoDetectDomain(topic)
		}
		if inv.GetDomain(domainName) == nil {
			return mcp.NewToolResultError(fmt.Sprintf("unknown domain %q; valid: %v", domainName, inv.DomainNames())), nil
		}

		sess := s.sessions.Create("investigate", types.SessionModeOnceStateful, "")
		state := inv.CreateInvestigation(sess.ID, topic, domainName)

		result := map[string]any{
			"session_id":     sess.ID,
			"topic":          state.Topic,
			"domain":         state.Domain,
			"coverage_areas": state.CoverageAreas,
			"guidance": func() string {
				base := fmt.Sprintf("Begin investigation [%s domain]. ", state.Domain)
				if len(state.CoverageAreas) > 0 {
					base += fmt.Sprintf("Recommended first area: %s. ", state.CoverageAreas[0])
				}
				return base + "Read implementations, not descriptions. Then call finding action."
			}(),
		}
		if domainName == "" {
			result["available_domains"] = inv.DomainNames()
		}
		return marshalToolResult(result)

	case "finding":
		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for finding"), nil
		}
		desc := request.GetString("description", "")
		if desc == "" {
			return mcp.NewToolResultError("description required for finding"), nil
		}
		source := request.GetString("source", "")
		if source == "" {
			return mcp.NewToolResultError("source required for finding"), nil
		}
		severity := request.GetString("severity", "P2")
		confidence := request.GetString("confidence", "")

		input := inv.FindingInput{
			Description:  desc,
			Severity:     inv.Severity(severity),
			Source:       source,
			Confidence:   inv.Confidence(confidence),
			CoverageArea: request.GetString("coverage_area", ""),
			Corrects:     request.GetString("corrects", ""),
		}

		finding, correction, findErr := inv.AddFinding(sessionID, input)
		if findErr != nil {
			return mcp.NewToolResultError(findErr.Error()), nil
		}

		result := map[string]any{
			"finding_id": finding.ID,
			"hint":       "Continue investigating, then call assess to check convergence + coverage.",
		}
		if correction != nil {
			result["correction"] = map[string]any{
				"corrected":      correction.OriginalID,
				"original_claim": correction.OriginalClaim,
				"new_claim":      correction.CorrectedClaim,
			}
		}
		return marshalToolResult(result)

	case "assess":
		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for assess"), nil
		}
		assessResult, assessErr := inv.Assess(sessionID)
		if assessErr != nil {
			return mcp.NewToolResultError(assessErr.Error()), nil
		}
		return marshalToolResult(assessResult)

	case "report":
		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for report"), nil
		}
		state := inv.GetInvestigation(sessionID)
		if state == nil {
			return mcp.NewToolResultError(fmt.Sprintf("investigation %q not found", sessionID)), nil
		}

		report := inv.GenerateReport(state)

		result := map[string]any{
			"report":            report,
			"findings_count":    len(state.Findings),
			"corrections_count": len(state.Corrections),
			"iterations":        state.Iteration,
		}

		cwd := request.GetString("cwd", "")
		if cwd != "" {
			filepath, saveErr := inv.SaveReport(cwd, state.Topic, report)
			if saveErr == nil {
				result["saved_to"] = filepath
			}
		}

		inv.DeleteInvestigation(sessionID)
		return marshalToolResult(result)

	case "status":
		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for status"), nil
		}
		state := inv.GetInvestigation(sessionID)
		if state == nil {
			return mcp.NewToolResultError(fmt.Sprintf("investigation %q not found", sessionID)), nil
		}
		var unchecked []string
		for _, a := range state.CoverageAreas {
			if !state.CoverageChecked[a] {
				unchecked = append(unchecked, a)
			}
		}
		result := map[string]any{
			"topic":              state.Topic,
			"iteration":          state.Iteration,
			"findings_count":     len(state.Findings),
			"corrections_count":  len(state.Corrections),
			"coverage_unchecked": unchecked,
			"last_activity":      state.LastActivityAt,
		}
		return marshalToolResult(result)

	case "list":
		active := inv.ListInvestigations()
		cwd := request.GetString("cwd", "")
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		savedReports, _ := inv.ListReports(cwd)
		return marshalToolResult(map[string]any{
			"active_investigations": active,
			"active_count":          len(active),
			"saved_reports":         savedReports,
			"saved_count":           len(savedReports),
		})

	case "recall":
		topic := request.GetString("topic", "")
		if topic == "" {
			return mcp.NewToolResultError("topic required for recall"), nil
		}
		cwd := request.GetString("cwd", "")
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		result, err := inv.RecallReport(cwd, topic)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("recall error: %v", err)), nil
		}
		if result == nil {
			// Return available topics to help the user
			reports, _ := inv.ListReports(cwd)
			topics := make([]string, 0, len(reports))
			for _, r := range reports {
				topics = append(topics, r.Topic)
			}
			return marshalToolResult(map[string]any{
				"found":            false,
				"message":          fmt.Sprintf("No report found matching %q", topic),
				"available_topics": topics,
			})
		}
		return marshalToolResult(map[string]any{
			"found":    true,
			"filename": result.Filename,
			"topic":    result.Topic,
			"date":     result.Date,
			"content":  result.Content,
		})

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action %q", action)), nil
	}
}

// --- Consensus Handler ---

func (s *Server) handleConsensus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("consensus") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
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
		s.sendBusy(job.ID, "consensus", 0)
		go func() {
			defer s.sendIdle(job.ID)
			s.executeStrategy(jobCtx, job.ID, sess.ID, "consensus", params)
		}()
		return marshalToolResult(map[string]any{"job_id": job.ID, "session_id": sess.ID, "status": "running"})
	}

	result, err := s.orchestrator.Execute(ctx, "consensus", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("consensus failed: %v", err)), nil
	}
	return marshalToolResult(result)
}

// --- DeepResearch Handler ---

func (s *Server) handleDeepresearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("deepresearch") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
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

	defer client.Close()

	content, cacheHit, researchErr := client.Research(ctx, topic, outputFormat, nil, force)
	if researchErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("DeepResearch failed: %v", researchErr)), nil
	}

	// Persist result to disk so investigate recall can cross-search it.
	if !cacheHit {
		cwd := request.GetString("cwd", "")
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		_ = deepresearch.SaveEntryToDisk(cwd, topic, outputFormat, model, nil, content)
	}

	return marshalToolResult(map[string]any{
		"topic":   topic,
		"content": content,
		"cached":  cacheHit,
	})
}

// --- Debate Handler ---

func (s *Server) handleDebate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("debate") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
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
		s.sendBusy(job.ID, "debate", 0)
		go func() {
			defer s.sendIdle(job.ID)
			s.executeStrategy(jobCtx, job.ID, sess.ID, "debate", params)
		}()
		return marshalToolResult(map[string]any{"job_id": job.ID, "session_id": sess.ID, "status": "running"})
	}

	result, err := s.orchestrator.Execute(ctx, "debate", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("debate failed: %v", err)), nil
	}
	return marshalToolResult(result)
}

// --- Agent Run Handler ---

func (s *Server) handleAgentRun(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("agent") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
	agentName, err := request.RequireString("agent")
	if err != nil {
		return mcp.NewToolResultError("agent is required"), nil
	}
	prompt, err := request.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError("prompt is required"), nil
	}

	agent, agentErr := s.agentReg.Get(agentName)
	if agentErr != nil {
		available := s.agentReg.List()
		names := make([]string, len(available))
		for i, a := range available {
			names[i] = a.Name
		}
		return mcp.NewToolResultError(fmt.Sprintf("agent %q not found; available: %v", agentName, names)), nil
	}

	// Resolve CLI: explicit param > agent meta > role routing > default
	role := agent.Role
	if role == "" {
		role = "default"
	}

	cli := request.GetString("cli", "")
	if cli == "" {
		if v, ok := agent.Meta["cli"]; ok && v != "" {
			cli = v
		}
	}

	var rolePref types.RolePreference
	if pref, resolveErr := s.router.Resolve(role); resolveErr == nil {
		rolePref = pref
		if cli == "" && pref.CLI != "" {
			cli = pref.CLI
		}
	}
	if cli == "" {
		cli = "codex"
	}

	cwd := request.GetString("cwd", "")
	maxTurns := int(request.GetFloat("max_turns", 0))
	async := request.GetBool("async", false)

	// Agent frontmatter overrides for model, effort, timeout
	model := agent.Model
	effort := agent.Effort
	if rolePref.CLI != "" {
		envKey := "AIMUX_ROLE_" + strings.ToUpper(strings.ReplaceAll(role, "-", "_"))
		hasEnv := os.Getenv(envKey) != ""
		if rolePref.Model != "" && (hasEnv || model == "") {
			model = rolePref.Model
		}
		if rolePref.ReasoningEffort != "" && (hasEnv || effort == "") {
			effort = rolePref.ReasoningEffort
		}
	}
	timeoutSeconds := agent.Timeout
	if ts := int(request.GetFloat("timeout_seconds", 0)); ts > 0 {
		timeoutSeconds = ts
	}

	cliResolver := resolve.NewProfileResolver(s.cfg.CLIProfiles)

	runCfg := agents.RunConfig{
		Agent:    agent,
		CLI:      cli,
		Prompt:   prompt,
		CWD:      cwd,
		MaxTurns: maxTurns,
		Timeout:  timeoutSeconds,
		Model:    model,
		Effort:   effort,
		Executor: s.executor,
		Resolver: cliResolver,
	}

	if async {
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
		job := s.jobs.Create(sess.ID, cli)
		jobCtx, jobCancel := context.WithCancel(context.Background())
		s.jobs.RegisterCancel(job.ID, jobCancel)
		outputFormat := ""
		if profile, err := s.registry.Get(cli); err == nil {
			outputFormat = profile.OutputFormat
		}
		runCfg.OnOutput = func(resolvedCLI, line string) {
			format := outputFormat
			if profile, err := s.registry.Get(resolvedCLI); err == nil {
				format = profile.OutputFormat
			}
			s.progressSink(job.ID, format)(line)
		}
		s.sendBusy(job.ID, "agent:"+agentName, agentBusyEstimateMs(timeoutSeconds, maxTurns))

		go func() {
			defer s.sendIdle(job.ID)
			s.jobs.StartJob(job.ID, 0)
			s.sessions.Update(sess.ID, func(sess *session.Session) {
				sess.Status = types.SessionStatusRunning
			})
			result, runErr := agents.RunAgent(jobCtx, runCfg)
			if runErr != nil {
				s.jobs.FailJob(job.ID, types.NewExecutorError(runErr.Error(), runErr, "agent_run"))
				s.sessions.Update(sess.ID, func(sess *session.Session) {
					sess.Status = types.SessionStatusFailed
				})
				return
			}
			s.jobs.CompleteJob(job.ID, result.Content, 0)
			s.sessions.Update(sess.ID, func(sess *session.Session) {
				sess.Status = types.SessionStatusCompleted
				sess.Turns = result.Turns
			})
		}()

		return marshalToolResult(map[string]any{
			"agent":      agentName,
			"cli":        cli,
			"job_id":     job.ID,
			"session_id": sess.ID,
			"status":     "running",
		})
	}

	result, runErr := agents.RunAgent(ctx, runCfg)
	if runErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("agent %q failed: %v", agentName, runErr)), nil
	}

	return marshalToolResult(map[string]any{
		"agent":       agentName,
		"cli":         cli,
		"status":      result.Status,
		"turns":       result.Turns,
		"content":     result.Content,
		"duration_ms": result.DurationMS,
		"turn_log":    result.TurnLog,
	})
}

// --- Resource Handlers ---

func (s *Server) handleHealthResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	running := s.jobs.ListRunning()
	health := map[string]any{
		"version":        serverVersion,
		"total_sessions": s.sessions.Count(),
		"running_jobs":   len(running),
		"metrics":        s.metrics.Snapshot(),
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

func (s *Server) handleMetricsResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      request.Params.URI,
			MIMEType: "application/json",
			Text:     s.metrics.Snapshot().JSON(),
		},
	}, nil
}

// --- Helpers ---

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

// handleWorkflow executes a declarative multi-step pipeline as a single MCP call.
func (s *Server) handleWorkflow(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("workflow") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
	name := request.GetString("name", "workflow")
	stepsJSON, err := request.RequireString("steps")
	if err != nil {
		return mcp.NewToolResultError("steps is required"), nil
	}
	input := request.GetString("input", "")
	async := request.GetBool("async", false)

	// Parse steps from JSON array string
	var steps []orch.WorkflowStep
	if err := json.Unmarshal([]byte(stepsJSON), &steps); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid steps JSON: %v", err)), nil
	}

	def := orch.WorkflowDefinition{
		Name:  name,
		Steps: steps,
		Input: input,
	}
	defJSON, err := json.Marshal(def)
	if err != nil {
		return mcp.NewToolResultError("internal error: failed to marshal workflow definition"), nil
	}

	params := types.StrategyParams{
		Extra: map[string]any{
			"workflow": string(defJSON),
		},
	}

	if async {
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sess := s.sessions.Create("workflow", types.SessionModeOnceStateless, "")
		job := s.jobs.Create(sess.ID, "workflow")
		s.sendBusy(job.ID, "workflow", 0)
		go func() {
			defer s.sendIdle(job.ID)
			s.jobs.StartJob(job.ID, 0)
			result, stratErr := s.orchestrator.Execute(context.Background(), "workflow", params)
			if stratErr != nil {
				s.jobs.FailJob(job.ID, types.NewExecutorError(stratErr.Error(), stratErr, ""))
				return
			}
			s.jobs.CompleteJob(job.ID, result.Content, 0)
		}()
		return marshalToolResult(map[string]any{"job_id": job.ID, "status": "running"})
	}

	result, err := s.orchestrator.Execute(ctx, "workflow", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("workflow failed: %v", err)), nil
	}
	return marshalToolResult(result)
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
