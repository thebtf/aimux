# AGENTS.md — aimux v3

## Stack Configuration

```yaml
STACKS: [GO]
```

## Project Context

aimux is an MCP server that multiplexes AI CLI tools. It receives MCP tool calls
(exec, status, sessions, dialog, consensus, debate, audit, think, investigate, deepresearch, agents)
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
