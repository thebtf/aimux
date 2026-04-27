---
name: security
description: "10-category security checklist with investigate integration"
args:
  - name: scope
    description: "What to audit: file paths, module, or 'all'"
related: [audit, review, debug]
---
# Security Workflow

## Live Status

- **Security CLI:** {{RoleFor "secaudit"}} (role=secaudit)
- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}
- **Scope:** `{{.Args.scope}}`

---

## 10 Security Categories

Every scan covers all 10 categories. No exceptions.

| # | Category | Key Signals |
|---|----------|-------------|
| 1 | **Secrets** | Hardcoded keys, tokens, passwords, private keys in source |
| 2 | **Input Validation** | Missing schema checks, trusting external data, no length bounds |
| 3 | **SQL Injection** | String interpolation in queries, missing parameterized statements |
| 4 | **Authentication** | Unauthenticated endpoints, weak session tokens, insecure JWT |
| 5 | **XSS** | Unescaped output in HTML, `innerHTML` with user data, unsafe templates |
| 6 | **CSRF** | Missing CSRF tokens on state-mutating endpoints, SameSite not set |
| 7 | **Rate Limiting** | No throttling on auth endpoints, bulk operations, file uploads |
| 8 | **Data Exposure** | PII in logs, full objects returned to client, unredacted error messages |
| 9 | **Dependencies** | Known CVEs, unpinned versions, transitive risk |
| 10 | **Custom** | Domain-specific risks (e.g., LLM prompt injection, agent tool misuse) |

---

## Phase 1 — Scan All 10 Categories

**Goal:** Run security audit across the defined scope.

```
exec(role="secaudit", prompt="Security scan for scope: {{"{{.Args.scope}}"}}\n\nScan all 10 categories:\n1. Secrets\n2. Input Validation\n3. SQL Injection\n4. Authentication\n5. XSS\n6. CSRF\n7. Rate Limiting\n8. Data Exposure\n9. Dependencies\n10. Custom / domain-specific\n\nFor each category: list findings with file:line, severity, and description.")
```

**GATE:** All 10 categories must be explicitly addressed (even if "no findings") before Phase 2.
- If scope is `all`: break into sub-scopes and run parallel scans.
- If a category cannot be assessed (e.g., no SQL in codebase): mark explicitly as "N/A — not applicable".

{{template "evidence-table" .}}

---

## Phase 2 — Classify Findings P0-P3

**Goal:** Assign priority to every finding from Phase 1.

{{template "priority-scoring" .}}

Security-specific priority anchors:
- **P0**: Data breach, RCE, auth bypass, exposed credentials — fix immediately, do not merge
- **P1**: Privilege escalation, SSRF, broken session management — fix before next commit
- **P2**: Information disclosure, missing rate limits, weak defaults — fix in current phase
- **P3**: Security hardening improvements, defense-in-depth additions — fix when convenient

**GATE:** All findings classified with explicit justification before Phase 3.

---

## Phase 3 — Investigate Critical Findings

**Goal:** For P0 and P1 findings, start a structured investigation to understand blast radius.

```
investigate(action="start", topic="security findings in {{"{{.Args.scope}}"}}", domain="security")
```

For each P0/P1 finding, add evidence:
```
investigate(action="finding", session_id="{{"{{session_id}}"}}", finding="{{"{{finding_description}}"}}", source="{{"{{file_path}}:{{line_number}}}"}}")
```

Assess blast radius:
```
investigate(action="assess", session_id="{{"{{session_id}}"}}")
```

**GATE:** `assess` must return impact scope and attack vector before remediation begins.

{{template "verification-gate" .}}

---

## Phase 4 — Remediate

**Goal:** Fix findings in priority order, highest priority first.

For each finding, starting with P0:
```
exec(role="coding", prompt="Fix security finding:\nCategory: {{"{{category}}"}}\nFile: {{"{{file_path}}"}}\nFinding: {{"{{finding_description}}"}}\nAttack vector: {{"{{attack_vector}}"}}\nRequired fix: {{"{{remediation_approach}}}"}}")
```

After each batch of fixes, run a targeted review:
```
exec(role="codereview", prompt="Review security fix for {{"{{category}}"}} in {{"{{file_path}}"}}")
```

{{if ge .CLICount 2}}
### Escalation: Conflicting Remediation Approaches

When multiple P0/P1 findings conflict (e.g., auth bypass fix breaks CSRF protection), escalate:
```
consensus(topic="Conflicting security remediations:\nFinding A: {{"{{finding_a}}"}}\nFinding B: {{"{{finding_b}}"}}\nConflict: {{"{{conflict_description}}}"}}")
```
{{end}}

---

## Phase 5 — Verify

**Goal:** Re-scan affected categories to confirm remediation.

For each category where findings were fixed:
```
exec(role="secaudit", prompt="Re-scan category {{"{{category}}"}} in {{"{{fixed_files}}"}}. Confirm fix for: {{"{{original_finding}}}"}}")
```

Run regression check to ensure fixes don't introduce new vulnerabilities:
```
exec(role="secaudit", prompt="Full regression scan for: {{"{{fixed_files}}"}}\nOriginal findings fixed: {{"{{remediated_list}}}"}}")
```

**GATE:** Phase 5 is complete when all originally-found P0/P1 issues are confirmed VERIFIED as fixed.

---

## Acceptance Criteria

- [ ] All 10 categories scanned (or explicitly marked N/A)
- [ ] All findings classified P0-P3 with justification
- [ ] All P0/P1 findings investigated for blast radius
- [ ] All P0/P1 findings remediated with code review
- [ ] Re-scan of affected categories passes clean
- [ ] No new security findings introduced by fixes

---

## See Also

{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}

**Escalation path:** security → `consensus` (when remediation approaches conflict or blast radius is unclear)
**Receives from:** `audit` (P0 security findings routed here), `debug` (when bug has security implications)
