# Feature: mcp-aimux v3 — Full Go Rewrite

**Slug:** go-rewrite-v3
**Created:** 2026-04-05
**Status:** Draft
**Author:** AI Agent + User (from 24h investigation session)

## Overview

Rewrite mcp-aimux from TypeScript/Node to Go. Not a port — a redesign that eliminates runtime-caused production bugs (EPIPE, async lifecycle races, buffered stdout), unlocks 3x performance via native PTY, and introduces mandatory pair coding with cross-model review.

## Context

Session 2026-04-04/05 produced 20 architectural decisions (ADR-014) based on:
- 10 production audit runs revealing EPIPE server crash as root cause of ALL failures
- Benchmark matrix: text mode 180s vs JSON mode 566s (3.1x overhead)
- Industry research: multi-agent specialization > parallelism of identical agents
- ccg-workflow analysis: codeagent-wrapper patterns, diff-only coding, domain trust
- mcp-go SDK validation: v0.47.0, 8.5K stars, full MCP coverage
- mcp-mux already Go with MCP stdio proven in production

Constitution: `.agent/specs/constitution.md` (14 principles) governs all decisions.

## Functional Requirements

### FR-1: MCP Protocol Parity
The system MUST support all MCP capabilities of v2: 10 tools, resources (agent:// URIs), prompts (aimux-background), progress notifications, completions, logging. stdio transport via mcp-go SDK.

### FR-2: Executor Interface with PTY-First Strategy
The system MUST spawn CLI processes through a unified Executor interface. Default = ConPTY (Windows) or PTY (Linux/Mac) for unbuffered text output. Pipe+JSON fallback for environments without PTY. Runtime feature detection, zero config.

### FR-3: Mandatory Pair Coding
Every `exec(role="coding")` MUST execute as a pair: driver (codex, gpt-5.3-codex/spark) produces unified diff, reviewer (claude, sonnet) validates per-hunk (hunk = git unified diff hunk, delimited by `@@` markers). Two modes: fire-and-forget (default, aimux applies approved code) and complex (returns structured result to caller). Solo coding prohibited. See ADR-014 Decisions 14+15 for detailed pipeline design.

### FR-4: Session Modes
The system MUST support three session modes:
- LiveStateful: persistent process, multiple turns via stdin/stdout
- OnceStateful: spawn, run one turn, exit, resume later via CLI session ID
- OnceStateless: spawn, run, exit, no resume, no tracking
Session registry tracks all sessions by UUIDv7 with reuse-vs-fresh heuristics.

### FR-5: CLI Profile Plugins
Each CLI MUST be defined as a self-contained plugin directory (`cli.d/{name}/profile.yaml`). Command templates + feature flags + version overrides. Adding new CLI = add directory, zero core code changes.

### FR-6: Unified Orchestrator
Dialog, consensus, debate, pair MUST share a single orchestrator engine with Strategy pattern. Each mode = Strategy implementation. Shared: turn execution, retry with backoff, context propagation, progress streaming, abort handling.

### FR-7: Audit Pipeline v2
The audit tool MUST support three modes: quick (scan only), standard (scan + cross-model validation), deep (scan + validate + investigate HIGH). Scanner and validator roles configurable. Incremental findings output.

### FR-8: Push-Based Progress
Job progress MUST be delivered via internal Go channels (push). MCP clients receive progress via `notifications/progress` messages (MCP SDK native mechanism). status() = read-only snapshot only. Internal channel → MCP notification bridge in server/progress.go.

### FR-9: Typed Errors
All errors at API boundaries MUST carry structured type (ExecutorError, TimeoutError, ValidationError) + human message + partial output. String error messages prohibited.

### FR-10: Composable Prompt Templates
Prompts MUST be stored as reusable fragments in `prompts.d/` with `includes` composition. Output styles as optional parameter on all tools. Per-project overrides via `{cwd}/.aimux/prompts.d/`.

### FR-11: Domain Trust Hierarchies
Cross-model reviews MUST respect domain authority configured per-role. Domain authority = veto power: authoritative model can reject changes in its domain even if other model approved. Codex authoritative for backend/logic/security. Gemini authoritative for frontend/UI/design. Non-domain conflicts resolved by code facts, not model preference.

### FR-12: Pheromone Job Metadata
Jobs MUST carry metadata markers (`map[string]string`) beyond status. Predefined keys: `discovery` (found useful pattern/file), `warning` (risk detected, proceed with caution), `repellent` (tried this approach, failed — don't retry), `progress` (working on specific area). Orchestrator strategies read markers before making decisions (e.g., skip approach marked repellent, reuse discovery).

### FR-13: Agents v2
Agents MUST be first-class workflow objects with: prompt, tools whitelist, context sources, success criteria, max_turns, escalation rules. Agent execution = Orchestrator with agent-specific Strategy. Multi-step pipelines, not single prompts.

## Non-Functional Requirements

### NFR-1: Performance
ConPTY/PTY executor MUST complete audit scan in under 5 minutes for 100-file TypeScript codebase (vs 13 min with JSON mode in v2). exec(role="coding") pair cycle MUST complete in under 3 minutes for single-file changes.

### NFR-2: Reliability
Zero server crashes under 10,000 tool calls. EPIPE, broken pipe, process kill — all handled as errors, never as exceptions. Race detector clean (`go test -race ./...`).

### NFR-3: Memory
Under 30MB with 5 concurrent sessions. Under 50MB with 10 concurrent sessions + 3 live persistent processes.

### NFR-4: Distribution
Single static binary. No runtime dependencies. Cross-compile for Windows/Linux/macOS from single codebase.

### NFR-5: Startup
Cold start under 50ms. CLI discovery (parallel probing) under 2 seconds.

### NFR-6: Graceful Shutdown
On SIGTERM/SIGINT: drain in-flight jobs (30s timeout), flush WAL journal, close SQLite, kill child processes. No data loss on clean shutdown.

### NFR-7: Observability
sessions(health) MUST report: running jobs, stuck jobs, circuit breaker states, memory usage, uptime. Async logger MUST support log levels (debug/info/warn/error) with runtime level change.

## User Stories

### US1: Pair-Reviewed Code (P1)
**As an** orchestrating agent, **I want** every coding task to be pair-reviewed by a different model before code hits disk, **so that** I can trust delegated code without manual review.

**Acceptance Criteria:**
- [ ] exec(role="coding") returns report with driver_diff, reviewer_verdict, files_changed
- [ ] Rejected hunks are re-prompted (max 3 rounds)
- [ ] Fire-and-forget mode writes approved code without caller waiting
- [ ] Complex mode returns structured result without writing

### US2: Fast Audit (P1)
**As an** orchestrating agent, **I want** audit to complete in under 5 minutes with verified findings, **so that** I can run it frequently without blocking workflow.

**Acceptance Criteria:**
- [ ] quick mode under 5 min (PTY text mode)
- [ ] standard mode under 10 min (scan + validation)
- [ ] Findings have confidence grades (verified/confirmed/unconfirmed)

### US3: Persistent CLI Sessions (P1)
**As an** orchestrating agent, **I want** to keep a CLI process alive across multiple turns, **so that** audit scan→validate→investigate runs in one warm process without cold starts.

**Acceptance Criteria:**
- [ ] LiveStateful session: Send() multiple prompts to same process
- [ ] Zero cold start after first turn
- [ ] Session auto-closes after configurable idle timeout

### US4: Zero-Config CLI Addition (P2)
**As a** developer, **I want** to add a new CLI by dropping a YAML file in cli.d/, **so that** I don't need to modify core code.

**Acceptance Criteria:**
- [ ] New directory in cli.d/ detected on startup
- [ ] Profile YAML parsed with template + flags + features
- [ ] New CLI available in role routing immediately

### US5: Crash-Proof Server (P2)
**As a** system operator, **I want** the server to survive any CLI process failure without crashing, **so that** other concurrent jobs are not affected.

**Acceptance Criteria:**
- [ ] Broken pipe on any stream → error logged, job failed, server continues
- [ ] CLI process crash → job failed with partial output, server continues
- [ ] Concurrent job not affected by sibling job failure

## Edge Cases

- ConPTY unavailable (Docker, old Windows) → automatic fallback to pipe+JSON
- Spark model unavailable (non-Pro account) → fallback to base model, cached probe result
- Scanner and validator resolve to same CLI → warning, forced quick mode
- LiveStateful session idle > 1h → auto-close, next call creates fresh
- CLI exits mid-turn → partial output preserved, error typed as ExecutorError
- All reviewer hunks rejected → max rounds exceeded → escalate to caller
- Server restart → WAL replay recovers sessions, running jobs re-executed via CLI resume
- YAML config parse error → fail fast with specific line/column error

## Out of Scope

- GUI/web interface (CLI + MCP only)
- Multi-user auth/authz (single-user MCP over stdio)
- Cloud deployment (local binary, not SaaS)
- v2→v3 session data migration (clean break, different DB path — v3 has own WAL+SQLite)
- Real-time collaboration between multiple aimux instances

## Dependencies

- `github.com/mark3labs/mcp-go` — MCP SDK (v0.47.0+)
- `gopkg.in/yaml.v3` — YAML config parsing
- `modernc.org/sqlite` — Pure Go SQLite (no CGO)
- `golang.org/x/sys/windows` — ConPTY API
- `github.com/google/uuid` — UUIDv7 generation
- mcp-mux compatibility — stdio transport, tool discovery

## Success Criteria

- [ ] All 10 MCP tools functional with feature parity (verified via feature-parity.toml)
- [ ] Audit quick mode under 5 min on mcp-aimux codebase (PTY text mode)
- [ ] Zero EPIPE crashes under 10,000 tool calls stress test
- [ ] Race detector clean
- [ ] Memory under 30MB with 5 concurrent sessions
- [ ] Single binary, cross-compiled for 3 platforms
- [ ] mcp-mux compatible
- [ ] Constitution compliance verified by /speckit-analyze

## Open Questions

None. All decisions locked in ADR-014 (18 decisions) + constitution (14 principles).

## Clarifications

### Session 2026-04-05

| # | Category | Question | Resolution | Date |
|---|----------|----------|------------|------|
| C1 | Data Lifecycle | Session persistence across restart? | WAL replay + CLI resume. Running sessions re-spawned via `resume <session_id>`. Completed sessions in SQLite survive. | 2026-04-05 |
| C2 | Integration | v3 binary naming during transition? | Drop-in: binary named `aimux`, replaces node command in mcp config. Parallel testing via separate config profile. | 2026-04-05 |
| C3 | Terminology | Internal vs external naming? | External MCP tool names unchanged (backward compat). Internal Go names use clean Go conventions. Loom Engine → Orchestrator (promotion). "Loom" prefix dropped. pair_start/stop tools removed — pair is inside exec(coding). | 2026-04-05 |
