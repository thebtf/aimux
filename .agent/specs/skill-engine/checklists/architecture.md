# Architecture Requirements Quality Checklist

**Feature:** skill-engine
**Focus:** Architecture + Interconnection
**Created:** 2026-04-08
**Depth:** Standard

## Requirement Completeness

- [ ] CHK001 Are all 13 aimux tools accounted for in skill orchestration requirements? [Completeness, Spec §FR-12]
- [ ] CHK002 Are requirements defined for skills that DON'T map to a single tool (e.g., multi-tool pipelines like debug)? [Completeness, Spec §FR-6]
- [ ] CHK003 Are the 5 shared fragments (evidence-table, verification-gate, delegation-tree, priority-scoring, integrity-commandments) specified with enough detail to author? [Completeness, Spec §FR-9]
- [ ] CHK004 Are requirements specified for what happens when a skill references a tool the server doesn't have (e.g., deepresearch without Gemini)? [Completeness, Spec §FR-3]
- [ ] CHK005 Is the SkillData struct fully specified — are all fields listed with types and sources? [Completeness, Spec §FR-2]

## Requirement Clarity

- [ ] CHK006 Is "deeply choreographed workflows that virtuosically use all aimux tools" quantified with specific measurable criteria? [Clarity, Spec §NFR-6]
- [ ] CHK007 Is "100+ line workflow" (US1 AC) a sufficient quality signal, or should content quality criteria be defined (e.g., "every phase has a hard gate", "every tool call has parameters")? [Clarity, Spec §US1]
- [ ] CHK008 Is the distinction between FR-4 (Cross-Skill References) and FR-10 (Bidirectional Graph) clear — which is the requirement and which is the mechanism? [Clarity, Spec §FR-4/FR-10]
- [ ] CHK009 Is "metrics snapshot (requests, error rate)" specified with exact fields from pkg/metrics/MetricsSnapshot? [Clarity, Spec §FR-2]
- [ ] CHK010 Is "Loop detection hint" in FR-6 clearly described as a text instruction pattern, not engine logic? [Clarity, Spec §FR-6]

## Requirement Consistency

- [ ] CHK011 Are the terms "skill", "prompt", "workflow", "command", "tool" used consistently throughout spec, plan, and tasks? [Consistency, Glossary]
- [ ] CHK012 Is the NFR numbering consistent (no duplicates, no gaps) across spec.md? [Consistency, Spec §NFR-*]
- [ ] CHK013 Are plan phases aligned with task phases? (plan has 5 phases, tasks has 7 — is the mapping documented?) [Consistency, Plan/Tasks]
- [ ] CHK014 Is the "13 skills" count consistent between _map.yaml (FR-12), tasks (Phase 4), and success criteria? [Consistency, Spec §Success Criteria]

## Acceptance Criteria Quality

- [ ] CHK015 Can "aimux-debug prompt returns 100+ line workflow" be objectively measured? [Measurability, Spec §US1]
- [ ] CHK016 Can "Template rendering < 5ms per skill" be measured in a unit test without MCP overhead? [Measurability, Spec §NFR-1]
- [ ] CHK017 Can "server.go prompt handlers reduced from ~600 lines to ~100 lines" be verified by line count? [Measurability, Spec §Success Criteria]
- [ ] CHK018 Can "No Go code changes needed to add new skills" (FR-5) be verified by adding a test .md and observing registration? [Measurability, Spec §FR-5]

## Scenario Coverage

- [ ] CHK019 Are requirements defined for the zero-skill scenario (config/skills.d/ empty or missing)? [Coverage, Edge Case]
- [ ] CHK020 Are requirements defined for the zero-CLI scenario (no AI CLIs installed)? [Coverage, Spec §Edge Cases]
- [ ] CHK021 Are requirements defined for template rendering with ALL dynamic fields nil/empty simultaneously? [Coverage, Edge Case, Spec §NFR-3]
- [ ] CHK022 Are requirements defined for when _map.yaml exists but has skills not yet authored as .md? [Coverage, Edge Case]

## Interconnection Quality

- [ ] CHK023 Are session_id handoff requirements specified with enough detail to author a skill template that uses them? [Completeness, Spec §FR-6]
- [ ] CHK024 Are file handoff requirements (investigate report → next phase) specified with the exact mechanism (path in output, template var, etc.)? [Completeness, Spec §FR-6]
- [ ] CHK025 Are cross-skill invocation requirements specified — does the agent call an MCP prompt or follow a text instruction? [Clarity, Spec §FR-6]
- [ ] CHK026 Are escalation paths defined for EVERY skill (what happens when the skill's workflow fails)? [Coverage, Gap, Spec §FR-12]
- [ ] CHK027 Are receives_from relationships in _map.yaml specified for every skill that can be a routing target? [Completeness, Spec §FR-12]

## Constitution Alignment

- [ ] CHK028 Is the relationship between config/skills.d/ (new) and config/prompts.d/ (existing, P14) documented — do they coexist or does one replace the other? [Consistency, Constitution P14]
- [ ] CHK029 Are anti-stub requirements defined for skill TEMPLATE content quality (not just Go code)? [Gap, Constitution P17]
- [ ] CHK030 Is the requirement for immutable SkillData (templates cannot modify it) explicitly stated? [Gap, Constitution P11]

## Dependencies and Assumptions

- [ ] CHK031 Is the assumption that Go `text/template` supports `//go:embed` with underscore-prefixed directories documented? (embed requires `all:` directive for `_` files) [Assumption, Spec §NFR-5]
- [ ] CHK032 Is the assumption that `{{` in markdown code blocks won't conflict with Go template parsing validated? [Assumption, Spec §FR-1]
- [ ] CHK033 Is the dependency on gopkg.in/yaml.v3 for frontmatter parsing validated against the existing go.mod version? [Dependency, Plan §Tech Stack]
- [ ] CHK034 Is the assumption that caller CWD is available during MCP connect validated? [Assumption, Spec §NFR-6]
