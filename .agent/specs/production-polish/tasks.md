# Tasks: Production Polish — Parser Wiring + CLI Profiles

**Spec:** .agent/specs/production-polish/spec.md
**Plan:** .agent/specs/production-polish/plan.md
**Generated:** 2026-04-06

## Phase 1: Parser Dispatch + Server Wiring — US1 + US2

**Goal:** CLI output parsed according to profile's output_format. Callers get clean text, not raw JSONL/JSON.
**Independent Test:** `exec(cli="codex")` returns parsed text content; `exec(cli="gemini")` returns extracted text from JSON.

- [x] T001 [US1] [US2] Create `pkg/parser/dispatch.go` — `ParseContent(raw, format string) (parsed, cliSessionID string)` dispatcher routing to existing JSONL/JSON/text parsers with graceful fallback. Also update `pkg/parser/json.go` — add `Result` field to `JSONResponse` and `ExtractContent` fallback chain (claude returns `result` not `content`)
- [x] T002 [P] [US1] [US2] Create `pkg/parser/dispatch_test.go` — unit tests for ParseContent: JSONL extraction + session ID, JSON extraction (content/text/response/result fields), text passthrough, empty input, malformed input fallback, unknown format passthrough
- [x] T003 [US1] [US2] Update `pkg/server/server.go` — add `outputFormat string` parameter to `executeJob`, call `parser.ParseContent` before `CompleteJob`, store raw in separate variable
- [x] T004 [US1] [US2] Update `pkg/server/server.go` — update all 4 `executeJob` call sites (handleExec async/sync, handleAgents async/sync) to pass `profile.OutputFormat`
- [x] T005 [US1] [US2] Update `pkg/server/server.go` — add `raw_output` field to exec response in `handleExec` (both sync response and status retrieval)

---

**Checkpoint:** Parser wired. `go build ./...` + `go test ./...` all pass. exec returns parsed content.

## Phase 2: CLI Profiles — US3

**Goal:** Production profiles for goose, crush, gptme, cline, continue.
**Independent Test:** `go build ./...` succeeds. Config loads all profiles without error.

- [x] T006 [P] [US3] Create `config/cli.d/goose/profile.yaml` — `goose run` + `--text` flag, verified against source in `D:\Dev\_EXTRAS_\goose`
- [x] T007 [P] [US3] Create `config/cli.d/crush/profile.yaml` — `crush run` + positional prompt, verified against source in `D:\Dev\_EXTRAS_\crush`
- [x] T008 [P] [US3] Create `config/cli.d/gptme/profile.yaml` — `gptme` + positional prompt + `--non-interactive`, verified against source in `D:\Dev\_EXTRAS_\gptme`
- [x] T009 [P] [US3] Create `config/cli.d/cline/profile.yaml` — `cline task` + positional prompt, verified against source in `D:\Dev\_EXTRAS_\cline`
- [x] T010 [P] [US3] Create `config/cli.d/continue/profile.yaml` — `cn` + positional prompt + `-p` headless flag, verified against source in `D:\Dev\_EXTRAS_\continue`

---

**Checkpoint:** All 12 CLIs have production profiles. Config loads cleanly.

## Phase 3: Verification + Polish

- [x] T011 Verify with real codex CLI: exec returns parsed agent response text, not raw JSONL
- [x] T012 Verify with real claude CLI: exec returns extracted text from JSON result
- [x] T013 Verify with real consensus: mixed CLIs produce clean combined content
- [x] T014 Update `.agent/CONTINUITY.md` with completed feature

## Dependencies

- T001 blocks T003 (ParseContent + JSON Result field must exist before server can call it)
- T003 blocks T004 (signature change before call site updates)
- T004 blocks T005 (all call sites updated before response format change)
- T002 independent of T003-T005 [P]
- T006-T010 independent of each other [P], independent of T001-T005
- T011-T013 require Phase 1 complete

## Execution Strategy

- **MVP scope:** Phase 1 (T001-T005) — parser wired, clean content from exec
- **Parallel opportunities:** T001||T002, T006||T007||T008||T009||T010
- **Commit strategy:** One commit for Phase 1, one for Phase 2, verification as separate commit
