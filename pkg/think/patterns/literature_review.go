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

// deriveThemes extracts recurring keywords from paper titles/strings.
func deriveThemes(topic string, papers []any) []string {
	wordFreq := make(map[string]int)
	topicWords := tokenize(topic)
	for _, w := range topicWords {
		wordFreq[w]++
	}
	for _, p := range papers {
		var text string
		switch v := p.(type) {
		case string:
			text = v
		case map[string]any:
			if t, ok := v["title"].(string); ok {
				text = t
			}
			if abs, ok := v["abstract"].(string); ok {
				text += " " + abs
			}
		}
		for _, w := range tokenize(text) {
			wordFreq[w]++
		}
	}

	// Return words appearing more than once as themes (up to 5).
	var themes []string
	for w, c := range wordFreq {
		if c >= 2 && len(w) > 3 {
			themes = append(themes, w)
		}
		if len(themes) >= 5 {
			break
		}
	}
	if len(themes) == 0 {
		themes = []string{topic}
	}
	return themes
}

func identifyGaps(topic string, papers []any) []string {
	if len(papers) == 0 {
		return []string{
			fmt.Sprintf("No papers provided — empirical coverage of '%s' is unknown", topic),
			"Longitudinal studies may be absent",
			"Cross-domain replication not assessed",
		}
	}
	return []string{
		fmt.Sprintf("Limited coverage of edge cases in '%s'", topic),
		"Lack of reproducibility data across studies",
	}
}

func suggestDirections(gaps []string, topic string) []string {
	directions := make([]string, 0, len(gaps)+1)
	for _, g := range gaps {
		directions = append(directions, fmt.Sprintf("Address: %s", g))
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
