package investigate

import (
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/think"
)

// ThinkCallSpec is a strongly-typed representation of a think call.
// It replaces the fragile string-template approach used previously.
type ThinkCallSpec struct {
	Pattern string
	Params  map[string]any
}

// DispatchThinkCall executes a think pattern in-process and returns the result.
// This enables investigate to use think tools for self-enrichment without
// external MCP round-trips.
func DispatchThinkCall(spec ThinkCallSpec) (*think.ThinkResult, error) {
	handler := think.GetPattern(spec.Pattern)
	if handler == nil {
		return nil, fmt.Errorf("think pattern %q not found", spec.Pattern)
	}

	validInput, err := handler.Validate(spec.Params)
	if err != nil {
		return nil, fmt.Errorf("think validate: %w", err)
	}

	result, err := handler.Handle(validInput, "")
	if err != nil {
		return nil, fmt.Errorf("think handle: %w", err)
	}

	return result, nil
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
		Description: summary.String(),
		Source:      fmt.Sprintf("auto:think:%s", result.Pattern),
		Severity:    SeverityP3,
		Confidence:  ConfidenceInferred,
	}
}

func BuildAutoDelegatePrompt(topic string, coverageAreas []string) string {
	var prompt strings.Builder
	prompt.WriteString("Investigate the following topic and return concrete evidence.\n")
	prompt.WriteString(fmt.Sprintf("Topic: %s\n", topic))
	if len(coverageAreas) > 0 {
		prompt.WriteString(fmt.Sprintf("Coverage areas: %s\n", strings.Join(coverageAreas, ", ")))
	}
	prompt.WriteString("Return concise findings with concrete evidence and a short conclusion.")
	return prompt.String()
}

func DelegateFindingFromOutput(cli, topic, content string, coverageAreas []string) FindingInput {
	description := strings.TrimSpace(content)
	if description == "" {
		description = fmt.Sprintf("Delegate %s completed investigation for %s", cli, topic)
	}
	if runes := []rune(description); len(runes) > 240 {
		description = string(runes[:240]) + "..."
	}

	coverageArea := ""
	if len(coverageAreas) > 0 {
		coverageArea = coverageAreas[0]
	}

	return FindingInput{
		Description:  description,
		Source:       fmt.Sprintf("delegate:%s", cli),
		Severity:     SeverityP2,
		Confidence:   ConfidenceVerified,
		CoverageArea: coverageArea,
	}
}
