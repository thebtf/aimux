# Contributing to aimux

Thank you for your interest in contributing to aimux! This document covers the development setup, code style, and pull request process.

## Development Setup

### Prerequisites

- Go 1.25 or later
- At least one AI CLI installed (codex, gemini, claude, etc.)
- Git

### Build

```bash
git clone https://github.com/thebtf/aimux.git
cd aimux
go build ./cmd/aimux/
```

### Run Tests

```bash
# All tests (Windows/Mac/Linux)
go test ./... -timeout 300s

# Unit tests with coverage
go test ./pkg/... -cover

# E2E tests only (builds binary, runs MCP protocol tests)
go test ./test/e2e/ -v

# PTY tests via WSL (Linux PTY executor)
wsl -- bash -c "cd /mnt/d/Dev/aimux && /usr/local/go/bin/go test -v -cover ./pkg/executor/pty/"
```

### Static Analysis

```bash
go vet ./...
```

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- **Immutability by default** — never mutate function arguments or shared state. Return new objects.
- **Files < 800 lines** — split if larger
- **Functions < 50 lines** — extract helpers
- **No stubs** — every function must do what its signature promises (see Constitution P17)
- **Error handling** — always handle errors explicitly. No silent swallows.

## Project Structure

```
cmd/aimux/          — MCP server entry point (stdio transport)
cmd/testcli/        — 12 CLI emulators for e2e testing
pkg/server/         — MCP tool handlers
pkg/orchestrator/   — Multi-CLI strategies (consensus, debate, dialog, pair, audit)
pkg/executor/       — Process executors (ConPTY, PTY, Pipe)
pkg/resolve/        — CLI profile resolution (command, args, stdin piping)
pkg/parser/         — Output parsers (JSONL, JSON, text)
pkg/driver/         — CLI profile loading and registry
pkg/config/         — YAML configuration
pkg/session/        — SQLite session/job persistence
pkg/types/          — Shared interfaces and types
config/cli.d/       — CLI profile YAML files
config/prompts.d/   — Prompt templates
```

## Adding a New CLI

1. Create `config/cli.d/{name}/profile.yaml` with the correct fields
2. Verify against the CLI's actual `--help` output and source code
3. Create a testcli emulator in `cmd/testcli/{name}.go`
4. Add the emulator profile in `test/e2e/testdata/config/cli.d/{name}/profile.yaml`
5. Add e2e tests in `test/e2e/testcli_test.go`

## Pull Request Process

1. Create a feature branch: `git checkout -b feat/your-feature`
2. Make changes with tests
3. Run `go build ./... && go vet ./... && go test ./... -timeout 300s`
4. Commit with conventional format: `feat: description`, `fix: description`, `test: description`
5. Push and create PR against `master`
6. Ensure CI passes

## Reporting Issues

Open an issue on GitHub with:
- What you expected
- What happened instead
- Steps to reproduce
- Go version, OS, and CLI versions involved

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
