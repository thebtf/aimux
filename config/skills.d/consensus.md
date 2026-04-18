---
name: consensus
description: "Multi-model consensus with consensus ≠ correctness warning"
args:
  - name: question
    description: "Question or decision for multi-model consensus"
related: [review, debug]
---
# Multi-Model Consensus

## Live Status
- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}
{{if lt .CLICount 2}}
> **WARNING: Consensus requires 2+ CLIs. Only {{.CLICount}} available.**
> Configure additional CLIs in `config/cli.d/` before proceeding.
{{end}}

{{if .Args.question}}**Question:** {{.Args.question}}{{end}}

---

## Scale Decision

Analyze the question type before selecting a strategy:

| Question Type | Signal Keywords | Strategy |
|---------------|-----------------|----------|
| Factual / lookup | "what is", "does X support", "which version" | `consensus` — models check facts |
| Binary choice | "should we", "A or B", "yes/no" | `debate(max_turns=3)` |
| Architecture decision | "design", "pattern", "tradeoff", "approach" | `debate(max_turns=5)` |
| Risk assessment | "safe to", "risks of", "could this break" | `consensus` + `think(pattern="critical_thinking")` |
| Open-ended exploration | "how might", "what could", "brainstorm" | `dialog(max_turns=4)` |

### Keyword Analysis for: {{if .Args.question}}`{{.Args.question}}`{{else}}`(no question provided)`{{end}}

- Contains "should" / "A or B" / "versus" → recommend **debate**
- Contains "what is" / "does" / "which" → recommend **consensus**
- Contains "design" / "architect" / "tradeoff" → recommend **debate(max_turns=5)**
- Contains "explore" / "brainstorm" / "options" → recommend **dialog**

---

## Execute

### Option A — Consensus (factual / risk)

```
consensus(topic="{{"{{.Args.question}}"}}", synthesize=true)
```

### Option B — Debate (binary choice / architecture)

```
debate(topic="{{"{{.Args.question}}"}}", max_turns=4)
```

### Option C — Deep Architecture Debate

```
debate(topic="{{"{{.Args.question}}"}}", max_turns=5)
```

### Option D — Exploratory Dialog

```
dialog(prompt="{{"{{.Args.question}}"}}", max_turns=4)
```

---

{{template "verification-gate" .}}

---

## Post-Consensus Adversarial Check

After any consensus or debate result, run:

```
think(pattern="critical_thinking", artifact="{{"{{consensus.result}}"}}")
```

This catches:
- All models sharing the same training bias
- Hallucinated "facts" that all models confidently state
- Missing edge cases that no model raised

---

## Acceptance Criteria

- [ ] At least 2 CLIs participated (verified via tool output, not assumed)
- [ ] Synthesis produced — not just raw per-model outputs
- [ ] Adversarial check run after synthesis
- [ ] At least one claim labeled STALE or INFERRED in final output
- [ ] Consensus ≠ correctness warning applied to any action taken from result

## See Also
{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}
