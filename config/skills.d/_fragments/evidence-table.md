### Evidence Requirements

| Claim | Requires | NOT Sufficient |
|---|---|---|
| Tests pass | Test command output showing 0 failures | Previous run, "should pass" |
| Build succeeds | Build command exit code 0 | Linter passing |
| Bug fixed | Original symptom reproduced: now passes | "Code changed, should work" |
| Subagent done | `git diff` shows expected changes | Subagent says "done" |
| Requirements met | Line-by-line checklist vs spec | Tests passing |
