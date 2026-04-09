package patterns

import (
	"fmt"
	"strings"

	think "github.com/thebtf/aimux/pkg/think"
)

// biasCatalogs maps bias names to their trigger phrases.
var biasCatalogs = map[string][]string{
	"confirmation_bias":      {"proves my point", "as expected", "confirms", "validates my", "exactly what i thought", "as i predicted"},
	"anchoring":              {"initial estimate", "first impression", "starting point", "original assumption", "based on the first"},
	"sunk_cost":              {"already invested", "too far to stop", "can't waste", "put too much into", "come this far"},
	"availability_heuristic": {"recent example", "just saw", "heard about", "in the news", "happened recently"},
	"bandwagon":              {"everyone thinks", "popular opinion", "most people", "consensus is", "generally accepted"},
	"dunning_kruger":         {"i know everything", "simple enough", "how hard can", "obviously", "anyone can"},
	"survivorship_bias":      {"successful examples", "winners show", "look at those who made it", "they all did"},
	"framing_effect":         {"depends on how", "way you look at", "perspective changes", "if you frame"},
}

type criticalThinkingPattern struct{}

// NewCriticalThinkingPattern returns the "critical_thinking" pattern handler.
func NewCriticalThinkingPattern() think.PatternHandler { return &criticalThinkingPattern{} }

func (p *criticalThinkingPattern) Name() string { return "critical_thinking" }

func (p *criticalThinkingPattern) Description() string {
	return "Scan text for cognitive biases using trigger-phrase detection"
}

func (p *criticalThinkingPattern) Validate(input map[string]any) (map[string]any, error) {
	issue, ok := input["issue"]
	if !ok {
		return nil, fmt.Errorf("missing required field: issue")
	}
	s, ok := issue.(string)
	if !ok || s == "" {
		return nil, fmt.Errorf("field 'issue' must be a non-empty string")
	}
	return map[string]any{"issue": s}, nil
}

func (p *criticalThinkingPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	issue := validInput["issue"].(string)
	lowered := strings.ToLower(issue)

	var detectedBiases []map[string]any
	for biasName, triggers := range biasCatalogs {
		var matched []string
		for _, trigger := range triggers {
			if strings.Contains(lowered, trigger) {
				matched = append(matched, trigger)
			}
		}
		if len(matched) > 0 {
			detectedBiases = append(detectedBiases, map[string]any{
				"bias":     biasName,
				"triggers": matched,
			})
		}
	}

	recommendation := "No cognitive biases detected in the text."
	if len(detectedBiases) > 0 {
		recommendation = fmt.Sprintf("Detected %d potential cognitive bias(es). Review flagged phrases for objective reasoning.", len(detectedBiases))
	}

	data := map[string]any{
		"issue":          issue,
		"detectedBiases": detectedBiases,
		"biasCount":      len(detectedBiases),
		"recommendation": recommendation,
		"guidance":       BuildGuidance("critical_thinking", "full", []string{"issue"}),
	}

	// Tier 2A: text analysis
	primaryText := validInput["issue"].(string)
	if analysis := AnalyzeText(primaryText); analysis != nil {
		domain := MatchDomainTemplate(primaryText)
		if domain != nil {
			analysis.Gaps = DetectGaps(analysis.Entities, domain)
		}
		data["textAnalysis"] = analysis
	}

	return think.MakeThinkResult("critical_thinking", data, sessionID, nil, "", []string{"detectedBiases", "biasCount"}), nil
}
