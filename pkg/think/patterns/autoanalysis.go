package patterns

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Guidance provides progressive enrichment instructions in every pattern response.
type Guidance struct {
	CurrentDepth string   `json:"current_depth"` // "basic", "enriched", "full"
	NextLevel    string   `json:"next_level"`    // what providing more data unlocks
	Example      string   `json:"example"`       // copy-pasteable enriched call
	Enrichments  []string `json:"enrichments"`   // available optional fields
}

// stopWords is the set of common English words filtered out by ExtractKeywords.
var stopWords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "is": {}, "are": {}, "was": {}, "were": {},
	"to": {}, "for": {}, "of": {}, "in": {}, "on": {}, "with": {}, "and": {},
	"or": {}, "but": {}, "not": {}, "this": {}, "that": {}, "it": {}, "be": {},
	"as": {}, "at": {}, "by": {}, "from": {}, "has": {}, "have": {}, "had": {},
	"do": {}, "does": {}, "did": {}, "will": {}, "would": {}, "should": {},
	"can": {}, "could": {}, "may": {}, "might": {}, "must": {}, "shall": {},
	"need": {}, "how": {}, "what": {}, "when": {}, "where": {}, "which": {},
	"who": {}, "why": {},
}

// ExtractKeywords extracts lowercase keywords from text, filtering common stop
// words, deduplicating, and returning the result in sorted order.
func ExtractKeywords(text string) []string {
	lower := strings.ToLower(text)

	// Split by whitespace and punctuation.
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return unicode.IsSpace(r) || (unicode.IsPunct(r) && r != '-' && r != '_')
	})

	seen := make(map[string]struct{}, len(words))
	result := make([]string, 0, len(words))

	for _, w := range words {
		if w == "" {
			continue
		}
		if _, isStop := stopWords[w]; isStop {
			continue
		}
		if _, already := seen[w]; already {
			continue
		}
		seen[w] = struct{}{}
		result = append(result, w)
	}

	sort.Strings(result)
	return result
}

// BuildGuidance creates a Guidance struct for pattern responses.
//
// currentDepth must be one of: "basic", "enriched", "full".
// enrichments lists the optional fields the caller can add to get richer output.
func BuildGuidance(patternName, currentDepth string, enrichments []string) Guidance {
	var nextLevel string
	switch currentDepth {
	case "basic":
		nextLevel = fmt.Sprintf("Provide optional fields (%s) to unlock enriched %s analysis with domain-matched templates and keyword extraction", strings.Join(enrichments, ", "), patternName)
	case "enriched":
		nextLevel = fmt.Sprintf("Supply all remaining fields (%s) to unlock full %s output including DAG analysis, entity modeling, and component breakdown", strings.Join(enrichments, ", "), patternName)
	case "full":
		nextLevel = "All enrichment levels reached — output includes complete analysis, DAG, templates, and domain modeling"
	default:
		nextLevel = fmt.Sprintf("Unknown depth %q — use 'basic', 'enriched', or 'full'", currentDepth)
	}

	// Build a copy-pasteable example call showing optional fields.
	var exampleFields string
	if len(enrichments) > 0 {
		parts := make([]string, len(enrichments))
		for i, e := range enrichments {
			parts[i] = fmt.Sprintf(`"%s": "..."`, e)
		}
		exampleFields = ", " + strings.Join(parts, ", ")
	}
	example := fmt.Sprintf(`think(pattern: "%s", input: {<required fields>%s})`, patternName, exampleFields)

	return Guidance{
		CurrentDepth: currentDepth,
		NextLevel:    nextLevel,
		Example:      example,
		Enrichments:  enrichments,
	}
}
