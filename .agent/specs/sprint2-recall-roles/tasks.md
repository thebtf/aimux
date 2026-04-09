# Tasks: Investigate Recall + Role Prompts

**Spec:** .agent/specs/sprint2-recall-roles/spec.md
**Generated:** 2026-04-07

## Phase 1: Investigate Recall

**Goal:** Real report discovery — list, recall by topic, cleanup.
**Independent Test:** `go test ./pkg/investigate/ -run Report` passes.

- [x] T001 Add `ReportEntry` struct + `ListReports(cwd string) ([]ReportEntry, error)` to `pkg/investigate/report.go` — scan .agent/reports/ for investigate-*.md, parse filename for topic+date, return sorted by date desc
- [x] T002 Add `RecallResult` struct + `RecallReport(cwd, topicQuery string) (*RecallResult, error)` to `pkg/investigate/report.go` — slug substring match (case-insensitive) first, then content search (first 50 lines) as fallback, return newest match
- [x] T003 Add `CleanupExpiredReports(cwd string, maxAgeDays int) (int, error)` to `pkg/investigate/report.go` — delete investigate reports older than maxAgeDays based on file mtime
- [x] T004 [P] Create `pkg/investigate/report_recall_test.go` — Tests: list with 0 reports, list with reports (create temp files), recall by exact topic, recall by partial topic, recall by content match, recall miss, cleanup removes old files, cleanup keeps recent files

---

**Checkpoint:** `go test ./pkg/investigate/` passes with recall tests.

## Phase 2: Server Wiring

**Goal:** recall and list actions dispatch to real functions.
**Independent Test:** Handler tests pass with real recall/list results.

- [x] T005 Rewrite `recall` case in `handleInvestigate` (server.go) — call `RecallReport`, return found/content/filename or found=false with available topics from `ListReports`
- [x] T006 Enhance `list` case in `handleInvestigate` — include saved reports from `ListReports` alongside active investigations
- [x] T007 [P] Update handler tests — test recall with real topic match, test list includes saved reports

---

**Checkpoint:** Handler tests pass. recall returns real content.

## Phase 3: Role Prompts

**Goal:** 17 role prompt .md files loadable by prompt engine.
**Independent Test:** prompt engine resolves all 17 role names.

- [x] T008 Verify `pkg/prompt/Engine.Load()` handles subdirectories — if not, add recursive walking
- [x] T009 Create `config/prompts.d/roles/` directory with 17 role .md files: analyze, architect, challenge, codereview, coding, debug, docgen, planner, planning, precommit, refactor, research, review, secaudit, testgen, thinkdeep, tracer
- [x] T010 [P] Add prompt engine test — verify all 17 roles resolve via Engine.Resolve("roles/{role}")

---

**Checkpoint:** All role prompts loadable. injectBootstrap works with new roles.

## Phase 4: Cleanup + Polish

- [x] T011 Full regression: `go build ./... && go vet ./... && go test ./... -timeout 300s`
- [x] T012 Update `.agent/CONTINUITY.md` with completed Sprint 2

## Dependencies

- T001 blocks T002 (ListReports before RecallReport)
- T001, T002, T003 block T005, T006 (report functions before server wiring)
- T008 blocks T009 (verify engine before creating files)
- T004, T007, T010 independent [P]
- T011-T012 require Phase 1-3 complete

## Execution Strategy

- **Phase 1:** T001 → T002 → T003, T004 parallel
- **Phase 2:** T005, T006 sequential, T007 parallel
- **Phase 3:** T008 → T009, T010 parallel
- **Commit strategy:** One commit per phase
