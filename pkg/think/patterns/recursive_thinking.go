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

	if v, ok := input["baseCase"].(string); ok && v != "" {
		out["baseCase"] = v
	}
	if v, ok := input["recursiveCase"].(string); ok && v != "" {
		out["recursiveCase"] = v
	}
	if v, ok := input["convergenceCheck"].(string); ok && v != "" {
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

func (p *recursiveThinkingPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"problem":          {Type: "string", Required: true, Description: "The problem to decompose recursively"},
		"baseCase":         {Type: "string", Required: false, Description: "The base case condition"},
		"recursiveCase":    {Type: "string", Required: false, Description: "The recursive case description"},
		"convergenceCheck": {Type: "string", Required: false, Description: "How to verify convergence"},
		"currentDepth":     {Type: "number", Required: false, Description: "Current recursion depth"},
		"maxDepth":         {Type: "number", Required: false, Description: "Maximum allowed recursion depth"},
	}
}

func (p *recursiveThinkingPattern) Category() string { return "solo" }

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

	// Detect branch factor from problem text (e.g. "3-ary", "k-ary", "n-ary").
	branchFactor := 2
	for i := 0; i < len(problem)-3; i++ {
		ch := problem[i]
		if ch >= '2' && ch <= '9' && problem[i+1] == '-' &&
			problem[i+2] == 'a' && problem[i+3] == 'r' && len(problem) > i+4 && problem[i+4] == 'y' {
			branchFactor = int(ch - '0')
			break
		}
	}
	estimatedCallTreeNodes := int(math.Pow(float64(branchFactor), depthRemaining))

	stackRisk := "safe"
	if depthRemaining > 1000 {
		stackRisk = "overflow"
	} else if depthRemaining > 100 {
		stackRisk = "warn"
	}

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

	keywords := ExtractKeywords(problem)
	recursiveStructureDetected := detectRecursiveStructure(keywords)

	data := map[string]any{
		"problem":                    problem,
		"currentDepth":               currentDepth,
		"maxDepth":                   maxDepth,
		"depthWarning":               depthWarning,
		"convergenceWarning":         convergenceWarning,
		"hasBaseCase":                validInput["baseCase"] != nil,
		"hasRecursiveCase":           validInput["recursiveCase"] != nil,
		"depthRemaining":             depthRemaining,
		"depthPercentage":            depthPercentage,
		"isBaseCase":                 isBaseCase,
		"keywords":                   keywords,
		"recursiveStructureDetected": recursiveStructureDetected,
		"estimatedCallTreeNodes":     estimatedCallTreeNodes,
		"stackRisk":                  stackRisk,
		"guidance":                   BuildGuidance("recursive_thinking", "basic", []string{"baseCase", "recursiveCase", "convergenceCheck", "maxDepth"}),
	}
	return think.MakeThinkResult("recursive_thinking", data, sessionID, nil, "", []string{"depthWarning", "convergenceWarning", "depthRemaining", "depthPercentage", "isBaseCase"}), nil
}

// detectRecursiveStructure returns true when the problem text contains keywords
// that suggest a naturally recursive or hierarchical data structure.
func detectRecursiveStructure(keywords []string) bool {
	signals := map[string]bool{
		"nested": true, "recursive": true, "tree": true, "parse": true,
		"hierarchy": true, "hierarchical": true, "graph": true, "node": true,
		"children": true, "parent": true, "fractal": true, "self-similar": true,
	}
	for _, k := range keywords {
		if signals[k] {
			return true
		}
	}
	return false
}
