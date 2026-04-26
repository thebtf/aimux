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
	suggestedPattern, confidence := suggestPatternFromKeywords(keywords)

	// Auto-route: if confidence is high enough and the pattern is not the fallback,
	// execute the target pattern directly and annotate the result.
	if confidence >= autoRouteConfidenceThreshold && suggestedPattern != "sequential_thinking" {
		if result := tryAutoRoute(thought, suggestedPattern, sessionID); result != nil {
			return result, nil
		}
	}

	data := map[string]any{
		"thought":          thought,
		"thoughtLength":    len(thought),
		"keywords":         keywords,
		"suggestedPattern": suggestedPattern,
		"guidance":         BuildGuidance("think", "basic", []string{"thought"}),
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

// suggestPatternFromKeywords returns a pattern name and a confidence score based on
// keyword signals in the thought.
//
// Confidence = number of matching signal keywords / total keyword count.
// If total keywords is 0, confidence is 0 and the fallback pattern is returned.
func suggestPatternFromKeywords(keywords []string) (string, float64) {
	total := len(keywords)
	if total == 0 {
		return "sequential_thinking", 0.0
	}

	kwSet := make(map[string]bool, total)
	for _, k := range keywords {
		kwSet[k] = true
	}

	type candidate struct {
		pattern string
		signals []string
	}

	categories := []candidate{
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

	bestPattern := "sequential_thinking"
	bestMatches := 0

	for _, cat := range categories {
		matches := 0
		for _, sig := range cat.signals {
			if kwSet[sig] {
				matches++
			}
		}
		if matches > bestMatches {
			bestMatches = matches
			bestPattern = cat.pattern
		}
	}

	if bestMatches == 0 {
		return "sequential_thinking", 0.0
	}

	confidence := float64(bestMatches) / float64(total)
	return bestPattern, confidence
}
