# Feature: Think Tool — Full Pattern System

**Slug:** think-tool
**Created:** 2026-04-07
**Status:** Draft
**Author:** AI Agent (from v2 analysis + roadmap)

## Overview

The think MCP tool currently has a stub handler — it returns `"Thinking with pattern X about: Y"` for any pattern. The full implementation from mcp-aimux v2 (TypeScript) needs to be ported to Go v3: 17 pattern handlers with validation, session state management, complexity scoring for auto-consensus mode, and dialog configuration per pattern.

## Context

### V2 reference (TypeScript, D:\Dev\mcp-aimux\src\think\):
- `types.ts` — ThinkResult interface (pattern, status, timestamp, data, session_id, metadata, suggestedNextPattern), PatternHandler interface (name, description, validate, handle), makeThinkResult helper (Object.freeze)
- `registry.ts` (~30 lines) — Map-based handler registry: registerPattern, getPattern, getAllPatterns, clearPatterns
- `session.ts` (~79 lines) — In-memory session store: getOrCreateSession, getSession, updateSessionState, deleteSession, clearSessions, getSessionCount. Sessions are frozen on each access/update.
- `complexity.ts` (~107 lines) — 0-100 complexity scorer with 4 components: textLength (0.3), subItemCount (0.3), structuralDepth (0.2), patternBias (0.2). Produces "solo" or "consensus" recommendation.
- `dialog-config.ts` — Per-pattern dialog configuration: participants, topic/prompt templates, mode, synthesize flag, complexityBias. 12 patterns have dialog configs.
- `patterns/` — 17 pattern handlers, each 50-300 lines

### V3 current state (Go):
- `pkg/server/server.go:handleThink` (lines 1029-1056) — 27-line stub. Only extracts pattern + issue/topic, returns echo string.
- No `pkg/think/` package exists.

### Key design decisions from v2:
- PatternHandler interface: validate(input) → cleaned input, handle(validInput, sessionId?) → ThinkResult
- 6 stateful patterns use session store: sequential_thinking, scientific_method, debugging_approach, structured_argumentation, collaborative_reasoning, (plus any pattern in dialog mode)
- 11 stateless patterns: think, critical_thinking, decision_framework, problem_decomposition, recursive_thinking, metacognitive_monitoring, visual_reasoning, temporal_thinking, domain_modeling, architecture_analysis, stochastic_algorithm
- Complexity scoring determines solo vs consensus mode (threshold default 60)
- 12 patterns have dialog configs with roles, templates, modes
- makeThinkResult creates frozen (immutable) result objects

## Functional Requirements

### FR-1: Think Result Type
`ThinkResult` struct with: Pattern (string), Status ("success"/"failed"), Timestamp (ISO string), Data (map[string]any, immutable), SessionID (optional string), Metadata (optional map[string]any), SuggestedNextPattern (optional string), ComputedFields (optional []string).

### FR-2: Pattern Handler Interface
`PatternHandler` interface with: Name() string, Description() string, Validate(input map[string]any) (map[string]any, error), Handle(validInput map[string]any, sessionID string) (*ThinkResult, error).

### FR-3: Pattern Registry
Map-based registry: RegisterPattern(handler), GetPattern(name) → handler, GetAllPatterns() → []string, ClearPatterns() (testing only). Duplicate registration panics.

### FR-4: Session Store
In-memory session management with sync.RWMutex: GetOrCreateSession(id, pattern, initialState), GetSession(id), UpdateSessionState(id, patch), DeleteSession(id), ClearSessions(), GetSessionCount(). Sessions are immutable — each update creates a new copy.

### FR-5: Complexity Scorer
0-100 scoring with 4 components:
- textLength (weight 0.3): max string length across known fields / 500 * 100, capped at 100
- subItemCount (weight 0.3): sum of array lengths across known fields * 10, capped at 100
- structuralDepth (weight 0.2): max nesting depth * 25, capped at 100 (max recursion 5, max 10 items per level)
- patternBias (weight 0.2): from dialog config (-50 to +50), defaults to -50 for solo-only patterns

Result: ComplexityScore with total, component scores, recommendation ("solo" or "consensus"), threshold comparison.

### FR-6: Dialog Configuration
Per-pattern config: Participants ([]DialogParticipant with CLI + Role), TopicTemplate, PromptTemplate (with {key} interpolation), MaxTurns, Mode (sequential/parallel_compare/parallel/consensus), Synthesize flag, ComplexityBias (-50 to +50).

12 patterns have dialog configs:
| Pattern | Roles | Mode | Bias |
|---------|-------|------|------|
| mental_model | Model Advocate, Skeptic | sequential | 0 |
| debugging_approach | Debugger, Systems Engineer | sequential | 0 |
| critical_thinking | Analyst, Devil's Advocate | sequential | 10 |
| decision_framework | Decision Analyst, Risk Assessor | sequential | 30 |
| problem_decomposition | Architect, Integration Specialist | sequential | 10 |
| structured_argumentation | Proponent, Opponent | sequential | 20 |
| metacognitive_monitoring | Cognitive Scientist, Domain Expert | sequential | -10 |
| domain_modeling | Domain Expert, Software Architect | sequential | 10 |
| architecture_analysis | Solutions Architect, Risk Analyst | sequential | 10 |
| scientific_method | Researcher, Peer Reviewer | sequential | 10 |
| temporal_thinking | Systems Analyst, Historian | sequential | 0 |
| collaborative_reasoning | Lead Analyst, Critical Reviewer | consensus | 20 |

5 solo-only patterns (no dialog config): think, sequential_thinking, recursive_thinking, visual_reasoning, stochastic_algorithm.

### FR-7: 17 Pattern Implementations

Each pattern handler must:
1. Implement validate() with input type checking and field requirements
2. Implement handle() with pattern-specific logic
3. Return ThinkResult with meaningful data (not echo)

#### FR-7.1: think (base)
- Requires: thought (string)
- Returns: thought, thoughtLength
- Stateful: No

#### FR-7.2: critical_thinking
- Requires: issue (string)
- Logic: bias detection via keyword catalogs (confirmation_bias, anchoring, sunk_cost, availability_heuristic, etc.)
- Data: BIAS_CATALOGS with trigger phrases per bias type
- Returns: analysis with detected biases and triggers
- Stateful: No

#### FR-7.3: sequential_thinking
- Requires: thought (string), optional thoughtNumber, totalThoughts, isRevision, revisesThought, branchFromThought, branchId
- Logic: accumulates thought history, supports revisions and branches, similarity checking (Jaccard word similarity)
- Session state: thought history, branches
- Stateful: Yes

#### FR-7.4: scientific_method
- Requires: stage (observation/question/hypothesis/experiment/analysis/conclusion/iteration)
- Logic: stage lifecycle tracking, hypothesis evolution, entry linking (hypothesis→prediction→experiment→result)
- Session state: stageHistory, hypothesesHistory, entries with linking validation
- Stateful: Yes

#### FR-7.5: decision_framework
- Requires: decision (string), criteria ([]Criterion with name+weight), options ([]Option with name+scores)
- Logic: weighted scoring, normalization, ranking with tie detection
- Stateful: No

#### FR-7.6: problem_decomposition
- Requires: problem (string), optional methodology, subProblems, dependencies, risks, stakeholders
- Logic: decomposition analysis
- Stateful: No

#### FR-7.7: debugging_approach
- Requires: issue (string), approachName (string)
- Logic: 18 known debugging methods catalog (binary_search, bisect, trace, delta_debugging, wolf_fence, rubber_duck, printf, reverse, hypothesis, differential, stack_trace, git_blame, minimal_repro, divide_conquer, profiler, memory_dump, network_trace, static_analysis), hypothesis tracking with status lifecycle (untested→tested→confirmed/refuted)
- Session state: hypotheses list
- Stateful: Yes

#### FR-7.8: mental_model
- Requires: modelName (string), problem (string)
- Logic: 15 known models catalog (first_principles, inversion, second_order_thinking, occams_razor, pareto_principle, circle_of_competence, opportunity_cost, systems_thinking, hanlons_razor, map_is_not_territory, jobs_to_be_done, via_negativa, leverage_points, probabilistic_thinking, margin_of_safety), unknown models accepted
- Stateful: No

#### FR-7.9: metacognitive_monitoring
- Requires: task (string), optional knowledgeAssessment, claims, cognitiveProcesses, biases, uncertainties, confidence
- Logic: overconfidence detection (claims < 3 but confidence > 0.8)
- Stateful: No

#### FR-7.10: structured_argumentation
- Requires: topic (string), optional argument (claim/evidence/rebuttal with linking)
- Logic: accumulates arguments, validates linking (evidence/rebuttal must reference existing claim)
- Session state: arguments list
- Stateful: Yes

#### FR-7.11: collaborative_reasoning
- Requires: topic (string)
- Logic: 6 stages (problem-definition, ideation, critique, integration, decision, reflection), contribution types (observation, question, insight, concern, suggestion, challenge, synthesis)
- Session state: contributions, stage progress
- Stateful: Yes

#### FR-7.12: recursive_thinking
- Requires: problem (string), optional baseCase, recursiveCase, currentDepth, maxDepth, convergenceCheck
- Logic: recursion depth tracking, convergence warnings, base case detection
- Stateful: No

#### FR-7.13: domain_modeling
- Requires: domainName (string), optional entities, relationships, rules, constraints, description
- Logic: entity/relationship analysis
- Stateful: No

#### FR-7.14: architecture_analysis
- Requires: components (string[] or []Component with name+description+dependencies)
- Logic: ATAM-lite analysis, coupling detection from dependency graphs, importance levels (H/M/L)
- Stateful: No

#### FR-7.15: stochastic_algorithm
- Requires: algorithmType (mdp/mcts/bandit/bayesian/hmm), problemDefinition (string), optional parameters, iterations, result
- Logic: algorithm analysis based on type
- Stateful: No

#### FR-7.16: temporal_thinking
- Requires: timeFrame (string), optional states, events, transitions, constraints, analysis
- Logic: temporal analysis
- Stateful: No

#### FR-7.17: visual_reasoning
- Requires: operation (string), optional diagramType, elements, relationships, transformations, description
- Logic: visual/spatial analysis
- Stateful: No

### FR-8: Server Handler Rewrite
Replace handleThink stub with full dispatch: validate input → create/get session → call pattern handler → compute complexity → return ThinkResult with mode indicator.

### FR-9: MCP Tool Schema Update
Update think tool registration with pattern-specific params beyond just "pattern", "issue", "topic". Add: thought, session_id, mode (auto/solo/consensus), decision, criteria, options, etc. — all as optional strings since different patterns use different params.

## Non-Functional Requirements

### NFR-1: Backward Compatibility
Existing tests pass — pattern + issue/topic input still works. Additional fields are optional.

### NFR-2: Port Fidelity
Core algorithms (complexity scoring formula, session management, bias catalogs, debugging methods, mental models, decision scoring) MUST match v2 behavior exactly. Data catalogs ported verbatim.

### NFR-3: Immutability
All ThinkResult objects and session state updates must create new copies, never mutate existing objects. Go equivalent of Object.freeze.

### NFR-4: Zero Dependencies
Pure Go stdlib. No new external dependencies.

## User Stories

### US1: Pattern Execution (P0)
**As a** coding agent, **I want** to invoke think patterns with validated input, **so that** I get structured reasoning output instead of echo strings.

**Acceptance Criteria:**
- [ ] All 17 patterns registered and callable
- [ ] validate() enforces required fields per pattern
- [ ] handle() returns pattern-specific computed data
- [ ] No pattern returns echo/stub output

### US2: Stateful Sessions (P0)
**As a** coding agent using sequential_thinking, **I want** session state to persist across calls, **so that** I can build multi-step reasoning chains.

**Acceptance Criteria:**
- [ ] 6 stateful patterns maintain session state across calls
- [ ] Session state is immutable (new copy on each update)
- [ ] session_id passed through correctly

### US3: Complexity-Aware Mode (P1)
**As a** multi-model orchestrator, **I want** auto-mode complexity scoring, **so that** complex patterns automatically escalate to consensus dialog.

**Acceptance Criteria:**
- [ ] Complexity score computed from input characteristics
- [ ] 4-component scoring matches v2 formula
- [ ] recommendation: "solo" when < threshold, "consensus" when >= threshold
- [ ] Dialog config available for 12 patterns

## Edge Cases

- Pattern name not registered → error with list of available patterns
- Missing required field for pattern → validation error with field name
- Session ID for stateless pattern → ignored (no error)
- Empty input map → validation catches required fields
- Hypothesis update for nonexistent ID → error in debugging_approach
- Entry link to nonexistent entry → error in scientific_method
- Zero-weight criteria in decision_framework → handled (no division by zero)
- Components as mixed string[]/object[] in architecture_analysis → normalized

## Out of Scope

- Consensus/dialog execution (the think tool computes mode recommendation; actual dialog dispatch is separate)
- SQLite persistence for sessions (in-memory only, matching v2)
- Pattern hot-loading or plugin system

## Dependencies

- V2 reference: `D:\Dev\mcp-aimux\src\think/` (read-only)
- `pkg/server/server.go` (handleThink rewrite)
- No new external Go dependencies

## Success Criteria

- [ ] All 17 patterns implemented with correct validation and handling
- [ ] Session store works for 6 stateful patterns
- [ ] Complexity scorer matches v2 formula (4 components, weighted)
- [ ] Dialog configs available for 12 patterns
- [ ] handleThink dispatches to real pattern handlers
- [ ] All existing tests pass (280+)
- [ ] New unit tests for types, registry, session, complexity, all 17 patterns
- [ ] No pattern returns echo/stub output
