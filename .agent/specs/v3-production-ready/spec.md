# Feature: aimux v3 Production Readiness

**Slug:** v3-production-ready
**Created:** 2026-04-05
**Status:** Draft
**Author:** AI Agent (reviewed by user)

## Overview

Close all scaffold gaps in the aimux v3 Go rewrite (D:\Dev\aimux) to make the binary a production-ready drop-in replacement for v2 TypeScript. Currently the repo has 113 tests, 10 MCP tools, and compiles — but 8 tool handlers return "wiring pending" placeholders, executors return "not yet implemented", and deep research is a stub.

## Context

The go-rewrite-v3 spec produced a complete architectural scaffold: 18 packages, 10 MCP tools registered, 5 orchestrator strategies, 3 executor types. However, the scaffold has these gaps:

1. **ConPTY/PTY executors**: `Run()` returns error "not yet fully implemented". Only Pipe executor works.
2. **6 tool handlers return static strings**: audit, consensus, debate, dialog, agents all say "orchestrator wiring pending" instead of calling their strategies.
3. **DeepResearch**: returns "API integration pending" — no Google GenAI Interactions API integration.
4. **exec(coding) pair wiring**: PairCoding strategy params prepared but falls through to direct exec.
5. **No GitHub remote**: repo exists only locally at D:\Dev\aimux.
6. **Race detector never ran**: no GCC on Windows, CI not triggered (no remote).
7. **Agents tool**: returns empty list — agent discovery not wired to AgentRegistry.

Constitution (16 principles) and ADR-014 (20 decisions) govern all changes.

## Functional Requirements

### FR-1: ConPTY Executor Implementation
The ConPTY executor MUST spawn CLI processes via Windows CreatePseudoConsole API, read unbuffered text output, support timeout/cancel via context, and strip ANSI escapes from output. MUST fall back to Pipe executor if ConPTY unavailable.

### FR-2: PTY Executor Implementation
The PTY executor MUST spawn CLI processes via Unix pseudo-terminal, read unbuffered text output, support timeout/cancel, and strip ANSI. MUST fall back to Pipe executor if PTY unavailable (Docker, CI).

### FR-3: Tool→Strategy Wiring
Every MCP tool handler that currently returns a placeholder MUST call its corresponding orchestrator strategy through the real executor chain:
- audit → AuditPipeline strategy
- consensus → ParallelConsensus strategy
- debate → StructuredDebate strategy
- dialog → SequentialDialog strategy
- agents → AgentRegistry.Discover + List/Find/Run

### FR-4: Pair Coding End-to-End
The exec tool with role=coding MUST invoke PairCoding strategy: driver produces diff via real executor → diff parsed into hunks → reviewer validates per-hunk via real executor → approved hunks returned or applied.

### FR-5: DeepResearch API Integration
The deepresearch tool MUST call Google Gemini Deep Research API (Interactions API), upload files via Files API, track progress via MCP notifications every 15 seconds (using existing ProgressBridge), support caching (exact match by topic+format+model+files_hash), and return structured results.

### FR-6: Agent Discovery Wiring
The agents tool MUST call AgentRegistry.Discover() at startup, scan configured directories, and return real agent lists for list/find/info/run actions.

### FR-7: GitHub Repository
The v3 repo MUST be pushed to GitHub as github.com/thebtf/aimux with CI running (go test -race, vet, golangci-lint on 3 platforms).

## Non-Functional Requirements

### NFR-1: Performance
ConPTY/PTY text mode MUST be within 1.5x wall clock time of direct CLI invocation (baseline: same prompt via `codex -p "..." --full-auto` directly in shell). Pipe+JSON mode measured at 3.1x in v2 benchmarks — ConPTY/PTY MUST eliminate this overhead.

### NFR-2: Race Detector Clean
`go test -race ./...` MUST pass with zero data races on Linux (CI).

### NFR-3: mcp-mux Compatibility
Binary MUST work as mcp-mux upstream: stdio transport, standard MCP protocol, tool/resource/prompt responses match expected format.

### NFR-4: Zero Placeholder Responses
No tool handler MAY return hardcoded strings like "wiring pending" or "not yet implemented" for any supported action. Every code path MUST produce real behavior or a structured TypedError. Partial output MUST be preserved on timeout (Constitution P7).

### NFR-5: Error Propagation
Every wired tool handler MUST propagate TypedError from strategy/executor without silent swallowing. Strategy failures → TypedError with partial output. Executor failures → TypedError with cause chain. No string error messages at API boundaries.

## User Stories

### US1: Working exec(coding) with Pair Review (P1)
**As an** orchestrating agent, **I want** exec(role="coding") to produce pair-reviewed code end-to-end, **so that** no unreviewed code reaches disk.

**Acceptance Criteria:**
- [ ] exec(role="coding", prompt="add hello function") spawns driver CLI, gets diff
- [ ] Diff parsed into hunks, sent to reviewer CLI
- [ ] Reviewer verdicts (approved/modified/rejected) applied correctly
- [ ] Result includes ReviewReport with hunk counts and round count
- [ ] Fire-and-forget mode returns immediately with job_id

### US2: Working Multi-Model Tools (P1)
**As an** orchestrating agent, **I want** consensus/debate/dialog tools to actually run multi-model orchestration, **so that** I get real cross-model perspectives.

**Acceptance Criteria:**
- [ ] consensus(topic="...") spawns 2+ CLIs in parallel, returns blinded opinions
- [ ] debate(topic="...") runs adversarial turns with stance assignment
- [ ] dialog(prompt="...") runs sequential multi-turn with visible history
- [ ] All three support async mode with job_id return

### US3: Working Audit Pipeline (P1)
**As an** orchestrating agent, **I want** audit tool to run the full scan→validate→investigate pipeline, **so that** I get verified findings with confidence grades.

**Acceptance Criteria:**
- [ ] audit(mode="quick") scans codebase and returns FINDING: lines
- [ ] audit(mode="standard") validates findings via cross-model check
- [ ] audit(mode="deep") investigates HIGH+ findings
- [ ] Results include timing (pipeline stats) and finding counts

### US4: PTY/ConPTY Unbuffered Output (P2)
**As an** operator, **I want** ConPTY on Windows and PTY on Linux to provide unbuffered text output, **so that** JSON mode overhead is eliminated.

**Acceptance Criteria:**
- [ ] ConPTY executor spawns process, reads text output line-by-line
- [ ] PTY executor spawns process on Linux/Mac, reads text output
- [ ] Automatic fallback to Pipe when PTY/ConPTY unavailable
- [ ] ANSI escapes stripped from PTY/ConPTY output

### US5: GitHub + CI Green (P2)
**As a** developer, **I want** the repo on GitHub with passing CI, **so that** contributors can verify builds and PRs get automated checks.

**Acceptance Criteria:**
- [ ] GitHub repo created at github.com/thebtf/aimux
- [ ] CI runs on push to master and PRs
- [ ] go test -race passes on ubuntu-latest
- [ ] go vet + golangci-lint pass

## Edge Cases

- ConPTY not available (Docker, old Windows) → automatic Pipe fallback, no error
- PTY not available (Windows) → automatic Pipe fallback
- All participants in consensus fail → return error, not empty result
- Driver in pair coding produces non-diff output → passthrough as content (no review)
- Reviewer produces unparseable JSON → auto-approve with warning (graceful degradation)
- DeepResearch API key not configured → clear error with setup instructions
- Agent directory doesn't exist → empty list, no error

## Out of Scope

- v2→v3 data migration (clean break per go-rewrite-v3 spec)
- GUI/web interface (MCP + CLI only)
- Multi-user auth (single-user MCP over stdio)
- New tools not in v2 (feature parity first, new features after)

## Dependencies

- `golang.org/x/sys/windows` — ConPTY syscalls (CreatePseudoConsole)
- `github.com/creack/pty` — Unix PTY allocation
- `google.golang.org/genai` — Official Google Gen AI Go SDK (Deep Research API). Fallback: HTTP client to `generativelanguage.googleapis.com` REST API if SDK unavailable.
- GitHub CLI (`gh`) — repo creation and CI

## Success Criteria

- [ ] All 10 MCP tools return real results (zero placeholder responses)
- [ ] exec(role="coding") produces pair-reviewed diff end-to-end
- [ ] `go test -race ./...` passes on Linux (CI green)
- [ ] Binary works as mcp-mux drop-in (smoke test with real MCP client)
- [ ] Grep for "not yet implemented|wiring pending|pending" returns 0 results in pkg/

## Clarifications

### Session 2026-04-05

| # | Category | Question | Resolution | Date |
|---|----------|----------|------------|------|
| C1 | Performance | What is the baseline for NFR-1 "<3x overhead"? | Baseline = direct CLI shell invocation. Target: within 1.5x wall clock. | 2026-04-05 |
| C2 | Reliability | How should wired tool handlers propagate errors? | TypedError from strategy/executor, no silent swallowing, partial output preserved. | 2026-04-05 |
| C3 | Integration | Which Go SDK for Google GenAI? | `google.golang.org/genai` (official). Fallback: REST HTTP client. | 2026-04-05 |
| C4 | Carry-forward | What happens to go-rewrite-v3 tasks? | This spec supersedes. Checked items = baseline. Gaps become tasks here. | 2026-04-05 |

## Open Questions

None — all architecture locked in go-rewrite-v3 ADR-014. Clarifications resolved above.
