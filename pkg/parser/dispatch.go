package parser

// ParseContent dispatches to the appropriate parser based on output format.
// Returns parsed text content and an optional CLI session ID (from JSONL events).
// On parse failure, returns raw content as-is (graceful degradation).
func ParseContent(raw, format string) (parsed string, cliSessionID string) {
	if raw == "" {
		return "", ""
	}

	switch format {
	case "jsonl":
		events := ParseJSONL(raw)
		content := ExtractAgentMessages(events)
		sessionID := ExtractSessionID(events)
		if content != "" {
			return content, sessionID
		}
		// No agent_message events found — return raw
		return raw, sessionID

	case "json":
		resp, err := ParseJSON(raw)
		if err != nil || resp == nil {
			return raw, ""
		}
		content := ExtractContent(resp)
		if content != "" {
			return content, resp.SessionID
		}
		return raw, resp.SessionID

	default:
		// "text", "", or unknown — pass through
		return raw, ""
	}
}
