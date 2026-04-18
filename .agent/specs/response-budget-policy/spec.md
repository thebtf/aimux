**Status:** Draft

# MCP Response Budget Policy

## Overview

Every aimux MCP tool handler currently serializes its full internal state into the JSON response. Two measured offenders: `sessions(action=list)` returned 293k chars; `status` on a long completed codex job returned 140k chars. A separate live measurement confirmed `sessions(action=list, status=failed, limit=5)` returned 99,912 chars — proving that `limit` on row count alone is insufficient when per-row payload is unbounded.

Orchestrators (Opus and other LLMs calling aimux as an MCP server) avoid these tools out of context-budget fear, breaking the MCP contract. A single lookup call should return small, predictable metadata. Full content must be an explicit opt-in.

**Core principle: default = brief metadata. Full content = explicit opt-in. Every tool response respects a ~4k char default budget.**

## Context

Verified directly from source before writing this specification:

- `pkg/server/server.go::registerTools` (lines 427–857): registers 14 tools: `exec`, `status`, `sessions`, `audit`, `think`, `investigate`, `consensus`, `debate`, `dialog`, `agents`, `agent`, `deepresearch`, `workflow`, `upgrade`.
- `pkg/server/server.go::handleStatus` (lines 884–946): for completed/failed jobs, unconditionally sets `result["content"] = j.Content`. No budget check. Same pattern applies to the Loom task path (lines 897–908).
- `pkg/server/server.go::handleSessions action=list` (lines 955–976): calls `s.sessions.List()` returning all sessions with all fields, then appends all Loom tasks. Both collections are unbounded.
- `pkg/server/server.go::handleSessions action=info` (lines 978–991): returns full session struct plus all jobs via `ListBySessionSnapshot`, including their `Content` fields.
- `pkg/server/server_agents.go::handleAgents action=list` (lines 48–60): already returns summaries without system prompt. **Already partially compliant.**
- `pkg/server/server_agents.go::handleAgents action=info` (lines 70–85): calls `marshalToolResult(agent)` with the full agent struct. System prompt can be 500KB+.
- `pkg/server/server_investigate.go::handleInvestigate`: list/status/recall actions return full investigation state including finding chains.
- `deepresearch`: produces a synthesized report — inherently long. Documented exception.
- `think`: structured metadata response, not raw content. Not a primary offender.
- The `sessions` tool already accepts a `limit` param (line 501), but it is not wired to per-row field selection, making `limit=5` still return 99k chars.

## Functional Requirements

### FR-1: Uniform Optional Parameter Grammar

Every tool that returns structured data MUST accept the following optional parameters. No existing required parameter is renamed, removed, or made optional.

| Parameter | Type | Default | Applies to |
|---|---|---|---|
| `fields` | string (CSV) | `""` (brief defaults) | all tools returning objects |
| `limit` | integer | 20 | all list actions |
| `offset` | integer | 0 | all list actions |
| `include_content` | boolean | `false` | `status`, `sessions(info)`, `exec` (sync), `agent` (sync), `investigate(recall)`, `agents(info)` |
| `tail` | integer | `0` (disabled) | same as `include_content`-eligible actions |

**`fields`**: comma-separated whitelist of top-level response fields. When omitted or empty, handlers return the defined brief field set for that action (FR-2). When non-empty, only listed fields appear. Policy metadata fields (`truncated`, `hint`, pagination keys) are always included regardless of `fields`.

**`limit` / `offset`**: paginate list responses. Default limit is 20. Maximum limit is 100; values above 100 are clamped with `limit_clamped=true` in the response. `offset` is zero-based.

**`include_content`**: when `false` (default), content-bearing fields are omitted. When `true`, full content is returned. Applicable only on actions that have a content-bearing field.

**`tail`**: when `> 0`, returns the last N characters of the content field as `content_tail` instead of full content. Requires `tail >= 1`; zero or negative values are an error. `tail` and `include_content=true` are mutually exclusive on the same call: if both are supplied, `tail` takes precedence.

### FR-2: Default Brief Field Sets Per Action

Each action defines its brief output. Brief output must fit within ~4k chars on realistic fixtures (FR-9). Large fields are excluded unless explicitly requested.

| Tool / Action | Brief default fields |
|---|---|
| `status` | `job_id`, `status`, `progress`, `poll_count`, `session_id`, `error` (when present), `content_length` (integer, byte count), stall guidance (when present) |
| `sessions list` | per row: `id`, `status`, `cli`, `created_at`, `job_count`; plus pagination metadata |
| `sessions info` | `session.id`, `session.status`, `session.cli`, `session.created_at`; per job: `id`, `status`, `progress`, `content_length` |
| `sessions health` | all fields (already compact) |
| `sessions cancel / kill / gc` | all fields (already compact) |
| `investigate list` | per row: `session_id`, `topic`, `domain`, `status`, `finding_count` |
| `investigate status` | `session_id`, `topic`, `domain`, `status`, `finding_count`, `coverage_progress` |
| `investigate recall` | `session_id`, `topic`, `finding_count`; full report omitted unless `include_content=true` |
| `agents list` | `name`, `description`, `role`, `domain` (existing summary behavior — already compliant) |
| `agents info` | `name`, `description`, `role`, `domain`, `tools`, `when`; `system_prompt` excluded unless `include_content=true` |
| `agents find` | same as agents list per match |
| `exec` (async) | `job_id`, `status` |
| `exec` (sync) | `status`, `content_length`; full content only with `include_content=true` |
| `agent` (async) | `job_id`, `status`, `session_id` |
| `agent` (sync) | `status`, `content_length`; full content only with `include_content=true` |
| `consensus / debate / dialog / audit / workflow` (async) | `job_id`, `status`, `session_id` |
| `consensus / debate / dialog / audit / workflow` (sync) | compact summary; full transcript only with `include_content=true` |
| `think` | all fields (response is structured metadata, not raw content — already compact) |
| `investigate start / finding / assess / report / auto` | compact guided metadata (already compact via guidance envelope) |
| `deepresearch` | **exception** — full synthesized report always returned (FR-8) |
| `upgrade` | all fields (already compact) |

### FR-3: Truncation Marker

When a handler omits fields due to the brief default, the response MUST include:

```json
{
  "truncated": true,
  "hint": "content omitted (140213 chars). Use status(job_id=X, include_content=true) for full output."
}
```

`truncated` MUST be `false` or absent when no field was omitted. The `hint` string MUST name the omitted field, its approximate byte count, and the exact parameters to retrieve it.

### FR-4: Pagination Metadata on List Responses

All list responses that apply `limit`/`offset` MUST include:

```json
{
  "total": 53,
  "limit": 20,
  "offset": 0,
  "has_more": true
}
```

`total` is the full count before pagination. `has_more` is `true` when `offset + limit < total`.

### FR-5: MCP Tool Description Contract

The MCP tool description string for each tool MUST document the brief/full contract. Each affected description MUST state:

1. Default response is brief metadata (fits ~4k chars).
2. Which parameter enables full content (e.g., `include_content=true`).
3. For list actions: that `limit` (default 20, max 100) and `offset` are available.

Example addition to `status` description: `"Default returns metadata only (fits ~4k chars). Add include_content=true for full job output. Use tail=N for last N chars."`

### FR-6: Field Whitelist Validation

When `fields` is supplied, each field name is validated against the known field set for that action. An unrecognized field name MUST return an error response listing the valid field names. Silent dropping of unknown fields is forbidden.

### FR-7: Parameter Validation

| Parameter | Invalid value | Required response |
|---|---|---|
| `limit` | `> 100` | Clamp to 100, add `limit_clamped=true` |
| `limit` | `< 1` | Error: "limit must be >= 1" |
| `offset` | `< 0` | Error: "offset must be >= 0" |
| `tail` | `<= 0` | Error: "tail must be >= 1" |
| `fields` | unknown field name | Error: "unknown field 'X'; valid: <list>" |
| `fields` | empty string | Treated as omitted — brief defaults apply |

### FR-8: deepresearch Budget Exception

`deepresearch` returns a synthesized research report that is inherently long. It is exempt from the ~4k default budget. Its tool description MUST document this exception: `"Returns full synthesized report; not subject to the 4k default budget."` `include_content` and `tail` do not apply to this tool.

### FR-9: Default Response Budget

For non-exception tools, the default response (no budget parameters supplied) serialized as JSON MUST be <= 4096 chars on realistic fixtures. This is a soft target — the spec acknowledges that edge-case inputs (e.g., 50 sessions each with long CLI names) may slightly exceed this; tests use representative fixtures, not adversarial ones.

This budget applies to the default response only. Explicit `include_content=true` responses are unbounded.

### FR-10: content_length Field on Omitted Content

When content is available but omitted from the default response, the handler MUST include a `content_length` integer field with the byte count of the omitted content. This allows the caller to decide whether to request full content before doing so.

### FR-11: Backwards Compatibility

Callers that do not supply any of the new parameters receive brief default responses. No existing required parameter is renamed or removed. The top-level keys that previously existed in brief responses remain present (e.g., `sessions` array key, `job_id` key). Byte-for-byte response identity is not guaranteed.

## Non-Functional Requirements

### NFR-1: Per-Tool Default Budget Test

Each of the 13 non-exempt tools MUST have a test asserting that a call with no optional budget parameters returns a response of <= 4096 chars (measured on the final JSON-serialized tool result string) on a realistic fixture with non-trivial content.

### NFR-2: Overhead Budget

The budget enforcement logic (field selection, content omission, pagination, truncation marker construction) MUST add < 5ms latency per handler call, measured as p99 overhead on a fixed fixture in unit tests.

### NFR-3: No Breaking Change to Required Parameters

No tool's required parameters change. Existing callers depending only on required parameters receive valid brief responses.

### NFR-4: Field Whitelist is Statically Defined

Each handler's valid `fields` set MUST be defined as a named variable or constant in the handler's source file (or a shared package). Runtime reflection as the sole mechanism for field enumeration is prohibited.

### NFR-5: Error Responses Remain Compact

Error responses (invalid parameter, not found, validation failure) MUST be < 512 chars. They are not subject to the 4k budget but must not become a new source of bloat.

## User Stories

### US1 (P1): Orchestrator lists sessions without context overflow

**As an** orchestrator LLM calling `sessions(action=list)` on a server with 50+ sessions,
**I want** the response to contain pagination metadata and brief per-session rows,
**so that** my context budget is not consumed by a single tool call.

Acceptance criteria:
- `sessions(action=list)` with no optional params and 50 sessions in store returns <= 4096 chars.
- Response includes `total`, `limit=20`, `offset=0`, `has_more=true`.
- `sessions(action=list, limit=5, offset=0)` returns exactly 5 rows and `has_more=true`.
- `sessions(action=list, limit=500)` clamps to 100 and includes `limit_clamped=true`.
- Response includes `truncated=true` and `hint` when additional rows or fields were omitted.

### US2 (P1): Orchestrator retrieves job status without content overflow

**As an** orchestrator LLM polling `status(job_id=X)` on a completed job with 100k+ chars of output,
**I want** the default response to contain only job metadata,
**so that** I can confirm completion before deciding to fetch the full output.

Acceptance criteria:
- `status(job_id=X)` for a completed job with 100k-char content returns <= 4096 chars.
- Response includes `content_length` reflecting the omitted content byte count.
- Response includes `truncated=true` and `hint` referencing `include_content=true`.
- `status(job_id=X, include_content=true)` returns the full `content` field.
- `status(job_id=X, tail=500)` returns `content_tail` with the last 500 chars and `truncated=true`.

### US3 (P1): Orchestrator learns the brief/full contract from tool descriptions

**As an** orchestrator LLM reading the aimux MCP tool manifest before making calls,
**I want** each tool description to document the default-brief / opt-in-full contract,
**so that** I know which parameter to supply before making a large-content request.

Acceptance criteria:
- `status` tool description contains the string `include_content=true`.
- `sessions` tool description documents `limit`, `offset`, and `include_content=true`.
- `deepresearch` tool description contains the phrase "not subject to the 4k default budget".
- Every non-exempt tool description contains the phrase "fits ~4k chars" or equivalent.

### US4 (P2): Existing integrations continue working without parameter changes

**As an** existing MCP client calling aimux tools with only required parameters,
**I want** the response structure to remain valid (same top-level keys, same types),
**so that** I do not need to update my code after this change is deployed.

Acceptance criteria:
- `sessions(action=list)` without new params returns JSON with a `sessions` array key.
- `status(job_id=X)` without new params returns JSON with `job_id` and `status` keys.
- `agents(action=list)` without new params returns JSON with an `agents` array key.
- No tool returns an error when called without the new optional parameters.

### US5 (P2): Orchestrator retrieves agent system prompt on demand

**As an** orchestrator LLM calling `agents(action=info, agent=implementer)`,
**I want** the default response to exclude the system prompt (which can be 500KB),
**so that** routine agent discovery does not consume my context.

Acceptance criteria:
- `agents(action=info, agent=<any>)` without extra params returns <= 4096 chars.
- Response includes `truncated=true` and `hint` describing how to retrieve the system prompt.
- `agents(action=info, agent=<any>, include_content=true)` returns the `system_prompt` field.

## Edge Cases

**EC-1: Response already below budget.** When the handler's brief response fits within ~4k without omitting any field, `truncated` MUST be `false` or absent. No hint is emitted.

**EC-2: Invalid field name in `fields`.** `sessions(action=list, fields=id,nonexistent)` MUST return an error: `"unknown field 'nonexistent'; valid: id, status, cli, created_at, job_count"`. The call does not partially succeed.

**EC-3: `limit` above maximum.** `sessions(action=list, limit=200)` MUST return 100 rows, set `limit_clamped=true`, and include a `hint` noting the clamp.

**EC-4: `tail` is zero or negative.** `status(job_id=X, tail=-5)` and `status(job_id=X, tail=0)` MUST return an error: `"tail must be >= 1"`.

**EC-5: `include_content=true` on a running job.** Content does not exist yet; the field is absent. `truncated` MUST be `false`. No hint is emitted for a field that does not yet exist.

**EC-6: `fields` is an empty string.** Treated identically to omitting `fields` — brief defaults apply.

**EC-7: `offset` exceeds total.** `sessions(action=list, offset=9999)` returns an empty `sessions` array, correct `total`, and `has_more=false`. This is not an error.

**EC-8: `tail` larger than content.** `status(job_id=X, tail=999999)` when content is 500 chars returns the full content as `content_tail`. `truncated` MUST be `false`.

**EC-9: `include_content=true` on an action with no content-bearing field.** The response remains valid; no new field is invented. `truncated` is `false`.

## Out of Scope

- Streaming MCP responses (chunked transfer of large content).
- Client-side caching of tool responses.
- Response compression at transport layer.
- Field-level authorization (controlling which callers can access which fields).
- Cursor-based pagination beyond limit/offset.
- Automatic content summarization as an alternative to full content.

## Dependencies

- **mcp-go SDK** (`github.com/mark3labs/mcp-go v0.47.0`): `mcp.WithNumber`, `mcp.WithBoolean`, `mcp.WithString` are all available. No SDK upgrade required.
- **`pkg/session` Store**: `List()` currently returns `[]Session` slice (no pre-pagination count). FR-4 `total` field requires either a separate `Count()` call or counting before slicing. [NEEDS CLARIFICATION: does `session.Store` expose a `Count(status)` method, or must the handler call `List()` and slice in-memory? This determines whether FR-4 is a pure handler change or requires a `Store` interface update.]
- **`pkg/loom` LoomEngine**: `loom.List(projectID)` returns `[]Task`. Same count gap as sessions. The `sessions list` response currently merges both slices — pagination policy across two heterogeneous stores requires clarification.
- **`pkg/types` JobSnapshot**: `Content` field is the source of the 140k overflow. Brief path omits this field at the handler layer; the struct itself is unchanged.
- **`pkg/server/server_agents.go` `agents.Agent` struct**: `agents info` brief path must exclude the system prompt field. [NEEDS CLARIFICATION: confirm the exact field name on the `agents.Agent` struct that holds the prompt body — is it `SystemPrompt`, `Prompt`, or `Content`? `server_agents.go:85` calls `marshalToolResult(agent)` directly with no field selection.]

## Success Criteria

1. Each of the 13 non-exempt tools has a unit test asserting default response <= 4096 chars on a realistic fixture with non-trivial content.
2. `sessions(action=list)` with 50+ sessions in the fixture returns <= 4096 chars with no optional params.
3. `status(job_id=X)` with a 100k-char content job returns <= 4096 chars with no optional params; full content is returned when `include_content=true`.
4. All 14 tool descriptions in `registerTools()` document the brief/full contract (or the deepresearch exception).
5. All edge cases EC-1 through EC-9 have passing tests.
6. No existing test (857 total) regresses due to this change.
7. Existing calls using only required parameters return valid JSON with the same top-level keys as before.

## Open Questions

1. [NEEDS CLARIFICATION: `session.Store.List()` — does the current implementation support a pre-pagination total count, or does it require the handler to call `List()` and count in memory? This determines whether FR-4 is a pure handler layer change or requires a `Store` interface addition.]

2. [NEEDS CLARIFICATION: `sessions list` merges legacy sessions from `session.Store` and Loom tasks from `loom.List()` into a single response. Should `limit`/`offset` apply to each collection independently (two pagination cursors) or to a merged view? A merged view requires a defined sort order. This shapes the FR-4 pagination metadata structure.]

3. [NEEDS CLARIFICATION: `agents.Agent` struct field name for the system prompt body — confirm whether it is `SystemPrompt`, `Prompt`, or `Content` before FR-2 brief field set for `agents info` can be locked in implementation.]
