# CLI-Launching MCP Tools — Current Architecture (Frozen Snapshot)

**Captured:** 2026-04-27
**Frozen at:** master @ `9cc3433` (tag `v5.0.3`)
**Snapshot branch:** `snapshot/v5.0.3-pre-cli-purge` (origin)
**Status:** Audit baseline before full delete on master.

This document is a reverse-engineered snapshot of how every MCP tool that
ultimately spawns an AI CLI works in the v5.0.3 codebase. It captures wiring,
layering, contracts, and dead-code surface — not a redesign. The redesign
follows in a separate spec.

---

## 1. Scope

### Covered (10 CLI-launching tools)

`exec`, `agent`, `agents` (action=run only), `critique`, `investigate`
(action=auto only), `consensus`, `debate`, `dialog`, `audit`, `workflow`.

### Out of scope

- `think` (23 stateful/stateless reasoning patterns) — local computation, no
  CLI. Documented separately in `.agent/research/think-patterns-routing-data.md`.
- `status`, `sessions` — DB query only (jobs/sessions.db, loom_tasks).
- `deepresearch` — Gemini SDK over HTTP, not a CLI subprocess.
- `upgrade` — binary swap, no CLI.
- Non-CLI actions of `investigate` (start, finding, assess, report, list,
  recall, status) — pure in-process state mutation on `pkg/investigate/`
  session store.
- Non-CLI actions of `agents` (list, find, info) — registry queries only.

---

## 2. Layered Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│ MCP CLIENT (Claude Code, mcp-launcher, other MCP clients)           │
└──────────────────────────────────────────────────────────────────────┘
                              │ JSON-RPC over stdio (engine mode: IPC)
                              ▼
┌──────────────────────────────────────────────────────────────────────┐
│ Layer 1: MCP TOOL SURFACE  (pkg/server/server.go::registerTools)    │
│   14 mcp.AddTool calls. No gating. Schema published in InputSchema. │
└──────────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌──────────────────────────────────────────────────────────────────────┐
│ Layer 2: HANDLERS  (pkg/server/server_*.go::handle*)                │
│   Validate args → resolve CLI → choose path: legacy vs Loom → spawn │
└──────────────────────────────────────────────────────────────────────┘
                              │
            ┌─────────────────┴────────────────┐
            ▼                                  ▼
┌────────────────────────┐         ┌────────────────────────────────┐
│ Layer 3a: LOOM (async) │         │ Layer 3b: LEGACY SYNC          │
│   loom.Submit          │         │   executor.Run direct          │
│   pkg/loom/            │         │   pkg/jobs (deprecated)        │
└────────────────────────┘         └────────────────────────────────┘
            │                                  │
            ▼                                  │
┌────────────────────────┐                     │
│ Layer 4: WORKERS       │                     │
│  pkg/aimuxworkers/     │                     │
│   - CLIWorker          │                     │
│   - InvestigatorWorker │                     │
│   - OrchestratorWorker │                     │
│   - ThinkerWorker (no CLI; for completeness) │
└────────────────────────┘                     │
            │                                  │
            ▼                                  │
┌────────────────────────┐                     │
│ Layer 5: STRATEGIES    │                     │
│  pkg/orchestrator/     │                     │
│   consensus, debate,   │                     │
│   dialog, audit,       │                     │
│   workflow, pair       │                     │
└────────────────────────┘                     │
            │                                  │
            ▼ (M3 Strangler Fig optional path) │
┌────────────────────────┐                     │
│ Layer 6: DIALOGUE      │                     │
│  pkg/dialogue/         │                     │
│  pkg/swarm/  (handle   │                     │
│   reuse pool)          │                     │
└────────────────────────┘                     │
            │                                  │
            └────────────┬─────────────────────┘
                         ▼
┌──────────────────────────────────────────────────────────────────────┐
│ Layer 7: EXECUTOR  (pkg/executor/{conpty,pty,pipe})                 │
│   Spawn subprocess, drive prompt, capture stdout, error-classify.   │
└──────────────────────────────────────────────────────────────────────┘
                         │
                         ▼
┌──────────────────────────────────────────────────────────────────────┐
│ Layer 8: RESOLVE / REGISTRY / ROUTING                               │
│   pkg/resolve   — profile-aware command building                    │
│   pkg/driver    — registry of CLIProfiles                           │
│   pkg/routing   — role → CLI preference                             │
│   pkg/config    — YAML profile loader                               │
└──────────────────────────────────────────────────────────────────────┘
                         │
                         ▼
                ┌────────────────────┐
                │ config/cli.d/*/    │
                │   profile.yaml     │
                │   (12 profiles)    │
                └────────────────────┘
```

---

## 3. Tool-by-Tool Wiring

For each of the 10 CLI-launching tools below: registration site,
handler, dispatch path(s), worker, strategy, and final spawn site.

### 3.1 `exec`

| Aspect | Value |
|---|---|
| Registered | `server.go:526` (mcp.NewTool) |
| Handler | `server_exec.go:30` `handleExec` |
| Async path | `loom.Submit{WorkerTypeCLI}` → `aimuxworkers.CLIWorker.Execute` |
| Sync path | `s.executor.Run(args)` direct |
| Worker | `aimuxworkers.CLIWorker` (`cli.go:12`, embeds `loom/workers.SubprocessBase`) |
| Resolver | `resolve.NewProfileResolver(cfg.CLIProfiles).ResolveSpawnArgs(cli, prompt)` |
| Routing | `s.router.Resolve(role)` → `RolePreference{CLI, Model, Effort}` |
| Spawn | `executor.Run({CLI, Command, Args, CWD, Env, Stdin, Timeout})` → ConPTY/PTY/Pipe |
| Tests | `test/e2e/all_tools_test.go` `TestE2E_Exec_*` (4 tests) + `pkg/server/server_exec_fallback_test.go` |

**Intent:** Lowest-level entrypoint. Single CLI, raw prompt. Role drives CLI
selection; explicit `cli=` override allowed. Foundation that every other
single-CLI tool wraps.

### 3.2 `agent` *(deprecated)*

| Aspect | Value |
|---|---|
| Registered | `server.go:921` |
| Handler | `server_agents.go:313` `handleAgentRun` |
| Deprecation log | `s.log.Warn("agent tool is deprecated; use agents(action=run)...")` (line 316) |
| Async path | `loom.Submit{WorkerTypeCLI}` (same as exec) |
| Sync path | `s.executor.Run` direct |
| Worker | `aimuxworkers.CLIWorker` |
| Agent registry | `s.agentReg.Get(agentName)` + per-project overlay via `ProjectAgentsFromContext(ctx)` |
| Resolver | Same as exec; agent metadata (cli, model, effort, timeout) overrides defaults |
| Tests | `test/e2e/all_tools_test.go` `TestE2E_Agent_*` |

**Intent:** Named-agent dispatch. Pre-built system prompt + role-based CLI
selection. Replaced by `agents(action=run)` which adds BM25 semantic agent
selection and `selection_rationale`.

### 3.3 `agents` (action=run)

| Aspect | Value |
|---|---|
| Registered | `server.go:874` |
| Handler | `server_agents.go:28` `handleAgents` → switch case `"run"` → calls `handleAgentRun` |
| Action: list | Registry summaries (no CLI) |
| Action: find | BM25 keyword search (no CLI) |
| Action: info | Single agent details (no CLI) |
| Action: run | Same path as `agent` tool above |
| Tests | `TestE2E_Agents_List`, `TestE2E_Agents_Find`, `TestE2E_Agents_Run` |

**Intent:** Primary task-dispatch entrypoint. action=run delegates to
`handleAgentRun` (shared with deprecated `agent` tool). action=list/find/info
do NOT launch CLI.

### 3.4 `critique`

| Aspect | Value |
|---|---|
| Registered | `server.go:1031` |
| Handler | `server_critique.go:30` `handleCritique` |
| Async path | **None** — sync only, no Loom integration |
| Spawn | `s.executor.Run(args)` direct |
| Lens templates | `builtinLenses` map (4 entries: security, api-design, spec-compliance, adversarial) at `server_critique.go:16` |
| Response template | `critiqueResponsePrompt` constant — forces JSON `{findings:[{severity,location,issue,suggested_fix}],summary}` (line 24) |
| Routing | `s.router.Resolve("critic")` → fallback to `"default"` role |
| Resolver | `resolve.CommandBinary(profile.Command.Base)` + `resolve.BuildPromptArgs(profile, "", "", false, prompt)` |
| Tests | `pkg/server/server_critique_test.go` |

**Intent:** Single-CLI artifact review through a named lens. Lens = a
hard-coded system-prompt template that frames the reviewer's persona.

### 3.5 `investigate` (action=auto)

| Aspect | Value |
|---|---|
| Registered | `server.go:713` |
| Handler | `server_investigate.go:202` `handleInvestigate` — switches by action |
| Non-CLI actions | start, finding, assess, report, list, recall, status — pure state mutation on `pkg/investigate/` session store |
| CLI action | `auto` — single-shot CLI investigation |
| Async path | `loom.Submit{WorkerTypeInvestigator}` → `aimuxworkers.InvestigatorWorker` |
| Worker | `aimuxworkers.InvestigatorWorker` (`investigator.go:15`) |
| Resolver | `w.resolver.ResolveSpawnArgs(cli, task.Prompt)` |
| Default CLI | `codex` if neither task.CLI nor metadata override set |
| Domain detection | `inv.AutoDetectDomain(topic)` — generic / debugging |
| Tests | `TestE2E_Investigate_Start`, `TestE2E_Investigate_MissingAction`, `TestE2E_Investigate_StartMissingTopic` |

**Intent:** action=auto runs one CLI pass. start/finding/assess/report build a
structured investigation report iteratively without spawning CLIs.

### 3.6 `consensus`

| Aspect | Value |
|---|---|
| Registered | `server.go:782` |
| Handler | `server_orchestrate.go:60` `handleConsensus` |
| Async path | `loom.Submit{WorkerTypeOrchestrator, Metadata:{strategy:"consensus"}}` |
| Worker | `aimuxworkers.OrchestratorWorker` (`orchestrator.go:14`) |
| Strategy | `orchestrator.ParallelConsensus.Execute` (`pkg/orchestrator/consensus.go:44`) |
| Participants | `s.registry.EnabledCLIs()[:2]` — first 2 enabled |
| Min CLIs guard | `checkMinTwoCLIs(enabled)` (server_orchestrate.go:47) |
| Strangler Fig (M3) | `consensusStrategy.SetDialogue(dlgCtrl, s.swarm)` at `server.go:302` — when wired, uses `dialogue.ModeParallel` instead of legacy `executor.Run` per CLI |
| Tests | `TestE2E_Consensus_Basic`, `TestE2E_Consensus_MissingTopic` |

**Intent:** Blinded parallel opinion gathering across 2+ CLIs. Each
participant cannot see others' responses. Optional `synthesize` adds a
combining turn.

### 3.7 `debate`

| Aspect | Value |
|---|---|
| Registered | `server.go:816` |
| Handler | `server_orchestrate.go:165` `handleDebate` |
| Async path | `loom.Submit{WorkerTypeOrchestrator, Metadata:{strategy:"debate"}}` |
| Strategy | `orchestrator.StructuredDebate.Execute` (`pkg/orchestrator/debate.go`) |
| Default turns | 6 (`request.GetFloat("max_turns", 6)`) |
| Default synthesize | true (gives final verdict) |
| Strangler Fig | `debateStrategy.SetDialogue(dlgCtrl, s.swarm)` at `server.go:303` |
| Tests | `TestE2E_Debate_Basic`, `TestE2E_Debate_MissingTopic` |

**Intent:** Adversarial multi-turn argument with optional synthesized verdict.
Same first-2-CLIs participant model as consensus.

### 3.8 `dialog`

| Aspect | Value |
|---|---|
| Registered | `server.go:847` |
| Handler | `server_orchestrate.go:270` `handleDialog` |
| Async path | `loom.Submit{WorkerTypeOrchestrator, Metadata:{strategy:"dialog"}}` |
| Strategy | `orchestrator.SequentialDialog.Execute` (`pkg/orchestrator/dialog.go`) |
| Default turns | 6 |
| Strangler Fig | `dlgStrategy.SetDialogue(dlgCtrl, s.swarm)` at `server.go:301` |
| Known issue | Hangs 18+ minutes on long turn counts (memory `feedback_dialog_tool_hangs.md`) |
| Tests | `TestE2E_Dialog_Basic` |

**Intent:** Sequential turn-by-turn refinement between 2 CLIs. Not adversarial
— iterative.

### 3.9 `audit`

| Aspect | Value |
|---|---|
| Registered | `server.go:677` |
| Handler | `server_orchestrate.go:405` `handleAudit` |
| Required arg | `cwd` (validated via `validateCWD`) |
| Async path | `loom.Submit{WorkerTypeOrchestrator, Metadata:{strategy:"audit"}}` |
| Strategy | `orchestrator.AuditPipeline.Execute` (`pkg/orchestrator/audit.go`) |
| Roles | `cfg.Server.Audit.ScannerRole`, `cfg.Server.Audit.ValidatorRole` |
| Parallelism | `cfg.Server.Audit.ParallelScanners` |
| Modes | `quick` (scan only), `standard` (scan+validate), `deep` (scan+validate+investigate) |
| Tests | `TestE2E_Audit_Quick` |

**Intent:** Multi-stage codebase audit. Scanner CLI(s) run first; validator CLI
filters; deep mode adds investigation pass.

### 3.10 `workflow`

| Aspect | Value |
|---|---|
| Registered | `server.go:1000` |
| Handler | `server_orchestrate.go:506` `handleWorkflow` |
| Required arg | `steps` (JSON array of `orchestrator.WorkflowStep`) |
| Async path | `loom.Submit{WorkerTypeOrchestrator, Metadata:{strategy:"workflow", extra:{workflow:json}}}` |
| Strategy | `orchestrator.WorkflowStrategy.Execute` (`pkg/orchestrator/workflow.go`) |
| Step types | `tool: "exec" | "think" | "investigate"` — workflow strategy invokes inner tools |
| Tests | `pkg/orchestrator/workflow_test.go` |

**Intent:** Declarative pipeline runner. Composes other tools into a named
chain with templated cross-step references.

---

## 4. Subsystems Referenced by CLI-Launching Tools

### 4.1 Loom Engine (pkg/loom — vendored as `loom/` v0.1.0)

- Standalone Go module (`github.com/thebtf/aimux/loom`) — does NOT pull in MCP server.
- `LoomEngine` instance lives on `Server.loom` (initialised at `server.go:~310`).
- 4 worker types registered at `server.go:315-318`:
  - `WorkerTypeCLI`        ← `aimuxworkers.NewCLIWorker(executor, cliResolver)`
  - `WorkerTypeThinker`    ← `aimuxworkers.NewThinkerWorker()` (no CLI; used by think)
  - `WorkerTypeInvestigator` ← `aimuxworkers.NewInvestigatorWorker(executor, cliResolver)`
  - `WorkerTypeOrchestrator` ← `aimuxworkers.NewOrchestratorWorker(s.orchestrator)`
- Crash recovery: `s.loom.RecoverCrashed()` called once at boot.
- Persistence: SQLite (sessions.db at `~/.aimux/sessions.db` or `cfg.Server.SessionsDB`).
- Public API: `Submit`, `Get`, `List`, `Cancel`, `RecoverCrashed`, `Events.Subscribe`, `Close`.

### 4.2 Orchestrator (pkg/orchestrator)

- 6 strategy implementations (each implements `types.Strategy` from `pkg/types`):
  - `PairCoding`           — pair coding (2 CLIs collaborate)
  - `SequentialDialog`     — for `dialog` tool
  - `ParallelConsensus`    — for `consensus` tool
  - `StructuredDebate`     — for `debate` tool
  - `AuditPipeline`        — for `audit` tool
  - `WorkflowStrategy`     — for `workflow` tool
- Constructed at `server.go:298-311` and registered into `orch.New(...)`.
- Public API on `Orchestrator`: `Execute(ctx, strategyName, params)`, `Register(strategy)`.

### 4.3 Strangler Fig (pkg/dialogue + pkg/swarm) — M3 milestone

- `dialogue.New()` → `Controller` exposing `NewDialogue`, `NextTurn`, `Synthesize`, `Close`.
- `swarm.Swarm` reuses CLI handles (`Stateless` or stateful) across multiple turns
  to avoid re-warming the CLI between calls.
- 3 strategies opt in via `SetDialogue(ctrl, swarm)`:
  consensus, debate, dialog.
- When set, strategy uses `dialogue.ModeParallel` / `ModeSequential` instead of
  per-turn `executor.Run` calls.
- Falls back to legacy path on any error (consensus.go:69, 82, 87).
- audit, workflow, pair do NOT use dialogue — direct executor path only.

### 4.4 Executor (pkg/executor)

- Backends:
  - `conpty.Executor`   — Windows ConPTY (preferred on Windows)
  - `pty.Executor`      — POSIX PTY (Unix)
  - `pipe.Executor`     — fallback / non-interactive CLIs
- Selection in `pkg/server/server.go` boot (handleExec uses package-level helpers).
- Single interface `types.Executor.Run(ctx, args) (*types.ExecutorResult, error)`.
- `args` = `types.SpawnArgs{CLI, Command, Args, CWD, Env, Stdin, TimeoutSeconds, ReadOnly, ...}`.

### 4.5 Resolve / Driver / Routing (pkg/resolve, pkg/driver, pkg/routing)

- `driver.Registry`        — loads YAML profiles from `config/cli.d/*/profile.yaml`.
- `resolve.ProfileResolver` — given (cli, prompt, model, effort, readOnly), builds
  fully-formed `SpawnArgs`. Implements `types.CLIResolver`.
- `routing.Router`          — given role string, returns `RolePreference{CLI, Model, Effort}`.
  Capability-aware: rejects roles no CLI advertises in `capabilities`.

### 4.6 State Stores

- `s.sessions`              (`pkg/session.Store`) — per-tool session metadata, jobs (legacy).
- `s.jobs`                  (`pkg/jobs` legacy `JobManager`) — deprecated; LoomEngine replaces it for new tools.
- `s.agentReg`              (`pkg/agents.Registry`) — disk-loaded agents. Per-project overlay via `ProjectAgentsFromContext`.
- `s.registry`              (`pkg/driver.Registry`) — CLI profile registry.
- `s.metrics`               — Prometheus / OTel counters per CLI.

---

## 5. Bootstrap Order (server.go::New)

Excerpt of relevant initialization (lines ~280-340):

1. Load `cfg.CLIProfiles` from `config/cli.d/`.
2. `s.executor` constructed (multi-backend selection).
3. `s.swarm = swarm.New(...)` — handle pool.
4. Think GC goroutine started.
5. `cliResolver = resolve.NewProfileResolver(cfg.CLIProfiles)`.
6. Strategies constructed and wired:
   - `pair`, `dialog`, `consensus`, `debate`, `audit`, `workflow`
   - 3 of them get `SetDialogue(dlgCtrl, s.swarm)` (Strangler Fig).
7. `s.orchestrator = orch.New(log, ...strategies)`.
8. Loom workers registered: CLI, Thinker, Investigator, Orchestrator.
9. `s.loom.RecoverCrashed()`.
10. Prompt engine + think patterns initialised.
11. `s.registerTools()` — adds 14 MCP tools.
12. `s.registerResources()`, `s.registerPrompts()`.

---

## 6. Dead-Code Surface After Full Delete

When the 10 CLI-launching tool registrations are removed and their handlers
deleted, the following code becomes unreachable from MCP entrypoints:

### Definitely orphaned (handler-only paths)

- `pkg/server/server_exec.go`           — entire file (handleExec only)
- `pkg/server/server_agents.go`         — `handleAgentRun` + run-case in `handleAgents`; list/find/info would still need handler if `agents` tool is partially kept (decision pending)
- `pkg/server/server_critique.go`       — entire file (+ `builtinLenses` map)
- `pkg/server/server_orchestrate.go`    — entire file (5 handlers: consensus, debate, dialog, audit, workflow)
- `pkg/server/server_investigate.go`    — `auto` case only; non-CLI actions remain (decision pending)
- `pkg/server/server_cooldown.go`       — used only by exec; orphaned
- `pkg/server/server_exec_fallback_test.go` — fallback-path tests for exec

### Potentially orphaned (no other callers)

- `pkg/orchestrator/`        — all 6 strategies + `Orchestrator` struct
- `pkg/aimuxworkers/cli.go`        — CLIWorker (only loom registration calls it)
- `pkg/aimuxworkers/orchestrator.go` — OrchestratorWorker
- `pkg/aimuxworkers/investigator.go` — InvestigatorWorker
- `pkg/aimuxworkers/cli_spawn.go`    — `cliSpawnResolver`, `cliRunner`
- `pkg/dialogue/`            — Controller, Dialogue, Modes
- `pkg/swarm/`               — Swarm pool (also used by some test fixtures)
- `pkg/executor/`            — ConPTY/PTY/Pipe backends
- `pkg/resolve/`             — profile-aware command builder
- `pkg/driver/`              — Registry, Probe (still needed for `agents` action=list/info if tool kept)
- `pkg/routing/`             — Router (no consumer left if no CLI dispatch)
- `pkg/jobs/`                — already deprecated in favour of LoomEngine
- `pkg/orchestrator/holdout.go`, `pheromones.go`, `quality_gate.go`, `context.go` — strategy support

### Survives unaffected

- `pkg/think/`               — patterns (no CLI)
- `pkg/investigate/`         — investigation state machine (used by non-CLI actions)
- `pkg/agents/`              — agent registry (still needed if `agents` action=list/find/info kept)
- `pkg/loom/`                — vendored module; ThinkerWorker still useful; not strictly orphaned
- `pkg/aimuxworkers/thinker.go` — non-CLI worker, used by think tools
- `pkg/tools/deepresearch/`  — Gemini SDK
- `pkg/session/`             — store still used by think + investigate state
- `pkg/skills/`, `pkg/prompt/` — embedded skills + prompt overlay

### Config files orphaned

- `config/cli.d/*/profile.yaml` (12 profiles) — no longer needed at runtime, but
  tests may still reference them; keep as references for redesign.
- `config/p26/` — P26 tool classification artifact; references removed tools.
- `config/skills.d/aimux-guide.md`, `delegate.md`, `poll-wrapper-subagent.md` — describe removed tools; update or remove.

---

## 7. Test Surface (will break after delete)

### `test/e2e/all_tools_test.go`

```
TestE2E_Exec_*                  (4)   — handleExec gone
TestE2E_Agent_*                 (n)   — handleAgentRun gone
TestE2E_Agents_Run              (1)   — agents.run gone (list/find/info ok if kept)
TestE2E_Consensus_*             (2)
TestE2E_Debate_*                (2)
TestE2E_Dialog_Basic            (1)
TestE2E_Audit_Quick             (1)
TestE2E_Investigate_*           (3)   — Start/MissingAction still ok if tool kept
TestE2E_Workflow_*              (n)
TestE2E_Critique_*              (n)
TestE2E_DeepResearch_*          (2)   — survives
TestE2E_Status_*                (n)   — survives
TestE2E_Think_*                 (2)   — survives
```

### `pkg/server/p26_classification_test.go`

AST-parses `registerTools()` and validates a fixed tool set / action coverage.
Will fail until updated for the reduced surface.

### `pkg/server/server_test.go::TestNewServer_OrchestratorInitialized`

Asserts orchestrator has 5 strategies registered. Becomes obsolete once
orchestrator construction is removed.

### Strategy-level tests

- `pkg/orchestrator/*_test.go` — all become orphaned along with strategies
- `pkg/dialogue/*_test.go`     — orphan with package
- `pkg/swarm/*_test.go`        — orphan with package
- `pkg/executor/*_test.go`     — orphan with package
- `pkg/aimuxworkers/workers_test.go` — partial (Thinker test survives)

---

## 8. External Surface (what callers use today)

Documented for impact assessment when these MCP tools disappear:

- **Claude Code** — primary client. Recommended workflow per CONTINUITY.md
  is `mcp__aimux-dev__exec(role="coding", async=true)` for codex dispatch. Agent
  delegation goes through `mcp__aimux__agent` and `mcp__aimux-dev__agents`.
- **mcp-launcher** — used as e2e harness; persist mode reproducer for issue 103.
- **Other MCP-aware AI tools** — any tool that connects to aimux's stdio
  interface and reads tool descriptions via `tools/list`.

After the delete on master, only `aimux-dev.exe` should be regenerated;
`aimux.exe` (prod) remains at v5.0.3 until the new architecture lands.

---

## 9. Frozen-State References

| Marker | Value |
|---|---|
| Snapshot branch | `snapshot/v5.0.3-pre-cli-purge` |
| Frozen commit | `9cc343313f5149a44b99bfccbac2b6e67b0782a0` |
| Tag containing | `v5.0.3` |
| Audit date | 2026-04-27 |
| Auditor | orchestrator (Claude Opus 4.7) |
| Source of truth | `pkg/server/server.go::registerTools` (lines 520-1100) |

To inspect the frozen state on disk:
```bash
git checkout snapshot/v5.0.3-pre-cli-purge
go build ./cmd/aimux/                # compiles the v5.0.3 surface
git checkout master                  # back to working branch
```

---

## 10. What This Document is NOT

- Not a redesign. The redesign that motivates the delete is captured
  separately (TBD: spec).
- Not a complete audit of every think pattern, MCP resource, or skill —
  only the CLI-launching tool surface.
- Not a behavioural test charter — that lives in `test/e2e/all_tools_test.go`
  and the per-package `_test.go` files.
- Not a record of past decisions — see `engram` and CONTINUITY.md for those.

---

## 11. Pipeline v5 Connection Map (added 2026-04-27)

Audit found that the 10 CLI-launching tools are mostly NOT routed through the
v5 pipeline (Loom + Workflow + Dialogue Controller + Swarm + Executor V2)
that was shipped over milestones M1–M5.

### NOT connected to Pipeline v5 (7 of 10)

These handlers call `s.executor.Run()` directly, bypassing Swarm and Dialogue
Controller. CLI processes are spawned fresh per call (no reuse).

| Tool | Path |
|------|------|
| `exec` | `server_exec.go::handleExec` → `s.executor.Run()` |
| `agent` | `server_agents.go::handleAgentRun` → `s.executor.Run()` |
| `agents` (action=run) | same as `agent` |
| `critique` | `server_critique.go::handleCritique` → `s.executor.Run()` |
| `investigate` (action=auto) | `InvestigatorWorker.Execute` → `s.executor.Run()` |
| `audit` | `orchestrator.AuditPipeline.Execute` → `executor.Run()` per stage |
| `workflow` | `orchestrator.WorkflowStrategy.Execute` (LEGACY engine, NOT `pkg/workflow/` M4) |

### Partially connected via M3 Strangler Fig (3 of 10)

Handler still goes through `pkg/orchestrator/`, but the strategy body uses the
new path when `SetDialogue(ctrl, swarm)` has been called at boot
(`server.go:301-303`):

| Tool | Strategy | Migrated path |
|------|----------|---------------|
| `consensus` | `ParallelConsensus` | `executeWithDialogue` → `DialogueController(ModeParallel)` → SwarmParticipants → Swarm → Executor |
| `debate`    | `StructuredDebate`  | `executeWithDialogue` → `DialogueController(ModeStance)` → SwarmParticipants → Swarm → Executor |
| `dialog`    | `SequentialDialog`  | `executeWithDialogue` → `DialogueController(ModeSequential)` → SwarmParticipants → Swarm → Executor |

The legacy `executeLegacy` fallback in each of these three is gated by
`if c.dialogue != nil { executeWithDialogue } else { executeLegacy }`, plus
defensive fallbacks on swarm/dialogue errors. Both files print
`[DEPRECATED] %s using legacy executor path — migrate to Dialogue Controller (v5.0.0 will remove this)`
on the legacy path.

### Pipeline v5 layer ownership (after purge)

```
LAYER                     PACKAGE              SHIPPED       SURVIVES PURGE
─────────────────────────────────────────────────────────────────────────
Layer 5 — MCP Tool        pkg/server/server.go REGISTRATION  removed (10 AddTool calls)
Layer 4 — Workflow        pkg/workflow/        M4 v4.13.0    yes (8 workflows ready, no MCP entry)
Layer 4 — Dialogue        pkg/dialogue/        M3 v4.12.0    yes
Layer 3 — Swarm           pkg/swarm/           M2 v4.11.0    yes
Layer 3 — Router          pkg/routing/         (pre-v5)      yes
Layer 3 — Resolver        pkg/resolve/         (pre-v5)      yes
Layer 3 — Registry        pkg/driver/          (pre-v5)      yes
Layer 2 — Executor V2     pkg/executor/        M1 v4.10.0    yes
Layer 2 — API Executors   pkg/executor/api/?   M5 v4.14.0    yes (location TBD)
Layer 1 — Backends        pkg/executor/{conpty,pty,pipe}    yes
Cross-cut — Loom          loom/                v0.1.0        yes
Legacy strategies         pkg/orchestrator/    pre-v5        REMOVED (Strangler Fig rudiments)
```

### Big finding — pkg/workflow/ M4 not exposed

`pkg/workflow/` contains 8 ready domain workflow implementations
(`codereview.go`, `secaudit.go`, `debug.go`, `analyze.go`, `docgen.go`,
`precommit.go`, `refactor.go`, `testgen.go`). None are registered as MCP tools.
The current `workflow` tool routes through the LEGACY `pkg/orchestrator/workflow.go`
(WorkflowStrategy via OrchestratorWorker), not the M4 engine.

This is the redesign opportunity that motivates the v5.0.3 Layer 5 purge:
clean Layer 5 surface, then expose M4 domain workflows as proper MCP tools
per the wiring.md target architecture.

---

## 12. executeWithDialogue — what it does (added 2026-04-27)

Three strategies in `pkg/orchestrator/` (`consensus.go`, `debate.go`, `dialog.go`)
each have an `executeWithDialogue` method — the M3 Strangler Fig "new path"
that uses Pipeline v5 instead of direct `executor.Run()`.

All three follow the same shape:

1. **Build SwarmParticipants** — for each requested CLI:
   - `handle, err := c.swarm.Get(ctx, cli, swarm.Stateless)`
   - On any swarm error → fall through to `executeLegacy`
   - `dialogue.NewSwarmParticipant(swarm, handle, cli, role)` wraps the
     handle as a `dialogue.Participant`
2. **Create Dialogue session** — `c.dialogue.NewDialogue(DialogueConfig{...})`
   with mode-specific config:
   - consensus → `ModeParallel`, `MaxTurns: 0` (one parallel round)
   - debate → `ModeStance`, `MaxTurns: maxTurns`, `Stances: stancesMap`
   - dialog → `ModeSequential`, `MaxTurns: maxTurns`
3. **Drive turns** — `c.dialogue.NextTurn(ctx, dlg)` until `dlg.Status != StatusActive`.
   Controller picks the next participant per mode and calls its `Respond`.
4. **Optional synthesis** — `c.dialogue.Synthesize(ctx, dlg)` produces a
   `Synthesis` that combines all turns. Failure is non-fatal (legacy fallback
   only on swarm/dialogue creation, NOT on synthesis).
5. **Convert turns to StrategyResult** — flatten `dlg.Turns` to markdown,
   append `Synthesis.Content` if present.

### Why it matters

The Pipeline v5 path through `executeWithDialogue` brings:
- **Swarm-managed processes** — handles are reusable, lifecycle-tracked,
  can survive across calls (`Stateless` here = fresh per dialogue, but
  spawn/kill is centralised, not duplicated per turn).
- **Dialogue Controller as turn-taking algorithm** — one piece of code
  decides ordering, formatting, history threading. Strategies stop
  duplicating that logic.
- **Pluggable participants** — `dialogue.Participant` interface is what makes
  it possible (per wiring.md target) to mix CLI executors, API executors,
  and ThinkPattern participants in the same dialogue.

### Why it still has a legacy escape hatch

Every error path falls through to `executeLegacy` (the original turn-by-turn
`executor.Run()` loop). That fallback exists because Strangler Fig migration
is not complete:
- swarm.Get errors aren't handled gracefully inside dialogue
- dialogue.NewDialogue errors aren't recoverable inside the controller
- some CLIs may not have `Stateless` swarm support fully wired

After Layer 5 purge, the legacy fallback paths and the strategies themselves
go away. Pipeline v5 (`pkg/workflow/`, `pkg/dialogue/`, `pkg/swarm/`,
`pkg/executor/`) survives intact, and the next iteration wires the M4 domain
workflows directly to MCP tool registrations without going through
`pkg/orchestrator/` at all.

---

## 13. Change Log

| Date | Change |
|---|---|
| 2026-04-27 | Initial capture at v5.0.3 by orchestrator pre-purge. |
| 2026-04-27 | Added section 11 — Pipeline v5 connection map and migration status. |
| 2026-04-27 | Added section 12 — executeWithDialogue mechanics and Strangler Fig rationale. |
