# Project Constitution — aimux

**Project:** aimux v3 — Go MCP server multiplexing 12+ AI CLIs
**Version:** 3.1.0
**Ratified:** 2026-04-05
**Last Amended:** 2026-04-11

## Principles

### 1. No CLI Writes Files Directly
All external CLI processes run in ReadOnly or DiffOnly mode. Only aimux orchestrator (or calling agent in complex mode) writes to filesystem. CLIs produce text output, diffs, findings — never modify files themselves.

**Rationale:** Prevents uncontrolled mutations, enables atomic apply/rollback, enables review-before-write.

### 2. Solo Coding Prohibited — Always Pair
Every `exec(role="coding")` = mandatory pair: driver (codex) drafts diff + reviewer (sonnet) validates per-hunk. No code reaches disk without cross-model review. Fire-and-forget default; complex mode for critical tasks.

**Rationale:** Single-model code has blind spots. Cross-model validation catches bugs that same-family review misses (confirmation bias).

### 3. Correct Over Simple
Choose the architecturally correct approach unconditionally. No "quick fixes", "for now", "simplest approach". Proper abstraction when 3+ call sites exist. Root cause fix, not symptom suppression.

**Rationale:** Every shortcut in this codebase became a production bug. EPIPE, async IIFE lifecycle, completion_pattern — all were "simple" solutions that required hours to debug.

### 4. ConPTY-First, JSON-Fallback + Control/Data Plane Separation
Default executor = ConPTY (Windows) or PTY (Linux/Mac) for unbuffered text output. PipeExecutor with --json = fallback only (Docker, CI, old Windows). Text mode = 3x faster than JSON mode. Process lifecycle (spawn/kill/reap) and I/O handling (stream/pattern/collect) are separate subsystems — neither blocks the other. ProcessManager owns lifecycle, IOManager owns I/O.

**Rationale:** Benchmarked: text mode 180s, JSON mode 566s. JSON serialization overhead in codex is the dominant bottleneck. Monolithic Run() mixing process management with I/O caused timeout issues identical to v2 — separating control plane from data plane eliminates the class of bugs entirely.

### 5. Context.Context Everywhere
Every long-running operation receives context.Context. Cancellation propagates from parent to child process, DB operations, progress streams. No orphaned goroutines. No timing-dependent lifecycle (no setTimeout, no setInterval equivalents).

**Rationale:** v2's async IIFE + GC reaper timing caused 7 consecutive audit failures. Context-based lifecycle is deterministic.

### 6. Push, Not Poll
Job progress delivered via channels, not status polling. MCP progress notifications pushed to subscribers. status() = read-only snapshot, not the primary progress mechanism.

**Rationale:** v2 agents polled status() 20+ times despite anti-polling documentation. Structural problem needs structural solution.

### 7. Typed Errors With Partial Output
Every error carries: structured type (ExecutorError, TimeoutError, ValidationError) + human message + partial output (if any). String error messages prohibited at API boundaries.

**Rationale:** v2 audit failures all returned "Process exited unexpectedly" — no way to distinguish timeout with partial data from crash with nothing.

### 8. Single Source of Config
All tool behavior configurable via YAML config. No hardcoded role names, model names, or CLI names in tool source code. Defaults in config files, overrides via env vars.

**Rationale:** v2 had hardcoded roles in 6 tools — required code changes to adjust behavior. Config should be enough.

### 9. CLI Profiles = Plugin Dirs
Each CLI = self-contained directory (`cli.d/codex/profile.yaml`). Adding new CLI = add directory, no core code changes. Discovery by directory scan. User overrides shadow built-in.

**Rationale:** v2 monolithic 400-line TOML required editing central config for any CLI change.

### 10. Verify Before Ship
feature-parity.toml tracks every capability. Three levels: implemented → tested → verified (side-by-side with v2). Phase gates block merge without 100% coverage. Drop policy requires written justification.

**Rationale:** Rewrite projects fail when "80% done" ships and remaining 20% never gets finished.

### 11. Immutable By Default
All exported data structures are immutable. Function arguments never mutated. New objects returned instead of in-place modification. Shared state protected by RWMutex with minimal critical sections.

**Rationale:** v2 audit found 8 CQ-1 mutation violations. Mutable shared state causes subtle concurrency bugs that Go's race detector catches but humans miss.

### 12. Every Finding Has Evidence
No architectural decision without production data. No "should be faster" without benchmark. No "eliminates bugs" without specific bug reference. ADR-014 cites: 10 production audit runs, 6 benchmark configurations, 7 consecutive audit failures, specific commit hashes.

**Rationale:** v2 had decisions based on assumptions ("3 parallel auditors = 3x coverage") that production data disproved.

### 13. Domain Trust Hierarchies
Cross-model reviews weight opinions by domain authority. Codex authoritative for backend/logic/security. Gemini authoritative for frontend/UI/design. When models conflict on same domain — code facts win over opinions.

**Rationale:** ccg-workflow showed that undirected multi-model review produces noise. Trust rules produce signal.

### 14. Composable Prompt Templates
Prompts = reusable fragments in `prompts.d/` with `includes` composition. Skills = deep workflow templates in `skills.d/` with Go template conditionals and fragment includes. Output styles as optional parameter on all tools. Per-project overrides via `{cwd}/.aimux/prompts.d/` and `{cwd}/.aimux/skills.d/`.

**Rationale:** v2 prompts hardcoded in source. Changing audit prompt required code change + build + deploy. Skills extend this to full workflow orchestration with dynamic data injection.

### 15. Circuit Breakers on CLI Failures
Each CLI has a circuit breaker. N consecutive failures → circuit opens → requests routed to fallback CLI. Auto-recovery after cooldown period. Prevents cascading failures when a CLI is down or rate-limited.

**Rationale:** Production sessions showed codex rate limits causing 5+ minute stalls. Circuit breaker + fallback routing keeps sessions responsive.

### 16. Holdout Evaluation
For multi-model strategies (consensus, debate, audit): reserve one model as holdout evaluator that doesn't participate in generation. Evaluator scores output without being biased by having contributed to it. Same principle as train/test split.

**Rationale:** claude-octopus analysis showed "consensus ≠ correctness — three models agreeing doesn't mean they're right." Independent evaluation required.

### 17. No Stubs — Every Code Path Must Produce Real Behavior
Functions that compile but don't perform their stated purpose are prohibited. Detected via 8-rule STUB-* taxonomy: STUB-DISCARD (discarded computed values), STUB-HARDCODED (hardcoded return strings), STUB-TODO (unfinished markers), STUB-NOOP (logging-only bodies), STUB-PASSTHROUGH (params built then discarded), STUB-TEST-STRUCTURAL (constructor-only tests), STUB-COVERAGE-ZERO (untested exported functions), STUB-INTERFACE-EMPTY (zero-value interface implementations). Enforced at 3 levels: PairCoding reviewer prompt, Audit scanner rules, Pre-commit hooks.

**Rationale:** v3 Go rewrite produced 118 passing tests with 4 critical stubs undetected. `go build` + `go test` are structurally incapable of catching behavioral stubs. Defense-in-depth with explicit taxonomy catches all known evasion patterns.

### 18. Skills = Deep Workflows, Not Summaries
A skill template that says "call tool X" without specifying phases, hard gates, exact parameters, session handoffs, and escalation paths is a **STUB-SKILL** — same violation as P17. Every skill MUST contain: phased workflow with hard gates between phases, exact tool call parameters (not placeholders), conditional sections adapting to runtime state, acceptance criteria, and cross-skill routing for escalation/delegation.

**Rationale:** Research across 5 codebases (72 patterns) showed that shallow "call tool X" instructions produce the same failure rate as no instructions. Only deep workflows with phase enforcement produce reliable agent behavior.

### 19. Graph Map Before Authoring
No skill template may be authored without its entry existing in `config/skills.d/_map.yaml` first. The map defines: tools used, phases, related skills, fragments, escalation targets, and incoming routes. Skills are authored FROM the map, not improvised. Map MUST be updated when skills change.

**Rationale:** First implementation attempt (PR #24) produced 6 handlers improvised without a map — all lacked interconnection. Second attempt with explicit graph produced interconnected workflows that route between each other.

### 20. Interconnection Over Isolation
Every skill MUST escalate to at least one other skill AND receive from at least one other skill. Isolated skills (no escalates_to, no receives_from) are prohibited. The skill graph must be connected — no orphan nodes.

**Rationale:** An isolated skill is just a fancy help page. The value of the skill system is that debug routes to security, audit routes to debug, review routes to consensus — the interconnection IS the product.

### 21. Caller-Aware Composition
Skills MUST adapt to the calling agent's existing capabilities via conditional sections (`{{if .CallerHasSkill "tdd"}}`). Duplicating instructions for what the caller already knows how to do wastes context budget. Built-in skills = deep choreography using aimux tools. Discovered caller skills = capability map for deduplication.

**Rationale:** aimux is called from Claude Code (147+ skills), Codex (29+ skills), and other agents. Sending a 200-line TDD workflow to an agent that already has a TDD skill = context waste. Conditional composition = efficiency.

### 22. Config-as-Files — Extensible Without Rebuild
All extensible behavior lives in config directories: `cli.d/` (CLI profiles), `prompts.d/` (role prompts), `skills.d/` (workflow skills), `agents/` (agent definitions), `audit-rules.d/` (audit rules). Adding new behavior = drop a file. No Go code changes. Disk files override embedded defaults. Embedded via `go:embed` for single-binary distribution.

**Rationale:** Generalizes P9 (CLI Profiles) and P14 (Composable Prompts) to all extensible subsystems. Every time we hardcoded behavior in Go, it required rebuild + release to change. Config-as-files makes aimux user-customizable without forking.

### 23. Reasoning Effort Tiers
Every role has an explicit reasoning_effort classification: low (trivial tasks: docgen, lint, tracing), medium (standard implementation: coding, refactor, testgen), high (analytical: codereview, debug), xhigh (deep: thinkdeep, secaudit, planner, challenge). CLIs that support reasoning effort receive the tier via profile config. Minimum timeout enforced: 120s for high/xhigh tasks.

**Rationale:** codex with reasoning_effort=high takes 10-30s baseline. Without tier-aware timeouts, analytical roles (codereview, debug) hit default 30s limits. Tiers prevent wasted tokens on trivial tasks and prevent timeouts on complex ones.

### 24. Local-Only by Default
aimux is a local MCP server. stdio transport is primary. SSE/HTTP transport exists for development but is not production-hardened for network deployment. Rate limiting and bearer auth are defense-in-depth, not multi-user isolation. Network security hardening (per-user auth, session namespacing, audit trail) deferred indefinitely.

**Rationale:** User decision 2026-04-09. As MCP server, aimux's function is localized — single user via Claude Code or mcp-mux. Designing for network deployment before it's needed would be over-engineering.

### 25. Agent-First Architecture
The `agents` tool is the PRIMARY entry point for task execution. `exec` is a low-level fallback for specific CLI control. Agent tool auto-selects the best agent from registry based on task keywords (AutoSelectAgent with scored matching). Built-in agents (researcher, reviewer, debugger, implementer) provide out-of-box coverage. CWD-based re-discovery adds caller project's agents at runtime.

**Rationale:** Users called exec for everything because agent tool required knowing exact agent names. Auto-selection + built-ins make agent the natural first choice, giving users structured reasoning (agent role, model selection, reasoning effort) without manual configuration.

### 26. Long-Running Tool Calls Must Be Interruptible
Every MCP tool action that may run longer than 10 seconds MUST be asynchronous by default. Such actions MUST:

1. Return a `job_id` immediately and execute in the background
2. Expose cancellation via `sessions(action="cancel", job_id=...)` — cancel signal propagates to all child CLI processes (SIGTERM → grace period → SIGKILL on Unix; cascade kill on Windows)
3. Stream progress via MCP `notifications/progress` AND accumulate into `status(job_id).progress` for clients that poll
4. Fail fast and loud if async infrastructure (OnOutput wiring, progress push, cancel handler) is not connected — never silently downgrade to sync

Synchronous execution is acceptable only for deterministic, sub-second operations (config reads, registry lookups, one-shot state mutations). Any path that invokes an LLM, shells out to a CLI, or waits on network IO MUST follow the async+cancel+stream contract above.

**Prohibited:**
- Sync tool calls that wrap long-running CLI invocations with "just use a high timeout"
- Tool paths that return `job_id` but have no corresponding cancel handler
- Streaming channels that are defined but never wired (the current `handleAgentRun` async path bug — tracked as engram issue #8 — is a concrete violation)
- Any new tool action added to aimux without explicitly declaring whether it is sync-allowed or async-mandatory

**Rationale:** MCP protocol itself has no request timeout, so a sync tool call that blocks for 5 minutes hangs the entire caller session with no escape hatch. User decision 2026-04-10: "когда ты запускаешь задачу в блокирующей сессии и у задачи нет escape hatch — сессия повисает навечно. я против такого подхода." This happened during the aimux-investigate session where a sync delegation call was discussed — it would have hung the CC session with no way to cancel. The only currently-working async path (`exec` tool) demonstrates the correct pattern: OnOutput callback plumbed from executor → JobManager.AppendProgress → MCP notifications/progress, cancel via context.CancelFunc registered per job, child processes killed on cancel. All future stateful/delegating tool actions must follow this pattern, not invent new sync variants.

## Governance

### Amendment Process
1. Propose change with rationale and evidence
2. Run `/nvmd-constitution` with changes
3. Propagation check: verify all specs/plans/tasks align
4. Version bump (MAJOR for principle removal, MINOR for addition, PATCH for clarification)

### Compliance
- `/nvmd-analyze` checks constitution alignment automatically
- Constitution violations are always CRITICAL severity
- Principles can ONLY be changed via `/nvmd-constitution`

### Version History

| Version | Date | Change |
|---------|------|--------|
| 3.1.0 | 2026-04-11 | MINOR: Added P26 (Long-Running Tool Calls Must Be Interruptible). Evidence: investigate-session failure on 2026-04-10 where sync delegation would have hung the CC session; existing async-only workaround rule in memory was never codified; engram issue #8 is a concrete P26 violation. |
| 3.0.0 | 2026-04-09 | MAJOR: Added P23 (Reasoning Effort Tiers), P24 (Local-Only by Default), P25 (Agent-First Architecture). Updated P4 with control/data plane separation. Updated governance commands to /nvmd-*. Evidence: CLI profile audit, PRC findings, user decision on network scope. |
| 2.0.0 | 2026-04-08 | MAJOR: Restored P15-P16 (lost in editing). Added P18 (Skills = Deep Workflows), P19 (Graph Map Before Authoring), P20 (Interconnection Over Isolation), P21 (Caller-Aware Composition), P22 (Config-as-Files). Updated P14 to include skills.d/. Updated project description. Sequential numbering P1-P22. |
| 1.2.0 | 2026-04-05 | Added: P17 No Stubs — 8-rule STUB-* taxonomy, 3-level defense-in-depth |
| 1.1.0 | 2026-04-05 | Added: circuit breakers (P15), holdout evaluation (P16) from claude-octopus analysis |
| 1.0.0 | 2026-04-05 | Initial ratification — 14 principles from ADR-014 Go rewrite session |
