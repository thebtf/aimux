package think

import (
	"strings"

	"github.com/thebtf/aimux/pkg/routing"
)

// Recommendation is the output of PatternAdvisor.Evaluate.
type Recommendation struct {
	Action     string         // "continue" or "switch"
	Target     string         // target pattern name when Action == "switch"
	Reason     string
	StatePatch map[string]any // caller must apply this to session state via UpdateSessionState
}

// advisorStateKey is the session state key used to track advisor history.
const advisorStateKey = "_advisorHistory"

// maxSwitches is the maximum number of pattern switches allowed per session.
const maxSwitches = 3

// convergenceThreshold is the minimum Jaccard similarity of the last 3 results
// to trigger a convergence-based switch recommendation.
const convergenceThreshold = 0.8

// PatternAdvisor evaluates think results and recommends whether to continue
// with the current pattern or switch to a more appropriate one.
type PatternAdvisor struct {
	scorer *routing.BM25Scorer
}

// NewPatternAdvisor returns a PatternAdvisor backed by a BM25Scorer.
func NewPatternAdvisor() *PatternAdvisor {
	return &PatternAdvisor{scorer: routing.NewBM25Scorer()}
}

// Evaluate analyses the latest result in the context of the session and returns
// a Recommendation. It defaults to "continue" unless:
//   - the last 3 result summaries have Jaccard similarity > 0.8 (convergence stall), or
//   - a different pattern scores substantially higher for the result content.
//
// At most maxSwitches switches are allowed per session; once the limit is reached
// the advisor always recommends "continue".
func (a *PatternAdvisor) Evaluate(session *ThinkSession, result *ThinkResult) Recommendation {
	if session == nil || result == nil {
		return Recommendation{Action: "continue", Reason: "no session or result"}
	}

	// --- switch budget check ---
	switchCount := switchCountFromState(session.State)
	if switchCount >= maxSwitches {
		return Recommendation{
			Action: "continue",
			Reason: "maximum pattern switches reached",
		}
	}

	// --- update result history ---
	history := resultHistoryFromState(session.State)
	summary := resultSummary(result)
	history = append(history, summary)
	// Keep only the last 3.
	if len(history) > 3 {
		history = history[len(history)-3:]
	}

	// Build the state patch; the caller is responsible for applying it via UpdateSessionState.
	patch := map[string]any{
		advisorStateKey: history,
	}

	// --- convergence detection ---
	if len(history) >= 3 {
		if jaccardConverged(history, convergenceThreshold) {
			target := a.suggestAlternative(result.Pattern, summary)
			if target != "" && target != result.Pattern {
				return Recommendation{
					Action:     "switch",
					Target:     target,
					Reason:     "last 3 results are too similar (convergence stall); try a different approach",
					StatePatch: patch,
				}
			}
		}
	}

	return Recommendation{Action: "continue", Reason: "current pattern is effective", StatePatch: patch}
}

// RecordSwitch increments the switch counter in the session state.
// Must be called by the caller after acting on a "switch" recommendation.
func RecordSwitch(session *ThinkSession) {
	if session == nil {
		return
	}
	count := switchCountFromState(session.State) + 1
	UpdateSessionState(session.ID, map[string]any{"_advisorSwitches": count})
}

// --- internal helpers ---

// resultSummary extracts a concise text representation of a ThinkResult for
// similarity comparison.
func resultSummary(result *ThinkResult) string {
	parts := []string{result.Pattern}
	if result.Summary != "" {
		parts = append(parts, result.Summary)
	}
	for _, key := range []string{"thought", "conclusion", "recommendation", "decision", "analysis"} {
		if v, ok := result.Data[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, " ")
}

// jaccardConverged returns true if all strings in history have pairwise Jaccard
// similarity above the threshold.
func jaccardConverged(history []string, threshold float64) bool {
	if len(history) < 2 {
		return false
	}
	sets := make([]map[string]bool, len(history))
	for i, s := range history {
		sets[i] = tokenSet(s)
	}
	for i := 0; i < len(sets); i++ {
		for j := i + 1; j < len(sets); j++ {
			if jaccard(sets[i], sets[j]) < threshold {
				return false
			}
		}
	}
	return true
}

// jaccard computes the Jaccard similarity of two token sets.
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	intersection := 0
	for t := range a {
		if b[t] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// tokenSet converts text to a set of lowercase tokens.
func tokenSet(text string) map[string]bool {
	tokens := strings.Fields(strings.ToLower(text))
	set := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		set[t] = true
	}
	return set
}

// suggestAlternative uses BM25 to find the best alternative pattern for the
// given result content. Returns "" if no suitable alternative is found.
func (a *PatternAdvisor) suggestAlternative(currentPattern, content string) string {
	allNames := GetAllPatterns()
	if len(allNames) == 0 {
		return ""
	}

	items := make([]routing.Scorable, 0, len(allNames))
	names := make([]string, 0, len(allNames))
	for _, name := range allNames {
		if name == currentPattern {
			continue
		}
		handler := GetPattern(name)
		if handler == nil {
			continue
		}
		items = append(items, scorableText(handler.Description()))
		names = append(names, name)
	}

	if len(items) == 0 {
		return ""
	}

	ranked := a.scorer.Rank(content, items)
	if len(ranked) == 0 {
		return ""
	}
	return names[ranked[0].Index]
}

// switchCountFromState reads the "_advisorSwitches" counter from state.
func switchCountFromState(state map[string]any) int {
	if state == nil {
		return 0
	}
	v, ok := state["_advisorSwitches"]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

// resultHistoryFromState reads the "_advisorHistory" string slice from state.
func resultHistoryFromState(state map[string]any) []string {
	if state == nil {
		return nil
	}
	v, ok := state[advisorStateKey]
	if !ok {
		return nil
	}
	// Handle []string (stored directly by UpdateSessionState).
	if ss, ok := v.([]string); ok {
		out := make([]string, len(ss))
		copy(out, ss)
		return out
	}
	// Handle []any (e.g. after JSON round-trip or interface boxing).
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// scorableText wraps a string to satisfy routing.Scorable.
type scorableText string

func (s scorableText) ScoreText() string { return string(s) }
