package patterns

import (
	"fmt"
	"strings"

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
	} else {
		// No elements provided — derive suggestions from operation keywords.
		keywords := ExtractKeywords(operation)
		data["keywords"] = keywords
		data["suggestedElements"] = suggestVisualElements(operation, keywords)
	}

	computed := []string{"totalComponents"}
	if elementCount > 0 {
		computed = append(computed, "elementsByType", "isolatedElements", "density")
	} else {
		computed = append(computed, "keywords", "suggestedElements")
	}

	data["guidance"] = BuildGuidance("visual_reasoning", "basic", []string{"elements", "relationships", "transformations", "diagramType"})

	return think.MakeThinkResult("visual_reasoning", data, sessionID, nil, "", computed), nil
}

func (p *visualReasoningPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"operation":   {Type: "string", Required: true, Description: "The visual operation or analysis to perform"},
		"diagramType": {Type: "string", Required: false, Description: "Type of diagram (e.g. flowchart, sequence, class, network)"},
		"description": {Type: "string", Required: false, Description: "Textual description of the diagram"},
		"elements": {
			Type:        "array",
			Required:    false,
			Description: "Visual elements (nodes, shapes, components)",
			Items: map[string]any{
				"oneOf": []map[string]any{
					{"type": "string"},
					{
						"type": "object",
						"properties": map[string]any{
							"id":   map[string]any{"type": "string"},
							"name": map[string]any{"type": "string"},
							"type": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
		"relationships": {
			Type:        "array",
			Required:    false,
			Description: "Relationships or edges between elements",
			Items: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from":   map[string]any{"type": "string"},
					"to":     map[string]any{"type": "string"},
					"source": map[string]any{"type": "string"},
					"target": map[string]any{"type": "string"},
				},
			},
		},
		"transformations": {
			Type:        "array",
			Required:    false,
			Description: "Transformations to apply to the diagram",
			Items: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":        map[string]any{"type": "string"},
					"description": map[string]any{"type": "string"},
				},
			},
		},
	}
}

func (p *visualReasoningPattern) Category() string { return "solo" }

// suggestVisualElements returns starter elements based on operation type keywords.
func suggestVisualElements(operation string, keywords []string) []string {
	op := strings.ToLower(operation)
	kwSet := make(map[string]bool, len(keywords))
	for _, k := range keywords {
		kwSet[k] = true
	}

	if strings.Contains(op, "layout") || kwSet["layout"] || kwSet["page"] || kwSet["grid"] {
		return []string{"header", "sidebar", "content", "footer"}
	}
	if strings.Contains(op, "flow") || kwSet["flow"] || kwSet["process"] || kwSet["workflow"] {
		return []string{"start", "step1", "decision", "step2", "end"}
	}
	if strings.Contains(op, "class") || kwSet["class"] || kwSet["uml"] || kwSet["object"] {
		return []string{"Class", "Attribute", "Method", "Interface", "Relationship"}
	}
	if strings.Contains(op, "sequence") || kwSet["sequence"] || kwSet["message"] || kwSet["actor"] {
		return []string{"Client", "Server", "Database", "Cache"}
	}
	if strings.Contains(op, "network") || kwSet["network"] || kwSet["node"] || kwSet["graph"] {
		return []string{"NodeA", "NodeB", "NodeC", "Edge"}
	}
	// Default generic elements
	return []string{"element1", "element2", "connector"}
}

type visualElementStats struct {
	elementsByType   map[string]int
	isolatedElements []string
	density          float64
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
