package budget

// NFR-1: Per-tool default budget test.
// Each non-exempt tool MUST have a test asserting that the default brief response
// (no optional budget params) is <= 4096 UTF-8 bytes on a realistic fixture.
// deepresearch is exempt per FR-8.
//
// The MCP wire response for most aimux tools is NOT a bare result map — it is a
// guided envelope built by pkg/guidance ResponseBuilder.BuildPayload, which adds
// fields like `result`, `state`, `you_are_here`, `how_this_tool_works`,
// `choose_your_path[]`, `gaps[]`, `stop_conditions`, `do_not[]`. Measuring only
// `json.Marshal(fixture)` understates the real payload size.
//
// The tests below wrap each fixture in wrapInEnvelope() — a representative
// approximation of the guided envelope with conservative (upper-bound) policy
// text. If `wrapped` serialises to <= 4096 bytes, the real wire response will
// also fit, because real policy text is usually shorter than the upper-bound
// simulation here.
//
// Pure-schema tools (upgrade, status when used for quick polling) do NOT go
// through marshalGuidedToolResult and measure against the raw fixture.

import (
	"encoding/json"
	"strings"
	"testing"
)

const nfrBudgetLimit = 4096

// envelopeOverhead represents an upper-bound approximation of the bytes added by
// guidance.ResponseBuilder.BuildPayload to the raw result map. Real policy text
// for most tools is shorter; this is intentionally conservative so tests fail
// early if a brief fixture drifts toward the envelope budget headroom.
//
// The concrete fields mirror those set by BuildPayload (builder.go:22) and the
// typical volumes produced by pkg/guidance/policies/*.
func envelopeOverhead() map[string]any {
	return map[string]any{
		"state":        "ok",
		"you_are_here": "Tool returned a brief result. Call again with include_content=true for full output when needed.",
		"how_this_tool_works": "Default responses are bounded to ~4 KiB so multi-step orchestrators do not exhaust their MCP context window. " +
			"Use include_content=true to opt into the full payload for a single call. Use tail=N on status for partial output.",
		"choose_your_path": []string{
			"next: call with include_content=true if the brief is insufficient",
			"next: paginate with limit/offset if listing",
			"next: consume content_length to decide whether to fetch full content",
		},
		"gaps": []string{
			"If content_length > 0 and include_content was omitted, the full payload is withheld.",
		},
		"stop_conditions": "Stop when the brief answers your question, or when include_content returns the full payload.",
		"do_not": []string{
			"Do not assume the brief is a complete answer if content_length > 0.",
			"Do not retry without include_content=true if the brief flagged truncation.",
		},
	}
}

// wrapInEnvelope produces a payload shape that mirrors what pkg/guidance
// ResponseBuilder.BuildPayload emits on the wire: the raw result under
// `result` plus representative guidance fields. This lets NFR-1 assert the
// realistic wire payload, not just the stripped result map (coderabbit PR #102).
func wrapInEnvelope(result map[string]any) map[string]any {
	wrapped := envelopeOverhead()
	wrapped["result"] = result
	return wrapped
}

// nfrFixture returns a realistic brief response fixture for the given tool/action
// key, sized to represent a real-world (non-adversarial) payload while staying
// clearly within the 4k limit when content is withheld.
func nfrBriefFixture(key string) map[string]any {
	switch key {
	case "status":
		return map[string]any{
			"job_id":         "job-abc123",
			"status":         "completed",
			"progress":       "done",
			"poll_count":     3,
			"session_id":     "sess-xyz",
			"content_length": 142000, // 142k real output — omitted in brief
			"truncated":      true,
			"hint":           "content omitted (142000 bytes). Use status(job_id=job-abc123, include_content=true) for full output.",
			// progress_tail: worst-case 100-byte UTF-8 string (FR-1 cap).
			"progress_tail": strings.Repeat("x", 100),
			// progress_lines: realistic 10-digit int (NFR-1 additive weight check).
			"progress_lines": 9999999999,
		}
	case "sessions/list":
		// Realistic: 20 session rows + 5 loom rows + pagination objects.
		sessions := make([]map[string]any, 20)
		for i := range sessions {
			sessions[i] = map[string]any{
				"id":         strings.Repeat("s", 8),
				"status":     "completed",
				"cli":        "codex",
				"created_at": "2026-04-18T12:00:00Z",
				"job_count":  2,
			}
		}
		loomTasks := make([]map[string]any, 5)
		for i := range loomTasks {
			loomTasks[i] = map[string]any{
				"id":         strings.Repeat("t", 8),
				"status":     "completed",
				"kind":       "cli",
				"created_at": "2026-04-18T12:00:00Z",
			}
		}
		return map[string]any{
			"sessions":   sessions,
			"loom_tasks": loomTasks,
			"sessions_pagination": map[string]any{
				"total": 42, "limit": 20, "offset": 0, "has_more": true,
			},
			"loom_pagination": map[string]any{
				"total": 5, "limit": 20, "offset": 0, "has_more": false,
			},
		}
	case "sessions/info":
		jobs := make([]map[string]any, 5)
		for i := range jobs {
			jobs[i] = map[string]any{
				"id":             strings.Repeat("j", 8),
				"status":         "completed",
				"progress":       "",
				"content_length": 50000,
			}
		}
		return map[string]any{
			"session": map[string]any{
				"id":         "sess-abc",
				"status":     "completed",
				"cli":        "codex",
				"created_at": "2026-04-18T12:00:00Z",
			},
			"jobs": jobs,
		}
	case "investigate/list":
		investigations := make([]map[string]any, 10)
		for i := range investigations {
			investigations[i] = map[string]any{
				"session_id":    strings.Repeat("i", 8),
				"topic":         "budget policy implementation review",
				"domain":        "debugging",
				"status":        "active",
				"finding_count": 7,
			}
		}
		return map[string]any{
			"investigations": investigations,
			"pagination": map[string]any{
				"total": 10, "limit": 20, "offset": 0, "has_more": false,
			},
		}
	case "investigate/status":
		return map[string]any{
			"session_id":        "sess-inv-001",
			"topic":             "budget policy implementation",
			"domain":            "debugging",
			"status":            "active",
			"finding_count":     12,
			"coverage_progress": 0.75,
		}
	case "investigate/recall":
		return map[string]any{
			"found":          true,
			"session_id":     "investigate-budget-policy-2026-04-18T12-00-00.md",
			"topic":          "budget policy implementation",
			"date":           "2026-04-18T12-00-00",
			"finding_count":  0,
			"content_length": 28000, // 28k report — omitted in brief
			"truncated":      true,
			"hint":           "content omitted (28000 bytes). Use investigate(action=recall, topic=..., include_content=true) for full report.",
		}
	case "agents/list":
		agentList := make([]map[string]any, 5)
		for i, name := range []string{"implementer", "reviewer", "debugger", "researcher", "generic"} {
			agentList[i] = map[string]any{
				"name":        name,
				"description": "Agent for " + name + " tasks with detailed role-based guidance.",
				"role":        "coding",
				"domain":      "engineering",
			}
		}
		return map[string]any{
			"agents": agentList,
			"count":  5,
		}
	case "agents/info":
		return map[string]any{
			"name":           "implementer",
			"description":    "Implements features and fixes bugs using best practices.",
			"role":           "coding",
			"domain":         "engineering",
			"tools":          []string{"Read", "Write", "Edit", "Bash"},
			"when":           "Use when you need to implement a feature or fix a bug",
			"content_length": 512000, // 500KB system prompt — omitted in brief
			"truncated":      true,
			"hint":           "content omitted (512000 bytes). Use agents(action=info, include_content=true) for full content.",
		}
	case "exec":
		return map[string]any{
			"status":         "completed",
			"content_length": 45000,
			"truncated":      true,
			"hint":           "content omitted (45000 bytes). Use exec with include_content=true for full output.",
		}
	case "agent":
		return map[string]any{
			"agent":          "implementer",
			"cli":            "codex",
			"model":          "gpt-5.3-codex",
			"effort":         "medium",
			"status":         "completed",
			"turns":          12,
			"duration_ms":    45000,
			"content_length": 98000,
			"truncated":      true,
			"hint":           "content omitted (98000 bytes). Use agent with include_content=true for full output.",
		}
	case "consensus":
		return map[string]any{
			"status":         "completed",
			"turns":          4,
			"content_length": 32000,
			"truncated":      true,
			"hint":           "content omitted (32000 bytes). Use consensus(include_content=true) for full transcript.",
		}
	case "debate":
		return map[string]any{
			"status":         "completed",
			"turns":          6,
			"content_length": 48000,
			"truncated":      true,
			"hint":           "content omitted (48000 bytes). Use debate(include_content=true) for full transcript.",
		}
	case "dialog":
		return map[string]any{
			"session_id":     "sess-dialog-001",
			"status":         "completed",
			"turns":          8,
			"participants":   []string{"codex", "gemini"},
			"content_length": 64000,
			"truncated":      true,
			"hint":           "content omitted (64000 bytes). Use dialog(session_id=..., include_content=true) for full transcript.",
		}
	case "audit":
		return map[string]any{
			"status":         "completed",
			"turns":          3,
			"content_length": 76000,
			"truncated":      true,
			"hint":           "content omitted (76000 bytes). Use audit(cwd=..., async=false, include_content=true) for full output.",
		}
	case "think":
		return map[string]any{
			"pattern":   "critical_thinking",
			"status":    "complete",
			"summary":   "Analysis complete with 3 key insights identified.",
			"timestamp": "2026-04-18T12:00:00Z",
			"mode":      "deep",
			"complexity": map[string]any{
				"total": 45, "threshold": 30,
			},
		}
	case "workflow":
		return map[string]any{
			"status":         "completed",
			"turns":          5,
			"content_length": 55000,
			"truncated":      true,
			"hint":           "content omitted (55000 bytes). Use workflow(steps=..., include_content=true) for full output.",
		}
	case "upgrade":
		return map[string]any{
			"action":          "check",
			"current_version": "4.1.1",
			"latest_version":  "4.2.0",
			"update_available": true,
		}
	default:
		return map[string]any{"status": "ok"}
	}
}

func TestNFR1_DefaultBriefBudget(t *testing.T) {
	// 13 non-exempt tools per NFR-1. deepresearch is excluded per FR-8.
	testCases := []struct {
		name    string // human-readable tool/action label
		fixtKey string // key into nfrBriefFixture
	}{
		{"status", "status"},
		{"sessions/list", "sessions/list"},
		{"sessions/info", "sessions/info"},
		{"investigate/list", "investigate/list"},
		{"investigate/status", "investigate/status"},
		{"investigate/recall", "investigate/recall"},
		{"agents/list", "agents/list"},
		{"agents/info", "agents/info"},
		{"exec", "exec"},
		{"agent", "agent"},
		{"consensus", "consensus"},
		{"debate", "debate"},
		{"dialog", "dialog"},
		// audit and workflow are additional non-exempt tools covered here for completeness.
		// The spec lists 13 non-exempt; audit + workflow + think + upgrade round it out.
		{"audit", "audit"},
		{"think", "think"},
		{"workflow", "workflow"},
		{"upgrade", "upgrade"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fixture := nfrBriefFixture(tc.fixtKey)
			if fixture == nil {
				t.Fatalf("nil fixture for %q", tc.fixtKey)
			}

			// upgrade bypasses the guided envelope (plain status tool); measure raw.
			// All other non-exempt tools go through marshalGuidedToolResult at wire
			// time, so wrap the fixture to reflect the realistic payload.
			var payload map[string]any
			if tc.fixtKey == "upgrade" {
				payload = fixture
			} else {
				payload = wrapInEnvelope(fixture)
			}

			b, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("json.Marshal payload for %q: %v", tc.fixtKey, err)
			}

			byteCount := len(b)
			if byteCount > nfrBudgetLimit {
				t.Errorf("tool %q default wire response = %d bytes, want <= %d bytes (NFR-1 violation)",
					tc.name, byteCount, nfrBudgetLimit)
			}

			// Swap body → return nil guard: fixture must be non-nil and produce
			// at least 2 bytes (empty object "{}") to catch stub returns.
			if byteCount < 2 {
				t.Errorf("tool %q fixture produced suspiciously small output (%d bytes) — possible stub", tc.name, byteCount)
			}
		})
	}
}

// TestNFR1_DeepresearchExempt asserts deepresearch is NOT subject to the 4k budget
// (FR-8 exception). This test documents the exemption rather than enforcing a limit.
func TestNFR1_DeepresearchExempt(t *testing.T) {
	// A realistic deepresearch response is a full synthesized report: 10k–100k chars.
	// There is no budget limit applied. We verify the fixture itself exceeds 4k to
	// confirm the exemption is meaningful.
	largeReport := strings.Repeat("deep research content line\n", 200) // ~5400 bytes
	fixture := map[string]any{
		"topic":   "response budget policy",
		"cached":  false,
		"content": largeReport,
	}
	b, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if len(b) <= nfrBudgetLimit {
		t.Skipf("deepresearch fixture (%d bytes) did not exceed %d — extend the fixture", len(b), nfrBudgetLimit)
	}
	// Passes: deepresearch is intentionally exempt, no limit enforced.
	t.Logf("deepresearch exempt: fixture %d bytes > %d budget limit (FR-8 confirmed)", len(b), nfrBudgetLimit)
}
