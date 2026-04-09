# Plan: aimux v3 Gap Closure

**Spec:** spec.md
**Date:** 2026-04-05

## Approach

Fix stubs top-down by priority. Each task has a paired VERIFY task that independently
confirms the fix is real (not a disguised stub). No task marked complete without its
VERIFY counterpart passing.

All work in D:\Dev\aimux repo. Each fix: edit → build → test → verify → commit.

## Phase 1: Critical Stubs (P0)

### 1a. Pair Coding via Orchestrator (FR-1)
- Replace `_ = pairParams` with `s.orchestrator.Execute(ctx, "pair_coding", pairParams)`
- Handle orchestrator result → format as MCP response with ReviewReport
- If async=true, wrap in goroutine with job completion
- VERIFY: test that exec(role="coding") calls orchestrator (mock), returns ReviewReport

### 1b. Agents Run Action (FR-2)
- Replace JSON stub with actual execution:
  - Create session + job
  - Build spawn args from agent content + user prompt
  - Run via executor (sync or async based on agent config)
  - Return real CLI output
- VERIFY: test that agents(action="run") produces different output for different prompts

## Phase 2: Integration Wiring (P1)

### 2a. Session Resume (FR-3)
- Lookup session by ID in SessionManager
- Validate CLI matches stored session CLI
- Pass session context to spawn (for CLIs that support --continue)
- VERIFY: test that exec with session_id uses stored session data

### 2b. Bootstrap Prompt Injection (FR-4)
- Call prompt engine in exec handler: load TOML for role, prepend to prompt
- Handle missing TOML gracefully (use prompt as-is)
- VERIFY: test that spawned CLI receives bootstrap prefix in prompt

### 2c. Stdin Piping (FR-5)
- Check `ShouldUseStdin(prompt)` before building SpawnArgs
- If true, call `BuildStdinArgs()` to modify args + set StdinData
- Pipe executor already handles StdinData field
- VERIFY: test with 7000-char prompt → stdin piping activated

### 2d. Audit Validation Parsing (FR-6)
- Parse result.Content from validator CLI
- Extract per-finding verdicts (confirmed/rejected/uncertain)
- Update finding confidence based on validator response
- VERIFY: test with mock validator output → findings have mixed confidence

## Phase 3: Cleanup + Polish (P2)

### 3a. Spark Detector Fix (FR-7)
- Parse `codex --version` output for version string
- Check if version supports Spark model (version >= threshold or keyword match)
- VERIFY: test with mock version output → correct detection

### 3b. Dead Code Removal (FR-8)
- Delete pkg/tools/deepresearch/deepresearch.go
- Verify no imports reference old DeepResearch struct
- VERIFY: build passes, no references to old struct

### 3c. Completion Pattern (FR-9)
- Add pattern matching in pipe executor read loop
- When output matches CompletionPattern regex → stop reading, finalize
- VERIFY: test with echo + completion pattern → executor stops on match

### 3d. PTY/ConPTY Documentation (FR-10)
- Change error message to explain Pipe executor handles persistent sessions
- Add doc comment explaining the architectural choice
- VERIFY: error message is clear and actionable

## Phase 4: Final Verification

- Run `scripts/stub-grep.sh` → 0 findings
- Run `go test -race ./...` → all pass
- Run `go build ./...` → clean
- Update CONTINUITY.md

## Dependencies

Phase 1a, 1b → independent (parallel)
Phase 2a, 2b, 2c, 2d → independent (parallel)
Phase 3a, 3b, 3c, 3d → independent (parallel)
Phase 4 → depends on all above
