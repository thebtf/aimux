# Tasks: Orchestrator Profile-Aware Command Resolution

**Spec:** .agent/specs/orchestrator-profile-resolution/spec.md
**Plan:** .agent/specs/orchestrator-profile-resolution/plan.md
**Generated:** 2026-04-06

## Phase 1: Foundation — Extract + Interface

- [x] T001 Add `CLIResolver` interface to `pkg/types/interfaces.go`
- [x] T002 Create `pkg/resolve/args.go` — extract `CommandBinary`, `CommandBaseArgs`, `BuildPromptArgs` from `pkg/server/server.go`
- [x] T003 Create `pkg/resolve/resolver.go` — `ProfileResolver` implementation with `ResolveSpawnArgs(cli, prompt) (SpawnArgs, error)`
- [x] T004 Update `pkg/server/server.go` — replace inline `commandBinary`/`commandBaseArgs`/`buildArgs` with calls to `resolve` package
- [x] T005 [P] Create `pkg/resolve/args_test.go` — unit tests for `CommandBinary`, `CommandBaseArgs`, `BuildPromptArgs`
- [x] T006 [P] Create `pkg/resolve/resolver_test.go` — unit tests for `ProfileResolver.ResolveSpawnArgs` (profile lookup, prompt flag, stdin piping, completion pattern, unknown CLI error)

---

**Checkpoint:** Foundation complete — `resolve` package extracted, `CLIResolver` interface defined, `server.go` uses shared functions. `go build ./...` + `go test ./...` = 250 tests pass.

## Phase 2: Wire Orchestrator Strategies — US1 + US2

**Goal:** All 14 orchestrator SpawnArgs call sites use profile-resolved values. Correct binary, correct prompt flag.
**Independent Test:** `go test ./pkg/orchestrator/...` passes with nil resolver (fallback), `go test ./...` passes with ProfileResolver wired in server.

- [x] T007 [US1] [US2] Add shared `resolveOrFallback` helper in `pkg/orchestrator/helpers.go` and update `pkg/orchestrator/consensus.go` — add `CLIResolver` to constructor, replace 2 SpawnArgs constructions
- [x] T008 [P] [US1] [US2] Update `pkg/orchestrator/dialog.go` — add `CLIResolver` to constructor, replace 1 SpawnArgs construction with `resolveOrFallback`
- [x] T009 [P] [US1] [US2] Update `pkg/orchestrator/debate.go` — add `CLIResolver` to constructor, replace 3 SpawnArgs constructions (2 turns + 1 synthesis) with `resolveOrFallback`
- [x] T010 [P] [US1] [US2] Update `pkg/orchestrator/pair.go` — add `CLIResolver` to constructor, replace 2 SpawnArgs constructions with `resolveOrFallback`
- [x] T011 [P] [US1] [US2] Update `pkg/orchestrator/audit.go` — add `CLIResolver` to constructor, replace 2 SpawnArgs constructions with `resolveOrFallback`
- [x] T012 [US1] Update `pkg/server/server.go` — create `ProfileResolver`, pass to all strategy constructors

---

**Checkpoint:** All orchestrator strategies profile-aware. 0 hardcoded `-p` flags. 0 `Command: cli`. Existing mock tests pass via nil-resolver fallback. `go test ./...` = all pass.

## Phase 3: E2E Verification — US1 + US3 + US4

**Goal:** Prove orchestrator works with testcli emulators (different binaries, different prompt flags). Stdin piping and CompletionPattern verified.
**Independent Test:** Multi-CLI consensus/dialog e2e tests with testcli codex+gemini.

- [x] T013 [US1] Add e2e test `TestE2E_Orchestrator_ConsensusMultiCLI` — consensus with codex+gemini testcli emulators in `test/e2e/testcli_test.go`
- [x] T014 [P] [US1] Add e2e test `TestE2E_Orchestrator_DialogMultiCLI` — dialog with codex+gemini testcli emulators in `test/e2e/testcli_test.go`
- [x] T015 [US3] Add e2e test `TestE2E_Orchestrator_SynthesisStdinPiping` — consensus with long topic triggering stdin piping in `test/e2e/testcli_test.go`
- [x] T016 [US4] Verify CompletionPattern propagation — check SpawnArgs.CompletionPattern set in `pkg/resolve/resolver_test.go`

---

**Checkpoint:** Full e2e proof that orchestrator works with real CLI profile resolution. All tests pass.

## Phase 4: Polish

- [x] T017 Update `.agent/CONTINUITY.md` with completed feature
- [x] T018 Verify: `grep -rn "Command:.*cli\b" pkg/orchestrator/` returns 0 matches (no raw CLI names)
- [x] T019 Verify: `grep -rn '"-p"' pkg/orchestrator/` returns 0 matches (no hardcoded `-p`)

## Dependencies

- T001 blocks T003 (interface before implementation)
- T002 blocks T003, T004 (extracted functions before resolver and server update)
- T003 blocks T007-T012 (resolver exists before wiring)
- T004 blocks T012 (server uses resolve package before creating ProfileResolver)
- T005, T006 independent of each other [P]
- T007-T011 independent of each other [P] (different files, no shared state)
- T012 blocks T013-T015 (server wired before e2e tests)
- T007-T011 each block T012 (strategies updated before server wiring)

## Execution Strategy

- **MVP scope:** Phase 1-2 (T001-T012) — all 14 call sites fixed
- **Parallel opportunities:** T005||T006, T007||T008||T009||T010||T011, T013||T014
- **Commit strategy:** One commit per phase (3 total + polish)
