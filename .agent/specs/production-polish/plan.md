# Implementation Plan: Production Polish — Parser Wiring + CLI Profiles

**Spec:** .agent/specs/production-polish/spec.md
**Created:** 2026-04-06
**Status:** Draft

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Parsers | `pkg/parser/` (existing) | JSONL, JSON, text parsers already exist with tests |
| Server | `pkg/server/server.go` (modify) | Wire parsing into `executeJob` and response building |
| Profiles | `config/cli.d/` (new files) | Standard profile location |

## Architecture

```
CLI Process → executor.Run() → types.Result{Content: raw}
                                      ↓
                              parser.ParseContent(raw, outputFormat)
                                      ↓
                              types.Result{Content: parsed, RawOutput: raw}
                                      ↓
                              job.CompleteJob(parsed, ...)
```

**Key design:** Create a single `parser.ParseContent(raw, format string) (parsed string, cliSessionID string)` dispatcher function that routes to the right parser based on format. This keeps the integration point minimal — one function call in `executeJob`.

## API Contracts

### parser.ParseContent (new function)
- **Input:** `raw string` (CLI stdout), `format string` (from profile.OutputFormat)
- **Output:** `parsed string` (clean text content), `cliSessionID string` (extracted from JSONL if available)
- **Behavior:**
  - `"jsonl"` → `ParseJSONL` + `ExtractAgentMessages`, session ID from `ExtractSessionID`
  - `"json"` → `ParseJSON` + `ExtractContent`
  - `"text"` or `""` → return raw as-is
  - Parse error → return raw as-is (graceful degradation)

### executeJob signature change
- **Current:** `executeJob(ctx, jobID, sessionID string, args SpawnArgs, cb *CircuitBreaker)`
- **New:** `executeJob(ctx, jobID, sessionID string, args SpawnArgs, cb *CircuitBreaker, outputFormat string)`
- The `outputFormat` comes from `profile.OutputFormat` at the call site

### Response format change
- **Current:** `{"content": "<raw stdout>", "session_id": "...", "status": "..."}`
- **New:** `{"content": "<parsed text>", "raw_output": "<raw stdout>", "session_id": "...", "status": "..."}`

## File Structure

```
pkg/
  parser/
    dispatch.go      ← NEW: ParseContent dispatcher function
    dispatch_test.go  ← NEW: unit tests for dispatch
    jsonl.go          ← existing (no changes)
    json.go           ← existing (no changes)
    text.go           ← existing (no changes)
  server/
    server.go         ← MODIFY: wire parsing into executeJob + response
config/
  cli.d/
    goose/profile.yaml     ← NEW
    crush/profile.yaml     ← NEW
    gptme/profile.yaml     ← NEW
    cline/profile.yaml     ← NEW
    continue/profile.yaml  ← NEW
```

## Phases

### Phase 1: Parser Dispatch + Server Wiring (FR-1, FR-2, FR-3, FR-4)
1. Create `pkg/parser/dispatch.go` — `ParseContent` dispatcher
2. Add `outputFormat` parameter to `executeJob`
3. Call `ParseContent` in `executeJob` before `CompleteJob`
4. Update `handleExec` response to include `raw_output` field
5. Update all `executeJob` call sites with format parameter
6. Unit tests for `ParseContent`
7. Verify: `go build` + `go test` — all pass

### Phase 2: CLI Profiles (FR-5)
1. Create 5 production profiles from source code audit data
2. Verify testcli emulator profiles still work independently
3. Run full test suite

### Phase 3: Real CLI Verification
1. Build aimux binary
2. Test exec with real codex — verify parsed content vs raw
3. Test exec with real claude — verify JSON extraction
4. Test consensus with mixed CLIs

## Library Decisions

| Component | Library | Version | Rationale |
|-----------|---------|---------|-----------|
| All | stdlib + existing `pkg/parser/` | — | Pure Go, parsers already exist and tested |

## Unknowns and Risks

| Unknown | Impact | Resolution Strategy |
|---------|--------|-------------------|
| Orchestrator strategies currently use raw content for synthesis | MED | Content is parsed in `executeJob` which strategies call indirectly via `executor.Run`. Orchestrator gets `Result.Content` from executor, not from job store. Need to verify: does orchestrator use `executeJob`? → No, orchestrator calls `executor.Run` directly. Parse in orchestrator too? → Out of scope per spec. Orchestrator results go through strategy return values, not parser. |
| claude JSON `result` field contains nested JSON | LOW | `parser.ExtractContent` tries content/text/response fields. Claude output has `result` field. May need to add `Result` to field check list. |

## Constitution Compliance

No constitution.md exists. Plan follows project patterns:
- New function in existing parser package (consistent with existing architecture)
- Minimal changes to server.go (one new parameter, one function call)
- Profile files follow existing yaml format
