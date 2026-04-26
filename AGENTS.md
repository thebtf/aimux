# AGENTS.md — aimux v3

## Stack Configuration

```yaml
STACKS: [GO]
```

## Project Context

aimux is an MCP server that multiplexes AI CLI tools. It receives MCP tool calls
(exec, status, sessions, dialog, consensus, debate, audit, think, investigate, deepresearch, agents, critique)
and spawns the appropriate CLI subprocess.

## Agent Instructions

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
