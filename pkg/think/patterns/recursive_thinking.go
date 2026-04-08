package patterns

import (
	"fmt"
	"math"

	think "github.com/thebtf/aimux/pkg/think"
)

const defaultMaxRecursionDepth = 10.0

type recursiveThinkingPattern struct{}

// NewRecursiveThinkingPattern returns the "recursive_thinking" pattern handler.
func NewRecursiveThinkingPattern() think.PatternHandler { return &recursiveThinkingPattern{} }

func (p *recursiveThinkingPattern) Name() string { return "recursive_thinking" }

func (p *recursiveThinkingPattern) Description() string {
	return "Apply recursive decomposition with base/recursive cases and depth tracking"
}

func (p *recursiveThinkingPattern) Validate(input map[string]any) (map[string]any, error) {
	problem, ok := input["problem"]
	if !ok {
		return nil, fmt.Errorf("missing required field: problem")
	}
	ps, ok := problem.(string)
	if !ok || ps == "" {
		return nil, fmt.Errorf("field 'problem' must be a non-empty string")
	}
	out := map[string]any{"problem": ps}

	if v, ok := input["baseCase"].(string); ok {
		out["baseCase"] = v
	}
	if v, ok := input["recursiveCase"].(string); ok {
		out["recursiveCase"] = v
	}
	if v, ok := input["convergenceCheck"].(string); ok {
		out["convergenceCheck"] = v
	}
	if v, err := toFloat64(input["currentDepth"]); err == nil && v >= 0 {
		out["currentDepth"] = v
	}
	if v, err := toFloat64(input["maxDepth"]); err == nil && v > 0 {
		out["maxDepth"] = v
	}
	return out, nil
}

func (p *recursiveThinkingPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	problem := validInput["problem"].(string)

	currentDepth := 0.0
	if v, ok := validInput["currentDepth"].(float64); ok {
		currentDepth = v
	}
	maxDepth := defaultMaxRecursionDepth
	if v, ok := validInput["maxDepth"].(float64); ok {
		maxDepth = v
	}

	depthRemaining := math.Max(0, maxDepth-currentDepth)
	depthPercentage := 0.0
	if maxDepth > 0 {
		depthPercentage = (currentDepth / maxDepth) * 100.0
	}
	isBaseCase := currentDepth >= maxDepth

	depthWarning := ""
	if currentDepth >= maxDepth {
		depthWarning = fmt.Sprintf("Maximum recursion depth reached (%.0f/%.0f). Consider base case resolution.", currentDepth, maxDepth)
	}

	convergenceCheck, hasConvergence := validInput["convergenceCheck"].(string)
	convergenceWarning := ""
	noConvergenceDefined := !hasConvergence || convergenceCheck == ""
	if noConvergenceDefined && currentDepth > 3 {
		convergenceWarning = "No convergence check at depth > 3"
	} else if noConvergenceDefined {
		convergenceWarning = "No convergence check defined. Risk of infinite recursion."
	}

	data := map[string]any{
		"problem":            problem,
		"currentDepth":       currentDepth,
		"maxDepth":           maxDepth,
		"depthWarning":       depthWarning,
		"convergenceWarning": convergenceWarning,
		"hasBaseCase":        validInput["baseCase"] != nil,
		"hasRecursiveCase":   validInput["recursiveCase"] != nil,
		"depthRemaining":     depthRemaining,
		"depthPercentage":    depthPercentage,
		"isBaseCase":         isBaseCase,
	}
	return think.MakeThinkResult("recursive_thinking", data, sessionID, nil, "", []string{"depthWarning", "convergenceWarning", "depthRemaining", "depthPercentage", "isBaseCase"}), nil
}
