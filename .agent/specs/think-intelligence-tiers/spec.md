# Feature: Think Patterns Intelligence Tiers

**Slug:** think-intelligence-tiers
**Created:** 2026-04-09
**Status:** Draft
**Author:** AI Agent (reviewed by user)

> **Provenance:** Specified by claude-opus-4-6 on 2026-04-09.
> Evidence from: smoke tests (graceful intelligence PR #27), original thinking-patterns
> analysis (schema comparison), user directive ("patterns должны генерировать смыслы
> и помогать агенту думать"), user feedback ("patterns = agent's self-dialogue, not CLI delegation").
> Confidence: VERIFIED (smoke tests, code analysis) / INFERRED (tier design).

## Overview

Transform think patterns from structured data calculators into genuine reasoning tools
that help agents think. Four tiers of intelligence, each building on the previous.
Patterns remain the agent's conversation with itself — not delegation to external CLIs.

## Context

**Current state (after PRs #25-#27):**
- 23 patterns with restored computational logic (DAG, Ca/Ce, EV/variance, etc.)
- Domain templates + keyword analysis for graceful minimal-input handling
- Guidance field in every response for progressive enrichment
- EnableSampling PoC (mock-tested, not production-verified)

**What's missing:**
- Patterns analyze KEYWORDS, not MEANING. "Design auth" matches template, "Build secure user access control" doesn't.
- No structural text analysis (sentence parsing, entity extraction, relationship inference)
- No heuristic gap detection ("you mentioned security but no threat model")
- Sampling not verified with real client
- Patterns don't compose (output of one can't feed another)

**Core principle (user directive):**
> Think patterns = agent's conversation with itself to structure own reasoning.
> NOT delegation to external CLIs (exec/consensus) — those require rebuilding context.
> Sampling OK because it uses the SAME client LLM, not a separate CLI.

## Intelligence Tiers

### Tier 1: Keyword + Template (DONE — PR #27)
Pattern extracts keywords from input text, matches domain templates,
returns pre-built analysis + guidance. No semantic understanding.

**Status:** Implemented. 10 domain templates. Graceful defaults.

### Tier 2: Structural Analysis + Forced Reflection Protocol
Two complementary mechanisms proven by production systems:

**A. Text Structure Analysis** (from graceful intelligence research):
- **Entity extraction:** "Design auth with OAuth2 for mobile app" → entities: [auth, OAuth2, mobile-app]
- **Relationship inference:** "auth for mobile" → relationship: auth SERVES mobile
- **Gap detection:** "Design auth" + no mention of "tokens"/"sessions"/"logout" → gaps: [token-management, session-handling, logout-flow]
- **Negation awareness:** "without database dependency" → constraint: no-database
- **Question detection:** "should we use JWT or sessions?" → unresolved: [token-type-decision]
- **Complexity estimation:** sentence count, conjunction density, qualifier count → low/medium/high/epic

**B. Forced Reflection Protocol** (from pal-mcp-server production evidence):
- **Schema-as-scaffold:** field descriptions ARE thinking instructions. `hypothesis` described as "Concrete root cause theory from evidence. Can revise." — the schema teaches the agent how to fill it.
- **Multi-step forced pause:** pattern returns "STOP. Before continuing, verify: [checklist]." Agent MUST complete checklist before next call. This externally structures the agent's reasoning process.
- **Confidence tracking:** 7-level confidence scale with precise definitions (none/low/medium/moderate/high/very-high/certain). Agent must self-assess after each step.
- **Mandatory evidence gates:** "Do NOT proceed to hypothesis without at least 3 evidence items."

**Evidence:** pal-mcp-server (D:\Dev\_EXTRAS_\pal-mcp-server) proved this approach
in production. 14 role tools, each a multi-step state machine with forced pauses.
Zero algorithms — all intelligence via prompt engineering. Result: dramatically more
thorough agent reasoning through external structure.

### Tier 3: LLM-Augmented Analysis (Sampling)
When in-process analysis is insufficient AND sampling is available, pattern requests
LLM reasoning through the MCP sampling protocol. The LLM IS the same agent's LLM —
it's the agent thinking harder through a structured prompt, not a different agent.

**Key capabilities:**
- **Real decomposition:** "Design quantum error correction circuit" → LLM generates domain-specific sub-problems that no template can provide
- **Nuanced critique:** peer_review sends artifact to LLM with structured review rubric → gets actual review, not keyword matching
- **Creative alternatives:** decision_framework asks LLM for options it hasn't considered
- **Semantic comparison:** source_comparison asks LLM to find genuine agreements/contradictions, not word overlap

**Design constraint:** Sampling = same client LLM. Not a separate CLI. No context transfer cost.
Graceful degradation: if sampling unavailable → Tier 2 analysis returned.

### Tier 4: Tool Orchestration (DEFERRED)
Pattern calls other aimux tools (exec, investigate, audit). Requires context transfer
to external CLIs which is expensive and lossy.

**Status:** Deferred. Will revisit when evidence shows Tier 2-3 are insufficient.
Consensus/debate tools already handle multi-model deliberation — don't duplicate.

## Functional Requirements

### FR-1: Text Structure Analyzer (Tier 2)
Create `pkg/think/patterns/textanalysis.go` with:
- `AnalyzeText(text string) *TextAnalysis` returning:
  - Entities (nouns, proper nouns, technical terms)
  - Relationships (verb phrases connecting entities)
  - Gaps (expected entities for domain that are NOT mentioned)
  - Negations (constraints expressed as "without X", "no Y", "not Z")
  - Questions (sentences ending in "?", "should we", "which")
  - Complexity (sentence count, conjunction density → low/medium/high/epic)
- No external deps — regex + string analysis. <5ms per call.

### FR-2: Gap Detection Engine (Tier 2)
For each domain template, define EXPECTED entities/concepts. When AnalyzeText finds
a domain match, compare expected entities against mentioned entities. Report gaps.

Example: domain "auth" expects [registration, login, tokens, sessions, logout, roles, password-reset].
Input "Design auth with OAuth2 login" → present: [login, OAuth2], missing: [registration, tokens, sessions, logout, roles, password-reset].

Gap output includes: what's missing, why it matters, suggested enrichment.

### FR-3: Integrate Text Analysis into All Patterns (Tier 2)
Every pattern's Handle() should call AnalyzeText on primary text input and include
results in output:
- `textAnalysis.entities` — what the agent mentioned
- `textAnalysis.gaps` — what the agent probably needs but didn't mention
- `textAnalysis.complexity` — how big this problem is
- `textAnalysis.questions` — unresolved decisions detected in input

### FR-4: Forced Reflection Protocol (Tier 2B)
Add multi-step reasoning scaffolding to stateful patterns. Inspired by
pal-mcp-server's production-proven WorkflowTool pattern:

- **Step guidance messages:** When a stateful pattern (debugging_approach,
  scientific_method, sequential_thinking) detects the agent is rushing
  (e.g., hypothesis submitted without evidence), return a "STOP" directive
  with a mandatory checklist before proceeding.
- **Confidence tracking:** Add `confidence` field (0-1 with semantic labels:
  none/low/medium/high/certain) to pattern responses. Agent must self-assess.
- **Evidence gates:** Patterns can require minimum evidence count before
  allowing hypothesis/conclusion steps.
- **Schema descriptions as scaffolds:** Every field in MCP tool schema
  includes instructional description that teaches the agent HOW to fill it,
  not just WHAT to fill.

Example for debugging_approach:
- Step 1: Agent provides issue → pattern returns "Evidence needed. Read error logs,
  stack traces. Do NOT hypothesize until you have ≥3 evidence items."
- Step 2: Agent provides 3 findings → pattern returns "Now form hypothesis.
  Base it on evidence, not assumptions. Rate confidence 0-1."
- Step 3: Agent provides hypothesis with confidence=0.9 but only 3 evidence items →
  pattern returns "Overconfidence warning: 3 items rarely justify 0.9. Consider
  what you might be missing."

### FR-5: Production Sampling Verification (Tier 3)

Verify EnableSampling works with real Claude Code client:
- Build binary with sampling enabled
- Connect via Claude Code
- Call `problem_decomposition("Design quantum error correction")` without subProblems
- Confirm sampling request sent to client → LLM response received → structured result returned
- Document behavior when sampling is rejected by client (human-in-the-loop)

### FR-6: Sampling-Powered Pattern Enhancement (Tier 3)
For patterns where keyword/template analysis is clearly insufficient, add sampling path:
- `problem_decomposition`: real decomposition for non-template domains
- `peer_review`: actual review with LLM-generated objections (not keyword-matched)
- `decision_framework`: LLM-suggested criteria when domain templates don't match
- `critical_thinking`: LLM-powered bias detection beyond trigger phrases

Sampling is additive — Tier 2 result returned immediately, Tier 3 enriches it.
Source tagging: `"source": "sampling"` vs `"source": "text-analysis"` vs `"source": "domain-template"`.

### FR-7: Pattern Composition Protocol (Tier 2-3)
Define how output of one pattern becomes input to another:
- `suggestedNextPattern` already exists — formalize it
- Output format compatible with next pattern's input
- Agent doesn't need to manually copy-paste — guidance shows exact call

Example chain: `critical_thinking` finds "sunk cost" → suggests `decision_framework`
→ decision_framework output suggests `problem_decomposition` → decomposition suggests
`architecture_analysis`.

### FR-8: Complexity-Gated Tier Selection (Tier 2-3)
Pattern automatically selects tier based on input complexity:
- Low complexity (short text, known domain) → Tier 1 (template + keywords) — instant
- Medium complexity (longer text, partial domain match) → Tier 2 (text analysis + gaps) — <10ms
- High complexity (novel domain, no template match, many questions) → Tier 3 (sampling) — 1-5s
- Agent can force tier via param: `depth: "basic"|"analysis"|"deep"`

## Non-Functional Requirements

### NFR-1: Performance by Tier
- Tier 1: < 5ms (current, keyword + template)
- Tier 2: < 10ms (regex + string analysis, no external calls)
- Tier 3: 1-5s (sampling = LLM call, async-capable)

### NFR-2: Graceful Degradation
Each tier degrades to the one below:
- Tier 3 unavailable (no sampling) → Tier 2 returned
- Tier 2 fails (text too short for analysis) → Tier 1 returned
- Tier 1 no template match → generic keyword analysis
- ALL tiers → never empty output

### NFR-3: Source Provenance
Every field in output MUST have source attribution:
- `"source": "provided"` — user-supplied data
- `"source": "domain-template"` — from hardcoded template
- `"source": "text-analysis"` — from Tier 2 analysis
- `"source": "sampling"` — from Tier 3 LLM call
- `"source": "keyword-analysis"` — from Tier 1 keyword matching

### NFR-4: Backward Compatibility
All existing callers get identical results. New fields (textAnalysis, gaps,
complexity) are additive. Tier selection is automatic — no breaking changes.

## User Stories

### US1: Gap Detection Reveals Missing Requirements (P0)
**As an** AI agent calling `think(pattern="problem_decomposition", problem="Design auth with login")`,
**I want** the pattern to tell me what I FORGOT to mention (tokens, sessions, logout, roles),
**so that** I don't miss critical sub-problems.

**Acceptance Criteria:**
- [ ] textAnalysis.gaps lists ≥3 missing concepts for auth domain
- [ ] Each gap includes why it matters
- [ ] Gaps are domain-specific (auth gaps ≠ database gaps)

### US2: Complexity Drives Tier Selection (P0)
**As an** AI agent, **I want** simple problems handled instantly (Tier 1)
and complex novel problems to get deep analysis (Tier 3),
**so that** I don't wait 5 seconds for "Design auth" but get real help for novel domains.

**Acceptance Criteria:**
- [ ] "Design auth" → Tier 1 (<5ms, template match)
- [ ] "Design auth with OAuth2, SAML, MFA, device fingerprinting for compliance" → Tier 2 (text analysis, gaps)
- [ ] "Design quantum error correction for topological qubits" → Tier 3 if sampling available
- [ ] `depth: "deep"` forces Tier 3 regardless of complexity

### US3: Real Sampling Works with Claude Code (P1)
**As a** developer, **I want** to verify sampling works end-to-end with a real client,
**so that** Tier 3 is not just a mock.

**Acceptance Criteria:**
- [ ] Binary starts with sampling capability advertised
- [ ] Claude Code sends sampling request when pattern needs it
- [ ] LLM response received and structured by pattern
- [ ] Graceful degradation when sampling rejected

### US4: Pattern Chain Guides Agent Through Analysis (P1)
**As an** AI agent, **I want** each pattern to suggest the logical next pattern with
a ready-to-call example,
**so that** I can follow a reasoning chain without guessing.

**Acceptance Criteria:**
- [ ] critical_thinking → suggests decision_framework with pre-filled params
- [ ] problem_decomposition → suggests architecture_analysis with components from decomposition
- [ ] Output format of pattern A compatible with input format of suggested pattern B

## Edge Cases

- Text with only stop words after filtering → Tier 1 generic response
- Sampling request timeout (>30s) → return Tier 2 result, log timeout
- Domain with no template AND no sampling → Tier 2 text analysis only (still useful)
- Agent explicitly passes `depth: "basic"` → skip Tier 2-3 even if text is complex
- Very long input (>10000 chars) → truncate for analysis, note in guidance

## Out of Scope

- Tier 4: tool orchestration (exec/investigate delegation) — deferred
- NLP libraries (spaCy, NLTK) — use regex/string analysis only, keep zero external deps
- Training custom models for entity extraction
- Changing PatternHandler interface signature

## Dependencies

- PR #27 merged (graceful intelligence — Tier 1)
- PR #26 merged (pattern restoration + EnableSampling PoC)
- mcp-go SDK EnableSampling (verified available)
- Existing pkg/think/patterns/ infrastructure

## Success Criteria

- [ ] AnalyzeText produces useful entities/gaps/complexity for 5+ domains
- [ ] Gap detection identifies ≥3 missing concepts for known domains
- [ ] Complexity estimation routes correctly (low→T1, medium→T2, high→T3)
- [ ] Sampling verified with real Claude Code client (or documented as blocked)
- [ ] Pattern composition: at least one 3-pattern chain works end-to-end
- [ ] All existing tests pass
- [ ] Coverage > 80% for new code

## Data Model

### TextAnalysis (output of AnalyzeText)
```go
type TextAnalysis struct {
    Entities      []string        // nouns, technical terms, proper nouns
    Relationships []EntityRelation // verb phrases connecting entities
    Gaps          []Gap           // expected concepts not mentioned
    Negations     []string        // constraints ("without X", "no Y")
    Questions     []string        // unresolved decisions
    Complexity    string          // "low", "medium", "high", "epic"
}

type EntityRelation struct {
    Subject string // e.g., "auth"
    Verb    string // e.g., "serves"
    Object  string // e.g., "mobile"
}

type Gap struct {
    Expected string // what's missing
    Why      string // why it matters
}
```

### Reflection Directive (output of forced reflection)
```go
// Returned as data["reflection"] — advisory, not blocking
type ReflectionDirective struct {
    Directive string   // "STOP", "VERIFY", "CONTINUE"
    Checklist []string // items to check before proceeding
    Reason    string   // why the pause is needed
}
```

## Clarifications

### Session 2026-04-09

| # | Category | Question | Resolution | Date |
|---|----------|----------|------------|------|
| C1 | Data Model | TextAnalysis struct? | Flat Go struct with Entities, Relationships, Gaps, Negations, Questions, Complexity | 2026-04-09 |
| C2 | Reliability | Forced reflection mechanism? | Advisory via response data, not protocol enforcement. Agent reads directive and acts. | 2026-04-09 |
| C3 | Integration | Sampling prompt format? | Fixed templates per pattern. Partial response → extract parseable, fall back to Tier 2. | 2026-04-09 |
| C4 | Constraints | Entity extraction quality? | Regex-based: capitalized terms, quoted strings, technical terms. Accept false positives as guidance. | 2026-04-09 |

## Resolved Questions

None remaining.
