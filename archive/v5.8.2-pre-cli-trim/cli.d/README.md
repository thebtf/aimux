# archive/v5.8.2-pre-cli-trim/cli.d — Archived CLI Profiles

Captured 2026-05-07 from master before AIMUX-19 CLI trim.

## Why this folder exists

aimux reduced its active CLI surface from 12 CLIs to 3 (codex, claude, gemini).
This folder preserves the profiles of the 10 CLIs that are no longer active.

## Archived CLIs (10)

| CLI | Reason |
|-----|--------|
| aider | Not in active 3-CLI focus (codex/claude/gemini only) |
| cline | Not in active 3-CLI focus |
| codex-int | Interactive TUI variant of codex; no longer needed after Codex app-server adoption (AIMUX-18) — debug runs through structured channel instead |
| continue | Not in active 3-CLI focus |
| crush | Not in active 3-CLI focus |
| droid | Not in active 3-CLI focus |
| goose | Not in active 3-CLI focus |
| gptme | Not in active 3-CLI focus |
| opencode | Not in active 3-CLI focus |
| qwen | Not in active 3-CLI focus |

## Active CLIs (post-trim)

`codex`, `claude`, `gemini` — all profiles remain under `config/cli.d/`.

## Restoration

To restore a CLI profile, move its directory back to `config/cli.d/`.
`pkg/driver` discovers profiles from `config/cli.d/` at startup — no code
changes are needed.

The Pipeline v5 packages (`pkg/executor/`, `pkg/driver/`, `pkg/routing/`, etc.)
that would use these profiles remain in-repo as dormant seams.
