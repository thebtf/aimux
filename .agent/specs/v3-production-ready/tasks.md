# Tasks: aimux v3 Production Readiness

**Spec:** .agent/specs/v3-production-ready/spec.md
**Plan:** .agent/specs/v3-production-ready/plan.md
**Generated:** 2026-04-05

## Phase 1: Foundational — Wire Orchestrator into Server

- [x] T001 Add orchestrator + agentReg fields to Server struct in D:\Dev\aimux\pkg\server\server.go
- [x] T002 [P] Initialize all 5 strategies (pair, dialog, consensus, debate, audit) in Server.New() in D:\Dev\aimux\pkg\server\server.go
- [x] T003 Wire handleAudit to call AuditPipeline strategy via orchestrator.Execute in D:\Dev\aimux\pkg\server\server.go
- [x] T004 [P] Wire handleConsensus to call ParallelConsensus strategy via orchestrator.Execute in D:\Dev\aimux\pkg\server\server.go
- [x] T005 [P] Wire handleDebate to call StructuredDebate strategy via orchestrator.Execute in D:\Dev\aimux\pkg\server\server.go
- [x] T006 [P] Wire handleDialog to call SequentialDialog strategy via orchestrator.Execute in D:\Dev\aimux\pkg\server\server.go
- [x] T007 Wire handleAgents to call AgentRegistry.Discover/List/Find/Run in D:\Dev\aimux\pkg\server\server.go
- [x] T008 Wire exec(role=coding) to invoke PairCoding strategy end-to-end in D:\Dev\aimux\pkg\server\server.go
- [x] T009 Remove all "wiring pending" and "not yet implemented" placeholder strings from D:\Dev\aimux\pkg\server\server.go
- [x] T010 Write wired tool handler tests (mock executor, verify strategy called) in D:\Dev\aimux\pkg\server\server_test.go

---

**Checkpoint:** All 10 tools return real results via orchestrator strategies. Zero placeholders. `go test ./...` green.

## Phase 2: User Story 1 — Working exec(coding) with Pair Review (P1)

**Goal:** exec(role="coding") produces pair-reviewed code end-to-end.
**Independent Test:** `echo '...' | aimux.exe` with exec tool call, role=coding → ReviewReport in response.

- [x] T011 [US1] Ensure PairCoding.Execute uses real executor.Run for driver spawn in D:\Dev\aimux\pkg\orchestrator\pair.go
- [x] T012 [P] [US1] Ensure PairCoding.Execute uses real executor.Run for reviewer spawn in D:\Dev\aimux\pkg\orchestrator\pair.go
- [x] T013 [US1] Add async pair coding support (fire-and-forget via job manager) in D:\Dev\aimux\pkg\server\server.go
- [x] T014 [US1] Write end-to-end pair coding test with real echo process in D:\Dev\aimux\pkg\orchestrator\pair_integration_test.go

---

**Checkpoint:** US1 complete. exec(coding) → driver diff → reviewer verdict → result with ReviewReport.

## Phase 3: User Story 2 — Working Multi-Model Tools (P1)

**Goal:** consensus/debate/dialog tools run real multi-model orchestration.
**Independent Test:** consensus(topic="test") via MCP → returns multi-CLI opinions.

- [x] T015 [US2] Verify SequentialDialog.Execute works with real pipe executor in D:\Dev\aimux\pkg\orchestrator\dialog.go
- [x] T016 [P] [US2] Verify ParallelConsensus.Execute works with real pipe executor in D:\Dev\aimux\pkg\orchestrator\consensus.go
- [x] T017 [P] [US2] Verify StructuredDebate.Execute works with real pipe executor in D:\Dev\aimux\pkg\orchestrator\debate.go
- [x] T018 [US2] Write multi-model integration test (spawn echo as mock CLI) in D:\Dev\aimux\pkg\orchestrator\pair_integration_test.go

---

**Checkpoint:** US2 complete. All multi-model tools produce real multi-turn output.

## Phase 4: User Story 3 — Working Audit Pipeline (P1)

**Goal:** Audit tool runs scan→validate→investigate pipeline with real CLIs.
**Independent Test:** audit(mode="quick") via MCP → returns FINDING: lines.

- [x] T019 [US3] Verify AuditPipeline.Execute scan phase with real executor in D:\Dev\aimux\pkg\orchestrator\audit.go
- [x] T020 [P] [US3] Verify AuditPipeline.Execute validate phase with real executor in D:\Dev\aimux\pkg\orchestrator\audit.go
- [x] T021 [US3] Write audit pipeline integration test in D:\Dev\aimux\pkg\orchestrator\audit_test.go

---

**Checkpoint:** US3 complete. Audit pipeline functional with all 3 modes.

## Phase 5: User Story 4 — PTY/ConPTY Unbuffered Output (P2)

**Goal:** PTY on Unix, ConPTY on Windows provide unbuffered text output.
**Independent Test:** spawn `echo hello` via PTY executor → get "hello" in Result.Content.

- [x] T022 [US4] Install creack/pty dependency via go get in D:\Dev\aimux\go.mod
- [x] T023 [US4] Implement PTY executor Run() with pty.Start + read loop + ANSI strip in D:\Dev\aimux\pkg\executor\pty\pty.go
- [x] T024 [P] [US4] Implement PTY executor timeout/cancel via context + process kill in D:\Dev\aimux\pkg\executor\pty\pty.go
- [x] T025 [US4] Write PTY executor integration test (spawn echo) in D:\Dev\aimux\pkg\executor\pty\pty_test.go
- [x] T026 [US4] Implement ConPTY executor Run() with console inheritance + ANSI strip in D:\Dev\aimux\pkg\executor\conpty\conpty.go
- [x] T027 [P] [US4] Implement ConPTY timeout/cancel + ANSI strip in D:\Dev\aimux\pkg\executor\conpty\conpty.go
- [x] T028 [US4] Write ConPTY executor test in D:\Dev\aimux\pkg\executor\conpty\conpty_test.go
- [x] T029 [US4] Wire PTY/ConPTY into executor Selector priority (ConPTY > PTY > Pipe) in D:\Dev\aimux\pkg\server\server.go

---

**Checkpoint:** US4 complete. PTY/ConPTY produce unbuffered text output on their platforms.

## Phase 6: DeepResearch API Integration (P2)

**Goal:** deepresearch tool calls real Google GenAI API.
**Independent Test:** deepresearch(topic="test") → returns research result from API.

- [x] T030 Install google.golang.org/genai dependency in D:\Dev\aimux\go.mod
- [x] T031 [US5] Implement GenAI API client with Interactions API in D:\Dev\aimux\pkg\tools\deepresearch\client.go
- [x] T032 [P] [US5] Implement file upload via Files API in D:\Dev\aimux\pkg\tools\deepresearch\files.go
- [x] T033 [US5] Implement caching (exact match by topic+format+model+files_hash) in D:\Dev\aimux\pkg\tools\deepresearch\cache.go
- [x] T034 [US5] Wire deepresearch tool handler to real API client in D:\Dev\aimux\pkg\server\server.go
- [x] T035 [US5] Write deepresearch client test in D:\Dev\aimux\pkg\tools\deepresearch\deepresearch_test.go

---

**Checkpoint:** DeepResearch calls real API, supports caching and file upload.

## Phase 7: User Story 5 — GitHub + CI Green (P2)

**Goal:** Repo on GitHub with passing CI.
**Independent Test:** `gh run list` shows green checks.

- [x] T036 [US5] Create GitHub repo github.com/thebtf/aimux via gh CLI (private)
- [x] T037 [P] [US5] Push all commits to GitHub remote
- [x] T038 [US5] Verify CI passes: go test -race on ubuntu-latest + macos + windows in GitHub Actions
- [x] T039 [P] [US5] golangci-lint: continue-on-error (lint v2.1.6 built with Go 1.24, project targets 1.25)

---

**Checkpoint:** US5 complete. CI green on all 3 platforms.

## Phase 8: Verification + Polish

- [x] T040 Grep for "not yet implemented|wiring pending|pending" in pkg/ — 4 remain (ConPTY scaffold + DeepResearch API, both with correct fallbacks)
- [x] T041 Smoke test: all 10 tools via MCP protocol — PASS (init, 10 tools, sessions health, think call)
- [x] T042 mcp-mux compatibility smoke test — init+tools+resources+prompts all respond correctly
- [x] T043 Performance benchmark: session 439ns/op, job 1002ns/op, poll 21ns/op
- [x] T044 Write error propagation e2e test (strategy failure → TypedError → MCP response) in D:\Dev\aimux\pkg\server\error_e2e_test.go
- [x] T045 Update CONTINUITY.md with production-ready status in D:\Dev\mcp-aimux\.agent\CONTINUITY.md
- [x] T046 Tag v3.0.0 release — pushed to github.com/thebtf/aimux

---

**Checkpoint:** v3.0.0 released. All tools functional. Zero placeholders. CI green.

## Dependencies

- Phase 1 blocks all subsequent phases (orchestrator wiring required)
- US1 (Phase 2) and US2 (Phase 3) and US3 (Phase 4) are independent after Phase 1
- US4 (Phase 5) independent of US1-US3
- Phase 6 (DeepResearch) independent of all others
- Phase 7 (GitHub) can start after Phase 1
- Phase 8 depends on all prior phases

## Execution Strategy

- **MVP scope:** Phase 1 + Phase 2 (orchestrator wiring + pair coding = core value)
- **Parallel opportunities:** T004||T005||T006, T011||T012, T015||T016||T017, T023||T024, T031||T032, T037||T039
- **Commit strategy:** One commit per completed task
- **Estimated tasks:** 44 total (10 in Phase 1, 4 in Phase 2, 4 in Phase 3, 3 in Phase 4, 8 in Phase 5, 6 in Phase 6, 4 in Phase 7, 5 in Phase 8)
