# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [3.0.0] - 2026-04-06

Complete rewrite from TypeScript (v2) to Go. Single binary, zero external runtime dependencies.

### Added

- **12 CLI profiles**: codex, gemini, claude, qwen, aider, goose, crush, gptme, cline, continue, droid, opencode — all verified against CLI source code
- **11 MCP tools**: exec, status, sessions, consensus, dialog, debate, audit, think, investigate, agents, deepresearch
- **5 orchestration strategies**: PairCoding, SequentialDialog, ParallelConsensus, StructuredDebate, AuditPipeline
- **3 executor backends**: ConPTY (Windows), PTY (Linux/Mac), Pipe (fallback)
- **Profile-aware command resolution** (`pkg/resolve/`) — correct binary, prompt flags, stdin piping per CLI
- **Output parsing** (`pkg/parser/`) — JSONL, JSON, and text parsers wired into response path
- **Role-based CLI routing** — 14 roles (coding, codereview, thinkdeep, secaudit, debug, etc.)
- **Circuit breakers** per CLI with exponential backoff
- **SQLite persistence** (pure Go via modernc.org/sqlite) with session resume
- **Composable prompt templates** via `prompts.d/` with includes
- **Agent registry** with multi-source discovery (Loom Agents)
- **Deep research** via Google Gemini API with response caching
- **Dockerfile** for containerized deployment
- **306 tests** including 62 e2e tests via real MCP protocol
- **CI**: build + test on push/PR, weekly mutation testing (gremlins, 75% threshold)

### Changed

- Rewritten from TypeScript to Go for single-binary deployment
- CLI profiles moved from monolithic TOML to per-CLI YAML directories (`config/cli.d/`)
- Executor selection: ConPTY > PTY > Pipe (automatic best-available)

### Fixed

- CLI profile mismatches: codex `-p` was config profile selector not prompt flag
- claude `-p` was print mode flag not prompt flag
- droid/opencode needed `exec`/`run` subcommands for non-interactive mode
- Agents handler used raw `Command.Base` instead of `CommandBinary()`
- audit.go `validate` mutated input slice (immutability violation)
- Synthesis errors in consensus/debate silently swallowed

### Security

- All CLI processes sandboxed via `exec.Command` (no shell interpretation)
- No hardcoded secrets in codebase (verified by PRC audit)
- Config-driven flags only (no hardcoded CLI names in server code)

[3.0.0]: https://github.com/thebtf/aimux/releases/tag/v3.0.0
