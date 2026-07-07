# aimux Caller Guide

This guide is the supported caller path for the post Layer 5 aimux surface.
Use it when you need task execution, task evidence, structured thinking,
curated recipes, replay evidence, or the read-only viewer.

## Current Surface

Use these MCP tools:

| Need | Surface |
| --- | --- |
| Route generic code/review/spec/recipe work | `task` |
| Run standard or gate-oriented code review | `review` |
| Draft feature or change specifications | `spec` |
| Run caller-centered reasoning | `think(action=start|step|finalize)` |
| Read async state and health | `status`, `sessions` |
| Run Gemini-backed research | `deepresearch` |
| Check or apply binary updates | `upgrade` |
| Use a single cognitive move | one of the 22 cognitive move tools |


The removed broad Layer 5 tools are not part of the current surface: `exec`,
`agent`, `agents`, `workflow`, `dialog`, `debate`, `consensus`, `audit`,
`investigate`, and similar CLI-launching entry points stay unavailable until a
future design reintroduces them deliberately.

## Review

`review` is the dedicated caller-facing review methodology facade. It routes
through the same Loom-backed review/task backbone as `task(task_class="review")`
and returns the same accepted TaskResult fields and task resource URIs.

Useful forms:

```text
review(prompt="review this change", target="HEAD")
review(prompt="review this change", target="HEAD", gate=true)
review(prompt="review this change", recipe_id="code-review", target="HEAD")
```

Recipes outside this public review facade, including `second-opinion`,
`security-audit`, and `debug-investigation`, stay on `task(recipe_id=...)`;
`review` does not restore `audit`, `workflow`, or any other removed broad Layer 5 tool.


## Spec

`spec` is the dedicated caller-facing specification methodology facade. It routes
through the same Loom-backed task backbone as `task(task_class="spec")` and
returns the same accepted TaskResult fields and task resource URIs.

Useful forms:

```text
spec(prompt="write requirements and acceptance criteria", target="AIMUX-9 CR-007")
spec(prompt="turn this PRD into a feature spec", target=".agent/specs/feature/prd.md")
task(prompt="write requirements and acceptance criteria", task_class="spec", target="AIMUX-9 CR-007")
```

`spec` intentionally does not expose review-gate mode. Run `review(..., gate=true)`
after a concrete spec output exists.

## Task

`task` remains the generic execution entry point for code, review, spec, and
curated recipe work. Direct calls can route by `task_class`, and curated recipe
calls pass `recipe_id` through the same tool.

Useful forms:

```text
task(prompt="review this change", task_class="review", target="HEAD", gate=true)
task(prompt="write requirements and acceptance criteria", task_class="spec", target="AIMUX-9 CR-007")
task(prompt="review this change", recipe_id="code-review", target="HEAD")
task(prompt="give an independent assessment", recipe_id="second-opinion", target="HEAD")
task(prompt="make the requested code change", task_class="code", sandbox="workspace-write")
```

For code tasks, pair mode uses a driver and navigator. Solo diff uses
`navigator="none"` with `sandbox="read-only"`. Solo write uses
`navigator="none"` with `sandbox="workspace-write"` or `sandbox="danger"`.

## Think

`think(action=start|step|finalize)` is a visible reasoning harness. The caller
owns the answer, while aimux stores bounded work products, evidence,
confidence, objections, and gate feedback.

Typical flow:

```text
think(action="start", task="decide the migration path", context_summary="...")
think(action="step", session_id="...", chosen_move="decision_framework", work_product="...", evidence=[...], confidence=0.72)
think(action="finalize", session_id="...", proposed_answer="...")
```

Legacy `think(thought=...)` is not the supported shape.

## Task Resources

Use task resources for evidence instead of daemon log spelunking:

| Resource | Purpose |
| --- | --- |
| `aimux://tasks` | Bounded read-only task list with task/resource links. |
| `aimux://tasks/{task_id}` | Compact snapshot with status, progress summary, metadata, and links. |
| `aimux://tasks/{task_id}/viewer` | Read-only HTML detail view for humans and browser-capable clients. |
| `aimux://tasks/{task_id}/events` | Bounded lifecycle, runtime, and terminal artifacts. Add `kind=<lifecycle|terminal|runtime>`, `event_type=<type>`, `channel=<stdout|stderr|tool>`, `cursor=<seq>`, and `limit=<n>` for mid-flight runtime slices. |
| `aimux://tasks/{task_id}/progress` | Bounded progress artifacts for compact polling consumers. Supports `kind=progress`, `cursor=<seq>`, and `limit=<n>`. |

`kind` filters are resource-local: `/events?kind=progress` and
`/progress?kind=runtime` return `invalid_kind` instead of crossing surfaces.

Runtime events are projection-only evidence. The Loom task row remains the
canonical source for status, result, and lifecycle transitions. Structured JSONL
harness frames may normalize to `text_delta`, `status`, `tool_call`, or
`tool_result`; unknown frames and plain line-oriented harness output remain
visible as truthful `raw`/`stdout` runtime artifacts. Secrets are redacted before
artifact storage. The viewer is read-only. It has no forms, buttons, scripts,
task submission controls, mutation endpoints, or workflow controls. The task
list does not expose raw prompt, environment, or result payloads.

## Recipe Resources

Use recipe resources to discover compiled recipes:

| Resource | Purpose |
| --- | --- |
| `aimux://recipes` | Compact compiled recipe catalog. |
| `aimux://recipes/{recipe_id}` | Detail for one recipe. |

Supported recipe IDs:

| Recipe ID | Purpose |
| --- | --- |
| `code-review` | Read-only review worker in gate mode. |
| `second-opinion` | Read-only independent review worker in aggregate mode. |
| `security-audit` | Read-only workflow-backed security audit using compiled `pkg/workflow/secaudit.go` steps. |
| `debug-investigation` | Read-only workflow-backed debugging investigation using compiled `pkg/workflow/debug.go` steps. |

Workflow-backed recipes surface `recipe_workflow_id`,
`recipe_workflow_source`, and `recipe_workflow_steps` in recipe detail and task
metadata. They still run through `task(recipe_id=...)`; no public `workflow`,
`dialog`, `audit`, `consensus`, or `debate` tool is restored.

Recipe policy is fail-closed before worker spawn. If the selected provider
cannot enforce the recipe policy, `task` returns `CapabilityMismatch` with
`recipe_id`, `selected_cli`, `requested_policy`, `missing_capabilities`, and
`supported_capabilities`; no Loom task is submitted.

## Replay Evidence

Read-only curated recipe invocations can replay a matching completed source
task. A replay response uses `recipe_replay_cache_hit=true` and
`recipe_replay_source_task_id` to show which completed source task was reused.
The reusable source task exposes `recipe_replay_key_version`,
`recipe_replay_fingerprint`, and `recipe_replay_cache_hit=false` in task
resource metadata.

Replay is completed-only and read-only. Failed, crashed, cancelled, running,
policy-mismatched, direct non-recipe, or changed-precondition tasks are not
replayed as success.

## Worktree Metadata

Mutating code flows attach worktree preservation metadata so callers can recover
or review the touched checkout:

| Metadata | Meaning |
| --- | --- |
| `worktree_path` | Caller worktree path used by the task. |
| `worktree_branch` | Branch name when known. |
| `worktree_base_sha` | Source commit before mutation when known. |
| `worktree_preserve_reason` | Why the worktree was preserved. |

Read-only recipes and solo-diff flows do not emit preservation metadata.

## Safety Rules

- Do not use `mcp-launcher -mode tool -tool task` as a release or debug gate; it
  has previously hung and left smoke daemons behind. Use task resources and
  product smoke evidence instead.
- Do not expect removed broad workflow or CLI-launching tools to exist.
- Treat `aimux://tasks/{task_id}` and child resources as evidence surfaces, not
  execution surfaces.
- Treat `aimux://guides/caller` as the compiled caller guide for this binary.
