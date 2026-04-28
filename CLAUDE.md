# aimux v3 — Go MCP Server

## Project

Universal MCP server multiplexing 12 AI CLI tools (codex, gemini, claude, qwen, aider, goose, crush, gptme, cline, continue, droid, opencode).
Go rewrite of mcp-aimux (TypeScript v2). Single binary, zero external runtime dependencies.

## Stack

- **Language:** Go 1.25+
- **MCP SDK:** github.com/mark3labs/mcp-go v0.47.0
- **Database:** modernc.org/sqlite v1.48.1 (pure Go SQLite, no CGO)
- **Deep Research:** google.golang.org/genai v1.52.1 (Gemini API)
- **Engine:** github.com/thebtf/mcp-mux/muxcore v0.19.4 (daemon lifecycle, SessionHandler)
- **Build:** `go build ./cmd/aimux/`
- **Test:** `go test ./... -timeout 300s`

## Key Commands

```bash
go build ./...                    # build all
go test ./... -timeout 300s       # all tests (857 tests, 27 packages)
go test ./test/e2e/ -v            # e2e tests only (31 tests)
go test ./pkg/... -cover          # unit tests with coverage
go vet ./...                      # static analysis
```

## Architecture

Layer 5 was purged at v5.0.3. The repo now has a reduced live MCP surface
(status/sessions/deepresearch/upgrade plus 23 think patterns) sitting on top
of dormant Pipeline v5 packages awaiting a redesigned Layer 5. See
`docs/architecture/cli-tools-current.md` for the frozen pre-purge audit.

```
cmd/aimux/           — MCP server entry point (stdio/SSE/HTTP + muxcore engine daemon)
cmd/testcli/         — CLI emulators retained for future e2e use
pkg/server/          — Surviving MCP handlers (status, sessions, deepresearch, upgrade) + 23 think pattern handlers, SessionHandler, transport, helpers
pkg/think/           — 23 structured reasoning patterns (stateful + stateless)
pkg/aimuxworkers/    — ThinkerWorker (only surviving worker; CLI/Investigator/Orchestrator workers removed in purge)
pkg/guidance/        — Policy-driven response guidance (envelope, registry, builder)
pkg/guidance/policies/ — Surviving guidance policy: think (other policies are dormant Layer 5 seams)
pkg/skills/          — Embedded skill engine with disk overlay
pkg/prompt/          — Prompt engine with built-in + project overlay
pkg/session/         — SQLite session persistence with WAL crash recovery (JobManager deprecated → use LoomEngine)
pkg/tools/deepresearch/ — Gemini deep research with caching
pkg/types/           — Shared interfaces and types
pkg/metrics/         — Per-CLI request counters, error rates, latency
pkg/hooks/           — Before/after hook registry
pkg/ratelimit/       — Per-tool token bucket rate limiting
pkg/parser/          — JSONL/JSON output parsers (kept for future Layer 5)
pkg/config/          — YAML configuration + transport config

# Dormant Pipeline v5 (kept in-repo, not wired into Layer 5)
pkg/workflow/        — M4 v4.13.0 — 8 ready domain workflows (codereview, secaudit, debug, analyze, docgen, precommit, refactor, testgen) NOT exposed via MCP
pkg/dialogue/        — M3 v4.12.0 — Dialogue Controller (parallel/sequential/stance/round-robin)
pkg/swarm/           — M2 v4.11.0 — process pool, lifecycle, restart
pkg/executor/        — M1 v4.10.0 — Executor V2 (CLI + API adapters) + ConPTY/PTY/Pipe backends
pkg/resolve/         — Profile-aware CLI command resolution
pkg/driver/          — CLI profile loading, registry, binary probe
pkg/routing/         — Role → CLI routing with capability-aware fallback
loom/                — LoomEngine v0.1.0 (vendored standalone module): central task mediator (Submit/Get/List/Cancel/RecoverCrashed) + Worker registry

# Removed (snapshot/v5.0.3-pre-cli-purge for restoration)
# pkg/orchestrator/  — legacy strategies (consensus/debate/dialog/audit/workflow/pair) — REMOVED
# pkg/agents/        — agent registry — REMOVED
# pkg/investigate/   — investigation state machine — REMOVED (v1 port deferred)

config/cli.d/        — 12 CLI profiles (yaml) — kept for Pipeline v5 / future Layer 5
config/p26/          — P26 tool classification artifact (synced to reduced surface)
config/skills.d/     — empty post-purge; v5.0.3 contents archived under archive/v5.0.3/skills.d/
archive/v5.0.3/      — frozen v5.0.3 skills + map.yaml + README documenting restoration
docs/architecture/   — cli-tools-current.md (pre-purge audit)
```

## MCP Tools (4 + 23 think patterns)

status, sessions, deepresearch, upgrade

Think surface: 23 dedicated pattern tools, including `think`, `critical_thinking`, `decision_framework`, `debugging_approach`, and the rest of the registered pattern set.

## Engine Mode (muxcore)

Default for stdio transport. First invocation spawns daemon, subsequent connect as shims via IPC.

```
CC session → .mcp.json → aimux.exe (shim) → IPC socket → aimux daemon
                                                          ├── SessionHandler.HandleRequest()
                                                          ├── MCPServer.HandleMessage() (direct JSON-RPC)
                                                          ├── InProcessSession per ProjectContext.ID
                                                          └── Per-project MCP session state
```

- `SessionHandler`: direct JSON-RPC dispatch (no stdio transport overhead)
- `ProjectContext`: ID (hash of worktree root), Cwd, Env (per-session API keys)
- `ProjectLifecycle`: OnProjectConnect/OnProjectDisconnect manage per-project MCP sessions
- `ProjectContext.Env` injected into spawned CLI process environment
- Handler kept alongside SessionHandler for proxy mode (behind mcp-mux)
- Pipeline v5 packages (`workflow`, `dialogue`, `swarm`, `executor`, `resolve`, `driver`, `routing`) remain in-repo as dormant seams pending the Layer 5 redesign

## Two-Phase Daemon Init (issue #129)

Daemon starts in two phases to eliminate the 30s gap before shims can connect:

**Phase A** (~100ms): `lightweightDelegate` installed immediately. Shims that connect
during Phase A receive either:
- `-32001` JSON-RPC retry-hint (if `warmup_grace_seconds` expires before Phase B)
- Seamless re-dispatch to Phase B (if Phase B completes within grace window — common case)

**Phase B** (heavy init — warmup probes, registry, router): runs in a background goroutine
(`async_init: true`, default). On completion, `swapDelegateToFull` atomically replaces the
lightweight delegate and closes the ready channel, triggering re-dispatch for any waiting shims.

Config knobs (`config/default.yaml`):
- `warmup_grace_seconds: 15` — how long Phase A blocks before returning `-32001`
- `async_init: true` — set `false` for synchronous Phase B (tests, single-process mode)

Observability (`sessions` tool, `action: health`):
- `init_phase`: 0=Phase A, 1=Phase B in progress, 2=Phase B complete
- `init_duration_ms`: wall-clock time Phase A→B swap took
- `warmup_deferred_count`: number of requests that hit Phase A and waited

Files: `pkg/server/server_handler_delegate.go` (Phase A), `pkg/server/server_session.go`
(swap logic, `aimuxHandler`), `pkg/server/server.go` (`RunPhaseB`),
`test/e2e/cold_start_attach_test.go` (NFR gate: p99 ≤ 1s, 5 concurrent shims).

## CLI Profiles (12)

Each CLI has a profile in `config/cli.d/{name}/profile.yaml` defining:
- `binary` — executable name
- `command.base` — full command template (may include subcommands)
- `prompt_flag` — how prompt is passed (`-p`, `--message`, positional)
- `stdin_sentinel` — positional arg for stdin mode (e.g. "-" for codex)
- `model_fallback` — ordered list of models to try on quota errors
- `cooldown_seconds` — per-model cooldown after rate limit
- `completion_pattern` — regex to detect completion in stdout

Supported: codex, gemini, claude, aider, goose, gptme, qwen, cline, crush, droid, opencode, continue (cn)

## Testing

- Unit tests across the surviving `pkg/...` packages — `go test ./pkg/... -timeout 180s`
- e2e tests in `test/e2e/` exercising the reduced surface (status/sessions/deepresearch/upgrade/think patterns) plus daemon+shim handshake
- `loom/` is a standalone nested module — `cd loom && go test ./...`
- `cmd/aimux/`, `tools/loomlint/` build + test
- `CI:` Race detector (Linux/macOS), golangci-lint v2, stub-detection scanner, mutation testing (gremlins, weekly)

## Known Issues

- `progress_tail`/`progress_lines` not ported from legacy `JobManager` to LoomEngine — async tasks via loom emit no activity signal until terminal. Tracked as engram issue #173 (priority=high).
- `TestE2E_Think_AllPatterns` skipped — `sampleArgsFromSchema` does not understand XOR-required schemas (e.g. `scientific_method` requires `stage` OR `entry_type`). Re-enable when generator learns OneOf semantics.
- New Layer 5 surface to expose `pkg/workflow/` M4 domain workflows is not yet designed — current MCP surface is intentionally minimal.

## Working Directory

This is the primary project directory. `.agent/` data is gitignored but lives on disk locally.
TypeScript v2 at `D:\Dev\mcp-aimux` is the legacy version (kept for reference).
