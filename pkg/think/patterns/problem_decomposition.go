package patterns

import (
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

type problemDecompositionPattern struct{}

// NewProblemDecompositionPattern returns the "problem_decomposition" pattern handler.
func NewProblemDecompositionPattern() think.PatternHandler { return &problemDecompositionPattern{} }

func (p *problemDecompositionPattern) Name() string { return "problem_decomposition" }

func (p *problemDecompositionPattern) Description() string {
	return "Break a problem into sub-problems, dependencies, risks, and stakeholders"
}

func (p *problemDecompositionPattern) Validate(input map[string]any) (map[string]any, error) {
	problem, ok := input["problem"]
	if !ok {
		return nil, fmt.Errorf("missing required field: problem")
	}
	s, ok := problem.(string)
	if !ok || s == "" {
		return nil, fmt.Errorf("field 'problem' must be a non-empty string")
	}
	out := map[string]any{"problem": s}
	if v, ok := input["methodology"].(string); ok {
		out["methodology"] = v
	}
	if v, ok := input["subProblems"].([]any); ok {
		out["subProblems"] = v
	}
	if v, ok := input["dependencies"].([]any); ok {
		out["dependencies"] = v
	}
	if v, ok := input["risks"].([]any); ok {
		out["risks"] = v
	}
	if v, ok := input["stakeholders"].([]any); ok {
		out["stakeholders"] = v
	}
	return out, nil
}

func (p *problemDecompositionPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	problem := validInput["problem"].(string)

	countSlice := func(key string) int {
		if v, ok := validInput[key].([]any); ok {
			return len(v)
		}
		return 0
	}

	methodology := ""
	if v, ok := validInput["methodology"].(string); ok {
		methodology = v
	}

	data := map[string]any{
		"problem":          problem,
		"methodology":      methodology,
		"subProblemCount":  countSlice("subProblems"),
		"dependencyCount":  countSlice("dependencies"),
		"riskCount":        countSlice("risks"),
		"stakeholderCount": countSlice("stakeholders"),
		"totalComponents":  countSlice("subProblems") + countSlice("dependencies") + countSlice("risks") + countSlice("stakeholders"),
	}
	return think.MakeThinkResult("problem_decomposition", data, sessionID, nil, "", []string{"totalComponents"}), nil
}
