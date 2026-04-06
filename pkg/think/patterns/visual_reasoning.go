package patterns

import (
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

type visualReasoningPattern struct{}

// NewVisualReasoningPattern returns the "visual_reasoning" pattern handler.
func NewVisualReasoningPattern() think.PatternHandler { return &visualReasoningPattern{} }

func (p *visualReasoningPattern) Name() string { return "visual_reasoning" }

func (p *visualReasoningPattern) Description() string {
	return "Analyze and reason about visual structures, diagrams, and spatial relationships"
}

func (p *visualReasoningPattern) Validate(input map[string]any) (map[string]any, error) {
	operation, ok := input["operation"]
	if !ok {
		return nil, fmt.Errorf("missing required field: operation")
	}
	op, ok := operation.(string)
	if !ok || op == "" {
		return nil, fmt.Errorf("field 'operation' must be a non-empty string")
	}
	out := map[string]any{"operation": op}
	if v, ok := input["diagramType"].(string); ok {
		out["diagramType"] = v
	}
	if v, ok := input["description"].(string); ok {
		out["description"] = v
	}
	if v, ok := input["elements"].([]any); ok {
		out["elements"] = v
	}
	if v, ok := input["relationships"].([]any); ok {
		out["relationships"] = v
	}
	if v, ok := input["transformations"].([]any); ok {
		out["transformations"] = v
	}
	return out, nil
}

func (p *visualReasoningPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	operation := validInput["operation"].(string)

	countSlice := func(key string) int {
		if v, ok := validInput[key].([]any); ok {
			return len(v)
		}
		return 0
	}

	diagramType := ""
	if v, ok := validInput["diagramType"].(string); ok {
		diagramType = v
	}
	description := ""
	if v, ok := validInput["description"].(string); ok {
		description = v
	}

	data := map[string]any{
		"operation":           operation,
		"diagramType":         diagramType,
		"description":         description,
		"elementCount":        countSlice("elements"),
		"relationshipCount":   countSlice("relationships"),
		"transformationCount": countSlice("transformations"),
		"totalComponents":     countSlice("elements") + countSlice("relationships") + countSlice("transformations"),
	}
	return think.MakeThinkResult("visual_reasoning", data, sessionID, nil, "", []string{"totalComponents"}), nil
}
