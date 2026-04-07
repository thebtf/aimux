# Contributing to aimux

Go MCP server multiplexing 10+ AI CLI tools. Single binary, zero external runtime dependencies.

## Quick Start

```bash
git clone https://github.com/thebtf/aimux.git
cd aimux
go build ./cmd/aimux/          # build the binary
go test ./... -timeout 300s    # run all 462 tests (~75s)
go vet ./...                   # static analysis
```

## Development Setup

- **Go 1.25+** required (`go version` to verify)
- No additional toolchain — SQLite is bundled via `modernc.org/sqlite` (pure Go)
- Any editor with `gopls` support works (VS Code + Go extension, GoLand, Neovim)

PTY tests require Linux:
```bash
wsl -- bash -c "cd /mnt/d/Dev/aimux && go test -v -cover ./pkg/executor/pty/"
```

## Project Structure

```
cmd/aimux/          MCP server entry point (stdio transport)
cmd/testcli/        CLI emulators for hermetic e2e testing
pkg/server/         MCP tool handlers (exec, status, sessions, dialog, ...)
pkg/orchestrator/   Multi-CLI strategies (consensus, debate, dialog, pair, audit)
pkg/executor/       Process executors (pipe, conpty, pty)
pkg/driver/         CLI profile loading and registry
pkg/think/          Think patterns and registry
pkg/config/         YAML configuration
pkg/session/        SQLite session/job persistence
pkg/parser/         JSONL/JSON output parsers
pkg/types/          Shared interfaces
config/cli.d/       One directory per CLI — profile.yaml
config/prompts.d/   Role prompt files (.md)
```

## Adding a New CLI Profile

1. Create `config/cli.d/{name}/profile.yaml`. Use `config/cli.d/codex/profile.yaml` as reference.

   Key fields:
   - `name`, `binary`, `display_name`
   - `features` — capability flags (`streaming`, `headless`, `session_resume`, `jsonl`, ...)
   - `command.base` + `args_template` — how to invoke the CLI (Go template)
   - `prompt_flag` / `prompt_flag_type` — `"-p"` / `"flag"` or `""` / `"positional"`
   - `completion_pattern` — regex that signals the process has finished
   - `stdin_threshold` — pipe via stdin above this character count

2. Add a testcli emulator in `cmd/testcli/` that replicates the real CLI's output format, buffering behavior, and stdin EOF handling.

3. Add the emulator profile in `test/e2e/testdata/config/cli.d/{name}/profile.yaml`.

4. Add e2e coverage in `test/e2e/`.

No code changes needed to register the profile — `pkg/driver` discovers profiles from the config directory at startup.

## Adding a New Think Pattern

Each pattern lives in `pkg/think/patterns/` and implements `think.PatternHandler`:

```go
type PatternHandler interface {
    Name()        string
    Description() string
    Validate(input map[string]any) (map[string]any, error)
    Execute(input map[string]any) (map[string]any, error)
}
```

Steps:
1. Create `pkg/think/patterns/{name}.go` with your implementation.
2. Register it in `pkg/think/patterns/init.go`:
   ```go
   think.RegisterPattern(NewYourPattern())
   ```
3. Add unit tests alongside the implementation.

See `pkg/think/patterns/critical_thinking.go` for a pattern that validates input and produces structured output.

## Adding a New Orchestrator Strategy

Strategies implement `types.Strategy` from `pkg/types/interfaces.go`:

```go
type Strategy interface {
    Name() string
    Execute(ctx context.Context, params StrategyParams) (*StrategyResult, error)
}
```

Steps:
1. Create `pkg/orchestrator/{name}.go`.
2. Wire it into the orchestrator constructor in `pkg/server/` by passing it to `orchestrator.New(...)`.
3. Add an MCP tool handler in `pkg/server/` that calls it.
4. Add tests: unit tests for strategy logic, at least one e2e test for the MCP tool.

Reference implementations:
- `pkg/orchestrator/consensus.go` — parallel strategy, fan-out to multiple CLIs
- `pkg/orchestrator/pair.go` — stateful multi-turn strategy

## Code Style

**Immutability** — return new values, never mutate arguments or shared state. In-place mutation of shared collections is forbidden; use `filter()` / `sorted()` equivalents.

**Small files** — 200–400 lines typical, 800 hard limit. Split proactively when a file becomes hard to reason about.

**Functions < 50 lines** — extract helpers rather than nesting logic.

**No stubs** — every function must compute a real output from its inputs. If replacing the body with `return nil` wouldn't break tests, the function and its tests are both stubs.

**Error handling** — handle every error explicitly at every level. No silent swallowing. Log context server-side; return clear messages at MCP boundaries.

**Input validation** — validate at all system boundaries (MCP handlers, profile loading, strategy params). Fail fast with descriptive errors.

**Formatting** — `gofmt`. No manual decisions needed.

## Testing Requirements

| Layer | Location | What to cover |
|-------|----------|---------------|
| Unit | `pkg/.../` | Package logic in isolation; table-driven; mock via `pkg/types` interfaces |
| E2E | `test/e2e/` | MCP JSON-RPC over stdio against the real binary; uses testcli emulators |

```bash
go test ./pkg/... -cover          # unit tests with coverage
go test ./test/e2e/ -v            # e2e tests (requires built binary)
go test ./... -timeout 300s       # full suite
```

New packages: aim for ≥80% line coverage. New orchestrator strategies require both a unit test for strategy logic and an e2e test for the MCP tool that calls it.

## Commit Format

```
<type>: <short description>
```

Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `ci`

One logical change per commit. Commit after each sub-task; do not batch unrelated changes.

Examples:
```
feat: add openai-compatible profile for litellm
fix: handle EOF on stdin before completion pattern fires
test: add e2e coverage for consensus strategy timeout
refactor: extract profile validation into driver package
```

## Pull Request Process

1. Branch from `master`: `git checkout -b feat/your-feature`
2. Keep PRs focused — one feature or fix per PR
3. All of `go build ./...`, `go vet ./...`, and `go test ./... -timeout 300s` must pass
4. Describe what changed and why; include a test plan
5. PRs that add a CLI profile without a testcli emulator or e2e coverage will be asked to add them before merge

## Reporting Issues

Open a GitHub issue with: what you expected, what happened, steps to reproduce, Go version, OS, and CLI versions involved.

## Code of Conduct

This project follows the [Contributor Covenant v2.1](https://www.contributor-covenant.org/version/2/1/code_of_conduct/). Be respectful, constructive, and collaborative.

## License

By contributing you agree your contributions will be licensed under the MIT License.
