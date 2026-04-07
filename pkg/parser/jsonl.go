// Package parser provides output parsers for different CLI output formats.
package parser

import (
	"encoding/json"
	"strings"
)

// JSONLEvent represents a parsed JSONL event (codex output format).
// Supports both legacy format (type: "agent_message") and modern codex exec --json
// format (type: "item.completed" with nested item.type: "agent_message").
type JSONLEvent struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content,omitempty"`
	Item    *JSONLItem      `json:"item,omitempty"`
	// Common fields extracted lazily
	rawLine string
}

// JSONLItem represents a nested item in codex exec --json output.
type JSONLItem struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ParseJSONL parses JSONL output (one JSON object per line).
// Accepts both legacy events (agent_message, turn.completed) and modern
// codex exec events (thread.started, turn.started, item.completed, turn.completed).
func ParseJSONL(output string) []JSONLEvent {
	var events []JSONLEvent
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}

		var evt JSONLEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		evt.rawLine = line

		switch evt.Type {
		case "agent_message", "turn.completed", "turn.failed":
			// Legacy format
			events = append(events, evt)
		case "item.completed":
			// Modern codex exec --json format: extract item.type
			events = append(events, evt)
		case "thread.started":
			// Contains thread_id (used as session ID)
			events = append(events, evt)
		}
	}

	return events
}

// ExtractAgentMessages extracts text content from agent_message events.
// Handles both legacy (top-level agent_message) and modern (item.completed
// with item.type == "agent_message") formats.
func ExtractAgentMessages(events []JSONLEvent) string {
	var parts []string

	for _, evt := range events {
		// Modern format: item.completed with nested agent_message
		if evt.Type == "item.completed" && evt.Item != nil && evt.Item.Type == "agent_message" {
			if evt.Item.Text != "" {
				parts = append(parts, evt.Item.Text)
			}
			continue
		}

		// Legacy format: top-level agent_message
		if evt.Type != "agent_message" {
			continue
		}

		var text string
		if err := json.Unmarshal(evt.Content, &text); err == nil {
			parts = append(parts, text)
			continue
		}

		var obj struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(evt.Content, &obj); err == nil && obj.Text != "" {
			parts = append(parts, obj.Text)
		}
	}

	return strings.Join(parts, "")
}

// ExtractSessionID extracts a CLI session ID from JSONL events.
// Checks both thread_id (modern codex format) and session_id (legacy).
func ExtractSessionID(events []JSONLEvent) string {
	for _, evt := range events {
		// Modern format: thread.started has thread_id
		if evt.Type == "thread.started" {
			var obj struct {
				ThreadID string `json:"thread_id"`
			}
			if err := json.Unmarshal([]byte(evt.rawLine), &obj); err == nil && obj.ThreadID != "" {
				return obj.ThreadID
			}
		}

		// Legacy format: session_id field
		var obj struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(evt.rawLine), &obj); err == nil && obj.SessionID != "" {
			return obj.SessionID
		}
	}
	return ""
}
