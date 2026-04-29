# archive/v5.0.3 — Pre-Purge Archive

Captured 2026-04-27 from master @ `041eba5` (frozen at `v5.0.3` / commit `9cc3433`).

## Why this folder exists

The 10 CLI-launching MCP tools (`exec`, `agent`, `agents`, `critique`,
`investigate`, `consensus`, `debate`, `dialog`, `audit`, `workflow`) were
removed from master to clear the way for a new Layer 5 design built on the
already-shipped Pipeline v5 (`pkg/workflow/`, `pkg/dialogue/`, `pkg/swarm/`,
`pkg/executor/`, `loom/`).

This folder preserves the auxiliary configuration that described those
tools — skills.d entries that are no longer relevant to the reduced tool
surface but contain useful prose for the next iteration.

## Contents

### `skills.d/` — embedded MCP skill prompts (13 files)

All entries from `config/skills.d/` at v5.0.3. Each is a prompt template
embedded into the MCP server at build time and exposed as a `prompts/list`
entry to MCP clients. They reference removed tool names (`exec`, `agents`,
`consensus`, etc.) and aimux's previous routing model.

Move back into `config/skills.d/` only after the new Layer 5 surface is
designed and the prompts are rewritten against current tool names.

## Code preservation

Source code for the removed handlers and legacy strategies is NOT copied
here. To inspect or restore the v5.0.3 implementation:

```bash
git checkout snapshot/v5.0.3-pre-cli-purge   # frozen branch
go build ./cmd/aimux/                        # builds full v5.0.3 surface
git checkout master                          # back to working branch
```

Or specific files:
```bash
git show snapshot/v5.0.3-pre-cli-purge:pkg/server/server_exec.go
git restore --source=snapshot/v5.0.3-pre-cli-purge -- pkg/orchestrator/
```

## Architecture reference

`docs/architecture/cli-tools-current.md` (committed to master @ `041eba5`)
documents the full pre-purge wiring with Pipeline v5 connection map and
`executeWithDialogue` mechanics.
