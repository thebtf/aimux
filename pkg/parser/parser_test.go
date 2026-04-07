package parser_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/parser"
)

func TestParseJSONL_Legacy(t *testing.T) {
	input := `{"type":"tool_call","content":"ignored"}
{"type":"agent_message","content":"hello world"}
not json
{"type":"turn.completed","content":null}
{"type":"file_write","content":"ignored too"}
`

	events := parser.ParseJSONL(input)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "agent_message" {
		t.Errorf("first event type = %q, want agent_message", events[0].Type)
	}
	if events[1].Type != "turn.completed" {
		t.Errorf("second event type = %q, want turn.completed", events[1].Type)
	}
}

func TestParseJSONL_ModernCodex(t *testing.T) {
	// Real codex exec --json output format
	input := `{"type":"thread.started","thread_id":"019d659e-b7c6-7192-9da6-2442c7d47a74"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"hello world"}}
{"type":"turn.completed","usage":{"input_tokens":29400,"cached_input_tokens":3456,"output_tokens":143}}
`

	events := parser.ParseJSONL(input)
	if len(events) != 3 {
		t.Fatalf("expected 3 events (thread.started, item.completed, turn.completed), got %d", len(events))
	}
	if events[0].Type != "thread.started" {
		t.Errorf("first event type = %q, want thread.started", events[0].Type)
	}
	if events[1].Type != "item.completed" {
		t.Errorf("second event type = %q, want item.completed", events[1].Type)
	}
	if events[2].Type != "turn.completed" {
		t.Errorf("third event type = %q, want turn.completed", events[2].Type)
	}
}

func TestExtractAgentMessages_Legacy(t *testing.T) {
	input := `{"type":"agent_message","content":"Hello "}
{"type":"agent_message","content":"World"}
{"type":"turn.completed"}
`
	events := parser.ParseJSONL(input)
	text := parser.ExtractAgentMessages(events)

	if text != "Hello World" {
		t.Errorf("extracted = %q, want %q", text, "Hello World")
	}
}

func TestExtractAgentMessages_ModernCodex(t *testing.T) {
	input := `{"type":"thread.started","thread_id":"abc123"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"four"}}
{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":10}}
`
	events := parser.ParseJSONL(input)
	text := parser.ExtractAgentMessages(events)

	if text != "four" {
		t.Errorf("extracted = %q, want %q", text, "four")
	}
}

func TestExtractSessionID_ModernCodex(t *testing.T) {
	input := `{"type":"thread.started","thread_id":"019d659e-b7c6-7192-9da6-2442c7d47a74"}
{"type":"turn.started"}
`
	events := parser.ParseJSONL(input)
	sessionID := parser.ExtractSessionID(events)

	if sessionID != "019d659e-b7c6-7192-9da6-2442c7d47a74" {
		t.Errorf("session_id = %q, want 019d659e-b7c6-7192-9da6-2442c7d47a74", sessionID)
	}
}

func TestParseJSON(t *testing.T) {
	input := `Some stderr text
{"content":"result here","session_id":"abc123","exit_code":0}
`
	resp, err := parser.ParseJSON(input)
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	content := parser.ExtractContent(resp)
	if content != "result here" {
		t.Errorf("content = %q, want %q", content, "result here")
	}
	if resp.SessionID != "abc123" {
		t.Errorf("session_id = %q, want abc123", resp.SessionID)
	}
}

func TestFindOutermostJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple",
			input: `{"key":"value"}`,
			want:  `{"key":"value"}`,
		},
		{
			name:  "nested",
			input: `{"outer":{"inner":"val"}}`,
			want:  `{"outer":{"inner":"val"}}`,
		},
		{
			name:  "with prefix",
			input: `stderr noise {"key":"value"} trailing`,
			want:  `{"key":"value"}`,
		},
		{
			name:  "no json",
			input: "just text",
			want:  "",
		},
		{
			name:  "string with braces",
			input: `{"msg":"use {x} here"}`,
			want:  `{"msg":"use {x} here"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parser.FindOutermostJSON(tt.input)
			if got != tt.want {
				t.Errorf("FindOutermostJSON = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTextFindings(t *testing.T) {
	input := `Starting audit...
FINDING: [HIGH] no-hardcoded-secrets — Hardcoded API key detected (src/config.ts:42)
FINDING: [MEDIUM] unused-export — Unused exported function (src/utils.ts:10)
Some other output
FINDING: [CRITICAL] sql-injection — Unsanitized user input in query (src/db.ts:88)
`
	findings := parser.ParseTextFindings(input)

	if len(findings) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.Severity != "HIGH" {
		t.Errorf("severity = %q, want HIGH", f.Severity)
	}
	if f.Rule != "no-hardcoded-secrets" {
		t.Errorf("rule = %q, want no-hardcoded-secrets", f.Rule)
	}
	if f.File != "src/config.ts" {
		t.Errorf("file = %q, want src/config.ts", f.File)
	}
	if f.Line != 42 {
		t.Errorf("line = %d, want 42", f.Line)
	}

	if findings[2].Severity != "CRITICAL" {
		t.Errorf("third finding severity = %q, want CRITICAL", findings[2].Severity)
	}
}
