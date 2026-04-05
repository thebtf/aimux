package parser

import (
	"encoding/json"
	"strings"
)

// JSONResponse represents a parsed JSON response (gemini, claude, qwen format).
type JSONResponse struct {
	Content   string `json:"content,omitempty"`
	Text      string `json:"text,omitempty"`
	Response  string `json:"response,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	ExitCode  int    `json:"exit_code,omitempty"`
	// Stats fields (varies by CLI)
	Stats json.RawMessage `json:"stats,omitempty"`
}

// ParseJSON parses a complete JSON response from CLI output.
// Handles the case where JSON may be preceded by non-JSON text.
func ParseJSON(output string) (*JSONResponse, error) {
	// Find outermost JSON object
	jsonStr := FindOutermostJSON(output)
	if jsonStr == "" {
		return nil, nil
	}

	var resp JSONResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// ExtractContent gets the text content from a JSON response.
// Tries content, text, response fields in order.
func ExtractContent(resp *JSONResponse) string {
	if resp == nil {
		return ""
	}
	if resp.Content != "" {
		return resp.Content
	}
	if resp.Text != "" {
		return resp.Text
	}
	return resp.Response
}

// FindOutermostJSON finds the outermost JSON object in a string.
// This is important when stderr output gets mixed with JSON,
// preventing us from finding nested objects instead of the outer one.
func FindOutermostJSON(s string) string {
	// Find first opening brace
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}

	// Walk forward counting braces to find matching close
	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(s); i++ {
		ch := s[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' && inString {
			escaped = true
			continue
		}

		if ch == '"' {
			inString = !inString
			continue
		}

		if inString {
			continue
		}

		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}

	return ""
}
