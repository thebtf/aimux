package review

import (
	"encoding/json"
	"fmt"
	"strings"
)

// gateDecisionResponse is the JSON schema produced by buildReviewPrompt for gate decisions.
type gateDecisionResponse struct {
	Findings []any  `json:"findings"`
	Summary  string `json:"summary"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// ParseGateDecision extracts ALLOW/BLOCK decision from agent JSON output.
// The agent is instructed to embed decision and reason inside the JSON object.
// Unrecognised or invalid output -> fail-open ("allow", reason with parse error).
func ParseGateDecision(content string) (string, string) {
	// Find the JSON object in the content (agent may output preamble before JSON).
	start := strings.Index(content, "{")
	if start < 0 {
		return "allow", "gate output did not contain a JSON object"
	}
	var resp gateDecisionResponse
	if err := json.NewDecoder(strings.NewReader(content[start:])).Decode(&resp); err != nil {
		// Fail-open: malformed JSON should not block the gate.
		return "allow", fmt.Sprintf("gate output parse error: %v", err)
	}
	upper := strings.ToUpper(strings.TrimSpace(resp.Decision))
	switch upper {
	case "ALLOW":
		return "allow", resp.Reason
	case "BLOCK":
		return "block", resp.Reason
	default:
		return "allow", fmt.Sprintf("gate decision field %q not recognised (expected ALLOW or BLOCK)", resp.Decision)
	}
}
