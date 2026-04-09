# Feature: Orchestrator Profile-Aware Command Resolution

**Slug:** orchestrator-profile-resolution
**Created:** 2026-04-06
**Status:** Draft
**Author:** AI Agent (reviewed by user)

## Overview

Orchestrator strategies construct CLI spawn arguments with hardcoded assumptions instead of using CLI profile data. This causes incorrect binary names, wrong prompt flags, missing stdin piping, and missing completion patterns for all multi-CLI operations (consensus, debate, dialog, pair coding, audit). This feature unifies command resolution to use a single source of truth.

## Context

The exec handler (`server.go:556-568`) correctly resolves CLI commands using:
- `commandBinary(profile.Command.Base)` — extracts binary from multi-word command base
- `commandBaseArgs(profile.Command.Base)` — extracts subcommand/default flags
- `buildArgs(profile, model, effort, readOnly, prompt)` — respects per-CLI prompt flag, model flag, reasoning config
- Stdin piping when `len(prompt) > profile.StdinThreshold`
- `profile.CompletionPattern` (though not currently passed to SpawnArgs)

All 6 orchestrator strategies bypass this entirely. Each constructs SpawnArgs with:
```go
Command: cli,                    // CLI name, not binary path
Args:    []string{"-p", prompt}, // hardcoded -p flag
```

This works accidentally for CLIs where the binary name equals the profile name AND the CLI accepts `-p`. It fails for:
- testcli emulators (binary "testcli", command "testcli codex --json --full-auto")
- codex (positional prompt, not `-p`)
- aider (`--message`, not `-p`)
- goose (`-t`, not `-p`)
- crush (positional prompt)
- Any CLI with stdin piping needs (synthesis prompts can be 10K+ chars)

**11 call sites across 5 files. ~40% of aimux functionality affected.**
(consensus=2, dialog=1, debate=3, pair=2, audit=2, + debate synthesis=1 uses participants[0])

## Functional Requirements

### FR-1: Profile-Aware SpawnArgs Construction
All orchestrator strategies MUST resolve Command, Args, Stdin, and CompletionPattern from CLI profile data, using the same resolution logic as the exec handler. No hardcoded binary names or prompt flags.

### FR-2: CLIResolver Interface
A `CLIResolver` interface in `types` package with a single method: `ResolveSpawnArgs(cli string, prompt string) (SpawnArgs, error)`. Returns fully-resolved SpawnArgs including Command, Args (with correct prompt flag), Stdin (if threshold exceeded), CompletionPattern. Passed to each strategy constructor alongside `types.Executor`. When nil (test scenario), strategies fall back to legacy behavior.

### FR-3: Stdin Piping for Long Prompts
When a prompt exceeds the CLI's `StdinThreshold`, orchestrator MUST pipe it via `args.Stdin` instead of command-line argument. This is critical for synthesis prompts that concatenate full conversation history.

### FR-4: Completion Pattern Propagation
Orchestrator MUST set `args.CompletionPattern` from the CLI profile for CLIs that need it. Without this, the pipe executor cannot detect completion for CLIs that don't exit after responding.

### FR-5: Backward Compatibility
Existing orchestrator mock tests MUST continue to work without modification. The resolver MUST be injectable (interface), with a fallback that preserves current behavior for tests.

## Non-Functional Requirements

### NFR-1: No Circular Dependencies
The resolver interface MUST live in the `types` package (or a new `resolve` package). It MUST NOT import `config` or `driver` — the implementation lives in the server layer.

### NFR-2: Single Source of Truth
The resolution logic MUST be extracted from `server.go` into a shared function. Both exec handler and orchestrator resolver MUST call the same code. No duplication.

### NFR-3: Zero Performance Regression
Profile lookup is O(1) map access. No additional I/O or process spawning.

## User Stories

### US1: Orchestrator Uses Correct Binary (P1)
**As a** developer using aimux with testcli emulators, **I want** orchestrator strategies to resolve the correct binary for each CLI, **so that** multi-CLI operations (consensus, debate, dialog) work with any CLI configuration.

**Acceptance Criteria:**
- [ ] Consensus with codex+gemini testcli emulators returns combined output from both
- [ ] Dialog with codex+gemini alternates between both CLIs
- [ ] Pair coding with codex driver + gemini reviewer uses correct commands for each

### US2: Orchestrator Uses Correct Prompt Flag (P1)
**As a** developer, **I want** orchestrator strategies to use per-CLI prompt flags (positional, `-p`, `--message`, `-t`), **so that** each CLI receives its prompt in the format it expects.

**Acceptance Criteria:**
- [ ] Codex receives positional prompt (no flag prefix) — verified via orchestrator e2e with testcli
- [ ] Gemini receives `-p` prompt — verified via orchestrator e2e with testcli
- [ ] resolve.BuildPromptArgs unit tests cover all prompt flag types (positional, short, long)

### US3: Synthesis Prompts Piped via Stdin (P2)
**As a** developer, **I want** long synthesis prompts to be piped via stdin when they exceed the CLI's threshold, **so that** Windows 8191 char limit doesn't truncate prompts.

**Acceptance Criteria:**
- [ ] Synthesis prompt >6000 chars piped via stdin for codex
- [ ] Regular short prompts still passed as arguments

### US4: Completion Pattern Set for CLIs That Need It (P3)
**As a** developer, **I want** orchestrator to set CompletionPattern from CLI profile, **so that** CLIs that don't exit after responding still have their output captured correctly.

**Acceptance Criteria:**
- [ ] SpawnArgs.CompletionPattern set from profile when available
- [ ] CLIs without completion pattern work as before (process exit = complete)

## Edge Cases

- CLI profile not found for a CLI name in the resolver → return error (not silent fallback to hardcoded behavior)
- Orchestrator created with nil resolver (test scenario) → use legacy behavior (Command=cli, Args=["-p", prompt])
- CompletionPattern is empty string → omit from SpawnArgs (no change from current)
- Multiple CLIs in one strategy with different prompt flags → each resolved independently
- Synthesis prompt crosses stdin threshold but regular prompt doesn't → only synthesis piped

## Out of Scope

- Changing orchestrator strategy logic (turn management, blinding, stance assignment)
- Adding new strategies
- Changing the Executor interface
- Parser integration (parser.ParseJSONL is a separate concern)
- Changing how server.go selects CLIs for orchestrator strategies

## Dependencies

- `config.CLIProfile` struct (exists)
- `commandBinary()` / `commandBaseArgs()` (exist in server.go, need extraction)
- `buildArgs()` (exists in server.go, need extraction)
- testcli emulators (exist in cmd/testcli/)

## Success Criteria

- [ ] All 11 orchestrator SpawnArgs call sites use profile-resolved values
- [ ] 0 hardcoded `-p` flags in orchestrator code
- [ ] 0 `Command: cli` (raw CLI name) in orchestrator code
- [ ] New e2e tests: multi-CLI consensus and dialog with testcli emulators
- [ ] All 250 existing tests pass (0 regressions)
- [ ] commandBinary/commandBaseArgs/buildArgs extracted to shared package (not duplicated)

## Clarifications

### Session 2026-04-06

| # | Category | Question | Resolution | Date |
|---|----------|----------|------------|------|
| C1 | Domain/Data Model | What is the resolver interface signature? | `CLIResolver` interface with single method `ResolveSpawnArgs(cli string, prompt string) (SpawnArgs, error)` — returns fully-resolved SpawnArgs. Implementation in server layer calls `commandBinary`, `buildArgs`, checks stdin threshold. | 2026-04-06 |
| C2 | Reliability | When resolver returns error (CLI not found), fail or skip? | Fail the entire strategy operation with error. Silent fallback masks misconfigurations. | 2026-04-06 |
| C3 | Integration | Resolver passed to strategy constructors or injected via Orchestrator? | Passed to each strategy constructor — same pattern as `types.Executor`. Consistent with existing DI approach. | 2026-04-06 |
| C4 | Terminology | Canonical name for the interface? | `CLIResolver` in `types` package. Method: `ResolveSpawnArgs`. Not "builder", "factory", or generic "resolver". | 2026-04-06 |

## Open Questions

None — all clarifications resolved.
