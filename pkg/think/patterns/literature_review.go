package patterns

import (
	"fmt"
	"strings"

	think "github.com/thebtf/aimux/pkg/think"
)

type literatureReviewPattern struct{}

// NewLiteratureReviewPattern returns the "literature_review" pattern handler.
func NewLiteratureReviewPattern() think.PatternHandler { return &literatureReviewPattern{} }

func (p *literatureReviewPattern) Name() string { return "literature_review" }

func (p *literatureReviewPattern) Description() string {
	return "Systematic literature review — identify themes, gaps, and research directions"
}

func (p *literatureReviewPattern) Validate(input map[string]any) (map[string]any, error) {
	topicRaw, ok := input["topic"]
	if !ok {
		return nil, fmt.Errorf("missing required field: topic")
	}
	topic, ok := topicRaw.(string)
	if !ok || topic == "" {
		return nil, fmt.Errorf("field 'topic' must be a non-empty string")
	}

	out := map[string]any{"topic": topic}

	if v, ok := input["papers"].([]any); ok {
		out["papers"] = v
	}
	if v, ok := input["criteria"].([]any); ok {
		out["criteria"] = v
	}
	if v, ok := input["timeFrame"].(string); ok && v != "" {
		out["timeFrame"] = v
	}

	return out, nil
}

func (p *literatureReviewPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"topic": {Type: "string", Required: true, Description: "The research topic to review"},
		"papers": {
			Type:        "array",
			Required:    false,
			Description: "List of papers (strings or objects with title/abstract)",
			Items: map[string]any{
				"oneOf": []map[string]any{
					{"type": "string"},
					{
						"type": "object",
						"properties": map[string]any{
							"title":    map[string]any{"type": "string"},
							"abstract": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
		"criteria":  {Type: "array", Required: false, Description: "Inclusion/exclusion criteria", Items: map[string]any{"type": "string"}},
		"timeFrame": {Type: "string", Required: false, Description: "Time frame for the review"},
	}
}

func (p *literatureReviewPattern) Category() string { return "solo" }

func (p *literatureReviewPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	topic := validInput["topic"].(string)

	papers, _ := validInput["papers"].([]any)
	paperCount := len(papers)

	// Derive themes from paper titles/content by extracting keywords.
	themes := deriveThemes(topic, papers)

	// Identify gaps: concepts mentioned in topic but absent from papers.
	identifiedGaps := identifyGaps(topic, papers)

	// Suggest directions based on gaps.
	suggestedDirections := suggestDirections(identifiedGaps, topic)

	data := map[string]any{
		"topic":               topic,
		"paperCount":          paperCount,
		"themes":              themes,
		"identifiedGaps":      identifiedGaps,
		"suggestedDirections": suggestedDirections,
		"guidance":            BuildGuidance("literature_review", "full", []string{"papers", "criteria", "timeFrame"}),
	}

	return think.MakeThinkResult("literature_review", data, sessionID, nil, "source_comparison", nil), nil
}

// deriveThemes extracts bigrams from paper titles/abstracts using NgramExtract.
func deriveThemes(topic string, papers []any) []string {
	// Collect all text from papers.
	var allText strings.Builder
	allText.WriteString(topic)
	allText.WriteString(" ")
	for _, p := range papers {
		switch v := p.(type) {
		case string:
			allText.WriteString(v)
			allText.WriteString(" ")
		case map[string]any:
			if t, ok := v["title"].(string); ok {
				allText.WriteString(t)
				allText.WriteString(" ")
			}
			if abs, ok := v["abstract"].(string); ok {
				allText.WriteString(abs)
				allText.WriteString(" ")
			}
		}
	}

	// Extract bigrams as primary themes.
	bigrams := NgramExtract(allText.String(), 2, 5)
	if len(bigrams) >= 2 {
		return bigrams
	}

	// Fallback to single-word frequency when <2 bigrams found.
	words := NgramExtract(allText.String(), 1, 5)
	if len(words) > 0 {
		return words
	}
	return []string{topic}
}

func identifyGaps(topic string, papers []any) []string {
	if len(papers) == 0 {
		return []string{
			fmt.Sprintf("No papers provided — empirical coverage of '%s' is unknown", topic),
			"Longitudinal studies may be absent",
			"Cross-domain replication not assessed",
		}
	}

	// Tokenize topic into keywords.
	topicKeywords := make(map[string]bool)
	for _, w := range tokenize(topic) {
		if len(w) > 3 {
			topicKeywords[w] = true
		}
	}

	// Collect all paper text.
	var paperText strings.Builder
	for _, p := range papers {
		switch v := p.(type) {
		case string:
			paperText.WriteString(strings.ToLower(v))
			paperText.WriteString(" ")
		case map[string]any:
			if t, ok := v["title"].(string); ok {
				paperText.WriteString(strings.ToLower(t))
				paperText.WriteString(" ")
			}
			if abs, ok := v["abstract"].(string); ok {
				paperText.WriteString(strings.ToLower(abs))
				paperText.WriteString(" ")
			}
		}
	}
	paperContent := paperText.String()

	// Find topic keywords absent from paper content.
	var gaps []string
	for kw := range topicKeywords {
		if !strings.Contains(paperContent, kw) {
			gaps = append(gaps, fmt.Sprintf("Topic term '%s' not covered by any paper", kw))
		}
	}

	// Always add structural gap observations.
	if len(papers) < 5 {
		gaps = append(gaps, fmt.Sprintf("Limited sample size (%d papers) — may miss important perspectives", len(papers)))
	}
	gaps = append(gaps, "Lack of reproducibility data across studies")

	return gaps
}

func suggestDirections(gaps []string, topic string) []string {
	themes := NgramExtract(topic, 2, 3)
	directions := make([]string, 0, len(gaps)+1)
	for _, g := range gaps {
		if len(themes) > 0 {
			directions = append(directions, fmt.Sprintf("Investigate: %s (relevant to '%s')", g, themes[0]))
		} else {
			directions = append(directions, fmt.Sprintf("Address: %s", g))
		}
	}
	directions = append(directions, fmt.Sprintf("Meta-analysis of existing '%s' studies", topic))
	return directions
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !('a' <= r && r <= 'z')
	})
	return fields
}
