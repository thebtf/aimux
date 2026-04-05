// Package parser provides output parsers for different CLI output formats.
package parser

import (
	"encoding/json"
	"strings"
)

// JSONLEvent represents a parsed JSONL event (codex output format).
type JSONLEvent struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content,omitempty"`
	// Common fields extracted lazily
	rawLine string
}

// ParseJSONL parses JSONL output (one JSON object per line).
// Filters to only agent_message and turn.completed events.
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

		// Filter: only keep agent_message and turn.completed
		switch evt.Type {
		case "agent_message", "turn.completed":
			events = append(events, evt)
		}
	}

	return events
}

// ExtractAgentMessages extracts text content from agent_message events.
func ExtractAgentMessages(events []JSONLEvent) string {
	var parts []string

	for _, evt := range events {
		if evt.Type != "agent_message" {
			continue
		}

		// agent_message content is typically a string or an object with "text"
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
func ExtractSessionID(events []JSONLEvent) string {
	for _, evt := range events {
		var obj struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(evt.rawLine), &obj); err == nil && obj.SessionID != "" {
			return obj.SessionID
		}
	}
	return ""
}
