// Package server implements the MCP server using mcp-go SDK.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/thebtf/aimux/loom"
	loomworkers "github.com/thebtf/aimux/pkg/aimuxworkers"
	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/build"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/executor"
	pipeExec "github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/guidance"
	"github.com/thebtf/aimux/pkg/guidance/policies"
	"github.com/thebtf/aimux/pkg/hooks"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/metrics"
	"github.com/thebtf/aimux/pkg/prompt"
	"github.com/thebtf/aimux/pkg/ratelimit"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/server/budget"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/skills"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
	"github.com/thebtf/aimux/pkg/tools/deepresearch"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/updater"
	"github.com/thebtf/aimux/pkg/upgrade"
	"github.com/thebtf/mcp-mux/muxcore"
	"github.com/thebtf/mcp-mux/muxcore/engine"
)

// Version is the canonical aimux version string. Used in MCP serverInfo handshake,
// transport log lines, status tool, and updater checks. Single source of truth —
// cmd/aimux/main.go references this value directly to keep log lines and MCP
// handshake consistent across binary and transport layers.
// The actual string lives in pkg/build so thin binaries (shim mode) can import
// it without pulling in the full daemon dependency graph.
var Version = build.Version

// legacyInstructions is kept as fallback for proxy/shim mode where live state is unavailable.
const legacyInstructions = `aimux — AI CLI Multiplexer (4 MCP tools + 23 think patterns, post Layer 5 purge)

Reduced MCP surface: server state management, deep research via Gemini SDK,
and structured reasoning via 23 dedicated think pattern tools.

## Tool Selection — "I need to..."

| I need to... | Tool | Key params |
|---|---|---|
| Check async job status | status | job_id |
| Manage sessions | sessions | action (list/health/gc/cancel/kill/info/refresh-warmup) |
| Structured reasoning / analysis | <pattern_name> | 23 individual think pattern tools |
| Deep research via Gemini API | deepresearch | topic |
| Check / apply binary updates | upgrade | action (check/apply) |

## Notes
- Layer 5 CLI-launching tools (exec/agent/agents/critique/investigate/consensus/debate/dialog/audit/workflow) were removed at v5.0.3.
- Pipeline v5 packages (workflow, dialogue, swarm, executor, resolve, driver, routing, loom) remain in-repo as dormant seams pending the next Layer 5 design.
- Skill prompts archived under archive/v5.0.3/skills.d/ — prompts/list returns the reduced legacy set.

## Anti-Patterns
- Don't expect exec/agent/workflow tools — they were removed in the Layer 5 purge
- Don't assume dormant pipeline packages are reachable from MCP — they are isolated until rewired`

// Server holds all dependencies for the MCP server.
type Server struct {
	cfg                     *config.Config
	log                     *logger.Logger
	registry                *driver.Registry
	router                  *routing.Router
	sessions                *session.Registry
	jobs                    *session.JobManager
	breakers                *executor.BreakerRegistry
	mcp                     *server.MCPServer
	promptEng               *prompt.Engine
	hooks                   *hooks.Registry
	metrics                 *metrics.Collector
	store                   *session.Store
	gcCancel                context.CancelFunc
	skillEngine             *skills.Engine
	rateLimiter             *ratelimit.Limiter
	authToken               string
	engineName              string // canonical engine name for loom task scoping (AIMUX-10 FR-7)
	guidanceReg             *guidance.Registry
	cooldownTracker         *executor.ModelCooldownTracker
	sessionHandler          muxcore.SessionHandler // stored for upgrade tool routing
	applyUpgrade            func(context.Context, *upgrade.Coordinator, upgrade.Mode, bool) (*upgrade.Result, error)
	muxEngine               *engine.MuxEngine
	daemonControlSocketPath string           // live engine daemon control socket path for upgrade restart seam
	loom                    *loom.LoomEngine // central task mediator (LoomEngine v3)

	// dispatchMW resolves TenantContext at the MCP tool dispatch entry point
	// and emits audit events (AIMUX-12 Phase 5, T031).
	dispatchMW *DispatchMiddleware

	// logIngester receives forwarded log entries from shim peers via the
	// "notifications/aimux/log_forward" JSON-RPC notification path (AIMUX-11 Phase 2).
	logIngester *LogIngester

	// logPartitioner routes forwarded log entries to per-tenant log files when
	// muxcore provides SessionMeta with a non-empty TenantID (AIMUX-12 Phase 6, T039).
	// Nil in single-tenant / legacy mode — HandleNotification falls back to the shared log.
	logPartitioner logger.LogPartitionerWriter

	// Two-phase daemon init (issue #129): Phase A starts listening immediately via
	// lightweightDelegate; Phase B (heavy init) runs in the background goroutine started
	// by RunPhaseB. Fields below track progress for observability (status tool, T005).
	initPhase           atomic.Int32  // 0=pre-init, 1=phase-A, 2=phase-B complete
	initDurationMs      atomic.Int64  // wall-clock ms for Phase B to complete (set on swap)
	warmupDeferredCount atomic.Uint64 // HandleRequest calls deferred during Phase A
}

// deprecationOnce ensures the New deprecation warning fires at most once per process.
var deprecationOnce sync.Once

// NewDaemon creates a fully-initialised daemon-mode Server. This is the ONLY
// constructor that performs heavy init (SQLite open, migrate, reconcile,
// LoomEngine, skill engine, periodic snapshot loop).
//
// Callers MUST invoke NewDaemon only after detectMode() has confirmed daemon
// mode. Calling it from shim or legacy-proxy context will corrupt daemon
// persistent state (spec FR-3, NFR-3, architecture doc §2.3 anti-pattern).
func NewDaemon(cfg *config.Config, log *logger.Logger, reg *driver.Registry, router *routing.Router) *Server {
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
		cooldownTracker: executor.NewModelCooldownTracker(),
	}
	s.metrics = metrics.New()
	s.engineName = ResolveEngineName()

	// Wire the log ingester — daemon-side receiver for shim log_forward notifications
	// (AIMUX-11 Phase 2, T011). LocalSink() is non-nil in daemon mode.
	if sink := log.LocalSink(); sink != nil {
		s.logIngester = NewLogIngester(sink, cfg.Server.LogMaxLineBytes)
	}

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
			taskStore, taskStoreErr := loom.NewTaskStore(store.DB(), s.engineName)
			if taskStoreErr != nil {
				log.Warn("LoomEngine unavailable: %v", taskStoreErr)
			} else {
				s.loom = loom.New(taskStore)
				log.Info("LoomEngine initialized (shared SQLite)")
				log.Info("loom task scoping: engine_name=%s", s.engineName)
			}

		}
	}

	// Initialize DispatchMiddleware — wires tenant context resolution into every
	// MCP tool dispatch (AIMUX-12 Phase 5, T031). The middleware is always
	// constructed; in legacy-default mode (no tenants.yaml) it is a no-op pass-through.
	tenantReg := tenant.NewRegistry()
	auditLogPath := filepath.Join(filepath.Dir(config.ExpandPath(cfg.Server.DBPath)), ".aimux", "audit.log")
	if cfg.Server.DBPath == "" {
		// Fallback when db_path not configured (e.g. memory-only mode):
		// use a temp-dir-relative path that will not persist across restarts.
		auditLogPath = filepath.Join(os.TempDir(), "aimux-audit.log")
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(auditLogPath), 0700); mkdirErr != nil {
		log.Warn("audit: could not create audit log directory: %v (audit events will be discarded)", mkdirErr)
		s.dispatchMW = NewDispatchMiddleware(tenantReg, &discardAuditLog{})
	} else {
		fileAudit, auditErr := audit.NewFileAuditLog(auditLogPath)
		if auditErr != nil {
			log.Warn("audit: could not open audit log %s: %v (audit events will be discarded)", auditLogPath, auditErr)
			s.dispatchMW = NewDispatchMiddleware(tenantReg, &discardAuditLog{})
		} else {
			s.dispatchMW = NewDispatchMiddleware(tenantReg, fileAudit)
			log.Info("audit log: %s", auditLogPath)
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

	// Register LoomEngine workers for the reduced surface.
	if s.loom != nil {
		s.loom.RegisterWorker(loom.WorkerTypeThinker, loomworkers.NewThinkerWorker())

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
	// Only the think policy is wired into a live MCP tool. Consensus/debate/dialog/workflow
	// policies were Layer 5 dispatch-time guidance for tools removed in the v5.0.3 purge.
	// They remain as dormant Pipeline v5 seams in pkg/guidance/policies/ but are not
	// registered with the runtime registry.
	if err := s.guidanceReg.Register(policies.NewThinkPolicy()); err != nil {
		log.Warn("guidance: failed to register think policy: %v", err)
	}

	// Initialize hooks registry with built-in telemetry
	s.hooks = hooks.NewRegistry()
	s.hooks.RegisterAfter("builtin:telemetry", hooks.NewTelemetryHook())

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
		// Build live instructions at daemon construction time after agent and CLI discovery.
		// warmupComplete is false here because RunWarmup executes in a background goroutine
		// (see cmd/aimux/main.go) and has not finished by the time NewDaemon returns.
		// Clients will see "warmup in progress" for all profiles until a refresh-warmup
		// action is triggered, which is the accurate initial state.
		server.WithInstructions(buildInstructions(
			s.registry.EnabledCLIs(),
			false, // warmup runs in background — not yet complete at construction time
			s.registry.AllCLIs(),
			0,
			buildRoleMap(s.router),
		)),
	)

	// Enable sampling capability — allows think patterns to request LLM calls from the client.
	s.mcp.EnableSampling()

	s.registerTools()
	s.registerResources()
	s.registerPrompts()
	s.registerSkillPrompts()

	return s
}

// New creates a new MCP server with all dependencies wired.
// Deprecated: use NewDaemon for daemon-mode construction. Callers outside
// cmd/aimux/main.go (including tests) may continue to use New until a
// follow-up PR migrates them.
func New(cfg *config.Config, log *logger.Logger, reg *driver.Registry, router *routing.Router) *Server {
	deprecationOnce.Do(func() {
		log.Warn("aimuxServer.New is deprecated; use NewDaemon. See AIMUX-6 spec.")
	})
	return NewDaemon(cfg, log, reg, router)
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

// Tool returns the registered MCP tool definition for the named tool.
// Returns nil if the tool is not found.
// Used by tests to verify schema wiring on registered tools.
func (s *Server) Tool(name string) *mcp.Tool {
	if s == nil || s.mcp == nil {
		return nil
	}
	st := s.mcp.GetTool(name)
	if st == nil {
		return nil
	}
	return &st.Tool
}

// ToolDescription returns the description string that was registered for the named MCP tool.
// Returns an empty string if the tool is not found.
// Used by tests to verify that registered descriptions contain required structured sections.
func (s *Server) ToolDescription(name string) string {
	tool := s.Tool(name)
	if tool == nil {
		return ""
	}
	return tool.Description
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

	// Shutdown Swarm — closes all managed ExecutorV2 handles.
}

// RunPhaseB transitions the daemon from Phase A (lightweightDelegate) to Phase B
// (fullDelegate). It must be called exactly once after SessionHandler() returns.
//
// If async_init is false, RunPhaseB blocks until the delegate swap completes.
// If async_init is nil or true (the default), the swap runs in a background goroutine
// and RunPhaseB returns immediately.
//
// The method is a no-op when s.sessionHandler is not an *aimuxHandler (e.g. proxy /
// stdio / SSE / HTTP transport paths that do not use the delegate machinery).
func (s *Server) RunPhaseB(ctx context.Context) {
	h, ok := s.sessionHandler.(*aimuxHandler)
	if !ok || h == nil {
		// Not in engine mode — no delegate swap needed.
		return
	}

	s.initPhase.Store(1) // Phase A active: lightweightDelegate is live
	startedAt := time.Now()

	doSwap := func() {
		// Gauge writes (initDurationMs, initPhase=2) happen inside swapDelegateToFull
		// so any future caller automatically gets correct observability (ADR-001).
		s.swapDelegateToFull(h, startedAt)
		if s.log != nil {
			s.log.Info("phase-B complete: delegate swapped in %dms", s.initDurationMs.Load())
		}
	}

	asyncInit := s.cfg.Server.AsyncInit
	if asyncInit != nil && !*asyncInit {
		// Synchronous fallback (async_init: false in config).
		doSwap()
		return
	}
	// Default: swap in a background goroutine so RunPhaseB returns immediately
	// and the engine can begin serving requests via the lightweightDelegate.
	go doSwap()
}

// --- Tool Registration ---

func (s *Server) registerTools() {
	// status tool
	s.mcp.AddTool(
		mcp.NewTool("status",
			mcp.WithDescription("[manage — server state, no cost] Check async job status. "+
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
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(true),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(true),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			}),
		),
		s.handleStatus,
	)

	// sessions tool
	s.mcp.AddTool(
		mcp.NewTool("sessions",
			mcp.WithDescription("[manage — server state, no cost] Manage sessions and jobs: list, info, health, cancel, gc, refresh-warmup. "+
				"action=list returns dual-source brief rows (sessions + loom_tasks) — default fits ~4k chars. "+
				"Use sessions_limit/sessions_offset and loom_limit/loom_offset for independent pagination per source; "+
				"legacy limit/offset applies to both sources as a fallback. "+
				"action=info: per-job rows include content_length; add include_content=true to fetch job content. "+
				"action=refresh-warmup re-runs CLI warmup probes and updates the routing pool. "+
				"Session status=aborted indicates the daemon restarted while this session had running jobs (SIGKILL/crash recovery). "+
				"aborted_job_ids lists the job IDs that were aborted during that restart reconciliation. "+
				"last_seen_at on job rows tracks the most recent SnapshotJob write; used by ReconcileOnStartup to identify orphaned jobs. "+
				"Pass all=true to sessions(action=\"list\") to return a cross-engine global view (all daemons' tasks)."),
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
			mcp.WithBoolean("all",
				mcp.Description("Return cross-engine global view (default false: scoped to current daemon's engine)"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(false),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(false),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			}),
		),
		s.handleSessions,
	)

	// Pattern tools: 23 individual MCP tools, one per think pattern.
	// Replaces the single "think" tool with per-pattern tools.
	s.registerPatternTools()

	// deepresearch tool
	s.mcp.AddTool(
		mcp.NewTool("deepresearch",
			mcp.WithDescription("[delegate — external CLI, free for you] Deep research via Google Gemini API with file attachments and caching. "+
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
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(false),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(false),
				OpenWorldHint:   mcp.ToBoolPtr(true),
			}),
		),
		s.handleDeepresearch,
	)

	// upgrade tool — binary self-update from GitHub releases or local binary
	s.mcp.AddTool(
		mcp.NewTool("upgrade",
			mcp.WithDescription("[manage — server state, no cost] Check for and apply aimux binary updates from GitHub releases. "+
				"action=check: detect latest version. action=apply: download, verify checksum, replace binary. "+
				"After apply, daemon will exit when all CC sessions disconnect (deferred restart). "+
				"action=check returns compact status fields (fits ~4k chars); release_notes are omitted by default (release_notes_length is reported). "+
				"Use include_content=true to return the full release_notes body. "+
				"action=apply with force=true re-runs the full upgrade pipeline even when already up-to-date. "+
				"action=apply with source=<path> installs a local binary instead of downloading from GitHub (for dev iteration)."),
			mcp.WithString("action",
				mcp.Required(),
				mcp.Description("Action: check (detect latest version) or apply (download and replace binary)"),
				mcp.Enum("check", "apply"),
			),
			mcp.WithString("mode",
				mcp.Description("action=apply: upgrade mode (default auto). auto tries hot-swap then falls back to deferred, hot_swap requires live handoff, deferred skips hot-swap."),
				mcp.Enum(string(upgrade.ModeAuto), string(upgrade.ModeHotSwap), string(upgrade.ModeDeferred)),
				mcp.DefaultString(string(upgrade.ModeAuto)),
			),
			mcp.WithBoolean("include_content",
				mcp.Description("action=check: return full release_notes body (default false)"),
			),
			mcp.WithBoolean("force",
				mcp.Description("action=apply: re-download and re-install even if already at latest version. Use for testing the upgrade pipeline or recovering from a corrupted binary."),
			),
			mcp.WithString("source",
				mcp.Description("action=apply: path to local binary to install instead of downloading from GitHub. Use for dev iteration."),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(false),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(false),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			}),
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

		allFlag := request.GetBool("all", false)
		var allLoomTasks []*loom.Task
		projectID := projectIDFromContext(ctx)
		if s.loom != nil {
			if allFlag {
				tasks, taskErr := s.loom.ListAll()
				if taskErr != nil {
					s.log.Warn("sessions list: loom list all failed: %v", taskErr)
				} else {
					allLoomTasks = tasks
					s.log.Info("loom.scope.global_query: engine_name=%s rows=%d", s.engineName, len(allLoomTasks))
				}
			} else if projectID != "" {
				tasks, taskErr := s.loom.List(projectID)
				if taskErr != nil {
					s.log.Warn("sessions list: loom list failed for project %s: %v", projectID, taskErr)
				} else {
					allLoomTasks = tasks
				}
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
		// Two-phase init observability (issue #129).
		health["init_phase"] = s.initPhase.Load()
		health["init_duration_ms"] = s.initDurationMs.Load()
		health["warmup_deferred_count"] = s.warmupDeferredCount.Load()
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
		// Reset ALL circuit breakers after a successful re-probe. The user
		// explicitly asked us to re-verify CLI reachability, so prior stale
		// failures should not keep a now-healthy backend gated out. Without
		// this, a transient upstream outage that ended hours ago still leaves
		// the breaker open until the per-breaker cooldown elapses — producing
		// the "codex простаивает while breaker open" symptom reported 2026-04-21.
		s.breakers.ResetAll()
		enabled := s.registry.EnabledCLIs()
		// Binary-only fallback: if warmup marked every probeable CLI as
		// unavailable (common when the spawned daemon PATH cannot locate the
		// probe child process, or when quota errors surface as probe failures),
		// restore every CLI with a resolved binary to available. This matches
		// the startup fallback in cmd/aimux/main.go added in v4.5.2 PR #118.
		if len(enabled) == 0 {
			s.log.Warn("refresh-warmup: all CLI probes failed — restoring binary-only pool (health-gate bypassed)")
			for _, name := range s.registry.ProbeableCLIs() {
				s.registry.SetAvailable(name, true)
			}
			enabled = s.registry.EnabledCLIs()
		}
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
			"refreshed":                    true,
			"available":                    enabled,
			"excluded":                     excluded,
			"breakers_reset":               true,
			"binary_only_fallback_applied": len(enabled) > 0 && len(excluded) == 0,
		})

	default:
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
			"status":               "update_available",
			"current_version":      Version,
			"latest_version":       release.Version,
			"asset_name":           release.AssetName,
			"published_at":         release.PublishedAt,
			"release_notes_length": releaseNotesLen,
		}
		if includeContent {
			payload["release_notes"] = release.ReleaseNotes
		} else if releaseNotesLen > 0 {
			payload["truncated"] = true
			payload["hint"] = "release_notes omitted (" + fmt.Sprintf("%d", releaseNotesLen) + " bytes). Use upgrade(action=check, include_content=true) for full body."
		}
		return marshalToolResult(payload)

	case "apply":
		mode := upgrade.Mode(request.GetString("mode", string(upgrade.ModeAuto)))
		if mode != upgrade.ModeAuto && mode != upgrade.ModeHotSwap && mode != upgrade.ModeDeferred {
			return mcp.NewToolResultError(fmt.Sprintf("invalid upgrade mode %q (use auto, hot_swap, or deferred)", mode)), nil
		}
		force := request.GetBool("force", false)
		source := request.GetString("source", "")

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
		// Diagnostic: log actual runtime state of the type assertion. Tracks engram
		// issue #174 (hot-swap second-apply false-deferred). Remove once root cause
		// is verified.
		s.log.Info("upgrade-diag: handleUpgrade entered on Server=%p sessionHandler=%T (nil=%t) ctrlSocket=%q muxEngine!=nil=%t engineMode=%t",
			s, s.sessionHandler, s.sessionHandler == nil, s.daemonControlSocketPath, s.muxEngine != nil, engineMode)
		var sh upgrade.SessionHandler
		if engineMode {
			sh = h
		}

		coord := &upgrade.Coordinator{
			Version:         Version,
			BinaryPath:      binaryPath,
			SessionHandler:  sh,
			EngineMode:      engineMode,
			GracefulRestart: s.gracefulRestartFunc(),
			HandoffStatus:   s.handoffStatusFunc(),
			Logger:          s.log,
			Source:          source,
		}

		applyUpgrade := s.applyUpgrade
		if applyUpgrade == nil {
			applyUpgrade = func(ctx context.Context, coord *upgrade.Coordinator, mode upgrade.Mode, force bool) (*upgrade.Result, error) {
				return coord.Apply(ctx, mode, force)
			}
		}
		result, applyErr := applyUpgrade(ctx, coord, mode, force)
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
				"status":                  "updated_hot_swap",
				"previous_version":        result.PreviousVersion,
				"new_version":             result.NewVersion,
				"handoff_transferred_ids": result.HandoffTransferred,
				"handoff_duration_ms":     result.HandoffDurationMs,
				"message":                 result.Message,
			})
		case "deferred":
			status := "updated"
			payload := map[string]any{
				"status":           status,
				"previous_version": result.PreviousVersion,
				"new_version":      result.NewVersion,
				"message":          result.Message,
			}
			if result.HandoffError != "" {
				status = "updated_deferred"
				payload["status"] = status
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
