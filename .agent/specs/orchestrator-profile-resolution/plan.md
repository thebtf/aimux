# Implementation Plan: Orchestrator Profile-Aware Command Resolution

**Spec:** .agent/specs/orchestrator-profile-resolution/spec.md
**Created:** 2026-04-06
**Status:** Draft

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Interface | `types.CLIResolver` | Same package as `types.Executor`, `types.Strategy` — no new dependencies |
| Implementation | `pkg/resolve/resolver.go` | New package to avoid circular deps; imports `config` |
| Extraction target | `pkg/resolve/args.go` | `CommandBinary`, `CommandBaseArgs`, `BuildArgs` extracted from `server.go` |

## Architecture

```
types.CLIResolver (interface)          ← orchestrator depends on this
       ↑
resolve.ProfileResolver (impl)         ← server creates this, passes to strategies
       ↑
config.CLIProfile + driver.Registry    ← implementation uses these
```

**Data flow for orchestrator spawn:**
1. Strategy receives `CLIResolver` in constructor
2. Strategy calls `resolver.ResolveSpawnArgs("codex", prompt)` 
3. Resolver looks up profile, calls `BuildArgs`, checks stdin threshold
4. Returns complete `SpawnArgs` — strategy passes to `executor.Run()`

**Nil-resolver fallback (tests):**
```go
func (s *SomeStrategy) resolveOrFallback(cli, prompt string) types.SpawnArgs {
    if s.resolver != nil {
        args, err := s.resolver.ResolveSpawnArgs(cli, prompt)
        if err == nil {
            return args
        }
    }
    // Legacy fallback for tests with mock executors
    return types.SpawnArgs{CLI: cli, Command: cli, Args: []string{"-p", prompt}}
}
```

## Data Model

No new data entities. `CLIResolver` is a stateless interface:

```go
// types/interfaces.go
type CLIResolver interface {
    ResolveSpawnArgs(cli string, prompt string) (SpawnArgs, error)
}
```

## API Contracts

### CLIResolver.ResolveSpawnArgs
- **Input:** `cli string` (profile name, e.g. "codex"), `prompt string`
- **Output:** `SpawnArgs` with Command, Args, Stdin, CompletionPattern resolved from profile
- **Errors:** profile not found → `fmt.Errorf("CLI %q not configured", cli)`

### resolve.ProfileResolver (implementation)
- **Constructor:** `NewProfileResolver(profiles map[string]*config.CLIProfile) *ProfileResolver`
- **Method:** `ResolveSpawnArgs(cli, prompt string) (types.SpawnArgs, error)` — looks up profile, calls `BuildArgs`, applies stdin threshold

### Extracted functions (resolve package)
- `CommandBinary(base string) string` — first word of command.base
- `CommandBaseArgs(base string) []string` — remaining words
- `BuildPromptArgs(profile *config.CLIProfile, prompt string) []string` — base args + prompt flag resolution (core logic)

Design: `BuildPromptArgs` is the shared core — handles base args from command.base and per-CLI prompt flag. Server's `buildArgs` wraps it, adding model/effort/readOnly params on top. No duplication of prompt resolution logic.

## File Structure

```
pkg/
  resolve/
    resolver.go      ← CLIResolver interface impl (ProfileResolver)
    args.go          ← CommandBinary, CommandBaseArgs, BuildPromptArgs (extracted)
    resolver_test.go ← unit tests
  types/
    interfaces.go    ← CLIResolver interface added
  orchestrator/
    consensus.go     ← uses resolver.ResolveSpawnArgs instead of hardcoded
    dialog.go        ← same
    debate.go        ← same
    pair.go          ← same
    audit.go         ← same
  server/
    server.go        ← creates ProfileResolver, passes to strategies; buildArgs calls resolve.BuildPromptArgs
test/
  e2e/
    testcli_test.go  ← new multi-CLI orchestrator tests
```

## Phases

### Phase 1: Extract + Interface (foundation)
- Create `pkg/resolve/args.go` — extract `CommandBinary`, `CommandBaseArgs`, `BuildPromptArgs` from `server.go`
- Add `CLIResolver` interface to `types/interfaces.go`
- Create `pkg/resolve/resolver.go` — `ProfileResolver` implementation
- Update `server.go` `buildArgs` to call `resolve.BuildPromptArgs` (no duplication)
- Unit tests for `resolve` package
- **Verify:** `go build ./...` + `go test ./...` — all 250 tests pass

### Phase 2: Wire orchestrator strategies (the actual fix)
- Update all 5 strategy constructors to accept `CLIResolver`
- Add `resolveOrFallback` helper method to each strategy
- Replace all 14 `SpawnArgs{Command: cli, Args: ["-p", prompt]}` with `resolveOrFallback` call
- Update `server.go` to pass `ProfileResolver` to strategy constructors
- **Verify:** all 250 tests pass (mock tests use nil resolver → fallback)

### Phase 3: E2E verification + CompletionPattern
- Add `CompletionPattern` to ResolveSpawnArgs output
- New e2e tests: multi-CLI consensus and dialog with testcli emulators
- Verify testcli codex+gemini in consensus produces combined output
- **Verify:** all tests pass including new multi-CLI e2e tests

## Library Decisions

| Component | Library | Version | Rationale |
|-----------|---------|---------|-----------|
| All | stdlib | — | Pure Go refactoring, no external dependencies |

## Unknowns and Risks

| Unknown | Impact | Resolution Strategy |
|---------|--------|-------------------|
| Mock tests break due to changed constructor signatures | MED | FR-5 nil-resolver fallback ensures backward compat |
| server.go buildArgs has extra params (model, effort, readOnly) not needed by orchestrator | LOW | BuildPromptArgs handles common core; server wraps with extra params |

## Constitution Compliance

No constitution.md exists for this project. Plan follows existing patterns:
- Interface in types package (same as Executor, Strategy)
- Constructor injection (same as strategy constructors)
- Extracted shared logic in dedicated package (same as parser, prompt packages)
