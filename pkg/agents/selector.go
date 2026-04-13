package agents

import (
	"sort"
	"strings"
)

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
	// Verbs and qualifiers that cause false-positive prefix matches against agent names.
	"respond": true, "exactly": true, "nothing": true, "else": true,
	"just": true, "only": true, "about": true, "like": true,
	"then": true, "than": true, "more": true, "each": true,
	"every": true, "still": true, "after": true, "before": true,
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
// ContentPrefix (first 200 runes) is pre-computed at registration to avoid
// per-call []rune allocation.
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
	if tokenContains(strings.ToLower(a.ContentPrefix), keyword) {
		score += 1
	}
	return score
}

// minScoreThreshold is the minimum total score an agent must reach to beat the
// fallback. A threshold of 3 requires at least one name-level match (worth 3
// points), preventing weak content/role-only overlaps (score 1-2) from winning
// with 154+ registered agents where accidental prefix collisions are common.
const minScoreThreshold = 3

// AutoSelectAgent picks the best agent for a prompt using keyword scoring.
// An agent must reach minScoreThreshold to beat the fallback; scores below
// that indicate accidental keyword overlap rather than genuine intent.
// The second return value is the score of the selected agent (0 for fallback).
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

	if best.agent == nil || best.score < minScoreThreshold {
		return fallbackLocked(registry), 0
	}
	return best.agent, best.score
}

// AgentCandidate is a compact agent summary for LLM-driven selection.
type AgentCandidate struct {
	Name string `json:"name"`
	When string `json:"when"`
	Role string `json:"role,omitempty"`
}

// ListCandidates returns a compact list of agents for the calling LLM to choose from.
// Each entry has name + a "when to use" summary derived from the description.
// Sorted alphabetically. Limited to maxResults entries.
func ListCandidates(registry *Registry, maxResults int) []AgentCandidate {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	candidates := make([]AgentCandidate, 0, len(registry.agents))
	for _, a := range registry.agents {
		when := a.Description
		if when == "" {
			when = a.Domain
		}
		// Truncate long descriptions to keep response compact
		if len(when) > 120 {
			when = when[:117] + "..."
		}
		candidates = append(candidates, AgentCandidate{
			Name: a.Name,
			When: when,
			Role: a.Role,
		})
	}

	// Sort by name for stable output
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Name < candidates[j].Name
	})

	if maxResults > 0 && len(candidates) > maxResults {
		candidates = candidates[:maxResults]
	}
	return candidates
}

// fallbackLocked returns the best available default agent.
// Prefers "general" (broad-purpose, no domain bias) over "implementer".
// Caller MUST hold registry.mu.RLock.
func fallbackLocked(registry *Registry) *Agent {
	if a, ok := registry.agents["general"]; ok {
		return a
	}
	return registry.agents["implementer"]
}
