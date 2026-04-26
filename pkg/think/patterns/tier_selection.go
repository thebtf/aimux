package patterns

import "strings"

// SelectTier determines which intelligence tier to use based on input analysis.
//
//   - "basic"    — Tier 1 (<5ms): explicit override only.
//   - "analysis" — Tier 2 (<10ms): low or medium complexity, or high without sampling.
//   - "deep"     — Tier 3 (1-5s): high/epic complexity with sampling available.
//
// Priority:
//  1. If explicitDepth is set ("basic", "analysis", "deep") → use it directly.
//  2. Estimate text complexity via sentence count.
//  3. Apply selection rules:
//     - complexity=="low"|"medium"                    → "analysis"
//     - complexity=="high"|"epic" AND hasSampling     → "deep"
//     - complexity=="high"|"epic" AND !hasSampling    → "analysis"
func SelectTier(text string, hasSampling bool, explicitDepth string) string {
	// Rule 1: explicit override always wins.
	switch explicitDepth {
	case "basic", "analysis", "deep":
		return explicitDepth
	}

	// Rule 2: estimate complexity from the input text.
	complexity := estimateComplexity(text)

	// Rule 3: tier selection.
	switch complexity {
	case "high", "epic":
		if hasSampling {
			return "deep"
		}
		return "analysis"
	default: // "low" or "medium"
		return "analysis"
	}
}

// estimateComplexity estimates text complexity by sentence count.
// Returns "low", "medium", "high", or "epic".
func estimateComplexity(text string) string {
	sentences := countSentences(text)
	switch {
	case sentences >= 10:
		return "epic"
	case sentences >= 5:
		return "high"
	case sentences >= 2:
		return "medium"
	default:
		return "low"
	}
}

// countSentences counts the number of sentences in text by splitting on sentence-ending punctuation.
func countSentences(text string) int {
	count := 0
	for _, r := range text {
		if r == '.' || r == '!' || r == '?' {
			count++
		}
	}
	// Treat text with no sentence-ending punctuation as one sentence.
	if count == 0 && strings.TrimSpace(text) != "" {
		return 1
	}
	return count
}
