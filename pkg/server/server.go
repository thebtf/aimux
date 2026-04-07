// Package server implements the MCP server using mcp-go SDK.
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
	"github.com/mark3labs/mcp-go/server"

	"github.com/thebtf/aimux/pkg/agents"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/hooks"
	inv "github.com/thebtf/aimux/pkg/investigate"
	"github.com/thebtf/aimux/pkg/metrics"
	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
	conptyExec "github.com/thebtf/aimux/pkg/executor/conpty"
	pipeExec "github.com/thebtf/aimux/pkg/executor/pipe"
	ptyExec "github.com/thebtf/aimux/pkg/executor/pty"
	"github.com/thebtf/aimux/pkg/logger"
	orch "github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/parser"
	"github.com/thebtf/aimux/pkg/prompt"
	"github.com/thebtf/aimux/pkg/resolve"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/tools/deepresearch"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

const serverVersion = "3.0.0-dev"

// aimuxInstructions is delivered to every MCP client on connect via server.WithInstructions().
// This replaces the need for an external SKILL.md file — the server documents itself.
const aimuxInstructions = `aimux — AI CLI Multiplexer (13 MCP tools, 12 CLIs, 23 think patterns)

One MCP server that routes prompts to 12 AI coding CLIs with role-based routing,
multi-model orchestration, structured reasoning, and deep investigation.

## Tool Selection — "I need to..."

| I need to...                         | Tool        | Key params                            |
|--------------------------------------|-------------|---------------------------------------|
| Run a prompt on an AI CLI            | exec        | prompt, role, cli, async              |
| Get consensus from multiple models   | consensus   | topic, synthesize                     |
| Have models debate a decision        | debate      | topic, max_turns                      |
| Multi-turn discussion between CLIs   | dialog      | prompt, max_turns                     |
| Structured reasoning/analysis        | think       | pattern (23 options)                  |
| Deep investigation with tracking     | investigate | action, topic, domain                 |
| Run a codebase audit                 | audit       | cwd, mode (quick/standard/deep)       |
| Execute a project agent              | agent       | agent (name), prompt                  |
| Chain multiple steps declaratively   | workflow    | steps (JSON), input                   |
| Check async job status               | status      | job_id                                |
| Manage sessions                      | sessions    | action (list/health/gc/cancel)        |
| Discover available agents            | agents      | action (list/find)                    |
| Deep research via Gemini             | deepresearch| topic                                 |

## Roles (exec tool) — don't pick CLI manually, use role=
coding → codex | codereview → gemini | debug → codex | secaudit → codex
analyze → gemini | refactor → codex | testgen → codex | planner → codex
If a CLI fails (rate limit, timeout), aimux auto-retries with the next capable CLI.

## Think Patterns (23) — use think tool with pattern=
Core: think, critical_thinking, sequential_thinking, scientific_method,
decision_framework, problem_decomposition, debugging_approach, mental_model,
metacognitive_monitoring, structured_argumentation, collaborative_reasoning,
recursive_thinking, domain_modeling, architecture_analysis, stochastic_algorithm,
temporal_thinking, visual_reasoning
Research: source_comparison, literature_review, peer_review,
replication_analysis, experimental_loop (stateful), research_synthesis

## Investigation — start → finding → assess → report → recall
Domains auto-detected from topic: security, performance, architecture,
debugging, research, generic. Cross-tool dispatch: assess auto-runs think.

## Anti-Patterns
- Don't specify cli= when role= is enough — let routing pick the best CLI
- Don't use sync exec for tasks >30s — use async=true
- Don't skip investigate for complex bugs — jumping to fix wastes time
- Don't run consensus with 1 CLI — needs 2+ for comparison`

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

			// Start periodic snapshot (every 30s)
			go s.runSnapshotLoop(store)
		}
	}

	// Start GC reaper for expired sessions
	gcCtx, gcCancel := context.WithCancel(context.Background())
	s.gcCancel = gcCancel
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
		server.WithInstructions(aimuxInstructions),
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

// ServeSSE starts the MCP server with Server-Sent Events transport.
// WARNING: No authentication is applied. Bind to localhost unless you add auth middleware.
func (s *Server) ServeSSE(addr string) error {
	addr = ensureLocalhostBinding(addr)
	s.log.Info("MCP server starting on SSE at %s (aimux v%s)", addr, serverVersion)
	if !isLocalhostAddr(addr) {
		s.log.Warn("SSE transport bound to non-localhost address %s — no authentication is configured", addr)
	}
	sseServer := server.NewSSEServer(s.mcp)
	return sseServer.Start(addr)
}

// ServeHTTP starts the MCP server with StreamableHTTP transport.
// WARNING: No authentication is applied. Bind to localhost unless you add auth middleware.
func (s *Server) ServeHTTP(addr string, opts ...server.StreamableHTTPOption) error {
	addr = ensureLocalhostBinding(addr)
	s.log.Info("MCP server starting on HTTP at %s (aimux v%s)", addr, serverVersion)
	if !isLocalhostAddr(addr) {
		s.log.Warn("HTTP transport bound to non-localhost address %s — no authentication is configured", addr)
	}
	httpServer := server.NewStreamableHTTPServer(s.mcp, opts...)
	return httpServer.Start(addr)
}

// ensureLocalhostBinding defaults to localhost if only a port is specified (e.g., ":8080" → "127.0.0.1:8080").
func ensureLocalhostBinding(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "127.0.0.1" + addr
	}
	return addr
}

// isLocalhostAddr checks if the address is bound to localhost/127.0.0.1.
func isLocalhostAddr(addr string) bool {
	return strings.HasPrefix(addr, "127.0.0.1") || strings.HasPrefix(addr, "localhost") || strings.HasPrefix(addr, "[::1]")
}

// runSnapshotLoop periodically saves in-memory state to SQLite.
func (s *Server) runSnapshotLoop(store *session.Store) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if err := store.SnapshotAll(s.sessions, s.jobs); err != nil {
			s.log.Warn("snapshot failed: %v", err)
		}
	}
}

// Shutdown stops background services (GC reaper, snapshot) and closes persistence.
func (s *Server) Shutdown() {
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
			mcp.WithDescription("Execute a prompt via an AI coding CLI. "+
				"Use role= for automatic CLI routing (coding→codex, codereview→gemini, debug→codex, secaudit→codex, analyze→gemini, refactor→codex, testgen→codex, docgen→codex, planner→codex, thinkdeep→codex). "+
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
			mcp.WithString("issue", mcp.Description("Issue to analyze (critical_thinking, debugging_approach)")),
			mcp.WithString("topic", mcp.Description("Topic (structured_argumentation, collaborative_reasoning)")),
			mcp.WithString("thought", mcp.Description("Thought text (think, sequential_thinking)")),
			mcp.WithString("session_id", mcp.Description("Session ID for stateful patterns")),
			mcp.WithString("decision", mcp.Description("Decision to evaluate (decision_framework)")),
			mcp.WithString("problem", mcp.Description("Problem to analyze (problem_decomposition, mental_model, recursive_thinking)")),
			mcp.WithString("task", mcp.Description("Task for metacognitive monitoring")),
			mcp.WithString("modelName", mcp.Description("Mental model name (mental_model)")),
			mcp.WithString("approachName", mcp.Description("Debugging method name (debugging_approach)")),
			mcp.WithString("domainName", mcp.Description("Domain name (domain_modeling)")),
			mcp.WithString("timeFrame", mcp.Description("Time frame (temporal_thinking)")),
			mcp.WithString("operation", mcp.Description("Visual operation (visual_reasoning)")),
			mcp.WithString("algorithmType", mcp.Description("Algorithm type: mdp, mcts, bandit, bayesian, hmm")),
			mcp.WithString("problemDefinition", mcp.Description("Problem definition (stochastic_algorithm)")),
			mcp.WithString("stage", mcp.Description("Stage (scientific_method, collaborative_reasoning)")),
			mcp.WithString("artifact", mcp.Description("Artifact to review (peer_review)")),
			mcp.WithString("claim", mcp.Description("Claim to analyze (replication_analysis)")),
			mcp.WithString("hypothesis", mcp.Description("Hypothesis to test (experimental_loop)")),
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

	// aimux-guide: comprehensive decision-making guide for all 13 tools
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-guide",
			mcp.WithPromptDescription("Complete guide to aimux tools — when and how to use each of the 13 MCP tools, role routing, think patterns, investigation flow, workflows"),
		),
		s.handleGuidePrompt,
	)

	// aimux-investigate: structured investigation protocol with convergence tracking
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-investigate",
			mcp.WithPromptDescription("Investigation protocol — structured deep analysis with convergence tracking"),
			mcp.WithArgument("topic",
				mcp.ArgumentDescription("What to investigate"),
			),
		),
		s.handleInvestigatePrompt,
	)

	// aimux-workflow: declarative multi-step pipeline builder
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-workflow",
			mcp.WithPromptDescription("Build declarative multi-step execution pipelines"),
			mcp.WithArgument("goal",
				mcp.ArgumentDescription("What the workflow should accomplish"),
			),
		),
		s.handleWorkflowPrompt,
	)

	// aimux-review: targeted code review plan from git diff
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-review",
			mcp.WithPromptDescription("Code review workflow — generates targeted review plan from git diff"),
			mcp.WithArgument("scope",
				mcp.ArgumentDescription("What to review: 'staged', 'branch', 'last-commit', or file paths"),
			),
		),
		s.handleReviewPrompt,
	)

	// aimux-debug: structured debug workflow
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-debug",
			mcp.WithPromptDescription("Debug workflow — structured investigation plan for any error or bug"),
			mcp.WithArgument("error",
				mcp.ArgumentDescription("Error message, symptom, or bug description to debug"),
			),
		),
		s.handleDebugPrompt,
	)

	// aimux-consensus: multi-model consensus plan
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-consensus",
			mcp.WithPromptDescription("Multi-model consensus — generates consensus or debate plan for a question"),
			mcp.WithArgument("question",
				mcp.ArgumentDescription("Question or decision to get multi-model consensus on"),
			),
		),
		s.handleConsensusPrompt,
	)

	// aimux-audit: codebase audit plan
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-audit",
			mcp.WithPromptDescription("Codebase audit — generates audit plan with quick/standard/deep modes"),
			mcp.WithArgument("cwd",
				mcp.ArgumentDescription("Directory to audit (defaults to current working directory)"),
			),
		),
		s.handleAuditPrompt,
	)

	// aimux-agent: agent execution plan
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-agent",
			mcp.WithPromptDescription("Agent execution — matches task to available agents and generates execution plan"),
			mcp.WithArgument("task",
				mcp.ArgumentDescription("Task description to match against available agents"),
			),
		),
		s.handleAgentExecPrompt,
	)

	// aimux-research: research workflow with think patterns
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-research",
			mcp.WithPromptDescription("Research workflow — multi-phase research plan using think patterns and investigation"),
			mcp.WithArgument("topic",
				mcp.ArgumentDescription("Research topic to investigate"),
			),
		),
		s.handleResearchPrompt,
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
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil
	}

	s.executeJob(ctx, job.ID, sess.ID, role, args, cb, profile.OutputFormat)

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

// executeJob runs a CLI process, parses output, and updates job/session state.
// When role is non-empty the router's fallback list is consulted on transient
// failures (rate limit, auth error, connection error) — up to 2 additional CLIs
// are tried before giving up.
func (s *Server) executeJob(ctx context.Context, jobID, sessionID, role string, args types.SpawnArgs, cb *executor.CircuitBreaker, outputFormat string) {
	s.jobs.StartJob(jobID, 0)
	s.sessions.Update(sessionID, func(sess *session.Session) {
		sess.Status = types.SessionStatusRunning
	})

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

	data, _ := json.Marshal(map[string]any{
		"session_id":   sess.ID,
		"status":       result.Status,
		"turns":        result.Turns,
		"content":      result.Content,
		"participants": result.Participants,
	})
	return mcp.NewToolResultText(string(data)), nil
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
		data, _ := json.Marshal(map[string]any{"agents": summaries, "count": len(summaries)})
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
			data, _ := json.Marshal(map[string]any{
				"agent":      agentName,
				"job_id":     job.ID,
				"session_id": sess.ID,
				"status":     "running",
			})
			return mcp.NewToolResultText(string(data)), nil
		}

		s.executeJob(ctx, job.ID, sess.ID, role, args, cb, profile.OutputFormat)

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
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
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
			"criteria", "options", "components", "subProblems", "dependencies",
			"risks", "stakeholders", "entities", "relationships", "rules",
			"constraints", "states", "events", "transitions", "transformations",
			"elements", "claims", "biases", "uncertainties", "cognitiveProcesses",
			"parameters", "argument", "contribution", "entry", "hypothesisUpdate",
			// Numeric
			"confidence", "thoughtNumber", "totalThoughts", "currentDepth",
			"maxDepth", "iterations", "revisesThought", "branchFromThought",
			// Boolean
			"isRevision",
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
			"total":      complexity.Total,
			"threshold":  complexity.Threshold,
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
			"guidance": fmt.Sprintf("Begin investigation [%s domain]. Recommended first area: %s. "+
				"Read implementations, not descriptions. Then call finding action.", state.Domain, state.CoverageAreas[0]),
		}
		if domainName == "" {
			result["available_domains"] = inv.DomainNames()
		}
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil

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
			Source:        source,
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
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil

	case "assess":
		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for assess"), nil
		}
		assessResult, assessErr := inv.Assess(sessionID)
		if assessErr != nil {
			return mcp.NewToolResultError(assessErr.Error()), nil
		}
		data, _ := json.Marshal(assessResult)
		return mcp.NewToolResultText(string(data)), nil

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
			"report":           report,
			"findings_count":   len(state.Findings),
			"corrections_count": len(state.Corrections),
			"iterations":       state.Iteration,
		}

		cwd := request.GetString("cwd", "")
		if cwd != "" {
			filepath, saveErr := inv.SaveReport(cwd, state.Topic, report)
			if saveErr == nil {
				result["saved_to"] = filepath
			}
		}

		inv.DeleteInvestigation(sessionID)
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil

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
			"iteration":         state.Iteration,
			"findings_count":    len(state.Findings),
			"corrections_count": len(state.Corrections),
			"coverage_unchecked": unchecked,
			"last_activity":     state.LastActivityAt,
		}
		data, _ := json.Marshal(result)
		return mcp.NewToolResultText(string(data)), nil

	case "list":
		active := inv.ListInvestigations()
		cwd := request.GetString("cwd", "")
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		savedReports, _ := inv.ListReports(cwd)
		data, _ := json.Marshal(map[string]any{
			"active_investigations": active,
			"active_count":          len(active),
			"saved_reports":         savedReports,
			"saved_count":           len(savedReports),
		})
		return mcp.NewToolResultText(string(data)), nil

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
			data, _ := json.Marshal(map[string]any{
				"found":            false,
				"message":          fmt.Sprintf("No report found matching %q", topic),
				"available_topics": topics,
			})
			return mcp.NewToolResultText(string(data)), nil
		}
		data, _ := json.Marshal(map[string]any{
			"found":    true,
			"filename": result.Filename,
			"topic":    result.Topic,
			"date":     result.Date,
			"content":  result.Content,
		})
		return mcp.NewToolResultText(string(data)), nil

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action %q", action)), nil
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

	// Persist result to disk so investigate recall can cross-search it.
	if !cacheHit {
		cwd := request.GetString("cwd", "")
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		_ = deepresearch.SaveEntryToDisk(cwd, topic, outputFormat, model, nil, content)
	}

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

// --- Agent Run Handler ---

func (s *Server) handleAgentRun(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	cli := request.GetString("cli", "")
	if cli == "" {
		if v, ok := agent.Meta["cli"]; ok && v != "" {
			cli = v
		}
	}
	if cli == "" {
		role := agent.Role
		if role == "" {
			role = "default"
		}
		if pref, resolveErr := s.router.Resolve(role); resolveErr == nil && pref.CLI != "" {
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

		go func() {
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

		data, _ := json.Marshal(map[string]any{
			"agent":      agentName,
			"cli":        cli,
			"job_id":     job.ID,
			"session_id": sess.ID,
			"status":     "running",
		})
		return mcp.NewToolResultText(string(data)), nil
	}

	result, runErr := agents.RunAgent(ctx, runCfg)
	if runErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("agent %q failed: %v", agentName, runErr)), nil
	}

	data, _ := json.Marshal(map[string]any{
		"agent":       agentName,
		"cli":         cli,
		"status":      result.Status,
		"turns":       result.Turns,
		"content":     result.Content,
		"duration_ms": result.DurationMS,
		"turn_log":    result.TurnLog,
	})
	return mcp.NewToolResultText(string(data)), nil
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

// --- Prompt Handlers ---

func (s *Server) handleBackgroundPrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	taskDesc := ""
	if args := request.Params.Arguments; args != nil {
		if desc, exists := args["task_description"]; exists && desc != "" {
			taskDesc = desc
		}
	}

	// Analyze task keywords to recommend the best role.
	recommendedRole := "coding"
	roleReason := "general implementation tasks default to the coding role"
	if taskDesc != "" {
		lower := strings.ToLower(taskDesc)
		switch {
		case strings.Contains(lower, "review") || strings.Contains(lower, "audit") || strings.Contains(lower, "analyze"):
			recommendedRole = "codereview"
			roleReason = "task involves reviewing or analyzing — codereview role activates the best review CLI"
		case strings.Contains(lower, "security") || strings.Contains(lower, "vuln") || strings.Contains(lower, "owasp"):
			recommendedRole = "secaudit"
			roleReason = "security keyword detected — secaudit role activates OWASP-aware CLIs"
		case strings.Contains(lower, "test") || strings.Contains(lower, "spec") || strings.Contains(lower, "coverage"):
			recommendedRole = "testgen"
			roleReason = "testing keyword detected — testgen role activates test-specialized CLIs"
		case strings.Contains(lower, "plan") || strings.Contains(lower, "design") || strings.Contains(lower, "architect"):
			recommendedRole = "planner"
			roleReason = "planning/design keyword detected — planner role activates architecture-aware CLIs"
		case strings.Contains(lower, "debug") || strings.Contains(lower, "bug") || strings.Contains(lower, "fix") || strings.Contains(lower, "crash"):
			recommendedRole = "debug"
			roleReason = "debug/fix keyword detected — debug role activates tracing-capable CLIs"
		case strings.Contains(lower, "refactor") || strings.Contains(lower, "cleanup") || strings.Contains(lower, "reorganize"):
			recommendedRole = "refactor"
			roleReason = "refactoring keyword detected — refactor role targets structure-preserving CLIs"
		case strings.Contains(lower, "research") || strings.Contains(lower, "investigate") || strings.Contains(lower, "explore"):
			recommendedRole = "analyze"
			roleReason = "research/exploration keyword detected — analyze role activates Gemini's long-context reasoning"
		}
	}

	var sb strings.Builder
	if taskDesc != "" {
		sb.WriteString(fmt.Sprintf("## Background Task: %s\n\n", taskDesc))
		sb.WriteString("### Recommended Execution\n\n")
		sb.WriteString(fmt.Sprintf("```\nexec(prompt=%q, role=%q, async=true)\n```\n\n", taskDesc, recommendedRole))
		sb.WriteString("Then poll for completion:\n")
		sb.WriteString("```\nstatus(job_id=\"<from exec response>\")\n```\n\n")
		sb.WriteString(fmt.Sprintf("### Why `%s` role?\n%s.\n\n", recommendedRole, roleReason))
		sb.WriteString("### Role Reference\n")
	} else {
		sb.WriteString("## Background Execution Protocol\n\n")
		sb.WriteString("Use `exec` with `async=true` for any task that may take >30s.\n\n")
		sb.WriteString("```\nexec(prompt=\"<your task>\", role=\"<role>\", async=true)\n```\n\n")
		sb.WriteString("Poll with:\n```\nstatus(job_id=\"<from exec response>\")\n```\n\n")
		sb.WriteString("### Roles\n")
	}
	sb.WriteString("| Role | Best for |\n")
	sb.WriteString("|------|----------|\n")
	sb.WriteString("| coding | Implementation, new features, boilerplate |\n")
	sb.WriteString("| codereview | Code review, analysis, critique |\n")
	sb.WriteString("| debug | Bug tracing, crash analysis |\n")
	sb.WriteString("| secaudit | Security audit, OWASP, vulnerability review |\n")
	sb.WriteString("| analyze | Research, exploration, holistic analysis |\n")
	sb.WriteString("| refactor | Refactoring, cleanup, reorganization |\n")
	sb.WriteString("| testgen | Test generation, coverage improvement |\n")
	sb.WriteString("| planner | Planning, design, architecture decisions |\n")
	sb.WriteString("| thinkdeep | Deep analysis, difficult reasoning |\n")

	return mcp.NewGetPromptResult(
		"Background execution protocol",
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(
				mcp.RoleAssistant,
				mcp.NewTextContent(sb.String()),
			),
		},
	), nil
}

func (s *Server) handleGuidePrompt(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	// Fetch live data.
	enabledCLIs := s.registry.EnabledCLIs()
	sessionCount := s.sessions.Count()
	snap := s.metrics.Snapshot()
	thinkPatterns := think.GetAllPatterns()

	var sb strings.Builder

	sb.WriteString("# aimux — AI CLI Multiplexer\n\n")

	// Live status block.
	sb.WriteString("## Current Status\n\n")
	if len(enabledCLIs) == 0 {
		sb.WriteString("**Enabled CLIs:** none detected (run `go build` and probe CLIs)\n")
	} else {
		sb.WriteString(fmt.Sprintf("**Enabled CLIs (%d):** %s\n", len(enabledCLIs), strings.Join(enabledCLIs, ", ")))
	}
	sb.WriteString(fmt.Sprintf("**Active Sessions:** %d\n", sessionCount))
	sb.WriteString(fmt.Sprintf("**Total Requests:** %d | **Error Rate:** %.1f%%\n\n",
		snap.TotalRequests, snap.ErrorRate*100))

	// Tool selection table (static — tools don't change at runtime).
	sb.WriteString("## Tool Selection — \"I need to...\"\n\n")
	sb.WriteString("| I need to... | Use | Key params |\n")
	sb.WriteString("|---|---|---|\n")
	sb.WriteString("| Run a prompt on an AI CLI | exec | prompt, role, cli, async |\n")
	sb.WriteString("| Get consensus from multiple models | consensus | topic, synthesize |\n")
	sb.WriteString("| Have models debate a decision | debate | topic, max_turns |\n")
	sb.WriteString("| Multi-turn discussion between CLIs | dialog | prompt, max_turns |\n")
	sb.WriteString("| Structured reasoning/analysis | think | pattern (see below) |\n")
	sb.WriteString("| Deep investigation with tracking | investigate | action, topic, domain |\n")
	sb.WriteString("| Run a codebase audit | audit | cwd, mode (quick/standard/deep) |\n")
	sb.WriteString("| Execute a project agent | agent | agent (name), prompt |\n")
	sb.WriteString("| Chain multiple steps | workflow | steps (JSON), input |\n")
	sb.WriteString("| Check async job status | status | job_id |\n")
	sb.WriteString("| Manage sessions | sessions | action |\n")
	sb.WriteString("| Discover available agents | agents | action (list/find) |\n")
	sb.WriteString("| Deep research via Gemini | deepresearch | topic |\n\n")

	// Dynamic CLI table — only show what is actually enabled.
	sb.WriteString("## Your Available CLIs\n\n")
	if len(enabledCLIs) == 0 {
		sb.WriteString("No CLIs detected on PATH. Install at least one AI CLI (codex, claude, gemini, etc.).\n\n")
	} else {
		sb.WriteString("| CLI | Status |\n")
		sb.WriteString("|-----|--------|\n")
		for _, cli := range enabledCLIs {
			sb.WriteString(fmt.Sprintf("| %s | available |\n", cli))
		}
		sb.WriteString("\n")
	}

	// Roles (static mapping).
	sb.WriteString("## Roles (exec tool)\n")
	sb.WriteString("coding → codex (code generation, TDD)\n")
	sb.WriteString("codereview → gemini (code review, analysis)\n")
	sb.WriteString("debug → codex (debugging, tracing)\n")
	sb.WriteString("secaudit → codex (security audit, OWASP)\n")
	sb.WriteString("analyze → gemini (holistic analysis)\n")
	sb.WriteString("refactor → codex (refactoring)\n")
	sb.WriteString("testgen → codex (test generation)\n")
	sb.WriteString("docgen → codex (documentation)\n")
	sb.WriteString("planner → codex (planning, architecture)\n")
	sb.WriteString("thinkdeep → codex (deep analysis)\n\n")

	// Think patterns — live from registry.
	sb.WriteString(fmt.Sprintf("## Think Patterns (%d registered)\n\n", len(thinkPatterns)))
	sb.WriteString(strings.Join(thinkPatterns, ", "))
	sb.WriteString("\n\n")

	// Investigation flow (static — protocol doesn't change).
	sb.WriteString("## Investigation Flow\n")
	sb.WriteString("1. `investigate(action=\"start\", topic=\"...\", domain=\"auto\")` → session_id + coverage areas\n")
	sb.WriteString("2. `investigate(action=\"finding\", session_id, description, source, severity, confidence)` × N\n")
	sb.WriteString("3. `investigate(action=\"assess\", session_id)` → convergence, coverage, recommendation\n")
	sb.WriteString("4. `investigate(action=\"report\", session_id, cwd)` → saved markdown report\n")
	sb.WriteString("5. `investigate(action=\"recall\", topic=\"...\")` → find past reports\n\n")
	sb.WriteString("Domains: generic, debugging, security, performance, architecture, research\n\n")

	// Workflow example (static).
	sb.WriteString("## Workflow Example\n")
	sb.WriteString("```json\n{\"steps\": [\n")
	sb.WriteString("  {\"id\": \"analyze\", \"tool\": \"exec\", \"params\": {\"role\": \"analyze\", \"prompt\": \"{{input}}\"}},\n")
	sb.WriteString("  {\"id\": \"review\", \"tool\": \"think\", \"params\": {\"pattern\": \"peer_review\", \"artifact\": \"{{analyze.content}}\"}},\n")
	sb.WriteString("  {\"id\": \"fix\", \"tool\": \"exec\", \"params\": {\"role\": \"coding\", \"prompt\": \"Fix: {{review.content}}\"}, \"condition\": \"{{review.content}} contains 'revision'\"}\n")
	sb.WriteString("]}\n```\n\n")

	// Anti-patterns (static).
	sb.WriteString("## Anti-Patterns\n")
	sb.WriteString("- DON'T specify cli= when role= is enough — let routing pick the best CLI\n")
	sb.WriteString("- DON'T use sync exec for tasks >30s — use async=true\n")
	sb.WriteString("- DON'T skip investigate for complex bugs — jumping to fix wastes time\n")
	sb.WriteString("- DON'T call think without a pattern — every call needs pattern= param\n")
	sb.WriteString("- DON'T run consensus with 1 CLI — needs 2+ for meaningful comparison\n")

	return mcp.NewGetPromptResult(
		"aimux tool guide",
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(
				mcp.RoleAssistant,
				mcp.NewTextContent(sb.String()),
			),
		},
	), nil
}

func (s *Server) handleInvestigatePrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	topic := ""
	if args := request.Params.Arguments; args != nil {
		if t, exists := args["topic"]; exists && t != "" {
			topic = t
		}
	}

	// Without a topic, emit the generic protocol guide.
	if topic == "" {
		content := "# aimux Investigation Protocol\n\n" +
			"Provide a `topic` argument to get a concrete, ready-to-execute investigation plan.\n\n" +
			"## Flow\n" +
			"1. `investigate(action=\"start\", topic=\"...\", domain=\"auto\")` → session_id\n" +
			"2. `investigate(action=\"finding\", session_id=\"...\", description=\"...\", source=\"...\", severity=\"P0-P3\", confidence=\"VERIFIED\")` × N\n" +
			"3. `investigate(action=\"assess\", session_id=\"...\")` → convergence decision\n" +
			"4. `investigate(action=\"report\", session_id=\"...\", cwd=\"/project\")` → report file\n\n" +
			"**Domains (auto-detected):** generic, debugging, security, performance, architecture, research\n"
		return mcp.NewGetPromptResult(
			"Investigation protocol",
			[]mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(content))},
		), nil
	}

	// Auto-detect domain.
	domain := inv.AutoDetectDomain(topic)
	domainAlgo := inv.GetDomain(domain)

	// List related past reports.
	cwd, _ := os.Getwd()
	pastReports, _ := inv.ListReports(cwd)
	topicLower := strings.ToLower(topic)
	var relatedReports []string
	for _, r := range pastReports {
		if strings.Contains(strings.ToLower(r.Topic), topicLower) ||
			strings.Contains(strings.ToLower(r.Filename), strings.ReplaceAll(topicLower, " ", "-")) {
			relatedReports = append(relatedReports, fmt.Sprintf("- %s (%s, %d bytes)", r.Topic, r.Date, r.Size))
		}
		if len(relatedReports) >= 5 {
			break
		}
	}

	// Get think patterns for domain-specific recommendations.
	allPatterns := think.GetAllPatterns()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Investigation Plan: %s\n\n", topic))
	sb.WriteString(fmt.Sprintf("**Domain:** %s (auto-detected from topic keywords)\n", domain))
	sb.WriteString(fmt.Sprintf("**Description:** %s\n\n", domainAlgo.Description))

	sb.WriteString("**Coverage Areas:**\n")
	for _, area := range domainAlgo.CoverageAreas {
		sb.WriteString(fmt.Sprintf("- %s\n", area))
	}
	sb.WriteString("\n")

	if len(relatedReports) > 0 {
		sb.WriteString("**Related Past Reports:**\n")
		for _, r := range relatedReports {
			sb.WriteString(r + "\n")
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("**Related Past Reports:** none found\n\n")
	}

	sb.WriteString("### Execution Steps\n\n")
	sb.WriteString("**1. Start investigation:**\n")
	sb.WriteString(fmt.Sprintf("```\ninvestigate(action=\"start\", topic=%q, domain=%q)\n```\n\n", topic, domain))

	sb.WriteString("**2. Investigate each coverage area systematically:**\n\n")
	for _, area := range domainAlgo.CoverageAreas {
		method := domainAlgo.Methods[area]
		if method == "" {
			method = "Investigate this area thoroughly."
		}
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", area, method))
		sb.WriteString(fmt.Sprintf("  ```\n  investigate(action=\"finding\", session_id=\"<from step 1>\", description=\"<your finding>\", source=\"<file:line or tool output>\", severity=\"P0-P3\", confidence=\"VERIFIED\", coverage_area=%q)\n  ```\n\n", area))
	}

	sb.WriteString("**3. After 5+ findings, assess convergence:**\n")
	sb.WriteString("```\ninvestigate(action=\"assess\", session_id=\"<id>\")\n```\n")
	sb.WriteString("→ If `CONTINUE`: investigate more areas\n")
	sb.WriteString("→ If `COMPLETE`: generate report\n\n")

	sb.WriteString("**4. Generate report:**\n")
	sb.WriteString(fmt.Sprintf("```\ninvestigate(action=\"report\", session_id=\"<id>\", cwd=%q)\n```\n\n", cwd))

	sb.WriteString("### Domain-Specific Guidance\n\n")

	if len(domainAlgo.AntiPatterns) > 0 {
		sb.WriteString("**Anti-patterns to avoid:**\n")
		for _, ap := range domainAlgo.AntiPatterns {
			sb.WriteString(fmt.Sprintf("- %s\n", ap))
		}
		sb.WriteString("\n")
	}

	if len(domainAlgo.Patterns) > 0 {
		sb.WriteString("**Watch for these patterns:**\n")
		for _, p := range domainAlgo.Patterns {
			sb.WriteString(fmt.Sprintf("- [%s] %s → %s\n", p.Severity, p.Indicator, p.FixApproach))
		}
		sb.WriteString("\n")
	}

	// Domain angles → recommend think patterns.
	angles := domainAlgo.Angles
	if len(angles) == 0 {
		angles = inv.DefaultAngles
	}
	sb.WriteString("### Cross-Tool Enhancement\n\n")
	sb.WriteString("When assess suggests a think call, use one of these patterns:\n\n")
	for _, angle := range angles {
		// Only list if the pattern is actually registered.
		registered := false
		for _, p := range allPatterns {
			if p == angle.ThinkPattern {
				registered = true
				break
			}
		}
		if registered {
			sb.WriteString(fmt.Sprintf("- **%s** (`think(pattern=%q, ...)`): %s\n", angle.Label, angle.ThinkPattern, angle.Description))
		}
	}

	return mcp.NewGetPromptResult(
		fmt.Sprintf("Investigation plan: %s", topic),
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(
				mcp.RoleAssistant,
				mcp.NewTextContent(sb.String()),
			),
		},
	), nil
}

func (s *Server) handleWorkflowPrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	goal := ""
	if args := request.Params.Arguments; args != nil {
		if g, exists := args["goal"]; exists && g != "" {
			goal = g
		}
	}

	// Gather live data.
	thinkPatterns := think.GetAllPatterns()

	var sb strings.Builder

	if goal == "" {
		// No goal — emit generic reference.
		sb.WriteString("# aimux Workflow Builder\n\n")
		sb.WriteString("Provide a `goal` argument to get a ready-to-execute pipeline JSON.\n\n")
		sb.WriteString("## Step Schema\n")
		sb.WriteString("```json\n{\n  \"id\": \"step_name\",\n  \"tool\": \"exec|think|investigate|consensus|audit\",\n  \"params\": { ... },\n  \"condition\": \"{{prev.content}} contains 'keyword'\",\n  \"on_error\": \"stop|skip|retry\"\n}\n```\n\n")
		sb.WriteString(fmt.Sprintf("**exec roles:** coding, codereview, debug, secaudit, analyze, refactor, testgen, planner, thinkdeep\n"))
		sb.WriteString(fmt.Sprintf("**think patterns (%d):** %s\n\n", len(thinkPatterns), strings.Join(thinkPatterns, ", ")))
		sb.WriteString("**Template variables:** `{{input}}`, `{{step_id.content}}`, `{{step_id.status}}`\n")
		return mcp.NewGetPromptResult(
			"Workflow builder guide",
			[]mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(sb.String()))},
		), nil
	}

	// Analyze the goal to determine the best pipeline shape.
	lower := strings.ToLower(goal)

	type workflowStep struct {
		ID        string
		Tool      string
		Params    map[string]any
		Condition string
		OnError   string
	}

	var steps []workflowStep
	var rationale []string

	switch {
	case strings.Contains(lower, "security") || strings.Contains(lower, "audit") || strings.Contains(lower, "vuln"):
		steps = []workflowStep{
			{ID: "audit", Tool: "audit", Params: map[string]any{"cwd": "{{input}}", "mode": "standard"}},
			{ID: "synthesize", Tool: "think", Params: map[string]any{"pattern": "research_synthesis", "artifact": "{{audit.content}}"}},
			{ID: "fix_plan", Tool: "exec", Params: map[string]any{"role": "planner", "prompt": fmt.Sprintf("Create a prioritized fix plan for: %s. Audit findings: {{synthesize.content}}", goal)}},
		}
		rationale = []string{
			"Step 1 (audit): full codebase scan with OWASP-aware analysis",
			"Step 2 (think/research_synthesis): group and prioritize findings",
			"Step 3 (exec/planner): produce actionable remediation plan",
		}

	case strings.Contains(lower, "bug") || strings.Contains(lower, "debug") || strings.Contains(lower, "fix") || strings.Contains(lower, "crash"):
		steps = []workflowStep{
			{ID: "investigate", Tool: "exec", Params: map[string]any{"role": "debug", "prompt": fmt.Sprintf("Investigate and identify root cause: %s. Input context: {{input}}", goal)}},
			{ID: "analyze", Tool: "think", Params: map[string]any{"pattern": "debugging_approach", "problem": "{{investigate.content}}"}},
			{ID: "fix", Tool: "exec", Params: map[string]any{"role": "coding", "prompt": "Implement fix based on root cause analysis: {{analyze.content}}"}, Condition: "{{analyze.content}} contains 'root cause'"},
			{ID: "verify", Tool: "exec", Params: map[string]any{"role": "testgen", "prompt": "Write regression tests for the fix: {{fix.content}}"}, Condition: "{{fix.content}} contains 'fix'"},
		}
		rationale = []string{
			"Step 1 (exec/debug): deep root cause investigation",
			"Step 2 (think/debugging_approach): structured hypothesis and elimination",
			"Step 3 (exec/coding): implement fix only if root cause identified",
			"Step 4 (exec/testgen): regression tests to prevent recurrence",
		}

	case strings.Contains(lower, "review") || strings.Contains(lower, "quality") || strings.Contains(lower, "critique"):
		steps = []workflowStep{
			{ID: "analyze", Tool: "exec", Params: map[string]any{"role": "analyze", "prompt": fmt.Sprintf("%s — initial analysis: {{input}}", goal)}},
			{ID: "review", Tool: "think", Params: map[string]any{"pattern": "peer_review", "artifact": "{{analyze.content}}"}},
			{ID: "validate", Tool: "consensus", Params: map[string]any{"topic": "{{review.content}}", "synthesize": true}},
		}
		rationale = []string{
			"Step 1 (exec/analyze): holistic initial analysis",
			"Step 2 (think/peer_review): structured critique with objections",
			"Step 3 (consensus): multi-model validation of the review",
		}

	case strings.Contains(lower, "refactor") || strings.Contains(lower, "cleanup") || strings.Contains(lower, "reorganize"):
		steps = []workflowStep{
			{ID: "plan", Tool: "exec", Params: map[string]any{"role": "planner", "prompt": fmt.Sprintf("Design refactoring plan for: %s. Target: {{input}}", goal)}},
			{ID: "decompose", Tool: "think", Params: map[string]any{"pattern": "problem_decomposition", "problem": "{{plan.content}}"}},
			{ID: "implement", Tool: "exec", Params: map[string]any{"role": "refactor", "prompt": "Execute refactoring as planned: {{decompose.content}}"}, Condition: "{{decompose.content}} contains 'step'"},
		}
		rationale = []string{
			"Step 1 (exec/planner): design refactoring strategy before touching code",
			"Step 2 (think/problem_decomposition): break into safe atomic steps",
			"Step 3 (exec/refactor): execute only if steps were identified",
		}

	case strings.Contains(lower, "consensus") || strings.Contains(lower, "compare") || strings.Contains(lower, "evaluate") || strings.Contains(lower, "decide"):
		steps = []workflowStep{
			{ID: "propose", Tool: "exec", Params: map[string]any{"role": "planner", "prompt": fmt.Sprintf("Enumerate options for: %s", goal)}},
			{ID: "frame", Tool: "think", Params: map[string]any{"pattern": "decision_framework", "decision": goal, "options": "{{propose.content}}"}},
			{ID: "validate", Tool: "consensus", Params: map[string]any{"topic": "{{frame.content}}", "synthesize": true}},
		}
		rationale = []string{
			"Step 1 (exec/planner): generate candidate options",
			"Step 2 (think/decision_framework): structured tradeoff analysis",
			"Step 3 (consensus): multi-model vote + synthesis",
		}

	default:
		// Generic: analyze → review → implement.
		steps = []workflowStep{
			{ID: "analyze", Tool: "exec", Params: map[string]any{"role": "analyze", "prompt": fmt.Sprintf("%s — initial analysis. Input: {{input}}", goal)}},
			{ID: "review", Tool: "think", Params: map[string]any{"pattern": "peer_review", "artifact": "{{analyze.content}}"}},
			{ID: "implement", Tool: "exec", Params: map[string]any{"role": "coding", "prompt": fmt.Sprintf("Implement: %s. Based on analysis: {{review.content}}", goal)}, Condition: "{{review.content}} contains 'recommendation'"},
		}
		rationale = []string{
			"Step 1 (exec/analyze): understand scope and requirements",
			"Step 2 (think/peer_review): critique the analysis",
			"Step 3 (exec/coding): implement only if analysis yielded recommendations",
		}
	}

	sb.WriteString(fmt.Sprintf("## Workflow for: %s\n\n", goal))
	sb.WriteString("### Ready-to-Execute Pipeline\n\n")
	sb.WriteString("Copy this JSON into the workflow tool's `steps` parameter:\n\n")
	sb.WriteString("```json\n")

	// Marshal steps to clean JSON.
	type jsonStep struct {
		ID        string         `json:"id"`
		Tool      string         `json:"tool"`
		Params    map[string]any `json:"params"`
		Condition string         `json:"condition,omitempty"`
		OnError   string         `json:"on_error,omitempty"`
	}
	jsonSteps := make([]jsonStep, len(steps))
	for i, st := range steps {
		jsonSteps[i] = jsonStep{
			ID:        st.ID,
			Tool:      st.Tool,
			Params:    st.Params,
			Condition: st.Condition,
			OnError:   st.OnError,
		}
	}
	if b, err := json.MarshalIndent(jsonSteps, "", "  "); err == nil {
		sb.Write(b)
	}
	sb.WriteString("\n```\n\n")

	sb.WriteString("### Why this pipeline?\n")
	for _, r := range rationale {
		sb.WriteString(fmt.Sprintf("- %s\n", r))
	}
	sb.WriteString("\n")

	sb.WriteString("### Available Steps\n\n")
	sb.WriteString("**exec roles:** coding, codereview, debug, secaudit, analyze, refactor, testgen, planner, thinkdeep\n\n")
	sb.WriteString(fmt.Sprintf("**think patterns (%d):** %s\n\n", len(thinkPatterns), strings.Join(thinkPatterns, ", ")))
	sb.WriteString("**Conditions:** `{{step_id.content}} contains 'keyword'` or `{{step_id.status}} == 'completed'`\n\n")
	sb.WriteString("**Error handling:** `on_error: \"stop\"` (default) | `\"skip\"` | `\"retry\"`\n")

	return mcp.NewGetPromptResult(
		fmt.Sprintf("Workflow: %s", goal),
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(
				mcp.RoleAssistant,
				mcp.NewTextContent(sb.String()),
			),
		},
	), nil
}

func (s *Server) handleReviewPrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	scope := "staged"
	if args := request.Params.Arguments; args != nil {
		if v, exists := args["scope"]; exists && v != "" {
			scope = v
		}
	}

	// P1: Dynamic data injection.
	enabledCLIs := s.registry.EnabledCLIs()
	reviewCLI := "gemini"
	reviewPref, _ := s.router.Resolve("codereview")
	if reviewPref.CLI != "" {
		reviewCLI = reviewPref.CLI
	}
	codingCLI := "codex"
	codingPref, _ := s.router.Resolve("coding")
	if codingPref.CLI != "" {
		codingCLI = codingPref.CLI
	}

	var sb strings.Builder
	sb.WriteString("# Code Review Workflow\n\n")

	// Live status.
	sb.WriteString("## Live Status\n")
	sb.WriteString(fmt.Sprintf("- **Review CLI:** %s (role=codereview)\n", reviewCLI))
	sb.WriteString(fmt.Sprintf("- **Fix CLI:** %s (role=coding)\n", codingCLI))
	sb.WriteString(fmt.Sprintf("- **Available CLIs (%d):** %s\n", len(enabledCLIs), strings.Join(enabledCLIs, ", ")))
	sb.WriteString(fmt.Sprintf("- **Scope:** %s\n\n", scope))

	// P3: Scale-decision table.
	sb.WriteString("## Scale Decision\n\n")
	sb.WriteString("| Change Size | Approach |\n")
	sb.WriteString("|---|---|\n")
	sb.WriteString("| 1-3 files, <100 lines | Quick: single exec(role=codereview) |\n")
	sb.WriteString("| 4-10 files, 100-500 lines | Standard: exec review → think peer_review → fix |\n")
	sb.WriteString("| 10+ files or 500+ lines | Deep: consensus(codereview) → investigate → phased fix |\n\n")

	// P2: Hard-gate phased workflow.
	sb.WriteString("## Workflow (hard gates)\n\n")

	sb.WriteString("### Phase 1 — Gather diff (MANDATORY before any review)\n")
	sb.WriteString("Read the actual diff yourself using your file/git tools. Do NOT skip to review without reading changes.\n\n")
	switch scope {
	case "last-commit":
		sb.WriteString("```\nRun: git diff HEAD~1\n```\n\n")
	case "branch":
		sb.WriteString("```\nRun: git diff origin/HEAD...HEAD\n```\n\n")
	case "staged":
		sb.WriteString("```\nRun: git diff --cached\n```\n\n")
	default:
		sb.WriteString(fmt.Sprintf("```\nRun: git diff -- %s\n```\n\n", scope))
	}
	sb.WriteString("**GATE: Do NOT proceed to Phase 2 until you have read and understood every changed file.**\n\n")

	sb.WriteString("### Phase 2 — Structured review\n")
	sb.WriteString("```\nexec(role=\"codereview\", prompt=\"Review these changes for: security (input validation, auth, secrets), correctness (edge cases, nil, error handling), quality (naming, complexity, dead code). Diff context: <paste diff summary>\")\n```\n\n")
	sb.WriteString("**GATE: Do NOT proceed to Phase 3 until review findings are documented.**\n\n")

	sb.WriteString("### Phase 3 — Critical thinking\n")
	sb.WriteString("```\nthink(pattern=\"peer_review\", artifact=\"<review findings>\")\n```\n\n")

	sb.WriteString("### Phase 4 — Fix (only if issues found)\n")
	sb.WriteString("```\nexec(role=\"coding\", prompt=\"Fix review findings: <findings>\")\n```\n\n")

	// P4: Acceptance criteria.
	sb.WriteString("## Acceptance Criteria\n")
	sb.WriteString("- [ ] Every changed file read before review started\n")
	sb.WriteString("- [ ] Security: no hardcoded secrets, no unvalidated input\n")
	sb.WriteString("- [ ] Correctness: error paths handled, edge cases considered\n")
	sb.WriteString("- [ ] Quality: no dead code introduced, naming consistent\n")

	return mcp.NewGetPromptResult(
		fmt.Sprintf("Code review plan: %s", scope),
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(sb.String())),
		},
	), nil
}

func (s *Server) handleDebugPrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	errDesc := ""
	if args := request.Params.Arguments; args != nil {
		if v, exists := args["error"]; exists && v != "" {
			errDesc = v
		}
	}

	if errDesc == "" {
		content := "# aimux Debug Workflow\n\nProvide an `error` argument to get a concrete, ready-to-execute debug plan.\n\n" +
			"Example: `aimux-debug(error=\"panic: nil pointer dereference in handler.go:42\")`\n"
		return mcp.NewGetPromptResult(
			"Debug workflow guide",
			[]mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(content))},
		), nil
	}

	// P1: Dynamic data.
	debugCLI := "codex"
	debugPref, _ := s.router.Resolve("debug")
	if debugPref.CLI != "" {
		debugCLI = debugPref.CLI
	}
	domainAlgo := inv.GetDomain("debugging")

	// Check past debug reports.
	cwd, _ := os.Getwd()
	pastReports, _ := inv.ListReports(cwd)
	var relatedReports []string
	errLower := strings.ToLower(errDesc)
	for _, r := range pastReports {
		if strings.Contains(strings.ToLower(r.Topic), errLower) {
			relatedReports = append(relatedReports, fmt.Sprintf("- %s (%s)", r.Topic, r.Date))
		}
		if len(relatedReports) >= 3 {
			break
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Debug Plan: %s\n\n", errDesc))

	// Live status.
	sb.WriteString("## Live Status\n")
	sb.WriteString(fmt.Sprintf("- **Debug CLI:** %s (role=debug)\n", debugCLI))
	if len(relatedReports) > 0 {
		sb.WriteString("- **Related past investigations:**\n")
		for _, r := range relatedReports {
			sb.WriteString(fmt.Sprintf("  %s\n", r))
		}
	}
	sb.WriteString("\n")

	// P3: Scale-decision.
	sb.WriteString("## Scale Decision\n\n")
	sb.WriteString("| Error Type | Approach |\n")
	sb.WriteString("|---|---|\n")
	sb.WriteString("| Known error, clear stack trace | Quick: think(debugging_approach) → exec(debug) |\n")
	sb.WriteString("| Intermittent or multi-component | Standard: investigate(debugging) → think → fix |\n")
	sb.WriteString("| Systemic, architectural, unknown | Deep: investigate(debugging) → consensus → phased fix |\n\n")

	// P2: Hard-gate workflow.
	sb.WriteString("## Workflow (hard gates)\n\n")

	sb.WriteString("### Phase 1 — Reproduce & gather evidence (MANDATORY)\n")
	sb.WriteString("Read error logs, stack traces, and relevant source code. Reproduce the error if possible.\n")
	sb.WriteString("**PROHIBITED: Do NOT hypothesize causes before reading the actual error output and source code.**\n\n")

	sb.WriteString("### Phase 2 — Structured investigation\n")
	sb.WriteString(fmt.Sprintf("```\ninvestigate(action=\"start\", topic=%q, domain=\"debugging\")\n```\n", errDesc))
	sb.WriteString("For each hypothesis, add a finding with evidence:\n")
	sb.WriteString("```\ninvestigate(action=\"finding\", session_id=\"<id>\", description=\"<hypothesis + evidence>\", source=\"<file:line>\", severity=\"P0-P3\", confidence=\"VERIFIED\")\n```\n\n")
	sb.WriteString("**GATE: Do NOT proceed to Phase 3 until at least 3 findings are recorded with VERIFIED evidence.**\n\n")

	sb.WriteString("### Phase 3 — Root cause analysis\n")
	sb.WriteString(fmt.Sprintf("```\nthink(pattern=\"debugging_approach\", issue=%q)\n```\n\n", errDesc))

	sb.WriteString("### Phase 4 — Fix & verify\n")
	sb.WriteString(fmt.Sprintf("```\nexec(role=\"debug\", prompt=\"Fix root cause: <from phase 3>. Error: %s\")\n```\n", errDesc))
	sb.WriteString("**GATE: Verify fix by reproducing the original trigger. If error recurs, return to Phase 2.**\n\n")

	// Domain methods.
	if len(domainAlgo.Methods) > 0 {
		sb.WriteString("## Debugging Methods\n")
		for area, method := range domainAlgo.Methods {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", area, method))
		}
		sb.WriteString("\n")
	}

	// P4: Acceptance criteria.
	sb.WriteString("## Acceptance Criteria\n")
	sb.WriteString("- [ ] Error reproduced and understood before fix attempted\n")
	sb.WriteString("- [ ] Root cause identified (not just symptom suppressed)\n")
	sb.WriteString("- [ ] Fix verified by reproducing original trigger\n")
	sb.WriteString("- [ ] Regression test covers the failure mode\n")

	return mcp.NewGetPromptResult(
		fmt.Sprintf("Debug plan: %s", errDesc),
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(sb.String())),
		},
	), nil
}

func (s *Server) handleConsensusPrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	question := ""
	if args := request.Params.Arguments; args != nil {
		if v, exists := args["question"]; exists && v != "" {
			question = v
		}
	}

	// P1: Dynamic data.
	enabledCLIs := s.registry.EnabledCLIs()
	cliCount := len(enabledCLIs)

	if question == "" {
		var sb strings.Builder
		sb.WriteString("# aimux Multi-Model Consensus\n\n")
		sb.WriteString("Provide a `question` argument to get a ready-to-execute consensus or debate plan.\n\n")
		sb.WriteString(fmt.Sprintf("**Available CLIs (%d):** %s\n\n", cliCount, strings.Join(enabledCLIs, ", ")))
		if cliCount < 2 {
			sb.WriteString("**WARNING:** Consensus requires 2+ CLIs. Install additional AI CLIs to enable multi-model features.\n")
		}
		return mcp.NewGetPromptResult(
			"Consensus guide",
			[]mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(sb.String()))},
		), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Consensus Plan: %s\n\n", question))

	// Live status.
	sb.WriteString("## Live Status\n")
	sb.WriteString(fmt.Sprintf("- **Available CLIs (%d):** %s\n", cliCount, strings.Join(enabledCLIs, ", ")))
	if cliCount < 2 {
		sb.WriteString("- **WARNING:** Need 2+ CLIs for consensus. Only exec(role=...) is available.\n")
	}
	sb.WriteString("\n")

	// P3: Scale-decision.
	sb.WriteString("## Scale Decision\n\n")
	sb.WriteString("| Question Type | Tool | Why |\n")
	sb.WriteString("|---|---|---|\n")
	sb.WriteString("| Factual (\"what is best practice for X?\") | consensus | Aggregates knowledge, reduces hallucination |\n")
	sb.WriteString("| Binary choice (\"X or Y?\") | debate(max_turns=3) | Adversarial arguments surface hidden tradeoffs |\n")
	sb.WriteString("| Architecture (\"how should we design X?\") | debate(max_turns=5) | Deeper exploration, more turns for complex topics |\n")
	sb.WriteString("| Validation (\"is this approach correct?\") | consensus(synthesize=true) | Quick convergence check |\n\n")

	// Recommend based on question keywords.
	lower := strings.ToLower(question)
	recommended := "consensus"
	reason := "general question — consensus aggregates multiple perspectives"
	if strings.Contains(lower, " or ") || strings.Contains(lower, " vs ") || strings.Contains(lower, "should we") ||
		strings.Contains(lower, "choose") || strings.Contains(lower, "which") {
		recommended = "debate"
		reason = "decision/comparison detected — debate surfaces adversarial tradeoffs"
	}

	sb.WriteString("## Recommended Execution\n\n")
	sb.WriteString(fmt.Sprintf("Based on question analysis: **%s** (%s)\n\n", recommended, reason))

	if recommended == "consensus" {
		sb.WriteString(fmt.Sprintf("```\nconsensus(topic=%q, synthesize=true)\n```\n\n", question))
		sb.WriteString("Alternative (if consensus disagrees):\n")
		sb.WriteString(fmt.Sprintf("```\ndebate(topic=%q, max_turns=4)\n```\n", question))
	} else {
		sb.WriteString(fmt.Sprintf("```\ndebate(topic=%q, max_turns=4)\n```\n\n", question))
		sb.WriteString("Alternative (for quick validation):\n")
		sb.WriteString(fmt.Sprintf("```\nconsensus(topic=%q, synthesize=true)\n```\n", question))
	}

	sb.WriteString("\n## Acceptance Criteria\n")
	sb.WriteString("- [ ] Multiple models contributed (not just one CLI)\n")
	sb.WriteString("- [ ] Disagreements surfaced and addressed\n")
	sb.WriteString("- [ ] Final recommendation is actionable, not \"it depends\"\n")

	return mcp.NewGetPromptResult(
		fmt.Sprintf("Consensus plan: %s", question),
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(sb.String())),
		},
	), nil
}

func (s *Server) handleAuditPrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	cwd, _ := os.Getwd()
	if args := request.Params.Arguments; args != nil {
		if v, exists := args["cwd"]; exists && v != "" {
			cwd = v
		}
	}

	// P1: Dynamic data.
	enabledCLIs := s.registry.EnabledCLIs()
	snap := s.metrics.Snapshot()

	var sb strings.Builder
	sb.WriteString("# Codebase Audit Workflow\n\n")

	// Live status.
	sb.WriteString("## Live Status\n")
	sb.WriteString(fmt.Sprintf("- **Target:** %s\n", cwd))
	sb.WriteString(fmt.Sprintf("- **Available CLIs (%d):** %s\n", len(enabledCLIs), strings.Join(enabledCLIs, ", ")))
	sb.WriteString(fmt.Sprintf("- **Total requests so far:** %d\n\n", snap.TotalRequests))

	// P3: Scale-decision.
	sb.WriteString("## Scale Decision\n\n")
	sb.WriteString("| Project Size | Recommended Mode | Time |\n")
	sb.WriteString("|---|---|---|\n")
	sb.WriteString("| Small (<50 files) | quick | ~30s, sync |\n")
	sb.WriteString("| Medium (50-500 files) | standard | ~2min, async recommended |\n")
	sb.WriteString("| Large (500+ files) | deep | ~5min+, async required |\n\n")

	// P2: Hard-gate workflow.
	sb.WriteString("## Workflow (hard gates)\n\n")

	sb.WriteString("### Phase 1 — Run audit\n")
	sb.WriteString(fmt.Sprintf("```\naudit(cwd=%q, mode=\"standard\")\n```\n", cwd))
	sb.WriteString("For large projects, use async:\n")
	sb.WriteString(fmt.Sprintf("```\naudit(cwd=%q, mode=\"deep\", async=true)\n```\n", cwd))
	sb.WriteString("Then poll: `status(job_id=\"<from response>\")`\n\n")
	sb.WriteString("**GATE: Do NOT interpret findings until audit completes. Partial results mislead.**\n\n")

	sb.WriteString("### Phase 2 — Triage findings\n")
	sb.WriteString("Read the audit output. Classify each finding:\n")
	sb.WriteString("- **P0 (critical):** security vulnerabilities, data loss risks\n")
	sb.WriteString("- **P1 (high):** correctness bugs, missing error handling\n")
	sb.WriteString("- **P2 (medium):** code quality, maintainability\n")
	sb.WriteString("- **P3 (low):** style, naming, minor cleanup\n\n")
	sb.WriteString("**GATE: Do NOT start fixing until all findings are triaged and prioritized.**\n\n")

	sb.WriteString("### Phase 3 — Investigate (if P0/P1 found)\n")
	sb.WriteString(fmt.Sprintf("```\ninvestigate(action=\"start\", topic=\"audit findings for %s\", domain=\"security\")\n```\n", cwd))
	sb.WriteString("Add each P0/P1 finding as an investigation finding for structured tracking.\n\n")

	sb.WriteString("### Phase 4 — Fix by priority\n")
	sb.WriteString("Fix P0 first, then P1. Use exec(role=\"coding\") for each fix batch.\n\n")

	// P4: Acceptance criteria.
	sb.WriteString("## Acceptance Criteria\n")
	sb.WriteString("- [ ] All P0 findings resolved\n")
	sb.WriteString("- [ ] All P1 findings resolved or documented as deferred\n")
	sb.WriteString("- [ ] Fixes verified (tests pass, no regressions)\n")
	sb.WriteString("- [ ] P2/P3 tracked in backlog if not fixed\n")

	return mcp.NewGetPromptResult(
		fmt.Sprintf("Audit plan: %s", cwd),
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(sb.String())),
		},
	), nil
}

func (s *Server) handleAgentExecPrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	task := ""
	if args := request.Params.Arguments; args != nil {
		if v, exists := args["task"]; exists && v != "" {
			task = v
		}
	}

	// P1: Dynamic data.
	allAgents := s.agentReg.List()
	enabledCLIs := s.registry.EnabledCLIs()

	if task == "" {
		var sb strings.Builder
		sb.WriteString("# aimux Agent Execution\n\n")
		sb.WriteString("Provide a `task` argument to auto-match the best agent for your task.\n\n")
		sb.WriteString(fmt.Sprintf("**Available CLIs (%d):** %s\n", len(enabledCLIs), strings.Join(enabledCLIs, ", ")))
		if len(allAgents) > 0 {
			sb.WriteString(fmt.Sprintf("**Discovered Agents (%d):**\n", len(allAgents)))
			for _, a := range allAgents {
				desc := a.Description
				if desc == "" {
					desc = "(no description)"
				}
				role := a.Role
				if role == "" {
					role = "default"
				}
				sb.WriteString(fmt.Sprintf("- **%s** [role=%s]: %s\n", a.Name, role, desc))
			}
		} else {
			sb.WriteString("**Agents:** none discovered. Add AGENTS.md or .claude/agents/*.md files.\n")
		}
		return mcp.NewGetPromptResult(
			"Agent execution guide",
			[]mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(sb.String()))},
		), nil
	}

	// Score each agent by keyword overlap with the task.
	taskLower := strings.ToLower(task)
	taskWords := strings.Fields(taskLower)
	type scored struct {
		agent *agents.Agent
		score int
	}
	var scored_ []scored
	for _, a := range allAgents {
		corpus := strings.ToLower(a.Description + " " + a.Name + " " + a.Role)
		score := 0
		for _, word := range taskWords {
			if len(word) > 2 && strings.Contains(corpus, word) {
				score++
			}
		}
		if score > 0 {
			scored_ = append(scored_, scored{agent: a, score: score})
		}
	}
	// Sort by score descending (simple insertion — small N).
	for i := 1; i < len(scored_); i++ {
		for j := i; j > 0 && scored_[j].score > scored_[j-1].score; j-- {
			scored_[j], scored_[j-1] = scored_[j-1], scored_[j]
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Agent Execution Plan: %s\n\n", task))

	// Live status.
	sb.WriteString("## Live Status\n")
	sb.WriteString(fmt.Sprintf("- **Available CLIs (%d):** %s\n", len(enabledCLIs), strings.Join(enabledCLIs, ", ")))
	sb.WriteString(fmt.Sprintf("- **Discovered Agents:** %d\n\n", len(allAgents)))

	// P2: Hard gate — agent first, exec as fallback.
	sb.WriteString("## Execution Priority (hard gate)\n\n")
	sb.WriteString("**RULE: Always try agent tool FIRST. exec is the fallback, not the default.**\n\n")

	if len(scored_) > 0 {
		sb.WriteString("### Matched Agents (by keyword relevance)\n\n")
		for i, entry := range scored_ {
			if i >= 3 {
				break
			}
			sb.WriteString(fmt.Sprintf("%d. **%s** (score=%d, role=%s): %s\n", i+1, entry.agent.Name, entry.score, entry.agent.Role, entry.agent.Description))
		}
		sb.WriteString("\n")

		best := scored_[0].agent
		sb.WriteString("### Recommended Execution\n")
		sb.WriteString(fmt.Sprintf("```\nagent(agent=%q, prompt=%q)\n```\n\n", best.Name, task))
	} else if len(allAgents) > 0 {
		sb.WriteString("### No keyword match found\n")
		sb.WriteString("Available agents:\n")
		for _, a := range allAgents {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", a.Name, a.Description))
		}
		sb.WriteString("\n")
	}

	// Fallback.
	sb.WriteString("### Fallback: Direct CLI\n")
	sb.WriteString("Only if no agent matches the task:\n")
	role := "coding"
	lower := strings.ToLower(task)
	switch {
	case strings.Contains(lower, "review") || strings.Contains(lower, "audit"):
		role = "codereview"
	case strings.Contains(lower, "debug") || strings.Contains(lower, "fix") || strings.Contains(lower, "bug"):
		role = "debug"
	case strings.Contains(lower, "test"):
		role = "testgen"
	case strings.Contains(lower, "plan") || strings.Contains(lower, "design"):
		role = "planner"
	case strings.Contains(lower, "research") || strings.Contains(lower, "analyze"):
		role = "analyze"
	}
	sb.WriteString(fmt.Sprintf("```\nexec(role=%q, prompt=%q, async=true)\n```\n\n", role, task))

	sb.WriteString("## Acceptance Criteria\n")
	sb.WriteString("- [ ] Agent tool attempted before falling back to exec\n")
	sb.WriteString("- [ ] Task completed with verifiable output (not just \"done\")\n")
	sb.WriteString("- [ ] async=true used for tasks >30s\n")

	return mcp.NewGetPromptResult(
		fmt.Sprintf("Agent plan: %s", task),
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(sb.String())),
		},
	), nil
}

func (s *Server) handleResearchPrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	topic := ""
	if args := request.Params.Arguments; args != nil {
		if v, exists := args["topic"]; exists && v != "" {
			topic = v
		}
	}

	// P1: Dynamic data.
	thinkPatterns := think.GetAllPatterns()
	enabledCLIs := s.registry.EnabledCLIs()
	hasDeepResearch := false
	for _, cli := range enabledCLIs {
		if cli == "gemini" {
			hasDeepResearch = true
			break
		}
	}

	if topic == "" {
		var sb strings.Builder
		sb.WriteString("# aimux Research Workflow\n\n")
		sb.WriteString("Provide a `topic` argument to get a multi-phase research plan.\n\n")
		sb.WriteString(fmt.Sprintf("**Research think patterns:** literature_review, source_comparison, peer_review, replication_analysis, experimental_loop, research_synthesis\n"))
		sb.WriteString(fmt.Sprintf("**All think patterns (%d):** %s\n", len(thinkPatterns), strings.Join(thinkPatterns, ", ")))
		if hasDeepResearch {
			sb.WriteString("**Deep research:** available via `deepresearch(topic=\"...\")` (Gemini)\n")
		}
		return mcp.NewGetPromptResult(
			"Research workflow guide",
			[]mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(sb.String()))},
		), nil
	}

	// Check past reports.
	cwd, _ := os.Getwd()
	pastReports, _ := inv.ListReports(cwd)
	topicLower := strings.ToLower(topic)
	var relatedReports []string
	for _, r := range pastReports {
		if strings.Contains(strings.ToLower(r.Topic), topicLower) ||
			strings.Contains(strings.ToLower(r.Filename), strings.ReplaceAll(topicLower, " ", "-")) {
			relatedReports = append(relatedReports, fmt.Sprintf("- %s (%s, %d bytes)", r.Topic, r.Date, r.Size))
		}
		if len(relatedReports) >= 5 {
			break
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Research Plan: %s\n\n", topic))

	// Live status.
	sb.WriteString("## Live Status\n")
	sb.WriteString(fmt.Sprintf("- **Available CLIs (%d):** %s\n", len(enabledCLIs), strings.Join(enabledCLIs, ", ")))
	if hasDeepResearch {
		sb.WriteString("- **Deep research (Gemini):** available\n")
	}
	if len(relatedReports) > 0 {
		sb.WriteString("- **Related past research:**\n")
		for _, r := range relatedReports {
			sb.WriteString(fmt.Sprintf("  %s\n", r))
		}
	}
	sb.WriteString("\n")

	// P3: Scale-decision.
	sb.WriteString("## Scale Decision\n\n")
	sb.WriteString("| Research Depth | Approach |\n")
	sb.WriteString("|---|---|\n")
	sb.WriteString("| Quick lookup (known topic) | think(literature_review) → done |\n")
	sb.WriteString("| Standard (compare approaches) | literature_review → source_comparison → synthesis |\n")
	sb.WriteString("| Deep (novel question, multi-source) | Full 4-phase pipeline + deepresearch + investigation |\n\n")

	// P2: Hard-gate phased workflow.
	sb.WriteString("## Workflow (hard gates)\n\n")

	sb.WriteString("### Phase 1 — Literature Review (MANDATORY)\n")
	sb.WriteString(fmt.Sprintf("```\nthink(pattern=\"literature_review\", topic=%q)\n```\n", topic))
	sb.WriteString("**GATE: Do NOT proceed to comparison until you have surveyed existing knowledge.**\n")
	if len(relatedReports) > 0 {
		sb.WriteString("**NOTE:** Past research exists (see above). Review it before duplicating effort.\n")
	}
	sb.WriteString("\n")

	sb.WriteString("### Phase 2 — Source Comparison\n")
	sb.WriteString(fmt.Sprintf("```\nthink(pattern=\"source_comparison\", topic=%q, sources=[\"<source1>\", \"<source2>\"])\n```\n", topic))
	sb.WriteString("**GATE: Do NOT proceed to peer review until sources are compared side by side.**\n\n")

	sb.WriteString("### Phase 3 — Adversarial Review\n")
	sb.WriteString(fmt.Sprintf("```\nthink(pattern=\"peer_review\", artifact=\"Research findings on %s\")\n```\n", topic))
	sb.WriteString("Challenge: What evidence would contradict these findings? What biases might exist?\n\n")

	sb.WriteString("### Phase 4 — Synthesis\n")
	sb.WriteString(fmt.Sprintf("```\nthink(pattern=\"research_synthesis\", topic=%q, findings=[\"<phase1>\", \"<phase2>\", \"<phase3>\"])\n```\n\n", topic))

	if hasDeepResearch {
		sb.WriteString("### Optional: Deep Research (Gemini)\n")
		sb.WriteString("For complex topics requiring web-scale knowledge:\n")
		sb.WriteString(fmt.Sprintf("```\ndeepresearch(topic=%q)\n```\n", topic))
		sb.WriteString("Feed results into Phase 4 synthesis.\n\n")
	}

	// P5: Output protocol.
	sb.WriteString("### As Automated Pipeline\n")
	sb.WriteString("```\n")
	sb.WriteString("workflow(steps=[\n")
	sb.WriteString(fmt.Sprintf("  {\"id\": \"lit\", \"tool\": \"think\", \"params\": {\"pattern\": \"literature_review\", \"topic\": %q}},\n", topic))
	sb.WriteString(fmt.Sprintf("  {\"id\": \"compare\", \"tool\": \"think\", \"params\": {\"pattern\": \"source_comparison\", \"topic\": %q, \"sources\": [\"{{lit.content}}\"]}},\n", topic))
	sb.WriteString("  {\"id\": \"review\", \"tool\": \"think\", \"params\": {\"pattern\": \"peer_review\", \"artifact\": \"{{compare.content}}\"}},\n")
	sb.WriteString(fmt.Sprintf("  {\"id\": \"synth\", \"tool\": \"think\", \"params\": {\"pattern\": \"research_synthesis\", \"topic\": %q, \"findings\": [\"{{lit.content}}\", \"{{compare.content}}\", \"{{review.content}}\"]}}\n", topic))
	sb.WriteString(fmt.Sprintf("], input=%q)\n", topic))
	sb.WriteString("```\n\n")

	// P4: Acceptance criteria.
	sb.WriteString("## Acceptance Criteria\n")
	sb.WriteString("- [ ] Multiple independent sources consulted (not just one)\n")
	sb.WriteString("- [ ] Contradictory evidence actively sought\n")
	sb.WriteString("- [ ] Claims classified: VERIFIED (tool output) / INFERRED / STALE (model memory)\n")
	sb.WriteString("- [ ] Synthesis actionable: concrete recommendations, not just summaries\n")

	return mcp.NewGetPromptResult(
		fmt.Sprintf("Research plan: %s", topic),
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(sb.String())),
		},
	), nil
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
		go func() {
			s.jobs.StartJob(job.ID, 0)
			result, stratErr := s.orchestrator.Execute(context.Background(), "workflow", params)
			if stratErr != nil {
				s.jobs.FailJob(job.ID, types.NewExecutorError(stratErr.Error(), stratErr, ""))
				return
			}
			s.jobs.CompleteJob(job.ID, result.Content, 0)
		}()
		data, _ := json.Marshal(map[string]any{"job_id": job.ID, "status": "running"})
		return mcp.NewToolResultText(string(data)), nil
	}

	result, err := s.orchestrator.Execute(ctx, "workflow", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("workflow failed: %v", err)), nil
	}
	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
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
