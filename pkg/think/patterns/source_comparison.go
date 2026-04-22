package patterns

import (
	"encoding/json"
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

type sourceComparisonPattern struct{}

// NewSourceComparisonPattern returns the "source_comparison" pattern handler.
func NewSourceComparisonPattern() think.PatternHandler { return &sourceComparisonPattern{} }

func (p *sourceComparisonPattern) Name() string { return "source_comparison" }

func (p *sourceComparisonPattern) Description() string {
	return "Compare multiple sources on a topic — agreements, disagreements, confidence matrix"
}

func (p *sourceComparisonPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"topic": {Type: "string", Required: true, Description: "The topic being compared across sources"},
		"sources": {
			Type:        "array",
			Required:    true,
			Description: "At least 2 sources (strings or objects with name/claim)",
			Items: map[string]any{
				"oneOf": []map[string]any{
					{"type": "string"},
					{
						"type": "object",
						"properties": map[string]any{
							"name":  map[string]any{"type": "string"},
							"claim": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
	}
}

func (p *sourceComparisonPattern) Category() string { return "solo" }

func (p *sourceComparisonPattern) Validate(input map[string]any) (map[string]any, error) {
	// Parse JSON string params from MCP schema
	if s, ok := input["sources"].(string); ok && s != "" {
		var parsed []any
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return nil, fmt.Errorf("sources: invalid JSON: %w", err)
		}
		input["sources"] = parsed
	}

	topicRaw, ok := input["topic"]
	if !ok {
		return nil, fmt.Errorf("missing required field: topic")
	}
	topic, ok := topicRaw.(string)
	if !ok || topic == "" {
		return nil, fmt.Errorf("field 'topic' must be a non-empty string")
	}

	sourcesRaw, ok := input["sources"]
	if !ok {
		return nil, fmt.Errorf("missing required field: sources")
	}
	sources, ok := sourcesRaw.([]any)
	if !ok || len(sources) < 2 {
		return nil, fmt.Errorf("field 'sources' must be a list with at least 2 items")
	}

	// Normalize each source into a map with at least a name field.
	normalized := make([]any, 0, len(sources))
	for i, s := range sources {
		switch v := s.(type) {
		case string:
			if v == "" {
				return nil, fmt.Errorf("sources[%d] must be a non-empty string or map", i)
			}
			normalized = append(normalized, map[string]any{"name": v, "claim": ""})
		case map[string]any:
			name, _ := v["name"].(string)
			if name == "" {
				return nil, fmt.Errorf("sources[%d] map must have a non-empty 'name' field", i)
			}
			claim, _ := v["claim"].(string)
			normalized = append(normalized, map[string]any{"name": name, "claim": claim})
		default:
			return nil, fmt.Errorf("sources[%d] must be a string or map", i)
		}
	}

	return map[string]any{
		"topic":   topic,
		"sources": normalized,
	}, nil
}

func (p *sourceComparisonPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	topic := validInput["topic"].(string)
	sources := validInput["sources"].([]any)

	// Build pairwise comparison matrix.
	matrix := make([]map[string]any, 0)
	agreements := 0
	total := 0

	for i := 0; i < len(sources); i++ {
		for j := i + 1; j < len(sources); j++ {
			sa := sources[i].(map[string]any)
			sb := sources[j].(map[string]any)
			claimA, _ := sa["claim"].(string)
			claimB, _ := sb["claim"].(string)

			agreement := "uncertain"
			if claimA != "" && claimB != "" {
				if claimA == claimB {
					agreement = "agree"
					agreements++
				} else {
					agreement = "disagree"
				}
			}
			total++

			matrix = append(matrix, map[string]any{
				"source_a":  sa["name"],
				"source_b":  sb["name"],
				"agreement": agreement,
			})
		}
	}

	overallConsensus := 0.0
	if total > 0 {
		overallConsensus = float64(agreements) / float64(total) * 100.0
	}

	data := map[string]any{
		"topic":            topic,
		"sourceCount":      len(sources),
		"comparisonMatrix": matrix,
		"overallConsensus": overallConsensus,
		"guidance":         BuildGuidance("source_comparison", "full", []string{"topic", "sources"}),
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

	return think.MakeThinkResult("source_comparison", data, sessionID, nil, "", nil), nil
}
