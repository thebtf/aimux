## Delegation modes

aimux `task` with `task_class=code` runs a driver CLI plus a navigator CLI that reviews the driver's output. The sandbox setting controls the artifact shape:

### `pair, diff` — default

- `sandbox: "read-only"`
- Driver returns a unified patch as text. Files in the worktree are not touched.
- Navigator reviews the patch text and produces a verdict (approve / revise / escalate).
- Caller receives: patch text, verdict, `task_id`, `rounds`.
- Best for most tasks. Caller stays gatekeeper. Reversibility is built in — nothing applies without an explicit caller action.

### `pair, write` — opt-in

- `sandbox: "workspace-write"`
- Driver writes files directly. Navigator reviews the resulting `git diff`.
- Caller receives: change summary, verdict, `task_id`, `rounds`. Files are modified.
- Best for tasks where the artifact is hard to express as a unified patch (new files, renames), or where build/test on real files is required during the run.
- Recovery: preserve first, then revert the specific diff — `git apply -R path/to/patch.diff`, `git restore` on the touched files, or a focused commit revert. Broad operations like `git reset --hard` only with explicit user authorization.

## Result protocol

After `task` returns, regardless of mode:

1. **Inspect.** Read the patch (diff mode) or the `git diff` (write mode). Do not trust the navigator on its own. Focus on boundaries, error handling, side effects, nil/error paths.
2. **Evidence gate.** Run build and tests. In diff mode, apply the returned patch to a working branch or temporary copy and gate there. In write mode, gate runs against the modified worktree. Without evidence, do not accept.
3. **Decide.**
   - **Apply.** Patch is correct and the gate passes. In diff mode, apply the *returned patch* (`git apply path/to/patch.diff`). Do not re-invoke `task` with `workspace-write` and the same prompt expecting the same diff — non-determinism is real.
   - **Refine.** Close but not right. Reformulate the prompt and rerun `task`.
   - **Reject.** Wrong approach. Reconsider the task contract or do the work manually.

## Navigator verdict interpretation

The navigator's verdict is a signal, not evidence:

- **Approve.** The navigator did not spot blocking issues. Inspect anyway — navigators miss things.
- **Revise.** The navigator wants changes. Read the rationale, decide if it changes your decision.
- **Escalate.** The navigator could not give a clean verdict (timeout, low-confidence fallback). Treat as approve-with-doubt: inspect carefully, rely on the evidence gate.

Low confidence is reason for careful inspection, not for automatic rejection. Trust the diff, not the score.
