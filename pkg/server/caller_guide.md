# aimux Caller Guide

This guide is the supported caller path for the post Layer 5 aimux surface.
Use it when you need task execution, task evidence, structured thinking,
curated recipes, replay evidence, or the read-only viewer.

## Current Surface

Use these MCP tools:

| Need | Surface |
| --- | --- |
| Route code or review work | `task` |
| Run caller-centered reasoning | `think(action=start|step|finalize)` |
| Read async state and health | `status`, `sessions` |
| Run Gemini-backed research | `deepresearch` |
| Check or apply binary updates | `upgrade` |
| Use a single cognitive move | one of the 22 cognitive move tools |

The removed broad Layer 5 tools are not part of the current surface: `exec`,
`agent`, `agents`, `workflow`, `dialog`, `debate`, `consensus`, `audit`,
`investigate`, and similar CLI-launching entry points stay unavailable until a
future design reintroduces them deliberately.

## Task

`task` is the only execution entry point for code and review work. Direct calls
can route by `task_class`, and curated recipe calls pass `recipe_id` through the
same tool.

Useful forms:

```text
task(prompt="review this change", task_class="review", target="HEAD", gate=true)
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
| `aimux://tasks/{task_id}/events` | Bounded lifecycle and terminal artifacts. |
| `aimux://tasks/{task_id}/progress` | Bounded progress artifacts. |

The viewer is read-only. It has no forms, buttons, scripts, task submission
controls, mutation endpoints, or workflow controls. The task list does not
expose raw prompt, environment, or result payloads.

## Recipe Resources

Use recipe resources to discover compiled recipes:

| Resource | Purpose |
| --- | --- |
| `aimux://recipes` | Compact compiled recipe catalog. |
| `aimux://recipes/{recipe_id}` | Detail for one recipe. |

Initial recipe IDs:

| Recipe ID | Purpose |
| --- | --- |
| `code-review` | Read-only review worker in gate mode. |
| `second-opinion` | Read-only independent review worker in aggregate mode. |

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
