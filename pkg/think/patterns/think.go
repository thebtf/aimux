package patterns

import (
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

const autoRouteConfidenceThreshold = 0.7

type thinkPattern struct{}

// NewThinkPattern returns the base "think" pattern handler.
func NewThinkPattern() think.PatternHandler { return &thinkPattern{} }

func (p *thinkPattern) Name() string { return "think" }

func (p *thinkPattern) Description() string {
	return "Base thinking pattern — records and reflects on a thought"
}

func (p *thinkPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"thought": {Type: "string", Required: true, Description: "The thought to record and reflect on"},
	}
}

func (p *thinkPattern) Category() string { return "solo" }

func (p *thinkPattern) Validate(input map[string]any) (map[string]any, error) {
	thought, ok := input["thought"]
	if !ok {
		return nil, fmt.Errorf("missing required field: thought")
	}
	s, ok := thought.(string)
	if !ok || s == "" {
		return nil, fmt.Errorf("field 'thought' must be a non-empty string")
	}
	return map[string]any{"thought": s}, nil
}

func (p *thinkPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	thought := validInput["thought"].(string)

	keywords := ExtractKeywords(thought)
	ranked := rankPatterns(keywords)

	var suggestedPattern string
	var confidence float64
	if len(ranked) == 0 {
		suggestedPattern = "sequential_thinking"
		confidence = 0.0
	} else {
		suggestedPattern = ranked[0].pattern
		confidence = ranked[0].score
	}

	// Build top-3 alternative patterns (may include the winner).
	alts := make([]string, 0, 3)
	for i := 0; i < len(ranked) && i < 3; i++ {
		alts = append(alts, ranked[i].pattern)
	}

	// Confidence level label.
	var confidenceLevel string
	switch {
	case confidence >= 0.3:
		confidenceLevel = "high"
	case confidence >= 0.15:
		confidenceLevel = "medium"
	default:
		confidenceLevel = "low"
	}

	// Auto-route: if confidence is high enough and the pattern is not the fallback,
	// execute the target pattern directly and annotate the result.
	if confidence >= autoRouteConfidenceThreshold && suggestedPattern != "sequential_thinking" {
		if result := tryAutoRoute(thought, suggestedPattern, sessionID); result != nil {
			return result, nil
		}
	}

	data := map[string]any{
		"thought":             thought,
		"thoughtLength":       len(thought),
		"keywords":            keywords,
		"suggestedPattern":    suggestedPattern,
		"alternativePatterns": alts,
		"confidenceLevel":     confidenceLevel,
		"guidance":            BuildGuidance("think", "basic", []string{"thought"}),
	}

	return think.MakeThinkResult("think", data, sessionID, nil, suggestedPattern, nil), nil
}

// tryAutoRoute attempts to execute targetPattern with the thought as the primary input.
// It returns nil if the target pattern cannot accept the thought field (missing required
// fields other than "thought"), allowing Handle to fall back to normal think behavior.
// Safety: performs at most one level of routing — the delegated pattern is called directly
// and its result is returned as-is (no recursive auto-routing).
func tryAutoRoute(thought, targetPattern, sessionID string) *think.ThinkResult {
	handler := think.GetPattern(targetPattern)
	if handler == nil {
		return nil
	}

	// Build the minimal input for the target pattern.
	// Use "thought" as the value for all required fields (each pattern
	// uses a different primary key: "issue", "decision", "thought", etc.).
	// Map common aliases so validation succeeds for the most frequent patterns.
	primaryInput := map[string]any{"thought": thought}
	for fieldName, schema := range handler.SchemaFields() {
		if schema.Required && fieldName != "thought" {
			primaryInput[fieldName] = thought
		}
	}

	validated, err := handler.Validate(primaryInput)
	if err != nil {
		// Target pattern requires fields we cannot satisfy from thought alone — fall back.
		return nil
	}

	result, err := handler.Handle(validated, sessionID)
	if err != nil {
		return nil
	}

	// Annotate with auto-routing provenance.
	newData := make(map[string]any, len(result.Data)+2)
	for k, v := range result.Data {
		newData[k] = v
	}
	newData["auto_routed_from"] = "think"
	newData["auto_routed_to"] = targetPattern

	result.Data = newData
	return result
}

// patternScore holds a scored pattern candidate.
type patternScore struct {
	pattern string
	score   float64
}

// rankPatterns scores every category against the keyword set and returns all candidates
// sorted by score descending. Candidates with score == 0 are omitted.
// The caller is responsible for appending the fallback if the result is empty.
func rankPatterns(keywords []string) []patternScore {
	total := len(keywords)
	if total == 0 {
		return nil
	}

	kwSet := make(map[string]bool, total)
	for _, k := range keywords {
		kwSet[k] = true
	}

	type category struct {
		pattern string
		signals []string
	}

	categories := []category{
		// Debug / error signals.
		{"debugging_approach", []string{"bug", "debug", "error", "crash", "fail", "broken", "exception", "panic", "nil", "undefined", "race", "deadlock", "goroutine", "segfault", "stacktrace"}},
		// Architecture / design signals.
		{"architecture_analysis", []string{"design", "architecture", "system", "structure", "service", "layer", "pattern", "module"}},
		// Decision / choice signals.
		{"decision_framework", []string{"choose", "choice", "decide", "decision", "option", "compare", "tradeoff", "versus", "vs", "pick", "select"}},
		// Research / literature signals.
		{"literature_review", []string{"research", "paper", "study", "literature", "evidence", "survey", "review"}},
		// Hypothesis / experiment signals.
		{"experimental_loop", []string{"hypothesis", "experiment", "test", "measure", "metric", "data", "result"}},
		// Recursive / decomposition signals.
		{"problem_decomposition", []string{"recursive", "decompose", "break", "sub-problem", "nested", "tree", "hierarchy"}},
	}

	ranked := make([]patternScore, 0, len(categories))
	for _, cat := range categories {
		matches := 0
		for _, sig := range cat.signals {
			if kwSet[sig] {
				matches++
			}
		}
		if matches > 0 {
			ranked = append(ranked, patternScore{
				pattern: cat.pattern,
				score:   float64(matches) / float64(total),
			})
		}
	}

	// Insertion sort — at most 6 elements, no import needed.
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && ranked[j].score > ranked[j-1].score; j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}

	return ranked
}

// suggestPatternFromKeywords returns a pattern name and a confidence score based on
// keyword signals in the thought.
//
// Confidence = number of matching signal keywords / total keyword count.
// If total keywords is 0, confidence is 0 and the fallback pattern is returned.
func suggestPatternFromKeywords(keywords []string) (string, float64) {
	ranked := rankPatterns(keywords)
	if len(ranked) == 0 {
		return "sequential_thinking", 0.0
	}
	return ranked[0].pattern, ranked[0].score
}
