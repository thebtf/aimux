# Feature: Production Polish — Parser Wiring + CLI Profiles

**Slug:** production-polish
**Created:** 2026-04-06
**Status:** Draft
**Author:** AI Agent (reviewed by user)

## Overview

aimux v3 returns raw CLI stdout as response content without parsing. CLIs like codex output JSONL events, claude/gemini output JSON with metadata — the meaningful text content is buried inside structured output. This feature wires the existing `pkg/parser/` into the server response path so callers get clean content. Additionally, 5 CLIs lack production profiles.

## Context

The `pkg/parser/` package already exists with 3 parsers (JSONL, JSON, text) and tests. The server's `executeJob` stores `result.Content` (raw stdout) directly into job state. When a caller retrieves the result, they get raw JSONL lines or JSON objects instead of the actual text content.

Example: codex returns JSONL with `agent_message` events containing the actual response text. The current output is 20+ lines of JSONL events. After parsing, it should be the clean agent response text.

Each CLI profile has an `output_format` field (`jsonl`, `json`, `text`) that determines which parser to use.

Additionally, 5 CLIs (goose, crush, gptme, cline, continue) have testcli emulators but no production profiles in `config/cli.d/`.

## Functional Requirements

### FR-1: Content Parsing in exec Response Path
After CLI process completes, server MUST parse `result.Content` according to the CLI profile's `output_format`. Parsed content replaces raw stdout in the response. Raw output preserved in a separate field for debugging.

### FR-2: Format-Specific Parsing
- `jsonl`: Use `parser.ParseJSONL` + `parser.ExtractAgentMessages` to extract text content
- `json`: Use `parser.ParseJSON` + `parser.ExtractContent` to extract text from content/text/response fields
- `text`: No parsing needed (pass through as-is)

### FR-3: Graceful Degradation
If parsing fails (malformed output, unexpected format), fall back to raw content. Never lose data by failing to parse.

### FR-4: CLI Session ID Extraction
For JSONL format, extract `session_id` from events via `parser.ExtractSessionID` and store in `Result.CLISessionID`.

### FR-5: Production Profiles for Remaining CLIs
Create `config/cli.d/{name}/profile.yaml` for goose, crush, gptme, cline, continue with correct command bases, prompt flags, and output formats verified against source code.

## Non-Functional Requirements

### NFR-1: Zero Performance Regression
Parsing is string operations on already-buffered output. No additional I/O.

### NFR-2: Backward Compatibility
Callers that process raw output MUST still function. The `content` field in response contains parsed text. A new `raw_output` field contains original stdout.

## User Stories

### US1: Clean Content from Codex (P1)
**As a** developer using aimux exec with codex, **I want** to receive the actual agent response text, **so that** I don't have to manually parse JSONL events.

**Acceptance Criteria:**
- [ ] exec(cli="codex") returns parsed text content, not raw JSONL
- [ ] CLI session ID extracted from JSONL events and available in response
- [ ] Raw JSONL preserved in separate field for debugging

### US2: Clean Content from JSON CLIs (P1)
**As a** developer using aimux exec with claude/gemini, **I want** to receive the extracted text content from JSON responses, **so that** I don't have to parse nested JSON structures.

**Acceptance Criteria:**
- [ ] exec(cli="claude") returns text content extracted from JSON result object
- [ ] exec(cli="gemini") returns text content extracted from JSON response

### US3: Production Profiles for All CLIs (P2)
**As a** developer, **I want** production profiles for all supported CLIs, **so that** I can use any CLI through aimux without custom configuration.

**Acceptance Criteria:**
- [ ] goose profile with correct `goose run` + `--text` flag
- [ ] crush profile with correct `crush run` + positional prompt
- [ ] gptme profile with correct positional prompt + `--non-interactive`
- [ ] cline profile with correct `cline task` + positional prompt
- [ ] continue profile with correct positional prompt + `-p` headless flag

## Edge Cases

- CLI outputs mixed stderr + stdout (stderr already separated by executor)
- CLI outputs empty JSONL (no agent_message events) → return empty string, not raw JSONL
- CLI outputs malformed JSON/JSONL → fall back to raw content
- Orchestrator strategies use parsed content for synthesis prompts → must get clean text
- Profile has `output_format: ""` (unset) → treat as text (pass through)

## Out of Scope

- Streaming parser (real-time event parsing during execution)
- New parser formats beyond jsonl/json/text
- Changing executor output buffering
- Wiring parser into orchestrator strategies (they get `Result.Content` which will be parsed)

## Dependencies

- `pkg/parser/` package (exists, tested)
- `config.CLIProfile.OutputFormat` field (exists)
- CLI source code repos in `D:\Dev\_EXTRAS_\` (for profile verification)

## Success Criteria

- [ ] exec(cli="codex") returns clean text content (verified with real CLI)
- [ ] exec(cli="claude") returns clean text content (verified with real CLI)
- [ ] All 5 new CLI profiles created and valid
- [ ] All existing tests pass (0 regressions)
- [ ] New unit tests for parse-and-extract path
- [ ] `go build ./...` + `go vet ./...` clean

## Open Questions

None.
