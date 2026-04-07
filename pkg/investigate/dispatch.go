package investigate

import (
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/think"
)

// DispatchThinkCall executes a think pattern in-process and returns the result.
// This enables investigate to use think tools for self-enrichment without
// external MCP round-trips.
func DispatchThinkCall(pattern string, params map[string]any) (*think.ThinkResult, error) {
	handler := think.GetPattern(pattern)
	if handler == nil {
		return nil, fmt.Errorf("think pattern %q not found", pattern)
	}

	validInput, err := handler.Validate(params)
	if err != nil {
		return nil, fmt.Errorf("think validate: %w", err)
	}

	result, err := handler.Handle(validInput, "")
	if err != nil {
		return nil, fmt.Errorf("think handle: %w", err)
	}

	return result, nil
}

// ParseSuggestedThinkCall extracts pattern and params from a suggested think call string.
// Format: mcp__aimux__think({ pattern: "X", key: "value", ... })
func ParseSuggestedThinkCall(suggestion string) (pattern string, params map[string]any, err error) {
	// Extract content between ({ and })
	start := strings.Index(suggestion, "({")
	end := strings.LastIndex(suggestion, "})")
	if start < 0 || end < 0 || end <= start {
		return "", nil, fmt.Errorf("cannot parse think call: %q", suggestion)
	}

	inner := strings.TrimSpace(suggestion[start+2 : end])

	params = make(map[string]any)
	// Parse key: "value" pairs
	for _, part := range strings.Split(inner, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		colonIdx := strings.Index(part, ":")
		if colonIdx < 0 {
			continue
		}

		key := strings.TrimSpace(part[:colonIdx])
		val := strings.TrimSpace(part[colonIdx+1:])
		// Remove quotes
		val = strings.Trim(val, "\"")

		if key == "pattern" {
			pattern = val
		} else {
			params[key] = val
		}
	}

	if pattern == "" {
		return "", nil, fmt.Errorf("no pattern found in think call: %q", suggestion)
	}

	return pattern, params, nil
}

// ThinkFindingFromResult creates a FindingInput from a think result for auto-dispatch.
func ThinkFindingFromResult(result *think.ThinkResult, sessionID string) FindingInput {
	// Summarize the think result data
	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("[auto:think:%s] ", result.Pattern))

	// Extract key data fields
	for key, val := range result.Data {
		switch v := val.(type) {
		case string:
			if len(v) > 200 {
				v = v[:200] + "..."
			}
			summary.WriteString(fmt.Sprintf("%s: %s. ", key, v))
		case bool:
			if v {
				summary.WriteString(fmt.Sprintf("%s: true. ", key))
			}
		case int, float64:
			summary.WriteString(fmt.Sprintf("%s: %v. ", key, v))
		}
	}

	return FindingInput{
		Description:  summary.String(),
		Source:       fmt.Sprintf("auto:think:%s", result.Pattern),
		Severity:     SeverityP3,
		Confidence:   ConfidenceInferred,
	}
}
