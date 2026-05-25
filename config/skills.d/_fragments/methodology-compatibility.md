## Methodology compatibility

This playbook is generic by default. It assumes SpecKit-shaped delivery (clarify → specify → plan → tasks → implement → verify) and TDD-shaped implementation discipline (RED → GREEN → REFACTOR) as best practices, without requiring any specific external skill platform.

**If your environment exposes nvmd-platform skills**, prefer the named routes; **if not**, the same discipline applies as plain-language best practices — the aimux `task` tool plus this skill set is enough.

**Escalation.** If the work needs more than this role can deliver — the goal is unclear, the contract is missing, or the level of process must be chosen — escalate to `/aimux:pm`. From there, when the consumer has nvmd-platform installed, `nvmd-platform:brainstorm` / `nvmd-specify` / `nvmd-plan` / `nvmd-tasks` / `tdd` routes become accessible.
