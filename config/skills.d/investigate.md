---
name: investigate
description: "Investigation protocol — domain auto-detect, convergence tracking"
args:
  - name: topic
    description: "What to investigate"
related: [debug, audit, research]
---
# Investigation Protocol

## Live Status
- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}
- **Investigate CLI:** {{RoleFor "analyze"}} (role=analyze)
{{if .PastReports}}- **Past Reports matching this topic:**
{{range .PastReports}}  - {{.Title}} ({{.Date}}) — session `{{.SessionID}}`
{{end}}{{end}}

{{if .Args.topic}}**Topic:** {{.Args.topic}}{{end}}

---

## Domain Auto-Detection

The `domain` parameter guides severity classification and evidence expectations.
Auto-detect from topic keywords:

| Keywords present | Domain |
|-----------------|--------|
| error, crash, panic, exception, nil, undefined, stack trace | `debugging` |
| CVE, injection, auth, token, secret, permission, privilege | `security` |
| slow, latency, memory, CPU, goroutine, timeout, OOM | `performance` |
| test, assertion, flaky, coverage, mock, fixture | `testing` |
| migration, schema, query, index, deadlock, connection | `database` |
| deploy, container, pod, restart, 502, health check | `infrastructure` |
| (none of the above) | `general` |

---

## Phase 1 — Start Investigation

Open a new session and establish scope.

```
investigate(action="start", topic="{{"{{.Args.topic}}"}}", domain="auto")
```

Record the returned `session_id` — all subsequent calls require it.

**GATE: Do NOT proceed until session_id is confirmed in tool output.**

---

## Phase 2 — Gather Findings

Add findings one at a time. Each finding needs a source location and confidence level.

```
investigate(action="finding", session_id="{{"{{session_id}}"}}", description="...", source="file:line", severity="P1", confidence="VERIFIED")
```

Severity scale:
- **P0** — system down, data loss, security breach
- **P1** — major feature broken, reproducible
- **P2** — degraded behavior, has workaround
- **P3** — cosmetic, low impact

Confidence levels:
- **VERIFIED** — confirmed this session via tool output or direct read
- **INFERRED** — reasonable conclusion from verified facts
- **STALE** — from training or model output, not confirmed
- **UNKNOWN** — pure guess — do NOT add as finding, investigate first

**GATE: Phase 3 MUST NOT start until:**
- Minimum 3 VERIFIED findings added
- At least 1 P0 or P1 finding (or explicit statement that none exist)
- Every finding has a `source` with file:line format

{{template "evidence-table" .}}

---

## Phase 3 — Assess and Decide

Ask the investigation engine whether to continue or complete.

```
investigate(action="assess", session_id="{{"{{session_id}}"}}")
```

The assessment returns one of:
- **CONTINUE** — not enough evidence, suggests next action
- **COMPLETE** — sufficient evidence for report

### Cross-Tool Dispatch

If the assessment suggests a think pattern, execute it immediately:

```
think(pattern="{{"{{assess.suggested_pattern}}"}}", artifact="{{"{{assess.findings_summary}}"}}")
```

If the assessment suggests a consensus check:

```
consensus(topic="Validate root cause: {{"{{assess.hypothesis}}"}}", synthesize=true)
```

---

## Phase 4 — Report

Generate the final investigation report.

```
investigate(action="report", session_id="{{"{{session_id}}"}}", cwd="{{"{{project_cwd}}"}}")
```

The report includes:
- Root cause classification
- Evidence chain (all findings with confidence levels)
- Impact assessment
- Recommended fix with acceptance criteria
- Prevention mechanism (what check prevents this category of bug)

---

## Phase 5 — Recall Past Investigations

Before starting a new investigation on a recurring topic, check history:

```
investigate(action="recall", topic="{{"{{.Args.topic}}"}}")
```

If a matching past report exists:
- Review its root cause — is this the same pattern?
- Check whether the fix was verified complete
- If same root cause recurs → escalate severity by one level

---

## See Also
{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

## Acceptance Criteria

- [ ] Session started with confirmed session_id
- [ ] Minimum 3 VERIFIED findings (not STALE, not INFERRED)
- [ ] Every finding has source file:line
- [ ] assess() called and result acted on
- [ ] Report generated in project cwd
- [ ] Past investigations checked via recall before starting
- [ ] Prevention mechanism identified in report
