package parser

import (
	"strings"
	"testing"
)

func TestParseContent_JSONL_ExtractsAgentMessages(t *testing.T) {
	raw := `{"type":"agent_message","content":"Hello"}
{"type":"progress","content":"50%"}
{"type":"agent_message","content":" world"}
{"type":"turn.completed","session_id":"sess-123"}`

	parsed, sessionID := ParseContent(raw, "jsonl")

	if parsed != "Hello world" {
		t.Errorf("parsed = %q, want %q", parsed, "Hello world")
	}
	if sessionID != "sess-123" {
		t.Errorf("sessionID = %q, want %q", sessionID, "sess-123")
	}
}

func TestParseContent_JSONL_NoAgentMessages(t *testing.T) {
	raw := `{"type":"progress","content":"50%"}
{"type":"turn.completed","session_id":"sess-456"}`

	parsed, sessionID := ParseContent(raw, "jsonl")

	// No agent_message events → returns raw
	if parsed != raw {
		t.Error("expected raw content when no agent_message events")
	}
	if sessionID != "sess-456" {
		t.Errorf("sessionID = %q, want %q", sessionID, "sess-456")
	}
}

func TestParseContent_JSON_ExtractsContent(t *testing.T) {
	raw := `{"content":"The answer is 42","session_id":"s1","exit_code":0}`

	parsed, sessionID := ParseContent(raw, "json")

	if parsed != "The answer is 42" {
		t.Errorf("parsed = %q, want %q", parsed, "The answer is 42")
	}
	if sessionID != "s1" {
		t.Errorf("sessionID = %q, want %q", sessionID, "s1")
	}
}

func TestParseContent_JSON_ExtractsText(t *testing.T) {
	raw := `{"text":"Response text","session_id":"s2"}`

	parsed, _ := ParseContent(raw, "json")

	if parsed != "Response text" {
		t.Errorf("parsed = %q, want %q", parsed, "Response text")
	}
}

func TestParseContent_JSON_ExtractsResponse(t *testing.T) {
	raw := `{"response":"API response","session_id":"s3"}`

	parsed, _ := ParseContent(raw, "json")

	if parsed != "API response" {
		t.Errorf("parsed = %q, want %q", parsed, "API response")
	}
}

func TestParseContent_JSON_ExtractsResult(t *testing.T) {
	// Claude returns the answer in "result" field
	raw := `{"type":"result","subtype":"success","result":"\n\n4","session_id":"s4"}`

	parsed, sessionID := ParseContent(raw, "json")

	if parsed != "\n\n4" {
		t.Errorf("parsed = %q, want %q", parsed, "\n\n4")
	}
	if sessionID != "s4" {
		t.Errorf("sessionID = %q, want %q", sessionID, "s4")
	}
}

func TestParseContent_JSON_WithPrecedingText(t *testing.T) {
	raw := `Some stderr noise
{"content":"actual response","session_id":"s5"}`

	parsed, _ := ParseContent(raw, "json")

	if parsed != "actual response" {
		t.Errorf("parsed = %q, want %q", parsed, "actual response")
	}
}

func TestParseContent_Text_Passthrough(t *testing.T) {
	raw := "Plain text response\nwith multiple lines"

	parsed, sessionID := ParseContent(raw, "text")

	if parsed != raw {
		t.Errorf("parsed = %q, want raw passthrough", parsed)
	}
	if sessionID != "" {
		t.Errorf("sessionID = %q, want empty for text format", sessionID)
	}
}

func TestParseContent_EmptyFormat_Passthrough(t *testing.T) {
	raw := "some output"

	parsed, _ := ParseContent(raw, "")

	if parsed != raw {
		t.Error("expected raw passthrough for empty format")
	}
}

func TestParseContent_UnknownFormat_Passthrough(t *testing.T) {
	raw := "some output"

	parsed, _ := ParseContent(raw, "xml")

	if parsed != raw {
		t.Error("expected raw passthrough for unknown format")
	}
}

func TestParseContent_EmptyInput(t *testing.T) {
	parsed, sessionID := ParseContent("", "jsonl")

	if parsed != "" {
		t.Errorf("parsed = %q, want empty", parsed)
	}
	if sessionID != "" {
		t.Errorf("sessionID = %q, want empty", sessionID)
	}
}

func TestParseContent_MalformedJSONL_Fallback(t *testing.T) {
	raw := "this is not jsonl at all\njust some text"

	parsed, _ := ParseContent(raw, "jsonl")

	// No valid JSONL events → returns raw
	if parsed != raw {
		t.Error("expected raw fallback for malformed JSONL")
	}
}

func TestParseContent_MalformedJSON_Fallback(t *testing.T) {
	raw := "not json at all"

	parsed, _ := ParseContent(raw, "json")

	// No valid JSON → returns raw
	if parsed != raw {
		t.Error("expected raw fallback for malformed JSON")
	}
}

func TestParseContent_JSON_EmptyFields_Fallback(t *testing.T) {
	// JSON parses but all content fields are empty
	raw := `{"exit_code":0,"session_id":"s6"}`

	parsed, sessionID := ParseContent(raw, "json")

	// All content fields empty → returns raw
	if parsed != raw {
		t.Errorf("parsed = %q, want raw fallback", parsed)
	}
	if sessionID != "s6" {
		t.Errorf("sessionID = %q, want %q", sessionID, "s6")
	}
}

func TestParseContent_JSONL_SessionIDFromTurnCompleted(t *testing.T) {
	raw := `{"type":"agent_message","content":"hi"}
{"type":"turn.completed","session_id":"unique-session-id"}`

	_, sessionID := ParseContent(raw, "jsonl")

	if sessionID != "unique-session-id" {
		t.Errorf("sessionID = %q, want %q", sessionID, "unique-session-id")
	}
}

func TestParseContent_JSON_ContentFieldPriority(t *testing.T) {
	// When multiple fields present, "content" takes priority
	raw := `{"content":"primary","text":"secondary","result":"tertiary"}`

	parsed, _ := ParseContent(raw, "json")

	if parsed != "primary" {
		t.Errorf("parsed = %q, want %q (content should take priority)", parsed, "primary")
	}
}

func TestParseContent_JSONL_LargeOutput(t *testing.T) {
	// Simulate realistic codex output with many JSONL lines
	var lines []string
	lines = append(lines, `{"type":"agent_message","content":"Part 1 of response. "}`)
	lines = append(lines, `{"type":"progress","content":"working..."}`)
	lines = append(lines, `{"type":"agent_message","content":"Part 2 of response."}`)
	lines = append(lines, `{"type":"turn.completed","session_id":"big-session"}`)

	raw := strings.Join(lines, "\n")
	parsed, sessionID := ParseContent(raw, "jsonl")

	if parsed != "Part 1 of response. Part 2 of response." {
		t.Errorf("parsed = %q", parsed)
	}
	if sessionID != "big-session" {
		t.Errorf("sessionID = %q", sessionID)
	}
}
