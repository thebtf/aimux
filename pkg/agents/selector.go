package agents

import "strings"

// stopWords are common English words that carry no task-relevant signal.
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true,
	"for": true, "in": true, "on": true, "at": true, "to": true,
	"of": true, "is": true, "it": true, "this": true, "that": true,
	"with": true, "from": true, "by": true, "be": true, "are": true,
	"was": true, "has": true, "have": true, "do": true, "does": true,
	"my": true, "me": true, "i": true, "we": true, "our": true,
	"not": true, "no": true, "can": true, "all": true, "any": true,
	"how": true, "what": true, "when": true, "why": true, "where": true,
	"using": true, "use": true, "please": true, "need": true, "want": true,
	"help": true, "should": true, "would": true, "could": true,
	"make": true, "some": true, "its": true, "into": true, "also": true,
}

// ExtractKeywords returns the first 5 significant words from a prompt,
// skipping stop words and very short tokens. Exported for use in logging.
func ExtractKeywords(prompt string) []string {
	words := strings.Fields(strings.ToLower(prompt))
	var keywords []string
	for _, w := range words {
		// Strip common punctuation
		w = strings.Trim(w, ".,!?;:\"'()")
		if len(w) < 3 {
			continue
		}
		if stopWords[w] {
			continue
		}
		keywords = append(keywords, w)
		if len(keywords) >= 5 {
			break
		}
	}
	return keywords
}

// tokenContains reports whether text contains keyword as an exact word or as a prefix
// of any whitespace-delimited token. This provides lightweight stemming: keyword
// "investigate" matches "investigation", "investigating", "investigates", etc.
func tokenContains(text, keyword string) bool {
	if strings.Contains(text, keyword) {
		return true
	}
	// Prefix match against each token (handles "investigation" ← "investigate")
	for _, tok := range strings.Fields(text) {
		tok = strings.Trim(tok, ".,!?;:\"'()")
		if strings.HasPrefix(tok, keyword) {
			return true
		}
	}
	return false
}

// scoreMatch returns how well an agent matches a single keyword.
// Scoring: name match = 3, domain match = 2, role match = 1, content match = 1.
// Uses prefix-aware matching so "investigate" matches "investigation", etc.
func scoreMatch(a *Agent, keyword string) int {
	score := 0
	if tokenContains(strings.ToLower(a.Name), keyword) {
		score += 3
	}
	if tokenContains(strings.ToLower(a.Domain), keyword) {
		score += 2
	}
	if tokenContains(strings.ToLower(a.Role), keyword) {
		score += 1
	}
	// Search content prefix (first 200 runes) — same limit as Find().
	contentPrefix := a.Content
	runes := []rune(contentPrefix)
	if len(runes) > 200 {
		contentPrefix = string(runes[:200])
	}
	if tokenContains(strings.ToLower(contentPrefix), keyword) {
		score += 1
	}
	return score
}

// AutoSelectAgent picks the best agent for a prompt using keyword scoring.
// If no agent scores above zero, it falls back to the "implementer" built-in.
// The second return value is the score of the selected agent.
func AutoSelectAgent(registry *Registry, prompt string) (*Agent, int) {
	keywords := ExtractKeywords(prompt)

	registry.mu.RLock()
	defer registry.mu.RUnlock()

	if len(keywords) == 0 {
		return fallbackLocked(registry), 0
	}

	type candidate struct {
		agent *Agent
		score int
	}

	var best candidate
	for _, a := range registry.agents {
		total := 0
		for _, kw := range keywords {
			total += scoreMatch(a, kw)
		}
		if total > best.score {
			best = candidate{agent: a, score: total}
		}
	}

	if best.agent == nil || best.score == 0 {
		return fallbackLocked(registry), 0
	}
	return best.agent, best.score
}

// fallbackLocked returns the "implementer" built-in agent.
// Caller MUST hold registry.mu.RLock.
func fallbackLocked(registry *Registry) *Agent {
	return registry.agents["implementer"]
}
