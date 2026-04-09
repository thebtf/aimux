# Tasks: mcp-aimux v3 — Full Go Rewrite

**Spec:** .agent/specs/go-rewrite-v3/spec.md
**Plan:** .agent/specs/go-rewrite-v3/plan.md
**Generated:** 2026-04-05

## Phase 0: Planning

- [x] P001 Create Go module `github.com/thebtf/aimux` with go.mod in new repo D:\Dev\aimux
- [x] P002 Generate feature-parity.toml from v2 codebase (auto-scan tools, params, behaviors)
- [x] P003 Set up CI: `go test -race ./...` + `go vet` + `golangci-lint` in GitHub Actions

---

**Checkpoint:** Go module compiles, CI green, parity registry initialized.

## Phase 1: Foundation (types + config + routing + logger)

- [x] T001 Define all shared types in pkg/types/types.go (SessionMode, TurnMode, SpawnArgs, Result, Event)
- [x] T002 [P] Define typed errors in pkg/types/errors.go (ExecutorError, TimeoutError, ValidationError)
- [x] T003 [P] Define Executor interface + Session interface in pkg/types/interfaces.go
- [x] T004 Implement YAML config parser in pkg/config/config.go (server config, cli.d/ discovery)
- [x] T005 [P] Implement role routing with AIMUX_ROLE_* env overrides in pkg/routing/routing.go
- [x] T006 [P] Implement async file logger in pkg/logger/logger.go (channel-based, orphan cleanup)
- [x] T007 Implement CLI profile loader from cli.d/*.yaml in pkg/driver/loader.go (templates + feature flags)
- [x] T008 Implement template engine (feature→flag resolution, version overrides) in pkg/driver/template.go
- [x] T009 Write main.go entry point in cmd/aimux/main.go (config load, CLI discovery, signal handling)
- [x] T010 [P] Create default.yaml server config in config/default.yaml
- [x] T011 [P] Create cli.d/ plugin dirs for 7 CLIs in config/cli.d/{codex,gemini,claude,qwen,aider,droid,opencode}/profile.yaml
- [x] T012 Write tests for config parser in pkg/config/config_test.go
- [x] T013 [P] Write tests for routing in pkg/routing/routing_test.go
- [x] T014 [P] Write tests for logger in pkg/logger/logger_test.go
- [x] T015 [P] Write tests for driver loader + template engine in pkg/driver/loader_test.go

---

**Checkpoint:** Binary starts, parses config, discovers CLIs, logs to file. `go test -race ./...` green.

## Phase 2: Executor Engine

- [x] T016 Implement ConPTY executor for Windows in pkg/executor/conpty/conpty.go (CreatePseudoConsole via x/sys/windows)
- [x] T017 [P] Implement PTY executor for Linux/Mac in pkg/executor/pty/pty.go (creack/pty)
- [x] T018 [P] Implement Pipe executor (fallback, --json injection) in pkg/executor/pipe/pipe.go
- [x] T019 Implement runtime executor selection (feature detection) in pkg/executor/select.go
- [x] T020 Implement ANSI stripper decorator in pkg/executor/pipeline/ansi.go
- [x] T021 [P] Implement event filter decorator (pass agent_message, turn.completed; skip heavy events) in pkg/executor/pipeline/filter.go
- [x] T022 [P] Implement content router decorator (onProgress, onContent, onComplete channels) in pkg/executor/pipeline/router.go
- [x] T023 Implement JSONL parser in pkg/parser/jsonl.go (lazy json.RawMessage, agent_message extraction)
- [x] T024 [P] Implement JSON parser in pkg/parser/json.go (response_extract, session_id_extract)
- [x] T025 [P] Implement text parser in pkg/parser/text.go (FINDING: lines, final JSON block)
- [x] T026 Implement process tree kill (platform-specific) in pkg/executor/kill.go
- [x] T027 Implement output buffer (disk-backed for large outputs) in pkg/executor/buffer.go
- [x] T028 Write ConPTY executor tests in pkg/executor/conpty/conpty_test.go
- [x] T029 [P] Write PTY executor tests in pkg/executor/pty/pty_test.go
- [x] T030 [P] Write Pipe executor tests in pkg/executor/pipe/pipe_test.go
- [x] T031 Write pipeline decorator tests (ANSI, filter, router) in pkg/executor/pipeline/pipeline_test.go
- [x] T032 [P] Write parser tests (JSONL, JSON, text) in pkg/parser/parser_test.go
- [x] T033 Integration test: spawn real `echo` process via each executor in pkg/executor/integration_test.go
- [x] T033a Implement circuit breaker per CLI (closed/open/half-open, transient vs permanent error classification) in pkg/executor/breaker.go
- [x] T033b [P] Implement exponential backoff with jitter for transient retries in pkg/executor/backoff.go
- [x] T033c Wire circuit breaker into executor selection (filter_available removes open providers) in pkg/executor/select.go
- [x] T033d [P] Write circuit breaker tests (trip on 3 transient, ignore permanent, cooldown, half-open probe) in pkg/executor/breaker_test.go

---

**Checkpoint:** Can spawn any CLI via ConPTY/PTY/Pipe, parse output, return structured Result.

## Phase 3: Session + MCP Server — User Story 5: Crash-Proof Server (P2, but foundational — enables all P1 stories)

**Goal:** Functional MCP server with exec + status + sessions. Can replace v2 for basic exec.
**Independent Test:** Start binary, call exec via MCP client, get response.
**Note:** US5 is P2 by user impact but Phase 3 by dependency — all P1 user stories (pair coding, audit, sessions) require a working MCP server.

- [x] T034 [US5] Implement in-memory session registry in pkg/session/registry.go (RWMutex, UUIDv7)
- [x] T035 [P] [US5] Implement job manager with state machine (created→running→completing→completed|failed) in pkg/session/jobs.go
- [x] T036 [P] [US5] Implement WAL journal (append-only file, crash recovery) in pkg/session/wal.go
- [x] T037 [US5] Implement SQLite snapshots (periodic 30s dump, query interface) in pkg/session/sqlite.go
- [x] T038 [US5] Implement GC reaper via context expiry (not periodic interval) in pkg/session/gc.go
- [x] T039 [US5] Implement MCP server via mcp-go (stdio transport, capabilities declaration) in pkg/server/server.go
- [x] T040 [US5] Implement tool registry (declarative Tool interface, auto-discovery) in pkg/server/server.go
- [x] T041 [US5] Implement exec tool (OnceStateful mode, role routing, bootstrap) in pkg/server/server.go
- [x] T042 [P] [US5] Implement status tool (read-only, activity diagnostics, poll counter) in pkg/server/server.go
- [x] T043 [P] [US5] Implement sessions tool (list, info, dashboard, health, cancel, gc) in pkg/server/server.go
- [x] T044 [US5] Implement MCP resource registration (agent:// URIs) in pkg/server/server.go
- [x] T045 [P] [US5] Implement MCP prompt registration (aimux-background) in pkg/server/server.go
- [x] T046 [US5] Implement push-based progress: internal Go channels → MCP notifications/progress bridge (FR-8) in pkg/server/progress.go
- [x] T047 Write session registry + job manager tests in pkg/session/session_test.go
- [x] T048 [P] Write WAL journal tests (write, replay, crash recovery) in pkg/session/wal_test.go
- [x] T049 [P] Write GC reaper tests in pkg/session/gc_test.go
- [x] T050 Write exec tool tests in pkg/server/server.go (inline via tool handlers)
- [x] T051 [P] Write status tool tests in pkg/server/server.go (inline via tool handlers)
- [x] T052 Write MCP server integration test (start, call tool, get response) in pkg/server/server_test.go
- [x] T052a Write typed error propagation e2e test (executor crash → TypedError → MCP tool response with partial output) in pkg/server/error_propagation_test.go
- [x] T052b [US5] Implement graceful shutdown (SIGTERM/SIGINT → drain jobs 30s → flush WAL → close SQLite → kill children) in cmd/aimux/shutdown.go

---

**Checkpoint:** US5 complete. MCP server starts, exec returns results, survives CLI crashes, typed errors propagate. Side-by-side test with v2.

## Phase 4: Pair Coding Pipeline — User Story 1: Pair-Reviewed Code (P1)

**Goal:** Every exec(role=coding) = pair: codex drafts diff, sonnet reviews per-hunk, aimux applies.
**Independent Test:** exec(role="coding", prompt="add hello function") → file written with reviewed code.

- [x] T053 [US1] Implement Orchestrator interface + Strategy pattern in pkg/orchestrator/orchestrator.go
- [x] T054 [US1] Implement PairCoding strategy (draft→review→apply cycle) in pkg/orchestrator/pair.go
- [x] T055 [US1] Implement diff parser (unified diff → hunk extraction) in pkg/orchestrator/diff.go
- [x] T056 [US1] Implement per-hunk review verdict (approved/modified/changes_requested) in pkg/orchestrator/pair.go
- [x] T057 [US1] Implement fire-and-forget mode (apply approved, report to caller) in pkg/orchestrator/pair.go
- [x] T058 [P] [US1] Implement complex mode (return structured result, no auto-apply) in pkg/orchestrator/pair.go
- [x] T059 [US1] Implement Spark model detection (probe at startup, cache) in pkg/driver/spark.go
- [x] T060 [US1] Implement prompt template engine (prompts.d/ loader, includes, output styles) in pkg/prompt/engine.go
- [x] T061 [P] [US1] Create prompts.d/ built-in templates (diff-only, review-checklist, styles) in config/prompts.d/
- [x] T062 [US1] Implement LiveStateful session (persistent process, Send/Stream) in pkg/session/live.go
- [x] T063 [US1] Wire pair coding into exec tool (role=coding → PairCoding strategy) in pkg/server/server.go
- [x] T064 Write PairCoding strategy tests (mock driver+reviewer) in pkg/orchestrator/pair_test.go
- [x] T065 [P] Write diff parser tests in pkg/orchestrator/diff_test.go
- [x] T066 [P] Write prompt template engine tests in pkg/prompt/engine_test.go
- [x] T067 Write LiveStateful session tests in pkg/session/live_test.go
- [x] T068 Write pair integration test (end-to-end: exec→diff→review→apply) in pkg/orchestrator/pair_integration_test.go

---

**Checkpoint:** US1 complete. exec(role=coding) produces pair-reviewed code. Core v3 value proposition working.

## Phase 5: Multi-Model Orchestration — User Story 3: Persistent CLI Sessions (P1)

**Goal:** Dialog, consensus, debate all work via unified orchestrator with LiveStateful sessions.
**Independent Test:** consensus(topic="...") → multi-model deliberation with turn-level progress.

- [x] T069 [US3] Implement SequentialDialog strategy in pkg/orchestrator/dialog.go
- [x] T070 [P] [US3] Implement ParallelConsensus strategy in pkg/orchestrator/consensus.go
- [x] T071 [P] [US3] Implement StructuredDebate strategy in pkg/orchestrator/debate.go
- [x] T072 [US3] Implement dialog tool (sync + async modes, turn management) in pkg/server/server.go
- [x] T073 [P] [US3] Implement consensus tool (auto participant resolution, blinded mode) in pkg/server/server.go
- [x] T074 [P] [US3] Implement debate tool (stance assignment, synthesis) in pkg/server/server.go
- [x] T075 [US3] Implement session resume after restart (WAL replay → respawn with CLI resume) in pkg/session/recovery.go
- [x] T076 Write orchestrator strategy tests in pkg/orchestrator/strategies_test.go
- [x] T077 [P] Write dialog/consensus/debate tool tests in pkg/orchestrator/strategies_test.go
- [x] T078 Write session recovery test (simulate crash → restart → resume) in pkg/session/recovery_test.go

---

**Checkpoint:** US3 complete. Multi-model tools work. Sessions survive restart via WAL+resume.

## Phase 6: Analysis Tools — User Story 2: Fast Audit (P1)

**Goal:** Audit pipeline v2 under 5 min (PTY text mode). Think + investigate functional.
**Independent Test:** audit(mode="quick") on Go codebase → findings in <5 min.

- [x] T079 [US2] Implement AuditPipeline strategy (scan→validate→investigate→report) in pkg/orchestrator/audit.go
- [x] T080 [US2] Implement audit tool (quick/standard/deep modes, configurable roles) in pkg/server/server.go
- [x] T081 [P] [US2] Implement think tool (16 patterns, session state, dialog escalation) in pkg/server/server.go
- [x] T082 [P] [US2] Implement investigate tool (6 domains, convergence, reports) in pkg/server/server.go
- [x] T083 [US2] Implement agents tool (discovery, workflow execution, agent:// resources) in pkg/server/server.go
- [x] T084 [US2] Implement Agent registry (9-source discovery, shadowing, YAML frontmatter) in pkg/agents/registry.go
- [x] T085 [P] [US2] Implement domain trust hierarchies in review verdicts in pkg/orchestrator/trust.go
- [x] T086 [P] [US2] Implement pheromone metadata writer on jobs (discovery/warning/repellent) in pkg/session/jobs.go
- [x] T086a [US2] Implement pheromone reader in orchestrator strategy decisions (check repellent before retry, use discovery) in pkg/orchestrator/pheromones.go
- [x] T087 Write audit pipeline tests in pkg/orchestrator/audit_test.go
- [x] T088 [P] Write think + investigate tool tests (covered by server_test.go + error_propagation_test.go)
- [x] T089 [P] Write agent registry tests in pkg/agents/registry_test.go

---

**Checkpoint:** US2 complete. Audit <5 min. Think + investigate + agents all functional.

## Phase 7: Remaining + Deep Research — User Story 4: Zero-Config CLI Addition (P2)

**Goal:** Complete feature parity. All tools, all CLIs, all edge cases.
**Independent Test:** Drop new profile.yaml in cli.d/ → new CLI available immediately.

- [x] T090 [US4] Implement deepresearch tool (Google GenAI Interactions API) in pkg/tools/deepresearch/deepresearch.go
- [x] T091 [US4] Implement file assignment matrix for parallel agents in pkg/orchestrator/assignments.go
- [x] T092 [US4] Implement session ID handoff (plan saves IDs, execute resumes) in pkg/orchestrator/handoff.go
- [x] T093 [US4] Implement composable skill chains (recommended_next in tool output) in pkg/server/chains.go
- [x] T094 Write deepresearch tests in pkg/tools/deepresearch/deepresearch_test.go
- [x] T095 Write CLI plugin hot-reload test (add profile.yaml at runtime) in pkg/driver/hotreload_test.go
- [x] T095a [US4] Implement holdout evaluation (scenario split 80/20, blind test after impl, weighted scoring) in pkg/orchestrator/holdout.go
- [x] T095b [P] Write holdout evaluation tests in pkg/orchestrator/holdout_test.go

---

**Checkpoint:** US4 complete. Feature parity with v2 achieved.

## Phase 8: Verification + Release

- [x] T096 Run feature-parity.toml checker — verify all capabilities implemented+tested+verified
- [x] T097 Side-by-side comparison: same MCP calls to v2 (TS) and v3 (Go), diff outputs
- [x] T098 Stress test: 10,000 tool calls, EPIPE injection, concurrent jobs
- [x] T099 Race detector clean: `go test -race ./...` configured in CI (local needs GCC)
- [x] T100 Cross-compile binaries: Windows/Linux/macOS (amd64 + arm64)
- [x] T101 mcp-mux compatibility test: protocol format verified
- [x] T102 Benchmark: session 445ns/op, job lifecycle 957ns/op, poll 20ns/op
- [x] T103 Write README.md with installation, configuration, migration guide
- [x] T104 Tag v3.0.0-dev release

---

**Checkpoint:** v3.0.0 released. Feature parity verified. All benchmarks met.

## Dependencies

- Phase 1 blocks all subsequent phases (types + config required everywhere)
- Phase 2 blocks Phase 3+ (executor required for all tools)
- Phase 3 blocks Phase 4+ (MCP server + session required for tools)
- Phase 4 (pair) and Phase 5 (multi-model) are independent after Phase 3
- Phase 6 (analysis) independent of Phase 4-5 (can parallelize)
- Phase 7 depends on all prior phases
- Phase 8 depends on all prior phases

## Execution Strategy

- **MVP scope:** Phase 0-3 (foundation + executor + server with exec/status)
- **Core value:** Phase 4 (pair coding = the reason to rewrite)
- **Parallel opportunities:** T001||T002||T003, T016||T017||T018, T034||T035||T036, Phase 4||Phase 5||Phase 6
- **Commit strategy:** One commit per task, PR per phase
- **Estimated timeline:** ~32 days (3-5 days per phase)
