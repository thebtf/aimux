# Tasks: Skill Engine

**Spec:** .agent/specs/skill-engine/spec.md
**Plan:** .agent/specs/skill-engine/plan.md
**Generated:** 2026-04-08

**Phase mapping (plan → tasks):** Plan Phase 1 = Tasks Phase 1+2, Plan Phase 2 = Tasks Phase 3, Plan Phase 3 = Tasks Phase 4, Plan Phase 4 = Tasks Phase 5, Plan Phase 5 = Tasks Phase 6+7. Tasks added Phase 0 (Planning) not in plan.

## Phase 0: Planning

- [x] P001 Analyze tasks and identify required agent capabilities
  AC: all tasks reviewed · executor assigned per task (MAIN/sonnet/codex) · no unassigned tasks remain
- [x] P002 Resolve Go template escaping strategy for MCP tool examples in skill templates
  AC: test template with `{{` literal renders correctly · escaping strategy documented
  RESULT: `{{"{{step.content}}"}}` renders as literal `{{step.content}}`. `{{.CLICount}}` renders dynamic value.

## Phase 1: Setup

- [x] T001 Create `pkg/skills/` package directory and `config/skills.d/` with `_fragments/` subdirectory
  AC: dirs exist: pkg/skills/, config/skills.d/, config/skills.d/_fragments/ · go build passes
- [x] T002 [P] Define types in `pkg/skills/types.go`: SkillMeta, SkillData, ArgDef structs
  AC: SkillMeta has Name/Description/Prompt/Args/Related/Tags/FilePath/IsFragment · SkillData has all fields from plan.md data model · compiles · swap body→return null ⇒ tests MUST fail
- [x] T003 [P] Define template FuncMap in `pkg/skills/funcs.go`: CallerHasSkill, JoinCLIs, RoleFor
  AC: CallerHasSkill("x") returns true when x in CallerSkills · JoinCLIs returns comma-separated · RoleFor("coding") returns CLI name · 3 unit tests · swap body→return null ⇒ tests MUST fail

- [x] G001 VERIFY Phase 1 (T001–T003) — build ✅ vet ✅ 9/9 tests ✅
  RUN: `go build ./pkg/skills/...`. Call Skill("code-review", "lite") on pkg/skills/*.go.
  CHECK: For each task — Read implementation, confirm AC met, run anti-stub check.
  ENFORCE: Zero stubs. Zero TODOs. Every parameter MUST influence output.
  RESOLVE: Fix ALL findings before marking this gate [x].

---

**Checkpoint:** Package structure and types defined. Compiles.

## Phase 2: Engine Foundation

- [x] T004 Implement frontmatter parser in `pkg/skills/engine.go`: split YAML header from markdown body, unmarshal to SkillMeta
  AC: parses `---\nname: test\n---\nbody` correctly · returns error on missing name · returns error on missing description · handles no-frontmatter files gracefully · 4 unit tests · swap body→return null ⇒ tests MUST fail
- [x] T005 Implement Engine.Load() in `pkg/skills/engine.go`: load embedded FS, overlay disk dir, parse templates with fragments
  AC: loads .md from embedded FS · disk file overrides embedded with same slug · _fragments/ loaded as named templates · underscore-prefix files not in Skills() list · WARN logged on override · 5 unit tests · swap body→return null ⇒ tests MUST fail
- [x] T006 Implement Engine.Render() in `pkg/skills/engine.go`: execute Go template with SkillData, missingkey=zero, recover()
  AC: renders `{{.CLICount}}` as actual number · renders `{{if .HasMultipleCLIs}}` conditionally · missing field renders as zero (not panic) · panicking template returns error string (not crash) · `{{template "fragment"}}` includes fragment · 5 unit tests · swap body→return null ⇒ tests MUST fail
- [x] T007 Implement Engine.Skills() and Engine.Get() in `pkg/skills/engine.go`
  AC: Skills() returns only prompt:true skills · Get("debug") returns SkillMeta · Get("nonexistent") returns nil · fragments excluded from Skills() · 3 unit tests · swap body→return null ⇒ tests MUST fail
- [x] T008 Write MINIMAL skill template `config/skills.d/debug.md` to validate engine pipeline (replaced by T017 with full version)
  AC: has valid frontmatter (name, description, args, related) · uses `{{.EnabledCLIs}}` and `{{if .HasMultipleCLIs}}` · uses `{{template "evidence-table" .}}` · renders without error · output >20 lines · NOTE: this is a test scaffold, T017 replaces with full 100+ line version

- [x] G002 VERIFY Phase 2 (T004–T008) — build ✅ vet ✅ 18/18 tests ✅ coverage 82.8%
  RUN: `go test ./pkg/skills/... -v -cover`. Call Skill("code-review", "lite") on pkg/skills/*.go.
  CHECK: Confirm AC met for each task. Anti-stub check on every function.
  ENFORCE: Zero stubs. Zero TODOs. Coverage >80% for pkg/skills/. Every parameter influences output.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Engine loads, parses, renders templates with dynamic data. Unit tested.

## Phase 3: Graph Map + Fragments (US2 foundation)

**Goal:** Create the skill graph map (_map.yaml) and all shared fragments. This is the prerequisite for authoring skills — no skill .md can be written before this phase.
**Independent Test:** `go test ./pkg/skills/... -run TestGraphValidation` passes; _map.yaml parseable; all fragments render.

- [x] T009 [US2] Create `config/skills.d/_map.yaml` with complete skill graph: all 13 skills, tools, phases, related, fragments, escalates_to, receives_from
  AC: YAML parseable · all 13 skills listed · every skill has tools/phases/related · tool_usage section covers all 13 aimux tools · fragments section lists all shared fragments with used_by
- [x] T010 [P] [US2] Implement graph computation in `pkg/skills/graph.go`: parse _map.yaml, build bidirectional adjacency, merge into SkillMeta.RelatedSkills
  AC: forward refs (debug→security) AND reverse refs (security→debug) computed · RelatedSkills populated on each SkillMeta · circular refs handled (A→B→A) · 4 unit tests · swap body→return null ⇒ tests MUST fail
- [x] T011 [P] [US2] Implement Engine.ValidateMap() in `pkg/skills/graph.go`: check map ↔ .md consistency
  AC: warns if _map.yaml lists skill without .md · warns if .md has related: not in map · warns if fragment in map but .md missing · returns []string warnings · 3 unit tests · swap body→return null ⇒ tests MUST fail
- [x] T012 [P] [US2] Create fragment `config/skills.d/_fragments/evidence-table.md`
  AC: contains verification evidence table (claim → requires → NOT sufficient) · uses Go template syntax for dynamic data · renders as standalone fragment
- [x] T013 [P] [US2] Create fragment `config/skills.d/_fragments/verification-gate.md`
  AC: contains "consensus ≠ correctness" warning · contains evidence classification (VERIFIED/INFERRED/STALE) · renders as standalone
- [x] T014 [P] [US2] Create fragment `config/skills.d/_fragments/delegation-tree.md`
  AC: contains full decision tree (<5 lines → direct, >20 → codex, etc.) · contains QUICK format template · renders as standalone
- [x] T015 [P] [US2] Create fragment `config/skills.d/_fragments/priority-scoring.md`
  AC: contains 3-factor scoring (severity × impact × likelihood) → P0-P4 · renders as standalone
- [x] T016 [P] [US2] Create fragment `config/skills.d/_fragments/integrity-commandments.md`
  AC: contains 6 feynman researcher commandments verbatim · renders as standalone

- [x] G003 VERIFY Phase 3 (T009–T016) — build ✅ 25 tests ✅ coverage 84.2% ✅ map valid ✅
  RUN: `go test ./pkg/skills/... -v`. Validate _map.yaml parses. Render each fragment standalone.
  CHECK: Map has all 13 skills. All fragments render. Graph computation produces bidirectional refs.
  ENFORCE: Zero stubs. Map is complete — no "TODO" entries.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Graph map complete. Fragments ready. Skills can now be authored from the map.

## Phase 4: Skill Templates — Core (US1 + US2 + US3)

**Goal:** Author all 13 skill templates FROM the graph map. Each skill orchestrates aimux tools through connected phases with session state, file handoffs, and cross-skill references.
**Independent Test:** Engine.Render() for each skill produces >50 lines; cross-references resolve; conditional sections adapt to 1-CLI vs multi-CLI data.

- [x] T017 [US1] Author `config/skills.d/debug.md` — 5-phase debug workflow (reproduce → investigate → root-cause → fix → verify) with session_id continuity, escalation, evidence-table fragment
  AC: >100 lines · 5 phases with hard gates · investigate session_id carried to think · exec receives report path · escalation section conditional on FailedAttempts · uses evidence-table and verification-gate fragments · related: [security, audit, review] · renders with mock SkillData
- [x] T018 [P] [US2] Author `config/skills.d/audit.md` — triage + investigation workflow routing P0 findings to debug/security skills
  AC: >100 lines · cross-references aimux-debug and aimux-security by prompt name · P0-P3 priority scoring via fragment · scale-decision table (T1-T4) · uses evidence-table fragment · related: [debug, security, review, investigate]
- [x] T019 [P] [US2] Author `config/skills.d/security.md` — 10-category security checklist with investigate integration
  AC: >100 lines · 10 categories from ECC research · investigate(domain="security") integration · cross-references audit and review · uses verification-gate fragment
- [x] T020 [P] [US3] Author `config/skills.d/review.md` — code review with CLI-adaptive sections
  AC: >100 lines · consensus section only if HasMultipleCLIs · debate section only if CLICount>=3 · single-CLI fallback uses think(peer_review) · uses evidence-table and priority-scoring fragments
- [x] T021 [P] [US3] Author `config/skills.d/consensus.md` — multi-model consensus with "consensus ≠ correctness" warning
  AC: >80 lines · available CLIs listed dynamically · verification-gate fragment included · debate recommended for binary choices · scale-decision table · related: [review, debug]
- [x] T022 [P] [US1] Author `config/skills.d/research.md` — 4-phase research pipeline with integrity commandments
  AC: >100 lines · 4 phases with hard gates (literature → comparison → adversarial → synthesis) · integrity-commandments fragment · deepresearch section conditional on HasGemini · past reports shown dynamically · related: [investigate, consensus]
- [x] T023 [P] Author `config/skills.d/investigate.md` — migration from handleInvestigatePrompt with domain auto-detect
  AC: >100 lines · domain auto-detect mentioned · coverage areas per domain · cross-tool dispatch to think · past reports shown · related: [debug, audit, research]
- [x] T024 [P] Author `config/skills.d/guide.md` — migration from handleGuidePrompt with tool selection table
  AC: >80 lines · live CLIs and metrics · tool selection table · role routing table · think patterns listed · related: [background, delegate]
- [x] T025 [P] Author `config/skills.d/workflow.md` — migration from handleWorkflowPrompt with pipeline builder
  AC: >80 lines · step schema · template vars explanation · goal-based pipeline generation · related: [delegate, guide]
- [x] T026 [P] Author `config/skills.d/background.md` — migration from handleBackgroundPrompt with role routing
  AC: >50 lines · keyword → role analysis · async=true guidance · status polling · related: [guide, delegate]
- [x] T027 [P] Author `config/skills.d/agent-exec.md` — agent-first execution with delegation tree
  AC: >80 lines · agent matching by keyword score · delegation-tree fragment · agent-first hard gate · exec as fallback · related: [delegate, guide]
- [x] T028 [P] Author `config/skills.d/delegate.md` — full delegation decision tree and QUICK format
  AC: >100 lines · delegation-tree fragment as primary content · 9 codex roles · QUICK format template · post-delegation 5-step validation · related: [agent-exec, review]
- [x] T029 [P] Author `config/skills.d/tdd.md` — TDD workflow with RED/GREEN/IMPROVE gates
  AC: >80 lines · RED gate: tests MUST fail first · GREEN gate: same tests pass · coverage gate: 80%+ · conditional on CallerHasSkill("tdd") · related: [review, debug]

- [x] G004 VERIFY Phase 4 (T017–T029) — 13 skills, 1861 lines total ✅ all >80 lines ✅ fragments included ✅
  RUN: Render every skill with mock SkillData (1 CLI and 3 CLIs). Validate cross-references resolve. Check fragment includes. Run ValidateMap() — must return zero warnings.
  CHECK: Every skill >50 lines. Every skill has related: matching _map.yaml. Conditional sections adapt to CLI count. Fragments render inline.
  ENFORCE P18 (STUB-SKILL): Every tool call has EXACT parameters (no `<placeholder>`). Every phase has PROHIBITED/GATE enforcement text. Every skill includes acceptance criteria section. Every skill has cross-skill routing (escalates_to target).
  ENFORCE P20: Every skill in _map.yaml has non-empty escalates_to AND receives_from.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** All 13 skills authored from graph map. Cross-references verified. Conditionals tested.

## Phase 5: Server Integration (US1 + US4)

**Goal:** Wire skill engine into MCP server. Skills auto-register as prompts. Old handlers coexist during transition.
**Independent Test:** `go test ./pkg/server/... -run TestSkillPrompt` — skill-based prompt returns rendered content.

- [x] T030 [US4] Integrate Engine into Server: add `skillEngine *skills.Engine` field, initialize in NewMCPServer(), load embedded + disk skills
  AC: server starts with skill engine · skills loaded from embedded FS · disk override works · WARN logged for overrides · go build passes
- [x] T031 [US4] Implement `registerSkillPrompts()` in `pkg/server/prompts.go`: iterate Skills(), register MCP prompts with args from frontmatter
  AC: each skill with prompt:true registered as aimux-{name} · args from frontmatter become MCP prompt arguments · no duplicate prompt names (skill-based vs old handlers) · 2 unit tests
- [x] T032 [US1] Implement `handleSkillPrompt()` generic handler and `buildSkillData()` helper in `pkg/server/prompts.go`
  AC: buildSkillData populates all SkillData fields (CLIs, metrics, reports, agents, patterns, args) · handleSkillPrompt calls Render and returns MCP prompt result · error returns graceful MCP error · 3 unit tests · swap body→return null ⇒ tests MUST fail
- [x] T033 [US4] Extract `registerPrompts()` and old handlers to `pkg/server/prompts_legacy.go`; new skill prompts in `pkg/server/prompts.go`
  AC: old handlers still work · new skill handlers coexist · no duplicate prompt registrations · go build passes · existing tests pass
- [x] T034 [P] Implement `//go:embed all:config/skills.d` in `pkg/server/embed.go` for single-binary distribution
  AC: uses `all:` prefix to include _-prefixed dirs · embedded FS contains all .md files from config/skills.d/ · includes _fragments/ · includes _map.yaml · go build produces working binary

- [x] G005 VERIFY Phase 5 (T030–T034) — BLOCKED until T030–T034 all [x]
  RUN: `go test ./... -timeout 300s`. `go build ./cmd/aimux/`. Call Skill("code-review", "lite") on changed files.
  CHECK: Server starts. Skill prompts registered. Old prompts still work. Embedded skills load.
  ENFORCE: Zero regressions. Both old and new handlers work.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Skills delivered via MCP prompts. Old and new coexist. Binary embeds all skills.

## Phase 6: Think Tool Fix + Caller Discovery (US3 + FR-11)

**Goal:** Fix think tool schema for 5 unusable patterns. Implement caller skill discovery.
**Independent Test:** `think(pattern="decision_framework", decision="X", criteria='[...]')` works via MCP. CallerHasSkill returns correct results.

- [x] T035 [P] Add 5 JSON-string parameters to think tool MCP schema in `pkg/server/server.go`: criteria, options, components, sources, findings
  AC: MCP schema includes all 5 new params as type string · description says "JSON array" · existing params unchanged · go build passes
- [x] T036 [P] Update pattern validators to parse JSON from string params: decision_framework, architecture_analysis, source_comparison, research_synthesis
  AC: `criteria='[{"name":"x","weight":0.3}]'` parsed correctly · invalid JSON returns clear error · missing JSON field falls back to existing behavior · 4 unit tests per pattern · swap body→return null ⇒ tests MUST fail
- [x] T037 [P] Implement caller skill discovery in `pkg/skills/discover.go`: scan CWD for .claude/skills/, .agents/skills/, AGENTS.md
  AC: finds .claude/skills/*.md → extracts names · finds AGENTS.md → extracts agent names · returns []string of discovered skill names · handles missing dirs gracefully · 4 unit tests · swap body→return null ⇒ tests MUST fail
- [x] T038 Wire caller discovery into buildSkillData(): populate CallerSkills field from CWD scan
  AC: SkillData.CallerSkills populated on prompt request · CallerHasSkill("tdd") works in templates · empty if no caller skills found · no error on missing CWD

- [x] G006 VERIFY Phase 6 (T035–T038) — BLOCKED until T035–T038 all [x]
  RUN: `go test ./... -timeout 300s`. Test think tool with JSON params. Test CallerHasSkill in template.
  CHECK: All 5 think patterns now usable via MCP. Caller discovery works. Templates adapt.
  ENFORCE: Zero regressions on existing think patterns.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Think tool fully usable. Caller awareness active. System complete.

## Phase 7: Polish + Migration

- [x] T039 Remove old prompt handlers from `pkg/server/server.go` (handleReviewPrompt, handleDebugPrompt, etc.) — replaced by skill templates
  AC: old handlers removed · registerPrompts() only registers guide/investigate/workflow/background (kept as Go for now, or migrated) · go build passes · no dead code
- [x] T040 [P] Update `server.WithInstructions()` to reference skill-based prompts instead of listing tools
  AC: instructions mention `aimux-debug`, `aimux-review` etc. as available commands · lists available skills dynamically
- [x] T041 [P] Update README.md skills/prompts section
  AC: documents config/skills.d/ directory · documents how to add custom skills · documents frontmatter schema
- [x] T042 Run full regression: `go test ./... -timeout 300s` + `go vet ./...` + build binary + manual MCP test
  AC: all tests pass · no vet warnings · binary starts · at least 3 skill prompts verified manually

- [x] G007 VERIFY Phase 7 (T039–T042) — BLOCKED until T039–T042 all [x]
  RUN: `go test ./... -timeout 300s`. `go vet ./...`. Build and start binary. Test 3 prompts.
  CHECK: Dead code removed. Instructions updated. README current. Binary works.
  ENFORCE: Zero dead code. Zero regressions. Documentation matches implementation.
  RESOLVE: Fix ALL findings before marking [x].

---

**Checkpoint:** Migration complete. Old handlers removed. Documentation updated.

## Dependencies

- G001 (Phase 1) blocks all Phase 2+ tasks
- G002 (Phase 2) blocks Phase 3+ (engine must work before map/fragments)
- G003 (Phase 3) blocks Phase 4 (map + fragments required before skill authoring — FR-12)
- G004 (Phase 4) blocks Phase 5 (skills must exist before server integration)
- Phase 5 and Phase 6 are partially independent (T035-T036 can run parallel to T030-T034)
- G005 + G006 block Phase 7 (cleanup only after both systems work)
- Within Phase 4: all T017-T029 are [P] (parallel — different files, no deps between skills)

## Execution Strategy

- **MVP scope:** Phase 0-5 (engine + map + skills + server integration)
- **Parallel opportunities:** T002||T003, T012-T016 (all fragments), T017-T029 (all skills), T035-T038 (think fix parallel to integration)
- **Commit strategy:** One commit per completed task, one PR per phase
- **Agent delegation:** Phase 4 skills (T017-T029) are ideal for parallel sonnet subagents — each skill is independent, different file
