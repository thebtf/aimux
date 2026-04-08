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

	elements, _ := validInput["elements"].([]any)
	relationships, _ := validInput["relationships"].([]any)
	transformations, _ := validInput["transformations"].([]any)

	diagramType := ""
	if v, ok := validInput["diagramType"].(string); ok {
		diagramType = v
	}
	description := ""
	if v, ok := validInput["description"].(string); ok {
		description = v
	}

	elementCount := len(elements)
	relationshipCount := len(relationships)
	transformationCount := len(transformations)

	data := map[string]any{
		"operation":           operation,
		"diagramType":         diagramType,
		"description":         description,
		"elementCount":        elementCount,
		"relationshipCount":   relationshipCount,
		"transformationCount": transformationCount,
		"totalComponents":     elementCount + relationshipCount + transformationCount,
	}

	if elementCount > 0 {
		stats := computeVisualElementStats(elements, relationships)
		data["elementsByType"] = stats.elementsByType
		data["isolatedElements"] = stats.isolatedElements
		data["density"] = stats.density
	}

	computed := []string{"totalComponents"}
	if elementCount > 0 {
		computed = append(computed, "elementsByType", "isolatedElements", "density")
	}

	return think.MakeThinkResult("visual_reasoning", data, sessionID, nil, "", computed), nil
}

type visualElementStats struct {
	elementsByType  map[string]int
	isolatedElements []string
	density         float64
}

func computeVisualElementStats(elements []any, relationships []any) visualElementStats {
	elementsByType := map[string]int{}
	elementIDs := []string{}

	for _, e := range elements {
		if obj, ok := e.(map[string]any); ok {
			typ := "unknown"
			if t, ok := obj["type"].(string); ok && t != "" {
				typ = t
			}
			elementsByType[typ]++
			if id, ok := obj["id"].(string); ok && id != "" {
				elementIDs = append(elementIDs, id)
			} else if name, ok := obj["name"].(string); ok && name != "" {
				elementIDs = append(elementIDs, name)
			}
		} else if s, ok := e.(string); ok {
			elementsByType["unknown"]++
			elementIDs = append(elementIDs, s)
		}
	}

	connectedIDs := map[string]bool{}
	for _, r := range relationships {
		if obj, ok := r.(map[string]any); ok {
			for _, field := range []string{"from", "to", "source", "target"} {
				if v, ok := obj[field].(string); ok && v != "" {
					connectedIDs[v] = true
				}
			}
		}
	}

	isolatedElements := []string{}
	for _, id := range elementIDs {
		if !connectedIDs[id] {
			isolatedElements = append(isolatedElements, id)
		}
	}

	n := len(elements)
	maxEdges := 1
	if n > 1 {
		maxEdges = n * (n - 1) / 2
	}
	density := float64(len(relationships)) / float64(maxEdges)

	return visualElementStats{
		elementsByType:   elementsByType,
		isolatedElements: isolatedElements,
		density:          density,
	}
}
