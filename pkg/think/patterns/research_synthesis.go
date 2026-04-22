package patterns

import (
	"encoding/json"
	"fmt"
	"strings"

	think "github.com/thebtf/aimux/pkg/think"
)

type researchSynthesisPattern struct{}

// NewResearchSynthesisPattern returns the "research_synthesis" pattern handler.
func NewResearchSynthesisPattern() think.PatternHandler { return &researchSynthesisPattern{} }

func (p *researchSynthesisPattern) Name() string { return "research_synthesis" }

func (p *researchSynthesisPattern) Description() string {
	return "Synthesize research findings into structured claims with evidence and confidence"
}

func (p *researchSynthesisPattern) Validate(input map[string]any) (map[string]any, error) {
	// Parse JSON string params from MCP schema
	if s, ok := input["findings"].(string); ok && s != "" {
		var parsed []any
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return nil, fmt.Errorf("findings: invalid JSON: %w", err)
		}
		input["findings"] = parsed
	}

	topicRaw, ok := input["topic"]
	if !ok {
		return nil, fmt.Errorf("missing required field: topic")
	}
	topic, ok := topicRaw.(string)
	if !ok || topic == "" {
		return nil, fmt.Errorf("field 'topic' must be a non-empty string")
	}

	findingsRaw, ok := input["findings"]
	if !ok {
		return nil, fmt.Errorf("missing required field: findings")
	}
	findings, ok := findingsRaw.([]any)
	if !ok || len(findings) == 0 {
		return nil, fmt.Errorf("field 'findings' must be a non-empty list")
	}
	for i, f := range findings {
		if _, ok := f.(string); !ok {
			return nil, fmt.Errorf("findings[%d] must be a string", i)
		}
	}

	return map[string]any{
		"topic":    topic,
		"findings": findings,
	}, nil
}

func (p *researchSynthesisPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"topic":    {Type: "string", Required: true, Description: "The research topic to synthesize"},
		"findings": {Type: "array", Required: true, Description: "List of research findings (strings)", Items: map[string]any{"type": "string"}},
	}
}

func (p *researchSynthesisPattern) Category() string { return "solo" }

func (p *researchSynthesisPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	topic := validInput["topic"].(string)
	findings := validInput["findings"].([]any)

	// Group findings into themes by shared keyword proximity to topic words.
	groups := groupFindings(topic, findings)

	// Build synthesized claims from groups.
	synthesizedClaims := make([]map[string]any, 0, len(groups))
	for theme, group := range groups {
		confidence := confidenceLevel(len(group))
		synthesizedClaims = append(synthesizedClaims, map[string]any{
			"claim":              fmt.Sprintf("Evidence on theme '%s': %s", theme, strings.Join(toStringSlice(group), "; ")),
			"supportingFindings": len(group),
			"confidenceLevel":    confidence,
		})
	}

	overallConclusion := fmt.Sprintf(
		"Based on %d findings across %d themes, the topic '%s' shows %s evidence coverage.",
		len(findings), len(groups), topic, overallStrength(len(findings)),
	)

	openQuestions := []string{
		fmt.Sprintf("What are the boundary conditions for the findings on '%s'?", topic),
		"Are there contradictory findings not yet reconciled?",
		"Which findings are most robust under replication?",
	}

	data := map[string]any{
		"topic":             topic,
		"findingCount":      len(findings),
		"synthesizedClaims": synthesizedClaims,
		"overallConclusion": overallConclusion,
		"openQuestions":     openQuestions,
		"guidance":          BuildGuidance("research_synthesis", "full", []string{"topic", "findings"}),
	}

	// Tier 2A: text analysis
	primaryText := validInput["topic"].(string)
	if analysis := AnalyzeText(primaryText); analysis != nil {
		domain := MatchDomainTemplate(primaryText)
		if domain != nil {
			analysis.Gaps = DetectGaps(analysis.Entities, domain)
		}
		data["textAnalysis"] = analysis
	}

	return think.MakeThinkResult("research_synthesis", data, sessionID, nil, "", nil), nil
}

// groupFindings naively groups findings by shared leading keyword with the topic.
func groupFindings(topic string, findings []any) map[string][]any {
	topicWords := tokenize(topic)
	groups := make(map[string][]any)

	for _, f := range findings {
		fs, _ := f.(string)
		words := tokenize(fs)
		matched := ""
		for _, w := range words {
			for _, tw := range topicWords {
				if w == tw && len(w) > 3 {
					matched = w
					break
				}
			}
			if matched != "" {
				break
			}
		}
		if matched == "" {
			matched = "general"
		}
		groups[matched] = append(groups[matched], f)
	}
	return groups
}

func confidenceLevel(n int) string {
	switch {
	case n >= 3:
		return "high"
	case n == 2:
		return "medium"
	default:
		return "low"
	}
}

func overallStrength(n int) string {
	switch {
	case n >= 5:
		return "strong"
	case n >= 2:
		return "moderate"
	default:
		return "limited"
	}
}

func toStringSlice(items []any) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
