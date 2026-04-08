---
name: research
description: "4-phase research pipeline with integrity commandments"
args:
  - name: topic
    description: "Research topic"
related: [investigate, consensus]
---
# Research Pipeline

## Live Status
- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}
{{if .HasGemini}}- **Deep Research:** `deepresearch` available via Gemini{{end}}
{{if .PastReports}}- **Past Reports for this topic:**
{{range .PastReports}}  - {{.Title}} ({{.Date}}) — {{.Summary}}
{{end}}{{end}}

## Scale Decision

Before starting, classify the query:

| Scope | Signal | Approach |
|-------|--------|----------|
| Quick lookup | Single verifiable fact, well-known library | 1 phase — think(pattern="think") only |
| Standard | Comparison, design tradeoff, emerging topic | 3 phases — Literature → Comparison → Synthesis |
| Deep | Novel domain, conflicting sources, high-stakes decision | 4 phases + deepresearch |

{{if .HasGemini}}**Gemini deepresearch is available** — use it for Deep scope queries.{{end}}

---

## Phase 1 — Literature Review

Gather primary sources. Do not synthesize yet. Do not form conclusions.

```
think(pattern="literature_review", topic="{{"{{.Args.topic}}"}}")
```

Collect every relevant source. Tag each as: primary source / secondary source / opinion.

**GATE: Phase 2 MUST NOT start until Phase 1 is complete.**
- At least 3 distinct sources identified
- Each source tagged with type and URL
- No synthesis performed — raw evidence only

---

## Phase 2 — Source Comparison

Compare sources side by side. Identify agreements, contradictions, gaps.

```
think(pattern="source_comparison", topic="{{"{{.Args.topic}}"}}")
```

For each pair of conflicting sources, record:
- What they agree on
- Where they contradict
- Which has stronger evidence basis

**GATE: Phase 3 MUST NOT start until sources are compared side by side.**
- Contradictions between sources explicitly listed
- No claim accepted on single-source basis
- Gap analysis complete (what is NOT covered by any source)

---

## Phase 3 — Adversarial Review

Play devil's advocate. Challenge your own findings.

```
think(pattern="peer_review", artifact="{{"{{step.content}}"}}")
```

Challenge questions to answer:
- What evidence would **contradict** the current findings?
- Which assumptions are load-bearing but unverified?
- What would a skeptical expert object to?
- Are there known failure modes in the sources used?

**GATE: Phase 4 MUST NOT start until adversarial review is complete.**
- At least 2 contradicting hypotheses explored
- Weakest claim in findings identified and labeled INFERRED or STALE
- Confidence classification applied to every major claim

---

## Phase 4 — Synthesis

Produce the final research output.

```
think(pattern="research_synthesis", topic="{{"{{.Args.topic}}"}}")
```

{{if .HasGemini}}
### Optional: Deep Research Pass

For Deep-scope queries, supplement synthesis with:

```
deepresearch(topic="{{"{{.Args.topic}}"}}")
```

Merge deepresearch output with Phase 1-3 findings. Flag any contradictions.
{{end}}

Synthesis structure:
1. **Summary** — 3-sentence TL;DR
2. **Key Findings** — bulleted, each with VERIFIED/INFERRED/STALE classification
3. **Contradictions** — unresolved conflicts between sources
4. **Confidence Map** — which claims are solid vs speculative
5. **Next Steps** — what would move INFERRED → VERIFIED

---

## Workflow Pipeline (automation)

```
workflow(steps=[
  {tool: "think", params: {pattern: "literature_review", topic: "{{"{{input.topic}}"}}"}},
  {tool: "think", params: {pattern: "source_comparison", topic: "{{"{{input.topic}}"}}"}},
  {tool: "think", params: {pattern: "peer_review", artifact: "{{"{{step1.content}}"}}"}},
  {tool: "think", params: {pattern: "research_synthesis", topic: "{{"{{input.topic}}"}}"}},
], input={topic: "{{"{{.Args.topic}}"}}"}})
```

---

{{template "integrity-commandments" .}}

---

{{template "verification-gate" .}}

---

## Acceptance Criteria

- [ ] Minimum 3 independent sources consulted
- [ ] Contradictions between sources explicitly sought and recorded
- [ ] Every claim classified: VERIFIED / INFERRED / STALE / BLOCKED / UNKNOWN
- [ ] Adversarial review completed — at least one claim downgraded
- [ ] No fabricated URLs or unnamed sources
- [ ] Final output includes confidence map

## See Also
{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}
