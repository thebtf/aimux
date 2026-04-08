---
name: debug
description: "Structured debug workflow — reproduce, investigate, root-cause, fix, verify"
args:
  - name: error
    description: "Error message, symptom, or bug description"
related: [security, audit, review]
---
# Debug Workflow

## Live Status
- **Debug CLI:** {{RoleFor "debug"}} (role=debug)
- **Available CLIs ({{.CLICount}}):** {{JoinCLIs}}

{{if .Args.error}}**Error:** {{.Args.error}}{{end}}

## Phase 1 — Reproduce
Read error logs. Find the file and line. Reproduce the error.
**GATE: Do NOT proceed until you have a VERIFIED reproduction.**

## Phase 2 — Investigate
```
investigate(action="start", topic="{{.Args.error}}", domain="debugging")
```

{{if .HasMultipleCLIs}}
## Multi-Model Verification
```
consensus(topic="Root cause analysis for: {{.Args.error}}")
```
{{end}}

{{template "evidence-table" .}}

## See Also
{{range .RelatedSkills}}- **aimux-{{.Name}}**: {{.Description}}
{{end}}
