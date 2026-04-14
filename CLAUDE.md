# aimux v3 — Go MCP Server

## Project

Universal MCP server multiplexing 12 AI CLI tools (codex, gemini, claude, qwen, aider, goose, crush, gptme, cline, continue, droid, opencode).
Go rewrite of mcp-aimux (TypeScript v2). Single binary, zero external runtime dependencies.

## Stack

- **Language:** Go 1.25+
- **MCP SDK:** github.com/mark3labs/mcp-go v0.47.0
- **Database:** modernc.org/sqlite v1.48.1 (pure Go SQLite, no CGO)
- **Deep Research:** google.golang.org/genai v1.52.1 (Gemini API)
- **Engine:** github.com/thebtf/mcp-mux/muxcore v0.18.1 (daemon lifecycle, SessionHandler)
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

```
cmd/aimux/           — MCP server entry point (stdio/SSE/HTTP + muxcore engine daemon)
cmd/testcli/         — 11 CLI emulators for e2e testing
pkg/server/          — 13 MCP tool handlers (split: server_exec, server_orchestrate, server_agents, server_investigate, server_transport, server_session) + stall detection + model fallback + SessionHandler
pkg/orchestrator/    — Multi-CLI strategies (consensus, debate, dialog, pair, audit, workflow)
pkg/executor/        — Process executors (ConPTY, PTY, Pipe) + ProcessManager/IOManager + error classification + model cooldown
pkg/driver/          — CLI profile loading, registry, binary probe
pkg/config/          — YAML configuration + transport config
pkg/session/         — SQLite session/job persistence with WAL crash recovery (JobManager deprecated → use LoomEngine)
pkg/loom/            — LoomEngine v3: central task mediator (Submit/Get/List/Cancel/RecoverCrashed)
pkg/loom/workers/    — Worker adapters: CLI (executor), Thinker (think patterns), Investigator, Orchestrator
pkg/guidance/        — Policy-driven response guidance (envelope, registry, builder)
pkg/guidance/policies/ — Tool-specific guidance policies (think, investigate, consensus, debate, dialog, workflow)
pkg/investigate/     — Investigation sessions with finding chains and severity triage
pkg/think/           — 23 structured reasoning patterns (stateful + stateless)
pkg/agents/          — Agent registry with project/user discovery + per-project DiscoverForProject
pkg/skills/          — Embedded skill engine with disk overlay
pkg/prompt/          — Prompt engine with built-in + project overlay
pkg/routing/         — Role → CLI routing with capability-aware fallback
pkg/resolve/         — Profile-aware CLI command resolution
pkg/ratelimit/       — Per-tool token bucket rate limiting
pkg/metrics/         — Per-CLI request counters, error rates, latency
pkg/hooks/           — Before/after hook registry
pkg/parser/          — JSONL/JSON output parsers
pkg/tools/deepresearch/ — Gemini deep research with caching
pkg/types/           — Shared interfaces and types
config/cli.d/        — 12 CLI profiles (yaml)
config/skills.d/     — 13 embedded MCP skill prompts
config/p26/          — P26 tool classification artifact
```

## MCP Tools (13)

exec, status, sessions, think, investigate, consensus, debate, dialog, agents, agent, audit, deepresearch, workflow

## Engine Mode (muxcore)

Default for stdio transport. First invocation spawns daemon, subsequent connect as shims via IPC.

```
CC session → .mcp.json → aimux.exe (shim) → IPC socket → aimux daemon
                                                          ├── SessionHandler.HandleRequest()
                                                          ├── MCPServer.HandleMessage() (direct JSON-RPC)
                                                          ├── InProcessSession per ProjectContext.ID
                                                          └── Per-project agent overlay
```

- `SessionHandler`: direct JSON-RPC dispatch (no stdio transport overhead)
- `ProjectContext`: ID (hash of worktree root), Cwd, Env (per-session API keys)
- `ProjectLifecycle`: OnProjectConnect (agent discovery), OnProjectDisconnect (cleanup)
- Per-project agent scoping: `agents/list` returns only project-relevant agents
- `ProjectContext.Env` injected into spawned CLI process environment
- `AIMUX_NO_ENGINE=1` bypasses engine for debugging (direct stdio)
- Handler kept alongside SessionHandler for proxy mode (behind mcp-mux)

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

- **857 tests** across 117 test files, 27 packages
- **testcli:** 11 authentic CLI emulators in `cmd/testcli/` that replicate real process behavior
- **e2e:** 31 binary subprocess tests via MCP JSON-RPC over stdio
- **unit:** Per-package tests for all core logic
- **CI:** Race detector (Linux/macOS), golangci-lint v2, stub-detection scanner, mutation testing (gremlins, weekly)

## Known Issues

- Orchestrator strategies use hardcoded `Command: cli` + `Args: ["-p", prompt]`
  instead of profile resolution. Spec exists locally.
- `pkg/parser/` (JSONL parser) exists but not wired into server response path

## Working Directory

This is the primary project directory. `.agent/` data is gitignored but lives on disk locally.
TypeScript v2 at `D:\Dev\mcp-aimux` is the legacy version (kept for reference).
