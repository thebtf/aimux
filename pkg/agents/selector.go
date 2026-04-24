package agents

import (
	"sort"
	"strings"

	"github.com/thebtf/aimux/pkg/routing"
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
//
// When prompt is non-empty the list is ranked by BM25 relevance score descending
// (with feedback adjustment when history is provided), so the most relevant
// agents appear first even if maxResults would otherwise hide them.
// When prompt is empty the list is sorted alphabetically for stable output.
// Limited to maxResults entries (0 = no limit).
func ListCandidates(registry *Registry, prompt string, maxResults int) []AgentCandidate {
	return listCandidatesWithFeedback(registry, prompt, maxResults, nil)
}

// listCandidatesWithFeedback is the internal implementation that accepts optional feedback.
func listCandidatesWithFeedback(registry *Registry, prompt string, maxResults int, feedback *FeedbackTracker) []AgentCandidate {
	// Use Registry.List() so stale filesystem-backed agents are purged at read
	// time (consistent with Get/Find behaviour). Without this, ListCandidates
	// would surface ghost entries whose source files were deleted after startup.
	liveAgents := registry.List()

	if prompt == "" {
		// Alphabetical sort — no scoring needed.
		candidates := make([]AgentCandidate, 0, len(liveAgents))
		for _, a := range liveAgents {
			candidates = append(candidates, AgentCandidate{
				Name: a.Name,
				When: agentWhenText(a),
				Role: a.Role,
			})
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Name < candidates[j].Name
		})
		if maxResults > 0 && len(candidates) > maxResults {
			candidates = candidates[:maxResults]
		}
		return candidates
	}

	// BM25 ranking via pkg/routing.
	scorer := routing.NewBM25Scorer()
	scorables := make([]routing.Scorable, len(liveAgents))
	for i, a := range liveAgents {
		scorables[i] = agentScoreText{a: a}
	}
	ranked := scorer.Rank(prompt, scorables)

	// Build a score map: agent index → BM25 score.
	bm25Score := make(map[int]float64, len(ranked))
	for _, r := range ranked {
		bm25Score[r.Index] = r.Score
	}

	type scored struct {
		AgentCandidate
		score float64
	}

	all := make([]scored, len(liveAgents))
	for i, a := range liveAgents {
		base := bm25Score[i] // 0.0 if not in ranked results
		adj := base
		if feedback != nil {
			adj = feedback.AdjustScore(base, a.Name, inferTaskCategory(prompt))
		}
		all[i] = scored{
			AgentCandidate: AgentCandidate{
				Name: a.Name,
				When: agentWhenText(a),
				Role: a.Role,
			},
			score: adj,
		}
	}

	// Sort by adjusted score descending; tiebreak alphabetically.
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].Name < all[j].Name
	})

	if maxResults > 0 && len(all) > maxResults {
		all = all[:maxResults]
	}

	candidates := make([]AgentCandidate, len(all))
	for i, s := range all {
		candidates[i] = s.AgentCandidate
	}
	return candidates
}

// SelectionRationale holds the scoring details for a SemanticSelect decision.
type SelectionRationale struct {
	AgentName     string  `json:"agent_name"`
	SemanticScore float64 `json:"semantic_score"`
	SuccessRate   float64 `json:"success_rate"`
	AdjustedScore float64 `json:"adjusted_score"`
	Reason        string  `json:"reason"`
}

// SemanticSelect picks the best agent for a prompt using BM25 scoring with
// feedback-adjusted scores. Returns the selected agent and a rationale struct
// with scoring details. Falls back to the generic/implementer agent when no
// agent scores above 0 after adjustment.
func SemanticSelect(registry *Registry, prompt string, history *DispatchHistory) (*Agent, SelectionRationale) {
	liveAgents := registry.List()
	if len(liveAgents) == 0 {
		fb := fallbackAgent(registry)
		return fb, SelectionRationale{
			AgentName: agentName(fb),
			Reason:    "no agents registered",
		}
	}

	scorer := routing.NewBM25Scorer()
	scorables := make([]routing.Scorable, len(liveAgents))
	for i, a := range liveAgents {
		scorables[i] = agentScoreText{a: a}
	}

	ranked := scorer.Rank(prompt, scorables)

	taskCategory := inferTaskCategory(prompt)

	type candidate struct {
		agent         *Agent
		semanticScore float64
		successRate   float64
		adjustedScore float64
	}

	var best candidate
	for _, r := range ranked {
		a := liveAgents[r.Index]
		var successRate float64
		if history != nil {
			successRate = history.GetSuccessRate(a.Name, taskCategory)
		} else {
			successRate = 0.5 // neutral prior
		}
		adjusted := 0.7*r.Score + 0.3*successRate
		if adjusted < 0.1 {
			adjusted = 0.1
		}
		if adjusted > best.adjustedScore {
			best = candidate{
				agent:         a,
				semanticScore: r.Score,
				successRate:   successRate,
				adjustedScore: adjusted,
			}
		}
	}

	if best.agent == nil {
		// No BM25 matches — fall back to generic agent.
		fb := fallbackAgent(registry)
		return fb, SelectionRationale{
			AgentName:     agentName(fb),
			SemanticScore: 0,
			SuccessRate:   0.5,
			AdjustedScore: 0.1,
			Reason:        "no BM25 matches; using fallback agent",
		}
	}

	reason := "BM25 semantic match"
	if history != nil {
		reason = "BM25 semantic match with feedback adjustment"
	}

	return best.agent, SelectionRationale{
		AgentName:     best.agent.Name,
		SemanticScore: best.semanticScore,
		SuccessRate:   best.successRate,
		AdjustedScore: best.adjustedScore,
		Reason:        reason,
	}
}

// --- helpers ---

// agentScoreText wraps *Agent to implement routing.Scorable.
type agentScoreText struct {
	a *Agent
}

func (s agentScoreText) ScoreText() string {
	// Compose a document from the agent's most descriptive fields.
	// Name carries the highest signal; When/Description provide topical depth.
	parts := []string{s.a.Name, s.a.Name} // double name weight
	if s.a.When != "" {
		parts = append(parts, s.a.When)
	}
	if s.a.Description != "" {
		parts = append(parts, s.a.Description)
	}
	if s.a.Domain != "" {
		parts = append(parts, s.a.Domain)
	}
	if s.a.Role != "" {
		parts = append(parts, s.a.Role)
	}
	if s.a.ContentPrefix != "" {
		parts = append(parts, s.a.ContentPrefix)
	}
	return strings.Join(parts, " ")
}

// agentWhenText returns the best available "when to use" text for an agent,
// truncated to 120 runes for compact candidate listings.
func agentWhenText(a *Agent) string {
	when := a.When
	if when == "" {
		when = a.Description
	}
	if when == "" {
		when = a.Domain
	}
	runes := []rune(when)
	if len(runes) > 120 {
		when = string(runes[:117]) + "..."
	}
	return when
}

// inferTaskCategory returns a short category label inferred from the prompt keywords.
// Used to namespace dispatch history records so feedback is task-type-specific.
func inferTaskCategory(prompt string) string {
	lower := strings.ToLower(prompt)
	switch {
	case containsAny(lower, "review", "audit", "check"):
		return "review"
	case containsAny(lower, "debug", "fix", "crash", "error", "bug"):
		return "debugging"
	case containsAny(lower, "research", "investigate", "analyze", "analyse"):
		return "research"
	case containsAny(lower, "implement", "build", "create", "write", "add"):
		return "coding"
	case containsAny(lower, "test", "spec", "tdd"):
		return "testing"
	case containsAny(lower, "refactor", "clean", "simplify"):
		return "refactor"
	default:
		return "general"
	}
}

// containsAny reports whether s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// fallbackAgent returns the best available fallback agent (generic → implementer → nil).
func fallbackAgent(registry *Registry) *Agent {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return fallbackLocked(registry)
}

// agentName returns the agent name or "<nil>" for safe nil handling.
func agentName(a *Agent) string {
	if a == nil {
		return "<nil>"
	}
	return a.Name
}

// fallbackLocked returns the best available default agent.
// Prefers "generic" (literal instruction follower) over "implementer".
// Caller MUST hold registry.mu.RLock.
func fallbackLocked(registry *Registry) *Agent {
	if a, ok := registry.agents["generic"]; ok {
		return a
	}
	return registry.agents["implementer"]
}

// compile-time assertion that agentScoreText satisfies routing.Scorable.
var _ routing.Scorable = agentScoreText{}
