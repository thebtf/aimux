# Tasks: aimux v3 Gap Closure

**Spec:** spec.md
**Plan:** plan.md
**Generated:** 2026-04-05
**Rule:** NO task marked [x] without its VERIFY-* counterpart also [x].

## Phase 1: Critical Stubs (P0)

- [x] T001 [P] Replace `_ = pairParams` with orchestrator.Execute("pair_coding") call, handle result as ReviewReport JSON in D:\Dev\aimux\pkg\server\server.go:434-475
- [x] VERIFY-T001 Confirm: (a) `_ = pairParams` line deleted, (b) orchestrator.Execute called with "pair_coding", (c) ReviewReport fields in response, (d) `go test ./pkg/server/...` passes
- [x] T002 [P] Replace agents(action="run") JSON stub with real execution: create session+job, spawn CLI with fullPrompt, return output in D:\Dev\aimux\pkg\server\server.go:705-725
- [x] VERIFY-T002 Confirm: (a) `"delegating to exec"` string removed, (b) executor.Run called with agent prompt, (c) different inputs produce different outputs in test, (d) `go test ./pkg/server/...` passes

---

**Checkpoint:** Both P0 stubs eliminated. exec(coding) and agents(run) produce real results.

## Phase 2: Integration Wiring (P1)

- [x] T003 Wire session resume: lookup session by ID, validate CLI, pass context to spawn in D:\Dev\aimux\pkg\server\server.go:390-396
- [x] VERIFY-T003 Confirm: (a) `_ = sessionID` line deleted, (b) session lookup from DB, (c) CLI validation logic present, (d) `go test ./pkg/server/...` passes
- [x] T004 [P] Wire bootstrap prompt injection: call prompt engine with role, prepend TOML content to prompt before spawn in D:\Dev\aimux\pkg\server\server.go (exec handler, before buildArgs)
- [x] VERIFY-T004 Confirm: (a) prompt engine called with role, (b) TOML content prepended to prompt, (c) missing TOML handled gracefully, (d) `go test ./pkg/server/...` passes
- [x] T005 [P] Wire stdin piping: check ShouldUseStdin(), call BuildStdinArgs() for long prompts in D:\Dev\aimux\pkg\server\server.go (exec handler, after buildArgs)
- [x] VERIFY-T005 Confirm: (a) StdinThreshold check present, (b) Stdin field populated for long prompts, (c) prompt arg empty when stdin used, (d) `go test ./pkg/server/...` passes
- [x] T006 Parse audit validator response instead of discarding: extract per-finding verdicts from result.Content in D:\Dev\aimux\pkg\orchestrator\audit.go:190-201
- [x] VERIFY-T006 Confirm: (a) `_ = result.Content` line deleted, (b) content parsed for verdicts, (c) findings get mixed confidence values, (d) `go test ./pkg/orchestrator/...` passes

---

**Checkpoint:** All P1 integrations wired. Session resume, bootstrap, stdin, audit validation all functional.

## Phase 3: Cleanup + Polish (P2-P3)

- [x] T007 Fix SparkDetector: parse codex --version output instead of discarding in D:\Dev\aimux\pkg\driver\spark.go:39-58
- [x] VERIFY-T007 Confirm: (a) `_ = output` line deleted, (b) version string parsed and checked, (c) detection uses len(version)>0, (d) `go test ./pkg/driver/...` passes
- [x] T008 Delete old deepresearch.go placeholder (superseded by client.go) in D:\Dev\aimux\pkg\tools\deepresearch\deepresearch.go
- [x] VERIFY-T008 Confirm: (a) deepresearch.go deleted, (b) tests rewritten to test real Client+Cache, (c) `go build ./...` passes, (d) `go test ./pkg/tools/deepresearch/...` passes
- [x] T009 Add completion pattern matching in pipe executor read loop in D:\Dev\aimux\pkg\executor\pipe\pipe.go
- [x] VERIFY-T009 Confirm: (a) CompletionPattern compiled as regex, (b) goroutine polls stdout for match, (c) process killed on match, (d) `go test ./pkg/executor/pipe/...` passes
- [x] T010 Update PTY/ConPTY Start() error messages to explain architectural choice (Pipe handles persistent sessions) in D:\Dev\aimux\pkg\executor\pty\pty.go and D:\Dev\aimux\pkg\executor\conpty\conpty.go
- [x] VERIFY-T010 Confirm: (a) error messages explain Pipe executor, (b) doc comments explain design, (c) `go build ./...` passes

---

**Checkpoint:** All stubs eliminated. Dead code removed. Completion patterns wired.

## Phase 4: Final Verification

- [x] T011 Run `scripts/stub-grep.sh` on pkg/ — must report 0 true-positive stubs
- [x] VERIFY-T011 Confirm: stub-grep output "PASSED: 0 stub patterns found"
- [x] T012 Run `go test -count=1 ./...` — all 18 packages pass, 0 failures (race detector unavailable on Windows without CGO)
- [x] VERIFY-T012 Confirm: 18 packages PASS, zero failures
- [x] T013 Run `go build ./...` — clean compilation, zero warnings
- [x] VERIFY-T013 Confirm: build output clean (empty = success)
- [x] T014 Update gap-analysis.md marking all 13 gaps as RESOLVED, update CONTINUITY.md
- [x] VERIFY-T014 Confirm: gap-analysis header shows "ALL 13 GAPS RESOLVED", CONTINUITY updated

---

**Checkpoint:** Production-ready. All gaps closed. All tests green. Zero stubs.

## Phase 5: Clarify/Analyze Fixes (post-hoc)

- [x] T015 Session resume: reject sessions in terminal states (failed/completed) — from Clarify C3 in D:\Dev\aimux\pkg\server\server.go
- [x] VERIFY-T015 Confirm: status check added before CLI validation, `go test ./pkg/server/...` passes
- [x] T016 CompletionPattern: use regexp.Compile (not MustCompile) for graceful invalid regex handling — from Clarify C4 in D:\Dev\aimux\pkg\executor\pipe\pipe.go
- [x] VERIFY-T016 Confirm: MustCompile replaced with Compile + nil check, `go test ./pkg/executor/pipe/...` passes

---

**Checkpoint:** Clarify/Analyze findings addressed. Spec, plan, tasks fully consistent.

## Dependencies

- T001 and T002 are independent (parallel)
- T003, T004, T005, T006 are independent (parallel), depend on Phase 1 completion
- T007, T008, T009, T010 are independent (parallel), no phase dependency
- T011-T014 depend on all prior phases

## Execution Strategy

- **Parallel:** T001||T002, T003||T004||T005||T006, T007||T008||T009||T010
- **Commit:** One commit per completed+verified task pair
- **Total:** 10 implementation tasks + 10 verification tasks + 4 final checks + 4 final verifications = 28 items
