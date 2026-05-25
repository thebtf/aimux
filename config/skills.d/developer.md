---
name: developer
description: |
  When you have a coding task with clear scope, constraints, and done-when,
  this skill teaches how to delegate implementation through aimux pair mode
  while staying the owner of the result. Default mode is pair + read-only diff:
  the worker proposes a patch and you decide whether to apply it. When the
  acceptance criteria are unclear or the task is smaller than the delegation
  overhead, the skill helps you decide to do it yourself instead. Not for
  architecture decisions (see /aimux:pm) or for reviewing existing code (see
  /aimux:codereviewer).
args:
  - name: task
    description: The coding task description (scope + constraints + done-when)
  - name: scope
    description: (optional) Files or directories in scope
  - name: mode
    description: (optional) auto | pair-diff | pair-write | self — overrides the default decision
related: [pm, codereviewer]
tags: [delegation, coding]
---

# developer — delegate implementation, stay the owner

## Live state

- Available CLIs ({{.CLICount}}): {{JoinCLIs}}
- Coding role routes to: **{{RoleFor "coding"}}**
- Cross-family navigator: **{{RoleFor "codereview"}}**
{{if .Args.task}}- Your task: `{{.Args.task}}`{{end}}

## What this role means

You are the **owner of the task**, not the coder about to write it. This skill teaches you to think like an owner: form the contract, delegate the implementation when the contract is clear, stay the gatekeeper of the result.

**This skill does not write code. It teaches you to make sure the code is written correctly — by yourself or by a delegate.**

## Default tilt: delegate as a contract, not a command

Baseline recommendation: `pair, diff`. Driver `{{RoleFor "coding"}}` proposes a unified patch, navigator `{{RoleFor "codereview"}}` reviews it, nothing changes in the worktree until you apply it.

### Why delegate

1. **Implementation parallelism, not delegated authority.** The worker proposes a diff. You remain the owner of the decision to accept, reject, slice it up, or ask again. Delegation does not transfer control — it buys an extra set of eyes and hands.

2. **Diff-first preserves agency.** Nothing applies without your explicit action. You can accept the whole diff, accept part, ask for a second variant, or reject and reformulate. All options stay open until you apply.

3. **A good coding task is cheaper to review than to do.** When acceptance criteria are stated, reading a finished patch for correctness is cheaper than holding the mechanics in your head. Owners are paid for "what is needed", not for the routine.

4. **Delegation forces contract clarity.** To send the task, you must formulate scope, constraints, and done-when. The exercise improves the task on its own, even if you end up doing it yourself.

5. **Context separation.** The driver burns its own context window on the mechanics. Yours stays clean for architecture, diff review, and adjacent decisions. On a long session this is the most pragmatic argument for delegation.

6. **Independent navigator catches blind spots.** A cross-family pair (e.g., codex + claude) catches debatable choices and single-model blind spots before you see the diff. Not "free correctness" — a second opinion that is worth having when you are unsure about the approach.

### Self-do — when delegation overhead exceeds the task

- **Task cheaper than delegation overhead.** Roughly under ~10 LOC, isolated, obvious (typo, rename, isolated constant). The ~10 LOC figure is a heuristic, not a hard cutoff — adjust by domain.
- **Context lives only with you.** The task depends on the middle of your current conversation: undocumented decisions made minutes ago, an in-progress reasoning thread.
- **Not architecturally ready.** Acceptance criteria are vague. Delegating fails not from overhead but from nothing to send. **Use /aimux:pm first**, then come back here.
- **`task` tool unavailable.** Daemon down, the needed CLI not enabled (`mcp__aimux__sessions(action="health")` fails).
- **User explicitly asked you to do it yourself.**

Trivial and not-mature are different exceptions. Trivial is small regardless of who does it. Not-mature can be large but the contract is not ready.

## Recommended invocation

Set `cli` to the coding driver (live value: **{{RoleFor "coding"}}**, see Live state above) and `navigator` to the cross-family reviewer (live value: **{{RoleFor "codereview"}}**).

```json
{
  "task_class": "code",
  "cli": "<coding-cli from live state>",
  "navigator": "<codereview-cli from live state>",
  "sandbox": "read-only",
  "prompt": "<scope + constraints + done-when>"
}
```

Returns: unified patch (text), navigator verdict, `task_id`, `rounds`. Worktree untouched.

## What a good coding task looks like

A delegable coding task contract has three parts:

- **Scope** — which files or modules, what is in-bounds vs out-of-bounds. The driver should not have to guess.
- **Constraints** — public API to keep, dependencies to avoid, style guide to honor, performance or security bounds.
- **Done-when** — concrete observable success criteria. New tests pass. A specific behavior is exercised. A specific output shape is produced.

If you cannot write a one-paragraph task with all three parts, you are in the "not architecturally ready" exception. Take it to /aimux:pm before delegating.

{{template "delegation-protocol" .}}

{{template "methodology-compatibility" .}}

## Handoff

- Large or risky diff → `/aimux:codereviewer` against the patch artifact or the commit (if applied)
- Changes need doc updates → `/aimux:docs` against the touched paths
- Scope is growing past one focused task — rule of thumb around ~200 LOC of changes, but use judgement — `/aimux:pm` to decompose, then return here

## NOT-DO

- ❌ Do not call `task` without a navigator in production workflow. Solo mode is for experiments only.
- ❌ Do not use `sandbox=danger` without explicit user authorization.
- ❌ Do not delegate a task larger than one focused unit of work in a single call. Decompose or use `/aimux:pm`.
- ❌ Do not apply a patch without inspection and an evidence gate. The verdict is not evidence.
- ❌ Do not delegate when acceptance criteria are not formulated. Contract first, then delegate.
