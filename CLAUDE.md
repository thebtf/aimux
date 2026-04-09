# aimux v3 — Go MCP Server

## Project

Universal MCP server multiplexing 10+ AI CLI tools (codex, gemini, claude, qwen, aider, goose, crush, gptme, cline, continue).
Go rewrite of mcp-aimux (TypeScript v2). Single binary, zero external runtime dependencies.

## Stack

- **Language:** Go 1.25+
- **MCP SDK:** github.com/mark3labs/mcp-go
- **Database:** modernc.org/sqlite (pure Go SQLite)
- **Deep Research:** google.golang.org/genai
- **Build:** `go build ./cmd/aimux/`
- **Test:** `go test ./... -timeout 300s`

## Key Commands

```bash
go build ./...                    # build all
go test ./... -timeout 300s       # all tests (250+, ~75s)
go test ./test/e2e/ -v            # e2e tests only (59 tests)
go test ./pkg/... -cover          # unit tests with coverage
go vet ./...                      # static analysis
```

## Architecture

```
cmd/aimux/       — MCP server entry point (stdio transport)
cmd/testcli/     — 10 CLI emulators for e2e testing
pkg/server/      — MCP tool handlers (exec, status, sessions, dialog, etc.)
pkg/orchestrator/ — Multi-CLI strategies (consensus, debate, dialog, pair, audit)
pkg/executor/    — Process executors (pipe, conpty, pty)
pkg/driver/      — CLI profile loading and registry
pkg/config/      — YAML configuration
pkg/session/     — SQLite session/job persistence
pkg/parser/      — JSONL/JSON output parsers
pkg/types/       — Shared interfaces and types
pkg/resolve/     — CLI command resolution (planned)
```

## CLI Profiles

Each CLI has a profile in `config/cli.d/{name}/profile.yaml` defining:
- `binary` — executable name
- `command.base` — full command template (may include subcommands)
- `prompt_flag` — how prompt is passed (`-p`, `--message`, positional)
- `stdin_threshold` — pipe via stdin above this char count
- `completion_pattern` — regex to detect completion in stdout

## Testing

- **testcli:** 10 authentic CLI emulators in `cmd/testcli/` that replicate real process behavior
  (output format, buffering, stdin EOF, stderr discipline, OTEL delay)
- **e2e:** Binary subprocess tests via MCP JSON-RPC over stdio
- **unit:** Per-package tests for all core logic

## Known Issues

- Orchestrator strategies use hardcoded `Command: cli` + `Args: ["-p", prompt]`
  instead of profile resolution. Spec ready: `.agent/specs/orchestrator-profile-resolution/`
- `pkg/parser/` (JSONL parser) exists but not wired into server response path

## Working Directory

This is the primary project directory. All `.agent/` data lives here.
TypeScript v2 at `D:\Dev\mcp-aimux` is the legacy version (kept for reference).
