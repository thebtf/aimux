package patterns

// SelectTier determines which intelligence tier to use based on input analysis.
//
//   - "basic"    — Tier 1 (<5ms): instant template match, known domain with low complexity.
//   - "analysis" — Tier 2 (<10ms): text analysis required; medium complexity or unknown domain.
//   - "deep"     — Tier 3 (1-5s): sampling/LLM required; high/epic complexity with sampling available.
//
// Priority:
//  1. If explicitDepth is set ("basic", "analysis", "deep") → use it directly.
//  2. Estimate text complexity via estimateComplexity (from textanalysis.go).
//  3. Check domain via MatchDomainTemplate.
//  4. Apply selection rules:
//     - complexity=="low"  AND domain matched  → "basic"
//     - complexity=="low"  AND no domain       → "analysis"
//     - complexity=="medium"                   → "analysis"
//     - complexity=="high"|"epic" AND sampling → "deep"
//     - complexity=="high"|"epic" AND !sampling → "analysis"
func SelectTier(text string, hasSampling bool, explicitDepth string) string {
	// Rule 1: explicit override always wins.
	switch explicitDepth {
	case "basic", "analysis", "deep":
		return explicitDepth
	}

	// Rule 2: estimate complexity from the input text.
	complexity := estimateComplexity(text)

	// Rule 3: check whether a domain template matches.
	domain := MatchDomainTemplate(text)

	// Rule 4: tier selection matrix.
	switch complexity {
	case "low":
		if domain != nil {
			return "basic"
		}
		return "analysis"
	case "medium":
		return "analysis"
	default: // "high" or "epic"
		if hasSampling {
			return "deep"
		}
		return "analysis"
	}
}
