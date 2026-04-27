# AGENTS.md — aimux v3

## Stack Configuration

```yaml
STACKS: [GO]
```

## Project Context

aimux is an MCP server. After the v5.0.3 Layer 5 purge, the live MCP surface is
**4 server tools + 23 think pattern tools**:

- `status` — async job status
- `sessions` — session/job management (action: list/health/gc/cancel/kill/info/refresh-warmup)
- `deepresearch` — Gemini SDK
- `upgrade` — binary update (action: check/apply, mode: auto/hot_swap/deferred)
- 23 think pattern tools (architecture_analysis, collaborative_reasoning, critical_thinking, debugging_approach, decision_framework, domain_modeling, experimental_loop, literature_review, mental_model, metacognitive_monitoring, peer_review, problem_decomposition, recursive_thinking, replication_analysis, research_synthesis, scientific_method, sequential_thinking, source_comparison, stochastic_algorithm, structured_argumentation, temporal_thinking, think, visual_reasoning)

Pre-purge CLI-launching tools (exec, agent, agents, critique, investigate, consensus, debate, dialog, audit, workflow) — **REMOVED**. Pipeline v5 packages (`pkg/workflow/`, `pkg/dialogue/`, `pkg/swarm/`, `pkg/executor/`, `pkg/resolve/`, `pkg/driver/`, `pkg/routing/`, `loom/`) remain in-repo as dormant seams pending the next Layer 5 design. Pre-purge architecture frozen at branch `snapshot/v5.0.3-pre-cli-purge` and documented in `docs/architecture/cli-tools-current.md`.

## Agent Instructions

### Memory-First — NON-NEGOTIABLE

**Before ANY destructive or non-trivial action, recall memory first.** Destructive includes — but is not limited to:

- Removing/replacing binaries (`aimux*.exe`, daemon swaps, hot-swaps)
- Killing processes, restarting daemons, dropping sockets
- `git rm`, `git reset --hard`, force-push, branch deletion, history rewrite
- Deleting files, directories, or test fixtures
- Reverting commits, dropping migrations, schema changes
- Any cleanup of "stale" state — `Remove-Item`, `rm -rf`, socket purge, cache wipe
- Replying to "how do I X" with a `Glob` of `scripts/` or a guess

**Required check (in order):**
1. `mcp__plugin_engram_engram__recall_memory(query: "...")` — project + global memories
2. `~/.claude/projects/<project>/memory/MEMORY.md` index
3. `.agent/specs/<feature>/` if a feature owns this area
4. THEN consult code, scripts, web

**Why:** memory contains verified procedures with anti-patterns table. The filesystem (`scripts/`, comments, file names) shows EVERY attempt — including failed workarounds and dead paths. Memory shows what actually works. Reading filesystem first means picking the workaround over the standard.

**Concrete example caught in this repo:** `scripts/graceful-upgrade-dev.ps1` is a kill+restart workaround for issue #170. The standard path is `mcp__aimux-dev__upgrade(source=..., force=true)`, documented in memory `feedback_dev_update_via_muxcore.md`. Glob over `scripts/*upgrade*` finds the workaround first; recall finds the standard first. Use recall.

If the memory protocol feels redundant: the redundancy is the safety. A memory hit is 5 seconds. A wrong destructive action is hours.

### Build & Test
- Always run `go build ./...` after modifying any `.go` file
- Run `go test ./...` before claiming "tests pass"
- e2e tests build the binary from source — no manual rebuild needed for them

### Code Patterns
- Interfaces in `pkg/types/interfaces.go`
- Strategy pattern for orchestrator (consensus, debate, dialog, pair, audit)
- Constructor injection for dependencies (Executor, CLIResolver)
- Profile-based CLI configuration via YAML in `config/cli.d/`
- **Response budget helper:** `pkg/server/budget/` shapes MCP tool responses to a
  ~4096-byte default with explicit `include_content=true` opt-in for full content.
  Handlers wire `budget.ParseBudgetParams` → `budget.ApplyFields` →
  `budget.AttachTruncation(envelope.Result, meta)`. Field whitelists live in
  `pkg/server/budget/fields.go`; pagination helpers in `pagination.go` and
  `pagination_dual.go`. Counter interfaces on `pkg/session.Store` /
  `pkg/session.Registry` and `loom.LoomEngine` / `loom.TaskStore` enable SQL
  COUNT without loading rows. See `.agent/specs/response-budget-policy/spec.md`.

### File Ownership
- `pkg/server/server.go` — 1355 LOC, main MCP handler file
- `pkg/orchestrator/` — 5 strategy files, DO NOT hardcode CLI names or flags
- `cmd/testcli/` — emulators for testing, DO NOT use in production

### Testing
- testcli emulators replicate real CLI process behavior
- Test profiles in `test/e2e/testdata/config/`
- `initTestCLIServer(t)` sets up aimux with testcli on PATH

### critique Tool

`critique(artifact, lens, cli, max_findings)` — delegates a code/design artifact to a CLI for
structured review using one of four built-in lenses:

| Lens | Focus |
|------|-------|
| `security` | Injection, auth flaws, secret exposure, input validation |
| `api-design` | REST/RPC contracts, naming conventions, backward compatibility |
| `spec-compliance` | Alignment with a stated spec or requirements document |
| `adversarial` | Adversarial prompt injection, misuse scenarios, trust boundary violations |

Response shape: `{findings, summary, cli_used, lens, tokens}` where `findings` is an array of
`{severity, location, issue, suggested_fix}` objects. Falls back to `{raw_output}` when the
CLI does not return parseable JSON. Missing `artifact` returns a validation error. Unknown `lens`
returns a validation error.

### agents Tool (Preferred) vs agent Tool (Deprecated)

**Use `agents(action="run", prompt="...")` — NOT `agent(agent="X", prompt="...")`.**

- `agents(action="run")` without an explicit `agent` parameter runs BM25 semantic auto-select
  and returns `selection_rationale` with score breakdown.
- `agents(action="find", prompt="...")` returns a ranked candidate list `{query, matches, count}`.
- `agent` (singular) still works but includes a `deprecated` field in its response directing
  callers to `agents(action="run")`. It will be removed in a future major version.

### Think Tool: Advisor + Enforcement Gates

Every per-pattern think tool call (e.g. `debugging_approach`, `decision_framework`) runs two
post-processing layers before returning:

1. **Enforcement gate** (`pkg/think/gates.go`) — checks per-pattern thresholds
   (min steps, min evidence, max confidence without evidence). Returns
   `gate_status: "complete" | "incomplete"` with a `gate_reason` field.

2. **Pattern advisor** (`pkg/think/advisor.go`) — uses BM25 to compare the result content
   against all pattern descriptions. Returns `advisor_recommendation: {action, target, reason}`
   where `action` is `"continue"` (stay in pattern) or `"switch"` (move to a better-fit pattern).

Both fields appear under the `result` key in the response (inside the guidance envelope):

```json
{
  "result": {
    "gate_status": "complete",
    "gate_reason": "...",
    "advisor_recommendation": {"action": "continue", "target": "", "reason": "..."},
    ...
  }
}
```

Stateless invocations (no `session_id`) always return `gate_status: "complete"`.
Max advisor-triggered pattern switches per session: 3.

## Architecture Glossary (v5)

| Term | Definition |
|------|-----------|
| ExecutorV2 | Unified interface for CLI and API backends (Send/SendStream/IsAlive/Close/Info) |
| LegacyExecutor | Original v4 executor interface (Run/Start/Name/Available) — deprecated, use ExecutorV2 |
| Swarm | Process/connection pool manager — sole entry point for executor access |
| Handle | Opaque reference to a Swarm-managed executor instance |
| SpawnMode | Executor lifecycle: Stateless (per-request), Stateful (per-session), Persistent (daemon) |
| Dialogue Controller | Participant-agnostic conference moderator (pkg/dialogue/) |
| Participant | Anything that speaks in a dialogue: CLI, API, think pattern, external agent |
| SwarmParticipant | Adapter: Executor via Swarm → Participant interface |
| PatternParticipant | Adapter: Think pattern → Participant interface |
| Domain Workflow | Multi-step scenario with quality gates (codereview, secaudit, debug, etc.) |
| Workflow Engine | Generic step executor: SingleExec, Dialogue, ThinkPattern, Gate, Parallel |
| Strangler Fig | Migration pattern: build new alongside legacy, re-wire callers incrementally |
| LegacyRun | Swarm bridge: legacy SpawnArgs/Result through Swarm lifecycle management |
| API Executor | HTTP API backend (OpenAI, Anthropic, Google AI) implementing ExecutorV2 |

### Upgrade Behavior
- `upgrade(action="apply")` supports `mode="auto|hot_swap|deferred"`.
- `mode="auto"` is the default: in engine mode it tries daemon-side graceful restart first and falls back safely if live handoff cannot complete.
- `mode="hot_swap"` requires live handoff and returns an error instead of silently falling back.
- `mode="deferred"` skips live handoff and preserves the legacy staged-restart path.
- Response semantics are intentional: `updated_hot_swap` means live daemon handoff succeeded, `updated_deferred` means auto mode fell back after a live-path failure, and `updated` is reserved for explicit deferred mode.
- Inspect `handoff_error` when `status="updated_deferred"`; this is the truthful fallback signal rather than a live-upgrade success.
- Hot-swap activation work supersedes the earlier structural-prep-only scope from engram #129; do not document the feature as "inactive pending upstream" in current project docs.

### Dev Binary Upgrade — STANDARD PROCEDURE (always use this)

When changes need to land on the running `aimux-dev.exe` (or `aimux.exe` for prod hot-deploy), use the live daemon hot-swap path. Do NOT use `scripts/graceful-upgrade-dev.ps1` — it is a kill+restart workaround for issue #170 and **NOT the standard procedure**.

```powershell
# 1. Build to a temp file (daemon holds aimux-dev.exe — direct overwrite fails on Windows)
.\scripts\build.ps1 -Output aimux-dev-next.exe

# 2. Smoke test the new binary in isolation (must succeed before step 3)
D:\Dev\mcp-launcher\mcp-launcher.exe -binary .\aimux-dev-next.exe -mode hold -hold 8
#    Expect: "tools: 27" (4 server + 23 think patterns) on the post-purge branch.
#    If handshake fails or count is wrong — DO NOT proceed.

# 3. Clean any stale aimux-dev-next processes left by mcp-launcher
Get-Process aimux-dev-next -ErrorAction SilentlyContinue | Stop-Process -Force

# 4. Issue the live hot-swap via the MCP tool
mcp__aimux-dev__upgrade(action="apply", source="D:/Dev/aimux/aimux-dev-next.exe", force=true)
#    Expect MCP error: "upstream restarted, request lost during reconnect" — that IS success.
#    Daemon restarts on the new binary; the CC session survives across the restart.

# 5. Verify
mcp__aimux-dev__sessions(action="health")     # daemon answers → upgrade landed
.\aimux-dev.exe --version                      # version string matches step 1 output
```

**Why this is the standard:** the upgrade tool atomically renames `source` over `aimux-dev.exe` daemon-side, no kill required, the running CC session reconnects to the new daemon transparently.

**What NEVER works (anti-patterns):**

| Attempt | Why it fails |
|---|---|
| `scripts/graceful-upgrade-dev.ps1` | Kill+restart workaround for issue #170; not the standard path. Use only when hot-swap fails repeatedly with file-lock errors AND you have permission to drop running shims. |
| `mcp__aimux-dev__upgrade(action="apply")` without `source=` | Downloads from GitHub, not the local dev build |
| Manual `Move-Item aimux-dev-next.exe aimux-dev.exe` | Daemon holds the file open — `Access is denied` |
| `mux_restart` against aimux | aimux is standalone stdio, not mcp-mux managed |
| `/mcp` Reconnect alone | Shim reconnects to OLD daemon with old code; binary never replaced |

**Recovery if "Access is denied" on step 4** (stale processes from prior failed attempts):

```powershell
Get-Process | Where-Object { $_.ProcessName -match "aimux" } | Stop-Process -Force
Remove-Item "$env:TEMP\mcp-mux-*.sock" -Force -ErrorAction SilentlyContinue
# Then: /mcp Reconnect, retry from step 4
```

Memory: `feedback_dev_update_via_muxcore.md` (project-scoped). If you forget the procedure mid-task, `recall_memory` it BEFORE checking `scripts/`.
