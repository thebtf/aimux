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

	// Contradiction detection: finding pairs with low Jaccard similarity that share a topic keyword.
	topicKW := make(map[string]bool)
	for _, w := range tokenize(topic) {
		if len(w) > 3 {
			topicKW[w] = true
		}
	}
	var contradictoryPairs []map[string]any
	findingStrings := toStringSlice(findings)
	for i := 0; i < len(findingStrings); i++ {
		for j := i + 1; j < len(findingStrings); j++ {
			sim := jaccardSimilarity(findingStrings[i], findingStrings[j])
			if sim < 0.15 {
				// Check for shared topic keyword.
				wordsI := tokenize(findingStrings[i])
				hasShared := false
				for _, w := range wordsI {
					if topicKW[w] {
						hasShared = true
						break
					}
				}
				if hasShared {
					contradictoryPairs = append(contradictoryPairs, map[string]any{
						"findingA":        findingStrings[i],
						"findingB":        findingStrings[j],
						"similarity":      sim,
					})
				}
			}
		}
	}
	if contradictoryPairs == nil {
		contradictoryPairs = []map[string]any{}
	}

	// themeOverlap: for findings assigned to "general", list their keywords that didn't match.
	themeOverlap := make(map[string][]string)
	if generalFindings, ok := groups["general"]; ok {
		for _, f := range generalFindings {
			fs, _ := f.(string)
			words := tokenize(fs)
			var unmatched []string
			for _, w := range words {
				if len(w) > 3 {
					matchesTopic := false
					for _, tw := range tokenize(topic) {
						if w == tw {
							matchesTopic = true
							break
						}
					}
					if !matchesTopic {
						unmatched = append(unmatched, w)
					}
				}
			}
			themeOverlap[fs] = unmatched
		}
	}

	data := map[string]any{
		"topic":              topic,
		"findingCount":       len(findings),
		"synthesizedClaims":  synthesizedClaims,
		"overallConclusion":  overallConclusion,
		"openQuestions":      openQuestions,
		"contradictoryPairs": contradictoryPairs,
		"themeOverlap":       themeOverlap,
		"guidance":           BuildGuidance("research_synthesis", "full", []string{"topic", "findings"}),
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
