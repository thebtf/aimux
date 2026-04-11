# Feature: Tool Response Guidance Layer

**Slug:** tool-response-guidance
**Created:** 2026-04-11
**Status:** Draft
**Author:** AI Agent (reviewed by user)

> **Provenance:** Specified by Claude Opus 4.6 on 2026-04-11.
> Evidence from: architecture doc `.agent/arch/tool-response-guidance/architecture.md`, observed
> failure mode in session 2026-04-10 (investigate session `019d7973-...` sat with 0 findings
> for hours because agent did not realize the tool is a notebook, not an autonomous
> investigator), constitution principles P3 (Correct Over Simple), P6 (Push Not Poll),
> P14 (Composable Prompt Templates), current `pkg/server/server.go` tool handlers.
> Confidence: VERIFIED (observed failure, code paths read).

## Clarifications

### Session 2026-04-11

| # | Category | Question | Resolution | Date |
|---|----------|----------|------------|------|
| C1 | Functional Scope | Should `action="auto"` ship in Phase 1 for `investigate`, or be deferred to Phase 1.5? | **Deferred to Phase 1.5.** Auto requires async+cancel+streaming per Constitution P26 (added 2026-04-11). Streaming depends on engram issue #8 (OnOutput wiring in `handleAgentRun` async path). Sync-only auto is explicitly forbidden by P26 because MCP has no request timeout — a sync delegation would hang the caller indefinitely with no escape hatch. Phase 1 ships guidance infrastructure + `investigate` manual path + description rewrite. Phase 1.5 ships `action="auto"` after issue #8 is fixed, following the reference implementation in the `exec` tool's async path (OnOutput → JobManager.AppendProgress → notifications/progress, context cancel per job, cascade kill). | 2026-04-11 |
| C2 | Constraints & Tradeoffs | What stall threshold should the guidance layer use, and how is it configured? | **Reframed from "session-age stall" to "active-job inactivity stall".** Original framing ("session without state change for 30 min") was wrong because it flagged intentionally-paused sessions. Real stall = active job produces no OnOutput events. Verified via SocratiCode search of `pkg/executor/iomanager.go:50` (bufio.Scanner.Scan → onOutput per line) + `pkg/server/server.go:1165` (executeJob OnOutput → AppendProgress + notifications/progress) — streaming is already unbuffered per-line. Defaults (global, in `config/default.yaml`, not per-tool): grace_period 60s, soft_warning 120s, hard_stall 600s, auto_cancel 900s. Auto-cancel ON by default at 15 min for runaway safety; users who legitimately run longer must raise the floor. Longer intervals chosen to accommodate reasoning-heavy tasks (codex high effort takes 10-30s to first token). Depends on `JobManager.AppendProgress` updating a new `LastOutputAt` field. | 2026-04-11 |
| C3 | Edge Cases | When a stateful tool is registered without a corresponding `ToolPolicy` implementation, how should the adapter behave? | **Loud-dev / silent-prod.** In development builds (`-tags=dev` or `AIMUX_ENV=development`), the registration check panics with a clear message listing the missing tool name and the required file path (`pkg/guidance/policies/<tool>.go`). In production builds, the adapter returns a minimal envelope `{state: "guidance_not_implemented", result: <raw>}`, logs WARNING once per tool via `sync.Once` on first call (no log spam), and continues serving the raw result. Breaking prod on a missing policy is worse than silent degradation, but in dev we want the gap loud and immediate. | 2026-04-11 |
| C4 | Domain/Data Model | Where does the original raw handler result live in the envelope — nested under a `result` field or flat-merged at the root? | **Nested `result` field.** Clean separation between guidance metadata and handler output. Breaking change for existing callers (`response.session_id` → `response.result.session_id`) — accepted because the alternative (flat merge with `_`-prefixed guidance keys) invites field-name collisions and makes the envelope ambiguous about which fields are contract vs. advisory. Motivated by Constitution P3 (Correct Over Simple). Migration: all in-repo tests and handlers updated atomically in Phase 1 PR. External callers tracked in release notes as BREAKING under a MINOR version bump. `guidance.UnwrapResult(response)` SDK helper provides one-line migration for Go consumers. | 2026-04-11 |
| C5 | Terminology | What is the precise set of stateful tools, and do single-shot orchestrator tools (consensus, debate) get full policies? | **Full policies for all 6 tools uniformly.** Symmetric treatment: investigate, think, consensus, debate, dialog, workflow each receive a dedicated ToolPolicy implementation with full envelope depth. Policy logic differs per tool to reflect semantic differences (see new "Terminology — Distinguishing Stateful Tools" section): consensus is a parallel blinded expert poll, debate is adversarial multi-turn dialectic with stances, dialog is collaborative turn-taking, workflow is resumable multi-stage pipeline. User corrected my initial framing that "debate = consensus"; verified via SocratiCode search of `pkg/orchestrator/debate.go` that StructuredDebate already implements the correct adversarial semantic ("participants have assigned stances (for/against) and see each other's arguments"). No refactoring of debate implementation needed — the policy just needs to surface its state (current_turn, max_turns, stances, rebuttals). Phase 3 scope adjusted from "pattern replication" to "full policies per tool" — larger but more correct. | 2026-04-11 |

- Q1 (FR-3 scope): `action="auto"` deferred from Phase 1 to Phase 1.5 because of Constitution P26 interruptibility requirement + issue #8 blocker on streaming.
- Q2 (FR-6 stall semantics): reframed from session-age to active-job inactivity. Defaults 60/120/600/900 seconds, global config, auto-cancel ON.
- Q3 (missing policy fallback): loud-dev / silent-prod. Dev build panics at registration; prod build returns minimal envelope + one-time WARNING log.
- Q4 (envelope result placement): nested `result` field. Breaking change accepted for clean separation; in-repo migration atomic, external callers informed via release notes.
- Q5 (stateful tool scope + consensus/debate distinction): full policies for all 6 tools. Consensus and debate semantically distinct — consensus = parallel blinded poll, debate = adversarial multi-turn dialectic with stances. Both get dedicated policies reflecting their different models.

## Overview

Stateful aimux tools (`investigate`, `think`, `consensus`, `debate`, `dialog`, `workflow`)
currently return status-shaped JSON responses (e.g. `{session_id, iteration:0, findings_count:0}`)
that describe **what is** instead of **what to do next**. Calling agents regularly misinterpret
the response as "work complete" and abandon sessions mid-workflow. This feature introduces a
**Response Guidance Layer** that transforms every stateful tool response into an instructional
envelope: where you are, what to do next, which branch to take (self-drive vs delegate), with
ready-to-paste example calls and explicit anti-patterns.

## Context

### The observed failure

On 2026-04-10 an agent called `investigate(action="start", topic="...")` and received the literal
response `{"session_id":"019d7973-...","iteration":0,"findings_count":0}`. The agent interpreted
this as "investigation launched in background" and moved on. The session stayed at zero findings
indefinitely. Diagnosis revealed the agent had skipped the multi-step flow description buried in
the tool's `description` field and assumed the name "investigate" implied autonomy.

### Why this is not a one-off

MCP schemas enforce parameter **syntax** (types, enums) but never **workflow semantics**. Any
LLM under load is likely to interpret tool names as autonomous verbs and skip dense description
prose. The problem generalizes to every stateful tool in aimux — `investigate`, `think` with
multi-step patterns, `consensus`, `debate`, `dialog`, `workflow` all have implicit state
machines the agent must drive manually, and none of them currently tell the agent how.

### What exists today

- 13 MCP tools registered in `pkg/server/server.go`
- 6 of them are stateful (maintain session state across calls)
- Each handler builds its response inline; there is no shared response composition layer
- Tool descriptions are single-paragraph strings; no structured WHEN/HOW/CHOOSE sections
- Only `exec` has a delegation path (direct CLI call); other tools have no autonomous mode
- Parallel problem: Issue #8 — async agent path does not wire OnOutput, so delegated jobs
  cannot stream progress back

### User statement of intent

> Calling agent must clearly understand how to use a tool, and in which cases. Any such tool
> the agent must be able to invoke for its own reasoning, or hand off to a delegate. Do not
> rename anything. Rethink the invocation, the hints. Make tools smart. aimux must SPEAK to
> the agent, not just return success.

## Functional Requirements

### FR-1: Guidance envelope on every stateful tool response

Every response from a stateful tool MUST be a JSON object matching this exact outer shape:

```json
{
  "state":               "<short machine identifier>",
  "you_are_here":        "<one-line position>",
  "how_this_tool_works": "<one-sentence tool role reminder>",
  "choose_your_path":    { ... },
  "gaps":                [ ... ],
  "stop_conditions":     "<plain-English>",
  "do_not":              [ ... ],
  "result":              { <original raw handler output> }
}
```

All guidance fields are **individually optional** and emitted only when applicable. The
`result` field is **always present** for handlers that produce output and contains the
raw, untouched handler result — the same JSON that the handler would have returned before
this feature existed.

Field semantics:

| Field | Purpose |
|-------|---------|
| `state` | Short machine-readable state identifier (e.g. `"notebook_ready"`, `"4_findings_collecting"`, `"ready_to_report"`) |
| `you_are_here` | One-line human-readable current position |
| `how_this_tool_works` | One-sentence reminder of what the tool does NOT do (present only on `start` or when agent appears confused) |
| `choose_your_path` | Named branches (`self`, `delegate`, `hybrid`) each with `when`, `next_call`, `example`, `then` |
| `gaps` | List of missing coverage areas or unsatisfied preconditions |
| `stop_conditions` | Plain-English description of when the workflow is considered complete |
| `do_not` | Explicit anti-pattern list |
| `result` | Nested raw handler output — strict separation from guidance metadata |

**Example — investigate(action="start") response:**

```json
{
  "state": "notebook_ready",
  "you_are_here": "Iteration 0. Zero findings. Convergence 0.0. Coverage 0.0.",
  "how_this_tool_works": "This is a scratchpad for YOUR investigation. It does not research anything itself.",
  "choose_your_path": {
    "self": { "when": "...", "next_call": "investigate(action='finding', ...)", "example": "..." },
    "delegate": { "when": "...", "next_call": "investigate(action='auto', ...)", "example": "..." }
  },
  "gaps": ["assumptions", "claims", "alternatives", "blind_spots", "ranking"],
  "stop_conditions": "convergence >= 1.0 AND coverage >= 80%",
  "do_not": ["Do not assume this tool researches in the background."],
  "result": {
    "session_id": "019d7973-8cd0-7c31-9fb5-26509c08ab12",
    "iteration": 0,
    "findings_count": 0
  }
}
```

**Breaking change notice:** Callers that previously read top-level fields such as
`response.session_id` MUST update to read `response.result.session_id`. This is an
intentional API contract change motivated by Constitution Principle 3 (Correct Over Simple) —
flat merging with prefixed guidance keys was considered but rejected because it invites
future field-name collisions and makes the envelope ambiguous about which fields are
caller contract vs. advisory metadata. Clean nested separation makes the distinction
unambiguous at every call site.

### FR-2: Per-tool next-action planning

Each stateful tool MUST have a dedicated `ToolPolicy` implementation that, given the current
session state, computes a `NextActionPlan`. The policy MUST:
- Detect the current workflow phase (initial, in-progress, stalled, ready-to-finalize)
- Identify missing coverage areas or unsatisfied preconditions
- Recommend a branch: `self` (agent drives next step manually) or `delegate` (agent hands off
  to a sub-CLI via `exec`) or `hybrid` (mix of both)
- Produce at least one concrete, paste-ready example call per recommended branch

### FR-3: Autonomous delegation mode — `action="auto"`

Every stateful tool MUST support an `action="auto"` invocation that delegates the entire
workflow to an AI CLI via `exec`. The `auto` handler MUST:
- Construct a prompt embedding the tool's state machine contract (required fields, output
  format, termination conditions)
- Dispatch via `exec` with an appropriate role (configurable per tool, default `thinkdeep`)
- Parse the CLI's output into state mutations (findings, assessments, reports)
- Return a job identifier the agent can poll via `status`
- Mark the session with `source="delegate"` so downstream analysis can distinguish
  agent-driven from delegated investigations
- **Comply with Constitution P26 (Long-Running Tool Calls Must Be Interruptible):**
  `auto` MUST be async by default, MUST support cancel via
  `sessions(action="cancel", job_id=...)` with cascade kill of child CLI processes, and
  MUST stream progress via `notifications/progress`. A sync-only `auto` implementation is
  explicitly forbidden — see NFR-6 below.

### FR-4: Structured tool descriptions

Every stateful tool description registered at MCP initialization MUST follow a structured
schema with these sections:
- **WHAT** (one sentence, imperative mood)
- **WHEN** (bulleted list of triggering situations)
- **WHY** (one sentence on the problem it solves)
- **HOW** (numbered flow of actions)
- **CHOOSE** (self vs delegate decision guidance)

Descriptions MUST call out explicitly what the tool does NOT do, to pre-empt autonomous
misinterpretation.

### FR-5: Handler–guidance separation of concerns

Internal tool handlers MUST return a typed `HandlerResult` struct containing the raw handler
output and a state snapshot. A central adapter in the MCP server layer wraps handlers, invokes
the guidance layer, and produces the final `CallToolResult`. External tool registration
signatures do NOT change — the adapter bridges the new internal contract to the existing
`mark3labs/mcp-go` surface.

### FR-6: Inactivity-based stall detection for active jobs

The guidance layer MUST detect stalled **active jobs** — a job in `running` state where no
new output has been written via the `OnOutput` callback for a threshold interval.

This replaces the earlier "session without state change" framing. A session without state
change is not necessarily a problem (the agent may be intentionally paused). An active job
with no output stream activity IS a problem — either the underlying CLI is hung, network
is stalled, or reasoning has exceeded reasonable limits.

**Data source:** `JobManager` exposes `job.LastOutputAt` — the timestamp of the most recent
`AppendProgress` call. The guidance layer computes `time.Since(job.LastOutputAt)` when
building a `status` response for a running job, and emits guidance fields based on the
following tiers:

| Tier | Inactivity (sec) | Envelope signal | Agent recommendation |
|---|---:|---|---|
| Grace period | 0 – 60 | none | job is reasoning/initializing, wait |
| Soft warning | 60 – 120 | `warning: "silent for Ns, still running"` | wait a bit more, nothing to do yet |
| Hard stall | 120 – 600 | `alert: "stalled >Ns, consider cancel"` | `choose_your_path.cancel_now` becomes recommended |
| Auto-cancel | ≥ 900 | job transitions to `status=failed`, `reason="stalled"` | cascade kill of child CLI processes |

**Grace period tuning:** 60 seconds covers typical LLM reasoning-initialization (e.g. codex
with `reasoning_effort=high` takes 10-30 sec to first token, with safety margin for network
latency). This is longer than earlier proposals because reasoning-heavy tasks legitimately
produce nothing during the think phase.

**Auto-cancel safety:** 900 seconds (15 minutes) is the default auto-cancel floor. This
protects against runaway jobs and budget overrun. Users who run very long tasks legitimately
(training loops, large-scale refactors) MUST explicitly raise this via config.

**Configuration:** All four thresholds are global defaults in `config/default.yaml`, not
per-tool (streaming behavior is a property of the executor/CLI, not the tool):

```yaml
streaming:
  grace_period_seconds: 60
  soft_warning_seconds: 120
  hard_stall_seconds: 600
  auto_cancel_seconds: 900
```

**Why not per-tool:** The `exec` tool's OnOutput plumbing is the same for every CLI — the
stall detection logic lives in the guidance layer above the tool handler, reading the same
`LastOutputAt` field. Per-tool overrides would introduce coupling that has no real benefit.

**Prerequisite:** `JobManager.AppendProgress` MUST update `LastOutputAt` atomically with
the progress append. A one-line change to `pkg/session/jobs.go`.

**Async-path coverage:** Soft warning + hard stall + auto-cancel all depend on OnOutput
being wired for the execution path. For the `exec` tool, this already works. For the
`agent` tool async path, this is blocked on engram issue #8 — once #8 lands, FR-6 extends
to agent jobs automatically without further changes.

### FR-7: Precondition validation

When an agent calls an action out of order (e.g. `report` before any `finding` has been
recorded), the guidance layer MUST convert the error into an instructional response containing
the corrective next call and an example. Silent failure (e.g. producing an empty report) is
forbidden.

### FR-8: Backward-compatible result payload

The envelope MUST be additive. Callers that previously read specific fields of the raw result
continue to work without modification. The guidance layer fields sit alongside the raw payload,
not in place of it.

### FR-9: Incremental rollout path

The feature MUST be deliverable in four phases, each independently merge-ready:

- **Phase 1:** Guidance infrastructure (ResponseBuilder, NextActionPlanner, GuidanceFormatter,
  ToolPolicy interface) + `investigate` full policy + description rewrite. NO `action="auto"`.
  Async/cancel/streaming scaffolding added but no auto-consumer yet.
- **Phase 1.5:** `action="auto"` for `investigate` only, blocked on engram issue #8 fix
  (OnOutput wiring in handleAgentRun async path). Delivered as a separate PR after #8 lands.
  Must pass the P26 integration test (start → cancel → cascade kill verified).
- **Phase 2:** `think` policy — covers all 23 patterns with a single policy file that
  dispatches per-pattern (5 stateful patterns get multi-step guidance, 18 one-shot
  patterns get a static "result already complete, don't re-call" envelope).
- **Phase 3:** `consensus`, `debate`, `dialog`, `workflow` policies — **full ToolPolicy
  implementations for all four, same depth as investigate and think**. Coverage includes:
  - `consensus` — parallel blinded multi-model poll. Policy flags when <2 CLIs are
    available (degenerates to single-model, not actual consensus). `next_call` suggests
    `synthesize=true` if the caller missed it. Stall detection via inactivity. Single-shot
    lifecycle but full envelope.
  - `debate` — adversarial multi-turn with stance assignment. Policy tracks current turn,
    exposes `current_turn` and `max_turns` in `you_are_here`, recommends `action="cancel"`
    if debate stalls on a single stance, recommends synthesis call when `max_turns` is
    reached. `action="auto"` delegates full debate to a chosen moderator CLI.
  - `dialog` — sequential two-CLI turn-taking. Policy tracks turn count and participant
    identities, warns on turn imbalance (one side dominating). Stall detection tracks
    time since last participant response.
  - `workflow` — multi-stage declarative pipeline. Policy reports current stage, which
    stages remain, and which stages failed with resumable state. `choose_your_path`
    offers resume-from-failed-stage or skip-stage branches.

  Symmetric treatment: same envelope shape, same policy interface, same integration
  test template. Phase 3 is the largest but is mostly replication of the Phase 1-2 pattern.

Each phase MUST be gated by passing tests and explicit opt-in — no big-bang cutover.

## Non-Functional Requirements

### NFR-1: Performance
Guidance layer overhead MUST add no more than **1 ms** per stateful tool call at p95, measured
over 1000 representative invocations. Policies are pure functions with no IO, cache lookups, or
goroutine spawns.

### NFR-2: Testability
Every `ToolPolicy` implementation MUST be unit-testable with fabricated state snapshots and
zero dependencies on MCP transport, session stores, or external services. Test coverage for
policy code MUST be at least 85%.

### NFR-3: Backward compatibility — structured migration, not silent preservation
Zero changes to external tool registration signatures. Zero changes to JSONSchema parameter
definitions for existing actions. Zero changes to tool names.

**Result payload IS breaking.** Per FR-1, raw handler output moves from the response root
to a nested `result` field. Callers that read `response.session_id` MUST migrate to
`response.result.session_id`. This is a deliberate tradeoff — see the FR-1 rationale on
why flat merging was rejected.

Migration support:

- **In-repo tests and handlers:** updated in the same Phase 1 PR that introduces the
  envelope. All internal tests (`pkg/server/*_test.go`, `test/e2e/*`) are updated atomically.
- **External callers** (mcp-mux, Claude Code clients, any user scripts): tracked in an
  aimux release note section. The version bump for Phase 1 MUST be MINOR (additive:
  envelope is new) but with a prominent **BREAKING** callout in release notes for callers
  reading nested fields.
- **Migration helper:** a small utility function `guidance.UnwrapResult(response)` is
  exposed in the Go SDK for consumers that want a one-line migration path. Returns the
  nested `result` field if present, otherwise the response itself (for tools without
  guidance).
- **Deprecation grace:** none. The envelope is introduced once, atomically, per tool. Tools
  without a registered policy continue to return the flat raw result (loud-dev / silent-prod
  per Q3). Once a tool gets a policy, its response shape changes in that commit.

**Non-goals of "backward compatibility":** this feature does NOT attempt to serve both
shapes from the same endpoint. Dual-shape responses double envelope size, confuse
clients, and delay the structural fix indefinitely.

### NFR-4: Observability
Every guidance-layer-emitted next-action recommendation MUST be logged with the session ID,
tool name, and chosen branch. Logs enable later analysis of which recommendations agents
actually followed vs ignored.

### NFR-5: Maintainability
Adding guidance to a new stateful tool MUST require no more than three file touches:
implement `ToolPolicy`, register it in the central adapter, update the tool description. No
changes to the guidance layer core for new tool onboarding.

### NFR-6: Interruptibility (Constitution P26)
Every delegating or long-running action introduced or modified by this feature MUST comply
with Constitution Principle 26 (Long-Running Tool Calls Must Be Interruptible):

- **Async by default** — any action that may run longer than 10 seconds returns a `job_id`
  immediately and executes in the background. The first concrete target is `action="auto"`
  on every stateful tool.
- **Cancelable** — `sessions(action="cancel", job_id=...)` terminates the job and cascade-kills
  all child CLI processes. The guidance layer MUST surface the cancel call in
  `choose_your_path` whenever a job is running.
- **Progress-streamed** — every intermediate state change (finding added, assess run, partial
  report) MUST be pushed as an MCP `notifications/progress` event AND appended to
  `status(job_id).progress`. Polling clients and streaming clients both see updates.
- **Fail-fast on missing infrastructure** — if the OnOutput callback, progress push, or
  cancel handler is not wired for a given code path, that path MUST refuse to start a job
  with a clear error. Silent degradation to a sync-blocking implementation is prohibited.

**Enforcement:** every Phase 1/2/3 PR MUST include an integration test that starts an async
action, cancels it mid-run, and asserts cascade termination. The existing `exec` tool's
async path is the reference implementation.

**Cross-reference:** Engram issue #8 (handleAgentRun async path does not wire OnOutput) is a
current P26 violation. Fixing it is a prerequisite for Phase 1.5 delivery of `action="auto"`.

## User Stories

### US1: Agent starts an investigation and receives actionable guidance (P1)

**As an** orchestrating LLM agent,
**I want** the `investigate(action="start")` response to tell me exactly what to do next,
**so that** I do not assume the tool is running autonomously and stall the session.

**Acceptance Criteria:**
- [ ] Calling `investigate(action="start", topic="...")` returns a response containing a non-empty `choose_your_path` object with at least two branches
- [ ] The response contains `how_this_tool_works` on the first call of a session
- [ ] At least one branch contains a `next_call` string that is a valid, paste-ready tool invocation
- [ ] The response contains `do_not` with at least one explicit anti-pattern
- [ ] The observed failure mode (agent interprets `start` as "done") does not recur in
  regression tests that simulate the agent reading only the response and deciding next action

### US2: Agent adds a finding and sees which coverage gaps remain (P1)

**As an** orchestrating LLM agent mid-investigation,
**I want** each `finding` response to tell me which coverage areas are still empty,
**so that** I know when to stop adding findings and when to call `assess` or `report`.

**Acceptance Criteria:**
- [ ] Response includes `gaps` listing unsatisfied coverage areas by name
- [ ] Response includes `you_are_here` describing current coverage state (e.g. "3 findings,
  3/5 areas covered")
- [ ] When all coverage areas are satisfied, response recommends `assess` or `report` in
  `choose_your_path`

### US3: Agent tries to report with insufficient evidence and gets a corrective response (P1)

**As an** orchestrating LLM agent,
**I want** `report` calls on incomplete sessions to return guidance instead of a hollow report,
**so that** I do not ship a report based on zero findings.

**Acceptance Criteria:**
- [ ] `report` with zero findings returns an error-shaped response with a corrective
  `next_call` example
- [ ] `report` with findings but low convergence returns a report marked PRELIMINARY with
  `gaps` showing what would be needed to produce a FINAL report
- [ ] `report(force=true)` produces the report regardless, but the envelope still notes the
  incompleteness

### US4: Agent delegates the full investigation to a sub-CLI (P2)

**As an** orchestrating LLM agent with limited context budget,
**I want** to hand off the entire investigation workflow to a delegate via `action="auto"`,
**so that** I receive a completed report without driving the state machine manually.

**Acceptance Criteria:**
- [ ] `investigate(action="auto", topic="...")` returns a job identifier
- [ ] Polling `status(job_id)` surfaces intermediate findings as they are parsed from the
  delegate's output (when Issue #8 is resolved — otherwise surfaces only the final state)
- [ ] On job completion, the session contains findings and a report generated by the delegate
- [ ] The session metadata records `source="delegate"` and the CLI used

### US5: Agent discovers correct usage from the tool description at registration (P2)

**As an** orchestrating LLM agent initializing a session,
**I want** tool descriptions to follow a structured WHEN/WHY/HOW/CHOOSE schema,
**so that** I understand when to use each tool before I call it.

**Acceptance Criteria:**
- [ ] All stateful tool descriptions contain WHAT, WHEN, WHY, HOW, CHOOSE sections
- [ ] Each description explicitly states what the tool does NOT do
- [ ] Descriptions are no longer single-paragraph prose

### US6: Stalled session gets a proactive alert (P3)

**As an** orchestrating LLM agent returning to a previously abandoned session,
**I want** the `status` response to flag the session as stalled and recommend next steps,
**so that** I do not accidentally continue a session that has lost relevance.

**Acceptance Criteria:**
- [ ] Active jobs without output for more than the `hard_stall_seconds` threshold return responses with
  `alert: "stalled >Ns, consider cancel"` and an age field
- [ ] The alert includes options to resume, abandon, or delegate

## Terminology — Distinguishing Stateful Tools

Each tool covered by this feature has a distinct semantic purpose. The policy per tool
must reflect this — they are NOT interchangeable.

| Tool | Semantic model | State shape | Single-shot or multi-call? |
|------|---------------|-------------|----------------------------|
| `investigate` | Scratchpad for structured investigation by the caller agent | Persistent session, N findings accumulated across calls, convergence/coverage metrics | Multi-call: start → finding × N → assess → report |
| `think` | Single-model structured reasoning (23 patterns) | 5 patterns are multi-step stateful (scientific_method, debugging_approach, sequential_thinking, collaborative_reasoning, structured_argumentation); 18 are one-shot | Mixed: depends on pattern |
| `consensus` | **Parallel blinded multi-model expert opinion poll.** Each participating CLI answers the same prompt in isolation; optional synthesis step produces an aggregate verdict. No cross-participant communication. | Orchestrator run, N parallel results, optional synthesis output | Single-shot: one call returns full result |
| `debate` | **Adversarial multi-turn discussion with assigned stances.** Participants argue opposing sides, see each other's arguments, refine positions over multiple turns. Truth is expected to emerge from the dialectic, not from a vote. Fundamentally different from consensus. | Orchestrator run with rounds, per-round arguments and rebuttals, optional verdict synthesis | Single-shot from caller's perspective (one `debate` call runs all N rounds), but internally multi-turn stateful |
| `dialog` | Sequential turn-taking between two CLIs for collaborative exploration. Not adversarial like debate — participants build on each other's points. | Orchestrator run with turns alternating between two participants | Single-shot from caller's perspective, internally multi-turn |
| `workflow` | Declarative multi-stage pipeline. Each stage may invoke another tool. Stage failures are resumable. | Persistent workflow run, current stage pointer, per-stage state | Single-shot from caller, but inspectable and resumable mid-execution |

**Key distinction (consensus vs debate):** `consensus` is a VOTE — independent opinions
collected in parallel, optionally aggregated. `debate` is a DIALECTIC — participants
argue, rebut, and refine across turns. A debate where participants never see each other
is just a consensus; a consensus with turn-taking is just a degenerate dialog. The
guidance policies for these three tools MUST reflect the semantic difference — same
envelope shape, different `how_this_tool_works` explanations, different `choose_your_path`
recommendations.

**Current implementation status** (verified via SocratiCode search of `pkg/orchestrator/debate.go`):
`StructuredDebate` is already implemented with the correct adversarial multi-turn semantic
("Participants have assigned stances (for/against) and see each other's arguments"). No
refactoring needed — the debate tool already matches the intended semantics above.

## Edge Cases

- **Action called before session exists** (e.g. `finding` with unknown `session_id`): guidance
  layer returns a constructive error with the `start` call as the next action.
- **Action called with malformed parameters**: MCP schema rejects the call before it reaches
  the handler. Guidance layer is not involved.
- **Concurrent modifications** — two agents calling `finding` on the same session in parallel:
  session state mutation is atomic; both calls succeed; each response reflects the state at
  the moment of completion.
- **`auto` mode delegate crashes mid-run**: job transitions to failed with a partial state;
  next guidance response offers resume, abandon, or re-delegate options.
- **Session state store failure**: handler returns an infrastructure error; guidance layer
  wraps with retry recommendation; no loss of guidance shape.
- **Extremely long session (hundreds of findings)**: guidance envelope stays bounded in size
  — `gaps` and `examples` are capped at 10 items; a truncation marker is included if capped.
- **Tool without a `ToolPolicy` implementation** (new tool added without guidance):
  **Loud-dev / silent-prod** fallback. In development builds (`-tags=dev` or `AIMUX_ENV=development`),
  registration-time check `panics` with a clear message listing the missing tool and the
  required file path (`pkg/guidance/policies/<tool>.go`). In production builds, the adapter
  returns a minimal envelope `{state: "guidance_not_implemented", result: <raw>}`, logs a
  WARNING once per tool on first call (via `sync.Once` to avoid log spam), and continues.
  Rationale: breaking production on a missing policy is worse than silent degradation, but
  in dev we want the gap to be loud and immediate so contributors cannot miss it.
- **`action="auto"` called on a session that already has findings**: default behavior is to
  append delegate findings to existing ones; the envelope clearly labels the merged source.

## Out of Scope

- Guidance layer for **one-shot tools** (`exec`, `status`, `sessions`, `audit`) — they already
  communicate results clearly without a state machine.
- **Streaming progress for `action="auto"`** — depends on Issue #8 (OnOutput wiring in async
  agent path). Phase 1 `auto` implementations run synchronously until #8 lands.
- **Migrating mcp-mux, engram, or other MCP servers** to the same envelope shape — cross-project
  standardization is a separate conversation.
- **User-visible UI changes** in Claude Code or other MCP clients — envelope is additive, any
  client renders it correctly as-is.
- **Runtime-editable policies** — policies live in Go code, not config files, in Phase 1-3.
  A possible follow-up is moving policies to `config/policies.d/`, but not in this feature.
- **Cross-tool workflow orchestration** — the guidance layer advises on a single tool at a
  time; workflows that chain multiple tools (e.g. `investigate → think → consensus`) are the
  `workflow` tool's job, out of scope here.
- **Machine-learning-driven next-action planning** — policies are handcrafted pure functions
  for Phase 1-3. Learned ranking is out of scope.

## Dependencies

- `pkg/server/server.go` (existing) — MCP tool router; receives new adapter layer
- `pkg/investigate/` (existing) — investigate session state model
- `pkg/think/` (existing) — think session state model
- `pkg/agents/runner.go` (existing) — agent runner used by `action="auto"`
- Go standard library only — no new third-party dependencies
- **Blocked dependency:** Issue #8 (async agent OnOutput wiring) — required for streaming
  `auto` mode but not required for sync `auto` mode or any non-auto guidance

## Success Criteria

- [ ] Regression test: simulated agent receiving `investigate(action="start")` response and
  choosing next action reaches a final `report` state in >95% of runs across 100 trials
- [ ] Zero stalled active jobs (no output for >15 min) in telemetry
  across 1 week of production use after Phase 1 ships
- [ ] 100% of stateful tools have a registered `ToolPolicy` by end of Phase 3
- [ ] 100% of stateful tools have WHEN/WHY/HOW/CHOOSE structured descriptions by end of
  Phase 3
- [ ] Benchmark: p95 guidance overhead ≤1 ms per call on representative workload
- [ ] Policy unit test coverage ≥85%
- [ ] Migration verified: all in-repo tests and handlers updated to read raw data from
  `response.result.*` in the same Phase 1 PR; release notes contain a BREAKING section
  listing every callsite change required for external consumers
- [ ] P26 integration test template green for every stateful tool: start async action →
  cancel mid-run → assert cascade termination → assert no goroutine leaks
- [ ] Loud-dev / silent-prod policy-missing fallback verified by a dev-build test that
  panics when a stateful tool is registered without a matching ToolPolicy

## Open Questions

_All clarification questions resolved in session 2026-04-11. Spec is ready for `/nvmd-plan`._
