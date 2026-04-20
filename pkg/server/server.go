// Package server implements the MCP server using mcp-go SDK.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/agents"
	loomworkers "github.com/thebtf/aimux/pkg/aimuxworkers"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/executor"
	pipeExec "github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/guidance"
	"github.com/thebtf/aimux/pkg/guidance/policies"
	"github.com/thebtf/aimux/pkg/hooks"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/metrics"
	orch "github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/prompt"
	"github.com/thebtf/aimux/pkg/ratelimit"
	"github.com/thebtf/aimux/pkg/resolve"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/server/budget"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/skills"
	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
	"github.com/thebtf/aimux/pkg/tools/deepresearch"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/updater"
	"github.com/thebtf/aimux/pkg/upgrade"
	"github.com/thebtf/mcp-mux/muxcore"
)

// Version is the canonical aimux version string. Used in MCP serverInfo handshake,
// transport log lines, status tool, and updater checks. Single source of truth —
// cmd/aimux/main.go references this value directly to keep log lines and MCP
// handshake consistent across binary and transport layers.
const Version = "4.5.0"

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

// Server holds all dependencies for the MCP server.
type Server struct {
	cfg             *config.Config
	log             *logger.Logger
	registry        *driver.Registry
	router          *routing.Router
	sessions        *session.Registry
	jobs            *session.JobManager
	breakers        *executor.BreakerRegistry
	executor        types.Executor
	mcp             *server.MCPServer
	orchestrator    *orch.Orchestrator
	agentReg        *agents.Registry
	promptEng       *prompt.Engine
	hooks           *hooks.Registry
	metrics         *metrics.Collector
	store           *session.Store
	gcCancel        context.CancelFunc
	skillEngine     *skills.Engine
	rateLimiter     *ratelimit.Limiter
	authToken       string
	projectDir      string // directory used for initial agent discovery
	guidanceReg     *guidance.Registry
	cooldownTracker *executor.ModelCooldownTracker
	sessionHandler  muxcore.SessionHandler // stored for upgrade tool deferred restart
	loom            *loom.LoomEngine       // central task mediator (LoomEngine v3)
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
		executor:        selectBestExecutor(), // ConPTY > PTY > Pipe (Constitution P4)
		cooldownTracker: executor.NewModelCooldownTracker(),
	}
	s.metrics = metrics.New()

	// Initialize rate limiter — per-tool token bucket.
	s.rateLimiter = ratelimit.New(cfg.Server.RateLimitRPS, cfg.Server.RateLimitBurst)

	// Initialize auth token — env var takes precedence over YAML config.
	// Reading secrets from env var is preferred; YAML auth_token is supported
	// for convenience but emits a warning to discourage committing secrets.
	s.authToken = os.Getenv("AIMUX_AUTH_TOKEN")
	if s.authToken == "" {
		s.authToken = cfg.Server.AuthToken
	}
	if cfg.Server.AuthToken != "" {
		log.Warn("server: auth_token loaded from YAML — prefer AIMUX_AUTH_TOKEN env var for secrets")
	}

	// AIMUX_SESSION_STORE=memory opt-out: skip all SQLite persistence.
	// Useful for tests and embedded scenarios where durability is not required.
	// Default (empty or "sqlite") preserves the v4.3.0 behavior.
	sessionStoreMode := os.Getenv("AIMUX_SESSION_STORE")
	if sessionStoreMode == "memory" {
		log.Info("SQLite persistence disabled (AIMUX_SESSION_STORE=memory)")
	}

	// Initialize SQLite persistence and WAL recovery
	dbPath := config.ExpandPath(cfg.Server.DBPath)
	if dbPath != "" && sessionStoreMode != "memory" {
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

			// Initialize LoomEngine with shared SQLite DB.
			taskStore, taskStoreErr := loom.NewTaskStore(store.DB())
			if taskStoreErr != nil {
				log.Warn("LoomEngine unavailable: %v", taskStoreErr)
			} else {
				s.loom = loom.New(taskStore)
				log.Info("LoomEngine initialized (shared SQLite)")
			}
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

	// Register LoomEngine workers (after executor + orchestrator are ready).
	if s.loom != nil {
		s.loom.RegisterWorker(loom.WorkerTypeCLI, loomworkers.NewCLIWorker(s.executor, cliResolver))
		s.loom.RegisterWorker(loom.WorkerTypeThinker, loomworkers.NewThinkerWorker())
		s.loom.RegisterWorker(loom.WorkerTypeInvestigator, loomworkers.NewInvestigatorWorker(s.executor, cliResolver))
		s.loom.RegisterWorker(loom.WorkerTypeOrchestrator, loomworkers.NewOrchestratorWorker(s.orchestrator))

		// Recover tasks that were dispatched/running when daemon last crashed.
		if n, err := s.loom.RecoverCrashed(); err != nil {
			log.Warn("loom crash recovery: %v", err)
		} else if n > 0 {
			log.Info("loom: recovered %d crashed tasks", n)
		}
	}

	// Initialize prompt engine with built-in and project prompts.d/
	builtInPrompts := filepath.Join(cfg.ConfigDir, "prompts.d")
	s.promptEng = prompt.NewEngine(builtInPrompts)
	if err := s.promptEng.Load(); err != nil {
		log.Warn("prompt engine load: %v", err)
	}

	// Initialize think patterns
	patterns.RegisterAll()

	// Initialize guidance policy registry — extensible, registry-driven policy resolution.
	s.guidanceReg = guidance.NewRegistry()
	if err := s.guidanceReg.Register(policies.NewInvestigatePolicy()); err != nil {
		log.Warn("guidance: failed to register investigate policy: %v", err)
	}
	if err := s.guidanceReg.Register(policies.NewThinkPolicy()); err != nil {
		log.Warn("guidance: failed to register think policy: %v", err)
	}
	if err := s.guidanceReg.Register(policies.NewConsensusPolicy()); err != nil {
		log.Warn("guidance: failed to register consensus policy: %v", err)
	}
	if err := s.guidanceReg.Register(policies.NewDebatePolicy()); err != nil {
		log.Warn("guidance: failed to register debate policy: %v", err)
	}
	if err := s.guidanceReg.Register(policies.NewDialogPolicy()); err != nil {
		log.Warn("guidance: failed to register dialog policy: %v", err)
	}
	if err := s.guidanceReg.Register(policies.NewWorkflowPolicy()); err != nil {
		log.Warn("guidance: failed to register workflow policy: %v", err)
	}

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
		Version,
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

// ToolDescription returns the description string that was registered for the named MCP tool.
// Returns an empty string if the tool is not found.
// Used by tests to verify that registered descriptions contain required structured sections.
func (s *Server) ToolDescription(name string) string {
	if s == nil || s.mcp == nil {
		return ""
	}
	st := s.mcp.GetTool(name)
	if st == nil {
		return ""
	}
	return st.Tool.Description
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
				"Use role= for task routing — CLI selection is driven by config (default.yaml roles section), not hardcoded mappings. "+
				"Unknown role names return a validation error immediately — no CLI is spawned. "+
				"Routing uses operator-configured priority from config cli_priority, not alphabetical order. "+
				"When async=true: returns job_id immediately; the CLI runs in the background. "+
				"To collect results: spawn a Sonnet subagent wrapper — see aimux guide skill (poll-wrapper-subagent pattern). "+
				"Sync mode: default returns brief metadata (fits ~4k chars) with content_length. Add include_content=true for full output."),
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
			mcp.WithBoolean("include_content",
				mcp.Description("Sync mode: return full CLI output instead of brief metadata (default false)"),
			),
		),
		s.handleExec,
	)

	// status tool
	s.mcp.AddTool(
		mcp.NewTool("status",
			mcp.WithDescription("Check async job status. "+
				"Returns a result map with status field set to one of: queued, running, completing, completed, failed, aborted. "+
				"status=aborted means the job was running when the daemon restarted (SIGKILL/crash); the job did not complete. "+
				"last_seen_at: timestamp of the last SnapshotJob write for this job; useful for determining when the daemon last observed it alive. "+
				"Default returns metadata only (fits ~4k chars). Add include_content=true for full job output. Use tail=N for last N chars. "+
				"When content is omitted, content_length gives the byte count of the full output. "+
				"progress_tail: last non-empty output line, UTF-8-safe truncated to 100 bytes — compact real-time activity signal. "+
				"progress_lines: total newline count in the accumulated progress buffer. "+
				"While status=running, the response may include stall_warning (key present after 120s of silence) "+
				"or stall_alert (key present after 600s of silence); both keys include cancel instructions. "+
				"stall_warning appears at TierSoftWarning (120s+ silent); stall_alert appears at TierHardStall (600s+) and TierAutoCancel (900s+)."),
			mcp.WithString("job_id",
				mcp.Required(),
				mcp.Description("Job ID from async exec response"),
			),
			mcp.WithBoolean("include_content",
				mcp.Description("Return full job output instead of brief metadata (default false)"),
			),
			mcp.WithNumber("tail",
				mcp.Description("Return last N chars of job output without fetching full content"),
			),
		),
		s.handleStatus,
	)

	// sessions tool
	s.mcp.AddTool(
		mcp.NewTool("sessions",
			mcp.WithDescription("Manage sessions and jobs: list, info, health, cancel, gc, refresh-warmup. "+
				"action=list returns dual-source brief rows (sessions + loom_tasks) — default fits ~4k chars. "+
				"Use sessions_limit/sessions_offset and loom_limit/loom_offset for independent pagination per source; "+
				"legacy limit/offset applies to both sources as a fallback. "+
				"action=info: per-job rows include content_length; add include_content=true to fetch job content. "+
				"action=refresh-warmup re-runs CLI warmup probes and updates the routing pool. "+
				"Session status=aborted indicates the daemon restarted while this session had running jobs (SIGKILL/crash recovery). "+
				"aborted_job_ids lists the job IDs that were aborted during that restart reconciliation. "+
				"last_seen_at on job rows tracks the most recent SnapshotJob write; used by ReconcileOnStartup to identify orphaned jobs."),
			mcp.WithString("action",
				mcp.Required(),
				mcp.Description("Action: list, info, kill, gc, health, cancel, refresh-warmup"),
				mcp.Enum("list", "info", "kill", "gc", "health", "cancel", "refresh-warmup"),
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
				mcp.Description("Max results per source (default 20, max 100). Use sessions_limit/loom_limit for independent control."),
			),
			mcp.WithNumber("offset",
				mcp.Description("Zero-based offset for list pagination (applies to both sources; use sessions_offset/loom_offset for independent control)"),
			),
			mcp.WithNumber("sessions_limit",
				mcp.Description("Max legacy session rows (default 20, max 100)"),
			),
			mcp.WithNumber("sessions_offset",
				mcp.Description("Zero-based offset for legacy session rows"),
			),
			mcp.WithNumber("loom_limit",
				mcp.Description("Max loom task rows (default 20, max 100)"),
			),
			mcp.WithNumber("loom_offset",
				mcp.Description("Zero-based offset for loom task rows"),
			),
			mcp.WithBoolean("include_content",
				mcp.Description("Return full job content in info action (default false)"),
			),
		),
		s.handleSessions,
	)

	// audit tool
	s.mcp.AddTool(
		mcp.NewTool("audit",
			mcp.WithDescription("Run multi-agent codebase audit: scan→validate→investigate. "+
				"Sync mode: default returns brief metadata (fits ~4k chars) with content_length. Add include_content=true for full audit output."),
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
			mcp.WithBoolean("include_content",
				mcp.Description("Sync mode: return full audit output instead of brief metadata (default false)"),
			),
		),
		s.handleAudit,
	)

	// think tool
	s.mcp.AddTool(
		mcp.NewTool("think",
			mcp.WithDescription(mustStatefulToolDescription("think")+" "+
				"Returns structured reasoning metadata (fits ~4k chars); all fields are compact by design."),
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
			mcp.WithDescription(mustStatefulToolDescription("investigate")+" "+
				"action=list: returns brief rows (fits ~4k chars); supports limit (default 20, max 100) and offset. "+
				"action=status: returns brief metadata (fits ~4k chars). "+
				"action=recall: default omits full report; add include_content=true to retrieve it."),
			mcp.WithString("action",
				mcp.Required(),
				mcp.Description("Action: start, finding, assess, report, auto, status, list, recall"),
				mcp.Enum("start", "finding", "assess", "report", "auto", "status", "list", "recall"),
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
				mcp.Description("Working directory for report file save (optional for action=report, auto)"),
			),
			mcp.WithString("cli",
				mcp.Description("Delegate CLI override (optional for action=auto)"),
			),
			mcp.WithBoolean("force",
				mcp.Description("Generate report even when evidence is incomplete (optional for action=report)"),
			),
			mcp.WithBoolean("include_content",
				mcp.Description("action=recall: return full saved report instead of brief (default false)"),
			),
			mcp.WithNumber("limit",
				mcp.Description("action=list: max rows returned (default 20, max 100)"),
			),
			mcp.WithNumber("offset",
				mcp.Description("action=list: zero-based pagination offset"),
			),
		),
		s.handleInvestigate,
	)

	// consensus tool
	s.mcp.AddTool(
		mcp.NewTool("consensus",
			mcp.WithDescription(mustStatefulToolDescription("consensus")+" "+
				"Sync mode: default returns brief metadata (fits ~4k chars) with content_length. Add include_content=true for full transcript."),
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
			mcp.WithBoolean("include_content",
				mcp.Description("Sync mode: return full consensus transcript instead of brief metadata (default false)"),
			),
		),
		s.handleConsensus,
	)

	// debate tool
	s.mcp.AddTool(
		mcp.NewTool("debate",
			mcp.WithDescription(mustStatefulToolDescription("debate")+" "+
				"Sync mode: default returns brief metadata (fits ~4k chars) with content_length. Add include_content=true for full transcript."),
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
			mcp.WithBoolean("include_content",
				mcp.Description("Sync mode: return full debate transcript instead of brief metadata (default false)"),
			),
		),
		s.handleDebate,
	)

	// dialog tool
	s.mcp.AddTool(
		mcp.NewTool("dialog",
			mcp.WithDescription(mustStatefulToolDescription("dialog")+" "+
				"Default returns brief metadata (fits ~4k chars) with content_length. Add include_content=true for full transcript."),
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
			mcp.WithBoolean("include_content",
				mcp.Description("Sync mode: return full dialog transcript instead of brief metadata (default false)"),
			),
		),
		s.handleDialog,
	)

	// agents tool
	s.mcp.AddTool(
		mcp.NewTool("agents",
			mcp.WithDescription("PRIMARY tool for task execution. "+
				"Actions: run (execute task with agent=<name>), list (show agents), find (search agents), info (agent details). "+
				"For run: specify agent=<name> to select the agent. If omitted, returns a candidate list for you to choose from. "+
				"Use find(prompt=<query>) to search agents by keyword, or list to see all available agents with descriptions. "+
				"action=list/find: default returns brief rows (fits ~4k chars); supports limit (default 20, max 100) and offset. "+
				"action=info: default omits system prompt (can be 500KB+); add include_content=true to retrieve it. "+
				"Prefer agents(action=run) over exec when you want an agent with a pre-built system prompt and role — "+
				"exec is for raw prompt dispatch when no matching agent exists or you need low-level CLI control. "+
				"Dispatch policy: "+agentsListHint+
				" Example of the name-match trap: codex-self-delegate is an experimental probe, not a codex-dispatch wrapper."),
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
			mcp.WithString("cwd",
				mcp.Description("Working directory (required for action=run)"),
			),
			mcp.WithBoolean("include_content",
				mcp.Description("action=info: return full agent system prompt (default false)"),
			),
			mcp.WithNumber("limit",
				mcp.Description("action=list/find: max rows returned (default 20, max 100)"),
			),
			mcp.WithNumber("offset",
				mcp.Description("action=list/find: zero-based pagination offset"),
			),
		),
		s.handleAgents,
	)

	// agent tool — runs a discovered agent through any CLI
	s.mcp.AddTool(
		mcp.NewTool("agent",
			mcp.WithDescription("Run a project agent through any CLI. Loads agent definition, "+
				"injects system prompt, delegates to CLI in autonomous mode. "+
				"The CLI IS the agent — it reads files, runs commands, edits code. "+
				"When async=true: returns job_id immediately; use the status tool to poll for completion. "+
				"For long-running agents, spawn a Sonnet subagent wrapper rather than polling in the main context — "+
				"see aimux guide skill (poll-wrapper-subagent pattern). "+
				"Sync mode: default returns brief metadata (fits ~4k chars) with content_length. Add include_content=true for full output."),
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
				mcp.Required(),
				mcp.Description("Working directory (required)"),
			),
			mcp.WithBoolean("async",
				mcp.Description("Run in background"),
			),
			mcp.WithNumber("timeout_seconds",
				mcp.Description("Timeout override in seconds"),
			),
			mcp.WithBoolean("include_content",
				mcp.Description("Sync mode: return full agent output instead of brief metadata (default false)"),
			),
		),
		s.handleAgentRun,
	)

	// deepresearch tool
	s.mcp.AddTool(
		mcp.NewTool("deepresearch",
			mcp.WithDescription("Deep research via Google Gemini API with file attachments and caching. "+
				"Returns full synthesized report; not subject to the 4k default budget. "+
				"This tool is synchronous — it blocks until research is complete and returns content directly (no job_id). "+
				"Results are cached by topic; use force=true to bypass the cache and trigger a fresh Gemini call. "+
				"Cache-miss calls can be slow (30s–120s depending on topic complexity) — plan accordingly."),
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
			mcp.WithDescription(mustStatefulToolDescription("workflow")+" "+
				"Sync mode: default returns brief metadata (fits ~4k chars) with content_length. Add include_content=true for full output."),
			mcp.WithString("name",
				mcp.Description("Workflow name (for logging)"),
			),
			mcp.WithString("steps",
				mcp.Required(),
				mcp.Description("JSON array of step definitions: [{id, tool, params, condition?, on_error?}]. Steps with async=true in their params produce job_id fields in the step result rather than blocking."),
			),
			mcp.WithString("input",
				mcp.Description("Initial input text (available as {{input}} in templates)"),
			),
			mcp.WithBoolean("async",
				mcp.Description("Run in background"),
			),
			mcp.WithBoolean("include_content",
				mcp.Description("Sync mode: return full workflow output instead of brief metadata (default false)"),
			),
		),
		s.handleWorkflow,
	)

	// upgrade tool — binary self-update from GitHub releases
	s.mcp.AddTool(
		mcp.NewTool("upgrade",
			mcp.WithDescription("Check for and apply aimux binary updates from GitHub releases. "+
				"action=check: detect latest version. action=apply: download, verify checksum, replace binary. "+
				"After apply, daemon will exit when all CC sessions disconnect (deferred restart). "+
				"action=check returns compact status fields (fits ~4k chars); release_notes are omitted by default (release_notes_length is reported). "+
				"Use include_content=true to return the full release_notes body."),
			mcp.WithString("action",
				mcp.Required(),
				mcp.Description("Action: check (detect latest version) or apply (download and replace binary)"),
				mcp.Enum("check", "apply"),
			),
			mcp.WithBoolean("include_content",
				mcp.Description("action=check: return full release_notes body (default false)"),
			),
		),
		s.handleUpgrade,
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

func (s *Server) handleStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	bp, budgetErr := budget.ParseBudgetParams(request)
	if budgetErr != nil {
		return mcp.NewToolResultError(budgetErr.Error()), nil
	}

	jobID, err := request.RequireString("job_id")
	if err != nil {
		return mcp.NewToolResultError("job_id is required"), nil
	}

	j := s.jobs.GetSnapshot(jobID)
	if j == nil {
		// Fallback: check LoomEngine TaskStore (async tasks routed through Loom
		// don't create legacy Job entries).
		if s.loom != nil {
			task, taskErr := s.loom.Get(jobID)
			if taskErr == nil && task != nil {
				result := map[string]any{
					"job_id": task.ID,
					"status": string(task.Status),
				}
				if task.Status.IsTerminal() {
					taskContentLen := len(task.Result)
					if bp.Tail > 0 {
						tail := task.Result
						if len(tail) > bp.Tail {
							tail = tail[len(tail)-bp.Tail:]
						}
						result["content_tail"] = tail
						result["content_length"] = taskContentLen
						meta := budget.BuildTruncationMeta(nil, taskContentLen, fmt.Sprintf("Use status(job_id=%s, include_content=true) for full output.", jobID))
						if meta.Truncated {
							result["truncated"] = meta.Truncated
							result["hint"] = meta.Hint
						}
					} else if bp.IncludeContent {
						result["content"] = task.Result
					} else {
						result["content_length"] = taskContentLen
						meta := budget.BuildTruncationMeta(nil, taskContentLen, fmt.Sprintf("Use status(job_id=%s, include_content=true) for full output.", jobID))
						if meta.Truncated {
							result["truncated"] = meta.Truncated
							result["hint"] = meta.Hint
						}
					}
					if task.Error != "" {
						result["error"] = task.Error
					}
				}
				whitelist := budget.FieldWhitelist["status"]
				filtered, _, applyErr := budget.ApplyFields(result, bp.Fields, whitelist)
				if applyErr != nil {
					return mcp.NewToolResultError(applyErr.Error()), nil
				}
				return marshalToolResult(filtered)
			}
		}
		return mcp.NewToolResultError(fmt.Sprintf("job %q not found", jobID)), nil
	}

	pollCount := s.jobs.IncrementPoll(jobID)

	result := map[string]any{
		"job_id":         j.ID,
		"status":         string(j.Status),
		"progress":       j.Progress,
		"poll_count":     pollCount,
		"session_id":     j.SessionID,
		"progress_tail":  j.LastOutputLine,
		"progress_lines": j.ProgressLines,
		// last_seen_at: time of the most recent SnapshotJob write for this job.
		// ProgressUpdatedAt is updated on every state transition (Create, StartJob,
		// AppendProgress, CompleteJob, FailJob, CancelJob) which corresponds 1:1 with
		// SnapshotJob calls, making it the correct in-memory proxy for the SQLite column.
		"last_seen_at": j.ProgressUpdatedAt,
	}

	if j.Status == types.JobStatusCompleted || j.Status == types.JobStatusFailed || j.Status == types.JobStatusAborted {
		if j.Error != nil {
			result["error"] = j.Error.Error()
		}
		contentLen := len(j.Content)
		if bp.Tail > 0 {
			tail := j.Content
			if len(tail) > bp.Tail {
				tail = tail[len(tail)-bp.Tail:]
			}
			result["content_tail"] = tail
			result["content_length"] = contentLen
			meta := budget.BuildTruncationMeta(nil, contentLen, fmt.Sprintf("Use status(job_id=%s, include_content=true) for full output.", jobID))
			if meta.Truncated {
				result["truncated"] = meta.Truncated
				result["hint"] = meta.Hint
			}
		} else if bp.IncludeContent {
			result["content"] = j.Content
		} else {
			result["content_length"] = contentLen
			meta := budget.BuildTruncationMeta(nil, contentLen, fmt.Sprintf("Use status(job_id=%s, include_content=true) for full output.", jobID))
			if meta.Truncated {
				result["truncated"] = meta.Truncated
				result["hint"] = meta.Hint
			}
		}
	}

	if j.Status == types.JobStatusRunning {
		// Use LastOutputAt when available; fall back to CreatedAt for jobs that
		// have never produced a line of output yet (zero value).
		baseline := j.LastOutputAt
		if baseline.IsZero() {
			baseline = j.CreatedAt
		}
		tier := evaluateInactivityTier(baseline, &s.cfg.Server)
		applyStallGuidance(result, tier, j.ID)
	}

	if pollCount >= 3 {
		result["warning"] = fmt.Sprintf("Polling detected (%d calls). Use a Sonnet subagent wrapper — see aimux guide skill (poll-wrapper-subagent pattern).", pollCount)
	}

	whitelist := budget.FieldWhitelist["status"]
	filtered, _, applyErr := budget.ApplyFields(result, bp.Fields, whitelist)
	if applyErr != nil {
		return mcp.NewToolResultError(applyErr.Error()), nil
	}
	return marshalToolResult(filtered)
}

func (s *Server) handleSessions(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action, err := request.RequireString("action")
	if err != nil {
		return mcp.NewToolResultError("action is required"), nil
	}

	switch action {
	case "list":
		// FR-11 (BREAKING): sessions(action=list) now returns dual-source shape.
		// Legacy merged "sessions" array is replaced by separate "sessions" and
		// "loom_tasks" arrays with independent pagination. Callers reading loom
		// task fields from the old merged sessions array must migrate to "loom_tasks".
		bp, budgetErr := budget.ParseBudgetParams(request)
		if budgetErr != nil {
			return mcp.NewToolResultError(budgetErr.Error()), nil
		}
		statusFilter := request.GetString("status", "")
		allSessions := s.sessions.List(types.SessionStatus(statusFilter))

		// Build session briefs using a single-pass job count map to avoid N+1.
		jobCounts := s.jobs.CountsBySession()
		sessionBriefs := make([]SessionBrief, len(allSessions))
		for i, sess := range allSessions {
			sessionBriefs[i] = SessionBrief{
				ID:        sess.ID,
				Status:    sess.Status,
				CLI:       sess.CLI,
				CreatedAt: sess.CreatedAt,
				JobCount:  jobCounts[sess.ID],
			}
		}

		var allLoomTasks []*loom.Task
		projectID := projectIDFromContext(ctx)
		if s.loom != nil && projectID != "" {
			tasks, taskErr := s.loom.List(projectID)
			if taskErr != nil {
				s.log.Warn("sessions list: loom list failed for project %s: %v", projectID, taskErr)
			} else {
				allLoomTasks = tasks
			}
		}
		// Apply status filter to loom tasks using the same direct-match semantics
		// as sessions.List(statusFilter): empty string means include all.
		if statusFilter != "" && len(allLoomTasks) > 0 {
			filteredLoom := make([]*loom.Task, 0, len(allLoomTasks))
			for _, t := range allLoomTasks {
				if string(t.Status) == statusFilter {
					filteredLoom = append(filteredLoom, t)
				}
			}
			allLoomTasks = filteredLoom
		}

		loomBriefs := make([]LoomTaskBrief, len(allLoomTasks))
		for i, t := range allLoomTasks {
			loomBriefs[i] = LoomTaskBrief{
				ID:                t.ID,
				Status:            t.Status,
				Kind:              string(t.WorkerType),
				CreatedAt:         t.CreatedAt,
				ProgressLineCount: 0,
			}
		}

		dsr := budget.PaginateDualSource(sessionBriefs, loomBriefs, bp)
		result := map[string]any{
			"sessions":            dsr.Sessions,
			"loom_tasks":          dsr.LoomTasks,
			"sessions_pagination": dsr.SessionsPagination,
			"loom_pagination":     dsr.LoomPagination,
		}
		filtered, _, applyErr := budget.ApplyFields(result, bp.Fields, budget.FieldWhitelist["sessions/list"])
		if applyErr != nil {
			return mcp.NewToolResultError(applyErr.Error()), nil
		}
		return marshalToolResult(filtered)

	case "info":
		bp, budgetErr := budget.ParseBudgetParams(request)
		if budgetErr != nil {
			return mcp.NewToolResultError(budgetErr.Error()), nil
		}
		if valErr := budget.ValidateContentBearingFields(
			bp.Fields,
			budget.ContentBearingFields["sessions/info"],
			bp.IncludeContent,
		); valErr != nil {
			return mcp.NewToolResultError(valErr.Error()), nil
		}

		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for info"), nil
		}
		sess := s.sessions.Get(sessionID)
		if sess == nil {
			return mcp.NewToolResultError("session not found"), nil
		}
		rawJobs := s.jobs.ListBySessionSnapshot(sessionID)

		jobBriefs := make([]JobBrief, len(rawJobs))
		for i, j := range rawJobs {
			brief := JobBrief{
				ID:            j.ID,
				Status:        j.Status,
				Progress:      j.Progress,
				ContentLength: len(j.Content),
			}
			if bp.IncludeContent {
				brief.Content = j.Content
			}
			jobBriefs[i] = brief
		}

		result := map[string]any{
			"session": map[string]any{
				"id":         sess.ID,
				"status":     sess.Status,
				"cli":        sess.CLI,
				"created_at": sess.CreatedAt,
			},
			"jobs": jobBriefs,
		}
		filtered, _, applyErr := budget.ApplyFields(result, bp.Fields, budget.FieldWhitelist["sessions/info"])
		if applyErr != nil {
			return mcp.NewToolResultError(applyErr.Error()), nil
		}
		return marshalToolResult(filtered)

	case "health":
		running := s.jobs.ListRunning()
		snap := s.metrics.Snapshot()
		health := map[string]any{
			"total_sessions": s.sessions.Count(),
			"running_jobs":   len(running),
			"per_project":    snap.PerProject,
		}
		// Include Loom task counts when available.
		if s.loom != nil {
			projectID := projectIDFromContext(ctx)
			if projectID != "" {
				if tasks, err := s.loom.List(projectID); err != nil {
					s.log.Warn("sessions health: loom list failed for project %s: %v", projectID, err)
					health["loom_error"] = err.Error()
				} else {
					health["loom_tasks"] = len(tasks)
				}
			}
		}
		// F2 shim-reconnect counters (muxcore v0.21.1+). Silent graceful
		// degradation if control socket unreachable — health endpoint
		// must never fail because of optional observability passthrough.
		if f2, err := queryF2Metrics(); err == nil {
			health["shim_reconnect_refreshed"] = f2.Refreshed
			health["shim_reconnect_fallback_spawned"] = f2.FallbackSpawned
			health["shim_reconnect_gave_up"] = f2.GaveUp
		} else {
			s.log.Debug("sessions health: F2 metrics unavailable: %v", err)
		}
		return marshalToolResult(health)

	case "cancel":
		jobID := request.GetString("job_id", "")
		if jobID == "" {
			return mcp.NewToolResultError("job_id required for cancel"), nil
		}
		// Try legacy JobManager first, then Loom.
		if s.jobs.CancelJob(jobID) {
			return mcp.NewToolResultText(`{"status":"cancelled"}`), nil
		}
		if s.loom != nil {
			if err := s.loom.Cancel(jobID); err == nil {
				return mcp.NewToolResultText(`{"status":"cancelled"}`), nil
			} else {
				return mcp.NewToolResultError(err.Error()), nil
			}
		}
		return mcp.NewToolResultError("job not found"), nil

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

	case "refresh-warmup":
		// Re-run CLI warmup probes and update registry availability.
		// Returns refreshed=false (with reason) when warmup is disabled via env or config.
		if os.Getenv("AIMUX_WARMUP") == "false" {
			return marshalToolResult(map[string]any{
				"refreshed": false,
				"reason":    "warmup disabled via AIMUX_WARMUP=false",
			})
		}
		if s.cfg != nil && !s.cfg.Server.WarmupEnabled {
			return marshalToolResult(map[string]any{
				"refreshed": false,
				"reason":    "warmup disabled via warmup_enabled: false in config",
			})
		}
		if err := driver.RunWarmup(ctx, s.registry, s.cfg); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("refresh-warmup failed: %v", err)), nil
		}
		enabled := s.registry.EnabledCLIs()
		all := s.registry.AllCLIs()
		enabledSet := make(map[string]bool, len(enabled))
		for _, e := range enabled {
			enabledSet[e] = true
		}
		var excluded []string
		for _, name := range all {
			if !enabledSet[name] {
				excluded = append(excluded, name)
			}
		}
		return marshalToolResult(map[string]any{
			"refreshed": true,
			"available": enabled,
			"excluded":  excluded,
		})

	default:
		// Delegate cooldown sub-actions before returning an error for truly unknown actions.
		if result, err := s.handleCooldown(ctx, request, action); result != nil || err != nil {
			return result, err
		}
		return mcp.NewToolResultError(fmt.Sprintf("unknown action %q", action)), nil
	}
}

// --- Resource Handlers ---

func (s *Server) handleHealthResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	running := s.jobs.ListRunning()
	health := map[string]any{
		"version":        Version,
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

func (s *Server) handleUpgrade(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action, err := request.RequireString("action")
	if err != nil {
		return mcp.NewToolResultError("action is required (check or apply)"), nil
	}

	switch action {
	case "check":
		release, checkErr := updater.CheckUpdate(ctx, Version)
		if checkErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("update check failed: %v", checkErr)), nil
		}
		if release == nil {
			return marshalToolResult(map[string]any{
				"status":          "up_to_date",
				"current_version": Version,
			})
		}
		// Brief: compact status fields. release_notes can exceed 4 KiB on feature releases,
		// so it is omitted by default; callers who need the full body use
		// upgrade(action=check, include_content=true) or fetch the GitHub release page.
		includeContent := request.GetBool("include_content", false)
		releaseNotesLen := len(release.ReleaseNotes)
		payload := map[string]any{
			"status":                "update_available",
			"current_version":       Version,
			"latest_version":        release.Version,
			"asset_name":            release.AssetName,
			"published_at":          release.PublishedAt,
			"release_notes_length":  releaseNotesLen,
		}
		if includeContent {
			payload["release_notes"] = release.ReleaseNotes
		} else if releaseNotesLen > 0 {
			payload["truncated"] = true
			payload["hint"] = "release_notes omitted (" + fmt.Sprintf("%d", releaseNotesLen) + " bytes). Use upgrade(action=check, include_content=true) for full body."
		}
		return marshalToolResult(payload)

	case "apply":
		binaryPath, exeErr := os.Executable()
		if exeErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("locate executable: %v", exeErr)), nil
		}

		// Detect engine mode and build upgrade.SessionHandler adapter in one assertion.
		// aimuxHandler is constructed only in engine/session mode; non-engine stdio
		// transport lacks IPC sockets to hand off, so hot-swap (Phase 3) is disabled
		// in that mode per clarification C2. aimuxHandler.SetUpdatePending() satisfies
		// the upgrade.SessionHandler interface directly.
		h, engineMode := s.sessionHandler.(*aimuxHandler)
		var sh upgrade.SessionHandler
		if engineMode {
			sh = h
		}

		coord := &upgrade.Coordinator{
			Version:        Version,
			BinaryPath:     binaryPath,
			SessionHandler: sh,
			EngineMode:     engineMode,
			Logger:         s.log,
		}

		result, applyErr := coord.Apply(ctx, upgrade.ModeAuto)
		if applyErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("update failed: %v", applyErr)), nil
		}

		// Response envelope. Phase 1: Method is always "deferred" or "up_to_date"
		// (hot-swap not yet implemented — blocked on engram #130 / muxcore v0.21.0+).
		// Phase 3 will add "hot_swap" branch with HandoffTransferred + HandoffDurationMs.
		switch result.Method {
		case "up_to_date":
			return marshalToolResult(map[string]any{
				"status":          "up_to_date",
				"current_version": Version,
			})
		case "hot_swap":
			return marshalToolResult(map[string]any{
				"status":                   "updated_hot_swap",
				"previous_version":         result.PreviousVersion,
				"new_version":              result.NewVersion,
				"handoff_transferred_ids":  result.HandoffTransferred,
				"handoff_duration_ms":      result.HandoffDurationMs,
				"message":                  result.Message,
			})
		case "deferred":
			// Preserve v4.3.0 wire format: status="updated" for backwards compat.
			// Phase 3 will switch to status="updated_deferred" when adding hot-swap.
			payload := map[string]any{
				"status":           "updated",
				"previous_version": result.PreviousVersion,
				"new_version":      result.NewVersion,
				"message":          result.Message,
			}
			if result.HandoffError != "" {
				payload["handoff_error"] = result.HandoffError
			}
			return marshalToolResult(payload)
		default:
			return marshalToolResult(map[string]any{
				"status":           "updated",
				"previous_version": result.PreviousVersion,
				"new_version":      result.NewVersion,
				"message":          result.Message,
			})
		}

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown upgrade action %q (use check or apply)", action)), nil
	}
}
