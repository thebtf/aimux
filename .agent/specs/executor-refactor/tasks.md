# Tasks: Executor Refactor

**Spec:** .agent/specs/executor-refactor/spec.md
**Plan:** .agent/specs/executor-refactor/plan.md
**Generated:** 2026-04-09

## Phase 0: Planning

- [x] P001 Assign executors
  AC: all tasks reviewed

## Phase 1: Shared Components (FR-1, FR-2, FR-7)

- [ ] T001 Extract `safeBuffer` from `pkg/executor/pipe/pipe.go` to `pkg/executor/safebuf.go`. Update pipe.go to import from new location. Remove duplicate from conpty.go if present.
  AC: `safebuf.go` has `safeBuffer` with `Write`, `String`, `Len`, `Reset` methods · pipe.go compiles using shared type · conpty.go compiles using shared type · `go build ./...` passes · 3 tests in `safebuf_test.go` (concurrent writes, String thread-safe, Reset clears) · swap body→return null ⇒ tests MUST fail

- [ ] T002 Create `ProcessManager` in `pkg/executor/process.go`: `Spawn(cmd *exec.Cmd) *ProcessHandle`, `Kill(h *ProcessHandle)`, `IsAlive(h *ProcessHandle) bool`, `Cleanup(h *ProcessHandle)`, `Shutdown()`. ProcessHandle: PID, cmd, stdout/stderr ReadCloser, done chan, exitCode, startedAt. Track handles in sync.Map.
  AC: Spawn starts process + returns handle with PID > 0 · Kill sends SIGTERM then SIGKILL after 5s · IsAlive returns false after Kill · Shutdown kills all tracked · Cleanup removes from tracking · 5 tests · swap body→return null ⇒ tests MUST fail

- [ ] T003 Create `IOManager` in `pkg/executor/iomanager.go`: `NewIOManager(stdout io.Reader, pattern string)`, `StreamLines()` (goroutine — reads line-by-line into safeBuffer, checks pattern), `PatternMatched() <-chan struct{}`, `Done() <-chan struct{}`, `Collect() string`, `Drain(timeout time.Duration)`. Apply `pipeline.StripANSI` per line.
  AC: StreamLines reads all lines from reader · pattern match signals within 1 line of appearance · Collect returns accumulated content · Drain waits up to timeout for EOF · ANSI stripped · WriteStdin pipes data to process stdin · no 100ms polling (line-based) · 6 tests · swap body→return null ⇒ tests MUST fail

- [ ] G001 VERIFY Phase 1 (T001–T003) — BLOCKED until T001–T003 all [x]
  RUN: `go test ./pkg/executor/ -v -count=1`. Call code-review lite on safebuf.go, process.go, iomanager.go.
  CHECK: All 3 files exist with tests. Build passes. No duplicate safeBuffer.
  ENFORCE: Zero stubs. Zero TODOs. Every parameter influences output.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Shared components ready — ProcessManager + IOManager + safeBuffer tested independently.

## Phase 2: Pipe Executor Refactor (FR-3, FR-4, FR-5)

**Goal:** Rewrite pipe.go Run() to use ProcessManager + IOManager as proof-of-concept.
**Independent Test:** `go test ./pkg/executor/pipe/ -v -count=1` — all existing + new tests pass.

- [ ] T004 [US1] Rewrite `pkg/executor/pipe/pipe.go` `Run()`: replace inline process management with `ProcessManager.Spawn()`, replace inline safeBuffer+pattern polling with `IOManager`, replace 4-way select with `iom.PatternMatched() | handle.Done() | timeout | ctx.Done()`.
  AC: Run() body < 40 lines (was ~80) · no `exec.Command` in Run() (delegated to ProcessManager) · no `regexp.Compile` in Run() (delegated to IOManager) · no `safeBuffer` definition in pipe.go · all 12 existing pipe tests pass · 1 new test: cancel returns partial output · swap body→return null ⇒ tests MUST fail

- [ ] T005 [P] [US2] Add partial output test to `pkg/executor/pipe/pipe_test.go`: spawn slow process, cancel after 1s, verify Result.Content is non-empty and Result.Partial is true.
  AC: test creates process that outputs lines slowly · cancel after 1s · Result.Partial == true · Result.Content contains at least 1 line · swap body→return null ⇒ tests MUST fail

- [ ] G002 VERIFY Phase 2 (T004–T005) — BLOCKED until T004–T005 all [x]
  RUN: `go test ./pkg/executor/pipe/ -v -count=1`. Code-review lite on pipe.go.
  CHECK: Run() uses ProcessManager + IOManager. No inline process management.
  ENFORCE: Zero stubs. Pipe tests pass. No regressions in other packages.
  RESOLVE: Fix ALL findings.

---

**Checkpoint:** Pipe executor refactored — pattern validated.

## Phase 3: ConPTY + PTY Refactor (FR-3, FR-7)

**Goal:** Apply same pattern to ConPTY and PTY executors.
**Independent Test:** `go test ./pkg/executor/conpty/ ./pkg/executor/pty/ -v -count=1`

- [ ] T006 [P] [US1] Rewrite `pkg/executor/conpty/conpty.go` `Run()` to use ProcessManager + IOManager. Remove inline safeBuffer, completion pattern goroutine, 4-way select.
  AC: Run() body < 40 lines · no `exec.Command` in Run() · no inline safeBuffer · all 6 existing conpty tests pass · swap body→return null ⇒ tests MUST fail

- [ ] T007 [P] [US1] Rewrite `pkg/executor/pty/pty.go` `Run()` to use ProcessManager + IOManager. Remove inline process management.
  AC: Run() body < 40 lines · no `exec.Command` in Run() · all existing pty tests pass (8 pass, platform-skipped as expected) · swap body→return null ⇒ tests MUST fail

- [ ] G003 VERIFY Phase 3 (T006–T007) — BLOCKED until T006–T007 all [x]
  RUN: `go test ./pkg/executor/... -v -count=1`. Code-review lite on conpty.go, pty.go.
  CHECK: All 3 executors use shared ProcessManager + IOManager. No duplicated logic.
  ENFORCE: Zero stubs. All executor tests pass.
  RESOLVE: Fix ALL findings.

---

**Checkpoint:** All 3 executors refactored — unified architecture.

## Phase 4: Persistent Session + Shutdown (FR-5, US3)

**Goal:** Update Start/Send/Stream to use IOManager. Wire ProcessManager.Shutdown into server.
**Independent Test:** `go test ./pkg/executor/... -v` + server shutdown test.

- [ ] T008 [US3] Update persistent session implementations (pipe `Start()/Send()`, conpty `Start()/Send()`) to use IOManager for I/O instead of direct pipe reads. ProcessManager tracks session processes for cleanup.
  AC: Send() uses IOManager.StreamLines + PatternMatched · session processes tracked by ProcessManager · Shutdown() kills persistent sessions · 2 tests: session send works, shutdown kills session · swap body→return null ⇒ tests MUST fail

- [ ] T009 [US3] Wire `ProcessManager.Shutdown()` into `pkg/server/server.go` `Shutdown()` method. Call pm.Shutdown() before store.Close().
  AC: Server.Shutdown() calls ProcessManager.Shutdown() · all tracked processes killed on shutdown · 1 test: verify shutdown cleans up · swap body→return null ⇒ tests MUST fail

- [ ] G004 VERIFY Phase 4 (T008–T009) — BLOCKED until T008–T009 all [x]
  RUN: `go test ./pkg/executor/... ./pkg/server/ -v -count=1`.
  CHECK: Persistent sessions use IOManager. Shutdown cleans up.
  ENFORCE: Zero stubs. All tests pass.
  RESOLVE: Fix ALL findings.

---

**Checkpoint:** Persistent sessions refactored. Server shutdown cleans up all processes.

## Phase 5: Integration + Smoke Test

- [ ] T010 Full regression: `go test ./... -timeout 300s` + `go vet ./...` + build binary
  AC: all packages pass · no vet warnings · binary builds · zero regressions from baseline

- [ ] T011 E2e tests: `go test ./test/e2e/ -count=1 -timeout 300s`
  AC: 59/59 e2e tests pass · no timeouts · no new failures

- [ ] T012 Smoke test: rebuild binary, reconnect aimux-dev MCP, test codex with role=codereview (reasoning=high) sync mode, verify response in < 20s total (CLI ~10s + aimux overhead < 10s).
  AC: exec role=codereview async=false timeout=30 returns completed (not timeout) · content is non-empty · duration < 20s · agent auto-select "reviewer" works

- [ ] G005 VERIFY Phase 5 (T010–T012) — BLOCKED until T010–T012 all [x]
  RUN: Full test suite + smoke tests.
  CHECK: Zero regressions. All smoke tests pass. Overhead < 10s.
  RESOLVE: Fix ALL findings.

---

**Checkpoint:** Executor refactor complete — control plane / data plane separated.

## Dependencies

- G001 blocks Phase 2+ (shared components must exist)
- Phase 2 and Phase 3 are independent after G001 (but Phase 2 validates pattern first)
- G002 + G003 block Phase 4 (all executors must be refactored before session work)
- G004 blocks Phase 5

## Execution Strategy

- **MVP:** Phase 0-2 (pipe executor refactored — validates the pattern)
- **Parallel:** T001 sequential → T002||T003, T006||T007, T004||T005
- **Commit:** one per phase
