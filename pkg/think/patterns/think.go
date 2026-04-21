package patterns

import (
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

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
	suggestedPattern := suggestPatternFromKeywords(keywords)

	data := map[string]any{
		"thought":          thought,
		"thoughtLength":    len(thought),
		"keywords":         keywords,
		"suggestedPattern": suggestedPattern,
		"guidance":         BuildGuidance("think", "basic", []string{"thought"}),
	}

	// Tier 2A: text analysis
	primaryText := validInput["thought"].(string)
	if analysis := AnalyzeText(primaryText); analysis != nil {
		domain := MatchDomainTemplate(primaryText)
		if domain != nil {
			analysis.Gaps = DetectGaps(analysis.Entities, domain)
		}
		data["textAnalysis"] = analysis
	}

	return think.MakeThinkResult("think", data, sessionID, nil, suggestedPattern, nil), nil
}

// suggestPatternFromKeywords returns a pattern name based on keyword signals in the thought.
func suggestPatternFromKeywords(keywords []string) string {
	kwSet := make(map[string]bool, len(keywords))
	for _, k := range keywords {
		kwSet[k] = true
	}

	// Debug / error signals.
	for _, k := range []string{"bug", "error", "crash", "fail", "broken", "exception", "panic", "nil", "undefined"} {
		if kwSet[k] {
			return "debugging_approach"
		}
	}
	// Architecture / design signals.
	for _, k := range []string{"design", "architecture", "system", "structure", "service", "layer", "pattern", "module"} {
		if kwSet[k] {
			return "architecture_analysis"
		}
	}
	// Decision / choice signals.
	for _, k := range []string{"choose", "choice", "decide", "decision", "option", "compare", "tradeoff", "versus", "vs", "pick", "select"} {
		if kwSet[k] {
			return "decision_framework"
		}
	}
	// Research / literature signals.
	for _, k := range []string{"research", "paper", "study", "literature", "evidence", "survey", "review"} {
		if kwSet[k] {
			return "literature_review"
		}
	}
	// Hypothesis / experiment signals.
	for _, k := range []string{"hypothesis", "experiment", "test", "measure", "metric", "data", "result"} {
		if kwSet[k] {
			return "experimental_loop"
		}
	}
	// Recursive / decomposition signals.
	for _, k := range []string{"recursive", "decompose", "break", "sub-problem", "nested", "tree", "hierarchy"} {
		if kwSet[k] {
			return "problem_decomposition"
		}
	}
	// Fallback — generic sequential thinking.
	return "sequential_thinking"
}
