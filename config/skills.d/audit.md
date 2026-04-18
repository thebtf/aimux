---
name: audit
description: "Codebase audit — triage findings and route to specialist skills"
args:
  - name: cwd
    description: "Directory to audit"
related: [debug, security, review, investigate]
---
# Audit Workflow

## Live Status

- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}
- **Requests processed:** {{.TotalRequests}}
- **Error rate:** {{.ErrorRate}}
- **Audit target:** `{{.Args.cwd}}`

---

## Scale Decision

Classify the target before selecting audit depth:

| Complexity | Signal | Mode |
|------------|--------|------|
| **T1** | Single package, <10 files | Quick (`mode="quick"`) |
| **T2** | Multi-package, 10-50 files | Standard (`mode="standard"`) |
| **T3** | Large codebase, 50-200 files | Deep (`mode="deep"`) |
| **T4** | Monorepo / legacy / mixed language | Deep + per-domain sub-audits |

---

## Phase 1 — Scan

**Goal:** Run the audit tool and capture raw findings.

```
audit(cwd="{{.Args.cwd}}", mode="standard")
```

For T3/T4 complexity, run domain-specific audits in parallel:
```
audit(cwd="{{.Args.cwd}}/pkg/auth", mode="deep")
audit(cwd="{{.Args.cwd}}/pkg/api", mode="deep")
```

**ASYNC NOTE:** `audit` runs asynchronously by default and returns a `job_id` immediately
(status: `running`). Use a poll-wrapper subagent pattern — call `status(job_id="...")` in a
loop until a terminal status, then read findings from the final payload (see the workflow
skill for the poll-wrapper-subagent pattern). If Phase 1 requires immediate findings in a
single step, run `audit(async=false)`.

**GATE:** Do NOT proceed until the audit tool has returned a findings list.
- If `audit` returns empty results: verify `cwd` is correct and the project is indexed.
- If `audit` times out: switch to `mode="quick"` and escalate scope incrementally.

{{template "evidence-table" .}}

---

## Phase 2 — Triage

**Goal:** Classify every finding by priority before routing.

Apply priority scoring to each finding:

{{template "priority-scoring" .}}

Triage output format:
```
P0: <finding> — <why critical> — Route: aimux-security or aimux-debug
P1: <finding> — <why high> — Route: aimux-debug or aimux-review
P2: <finding> — <why medium> — Fix: exec(role="coding")
P3: <finding> — <why low> — Backlog
```

**GATE:** Every finding MUST have a priority and a routing decision before Phase 3 begins.

---

## Phase 3 — Route Findings

**Goal:** Cross-skill routing is the core value of this skill. Dispatch findings to specialist skills.

### P0 Security Findings → aimux-security

```
aimux-security(scope="<affected file paths or module name>")
```

Example trigger: hardcoded secrets, SQL injection, unauthenticated endpoints, insecure deserialization.

### P0 Bug Findings → aimux-debug

```
aimux-debug(error="<finding description with file:line>")
```

Example trigger: nil dereference paths, data corruption, panic-inducing inputs, logic errors.

### P1 Code Quality → aimux-review

```
aimux-review(scope="<file paths or branch>")
```

Example trigger: inconsistent error handling, missing validation, architectural violations.

### Complex / Multi-Domain Findings → investigate

When a finding spans multiple systems or its root cause is unclear:
```
investigate(action="start", topic="<finding description>", domain="debugging")
```

**GATE:** All P0 and P1 findings must be dispatched before Phase 4 begins.

---

## Phase 4 — Fix Remaining P2/P3

**Goal:** Handle lower-priority findings that don't warrant specialist skill invocation.

For each P2 or P3 finding:
```
exec(role="coding", prompt="Fix finding: <finding description>\nFile: <path>\nContext: <surrounding code snippet>")
```

After all P2/P3 fixes:
```
exec(role="codereview", prompt="Review fixes for P2/P3 audit findings in: {{.Args.cwd}}\nFindings fixed: <list>")
```

**GATE:** Re-run a quick audit to confirm no regressions:
```
audit(cwd="{{.Args.cwd}}", mode="quick")
```

---

## Acceptance Criteria

- [ ] Audit tool returned findings for target directory (VERIFIED)
- [ ] All findings triaged to P0-P3 with explicit justification
- [ ] All P0 findings dispatched to aimux-security or aimux-debug
- [ ] All P1 findings dispatched to aimux-review or aimux-debug
- [ ] All P2/P3 findings fixed or explicitly backlogged
- [ ] Re-scan confirms no new findings introduced by fixes

---

## See Also

{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

**Escalation path:** audit → `aimux-debug` (P0 bugs), `aimux-security` (P0 security), `aimux-review` (P1 quality)
**Receives from:** `workflow` (automated pipeline audits), direct invocation
