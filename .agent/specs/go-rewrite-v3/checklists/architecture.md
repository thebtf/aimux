# Architecture Requirements Quality Checklist

**Feature:** mcp-aimux v3 — Full Go Rewrite
**Focus:** Architecture + cross-cutting concerns
**Created:** 2026-04-05
**Depth:** Standard

## Requirement Completeness

- [ ] CHK001 Are all 10 MCP tools enumerated with their parameter lists in spec? [Completeness, Spec §FR-1]
- [ ] CHK002 Are all 4 session modes (LiveStateful, OnceStateful, OnceStateless, Auto) defined with when-to-use criteria? [Completeness, Spec §FR-4]
- [ ] CHK003 Are all 3 executor implementations (ConPTY, PTY, Pipe) specified with platform selection rules? [Completeness, Spec §FR-2]
- [ ] CHK004 Are WAL replay recovery scenarios fully enumerated (running job, completing job, live session)? [Completeness, Clarification C1]
- [ ] CHK005 Are all 7 CLI profile plugins specified with their unique flags/templates? [Completeness, Spec §FR-5]
- [ ] CHK006 Are circuit breaker states and transitions documented (closed→open→half-open)? [Completeness, ADR Decision 19]
- [ ] CHK007 Are holdout evaluation split ratios and scoring weights defined? [Completeness, ADR Decision 20]

## Requirement Clarity

- [ ] CHK008 Is "fire-and-forget" mode behavior precisely defined — what exactly does aimux write to disk vs return to caller? [Clarity, Spec §FR-3]
- [ ] CHK009 Is "per-hunk review" granularity defined — what constitutes a hunk? Git diff hunk? File-level? [Clarity, Spec §FR-3]
- [ ] CHK010 Is "domain trust hierarchy" quantified — does authoritative mean "always wins" or "weighted higher"? [Clarity, ADR Decision 17]
- [ ] CHK011 Is "composable prompt templates" includes mechanism specified — recursive? max depth? circular reference handling? [Clarity, ADR Decision 18]
- [ ] CHK012 Is "auto" session strategy heuristic defined — what specific signals trigger LiveStateful vs OnceStateful? [Clarity, ADR Decision 11]
- [ ] CHK013 Is CLI version detection mechanism specified — how does template engine determine CLI version for version overrides? [Clarity, ADR Decision 12]

## Requirement Consistency

- [ ] CHK014 Are pair coding modes (fire-and-forget vs complex) consistent between spec FR-3 and plan Phase 4? [Consistency, Spec §FR-3 ↔ Plan §Phase 4]
- [ ] CHK015 Are session modes in spec FR-4 consistent with session types in plan data model? [Consistency, Spec §FR-4 ↔ Plan §Data Model]
- [ ] CHK016 Are audit modes (quick/standard/deep) consistent between spec FR-7, plan Phase 6, and constitution P4? [Consistency, Spec §FR-7 ↔ Plan §Phase 6]
- [ ] CHK017 Are typed error types in spec FR-9 consistent with executor Result struct in plan? [Consistency, Spec §FR-9 ↔ Plan §API Contracts]
- [ ] CHK018 Do all 20 ADR decisions have corresponding tasks in tasks.md? [Consistency, ADR-014 ↔ tasks.md]

## Acceptance Criteria Quality

- [ ] CHK019 Can "audit quick mode under 5 min" be measured deterministically (same machine, same codebase, same model)? [Measurability, Spec §NFR-1]
- [ ] CHK020 Can "zero EPIPE crashes under 10,000 tool calls" be automated as a stress test? [Measurability, Spec §NFR-2]
- [ ] CHK021 Can "memory under 30MB with 5 concurrent sessions" be measured reproducibly? [Measurability, Spec §NFR-3]
- [ ] CHK022 Can "feature parity verified via feature-parity.toml" be run as CI gate? [Measurability, Spec §Success Criteria]

## Edge Case Coverage

- [ ] CHK023 Are requirements defined for ConPTY + codex interaction failure (PTY mode but codex still buffers)? [Edge Case, Gap]
- [ ] CHK024 Are requirements defined for LiveStateful session when CLI crashes mid-turn? [Edge Case, Spec §FR-4]
- [ ] CHK025 Are requirements defined for pair coding when reviewer CLI is down (circuit breaker open)? [Edge Case, ADR Decision 19]
- [ ] CHK026 Are requirements defined for WAL file corruption on disk? [Edge Case, Clarification C1]
- [ ] CHK027 Are requirements defined for concurrent pair coding sessions modifying same file? [Edge Case, Gap]
- [ ] CHK028 Are requirements defined for YAML config syntax errors (fail fast vs partial load)? [Edge Case, ADR Decision 13]
- [ ] CHK029 Are requirements defined for mcp-go SDK version mismatch with MCP protocol version? [Edge Case, Gap]

## Non-Functional Requirements

- [ ] CHK030 Are startup time requirements defined with specific measurement method? [NFR, Spec §NFR-5]
- [ ] CHK031 Are graceful shutdown requirements defined (drain in-flight jobs, flush WAL, close SQLite)? [NFR, Gap]
- [ ] CHK032 Are logging requirements defined (log levels, rotation, retention, format)? [NFR, ADR Decision 16]
- [ ] CHK033 Are observability requirements defined (metrics, health endpoint, diagnostic commands)? [NFR, Gap]

## Dependencies and Assumptions

- [ ] CHK034 Is assumption "mcp-go v0.47+ supports tools+resources+prompts+completions" validated? [Dependency, Plan §Tech Stack]
- [ ] CHK035 Is assumption "modernc.org/sqlite supports WAL mode" validated? [Dependency, Plan §Tech Stack]
- [ ] CHK036 Is assumption "creack/pty works on macOS ARM64" validated? [Dependency, Plan §Tech Stack]
- [ ] CHK037 Is assumption "ConPTY available in GitHub Actions Windows runners" validated? [Dependency, Plan §Phase 8]
- [ ] CHK038 Is assumption "codex text mode via ConPTY produces unbuffered line output" validated (the critical PoC)? [Dependency, Plan §Unknowns]
