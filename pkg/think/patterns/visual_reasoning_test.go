package patterns

import (
	"testing"
)

func TestVisualReasoning_ElementsByType(t *testing.T) {
	p := NewVisualReasoningPattern()

	// Two nodes, one edge — different element types should produce a histogram.
	input := map[string]any{
		"operation":   "analyze",
		"diagramType": "flowchart",
		"elements": []any{
			map[string]any{"id": "a", "type": "node"},
			map[string]any{"id": "b", "type": "node"},
			map[string]any{"id": "c", "type": "edge"},
		},
		"relationships": []any{
			map[string]any{"from": "a", "to": "b"},
		},
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(validated, "s1")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	byType, ok := result.Data["elementsByType"].(map[string]int)
	if !ok {
		t.Fatalf("elementsByType missing or wrong type: %T", result.Data["elementsByType"])
	}
	if byType["node"] != 2 {
		t.Errorf("elementsByType[node] = %d, want 2", byType["node"])
	}
	if byType["edge"] != 1 {
		t.Errorf("elementsByType[edge] = %d, want 1", byType["edge"])
	}
}

func TestVisualReasoning_IsolatedElements(t *testing.T) {
	p := NewVisualReasoningPattern()

	// Elements a and b are connected; c is isolated.
	input := map[string]any{
		"operation": "layout",
		"elements": []any{
			map[string]any{"id": "a", "type": "node"},
			map[string]any{"id": "b", "type": "node"},
			map[string]any{"id": "c", "type": "node"},
		},
		"relationships": []any{
			map[string]any{"from": "a", "to": "b"},
		},
	}

	validated, _ := p.Validate(input)
	result, _ := p.Handle(validated, "s1")

	isolated, ok := result.Data["isolatedElements"].([]string)
	if !ok {
		t.Fatalf("isolatedElements missing or wrong type: %T", result.Data["isolatedElements"])
	}
	if len(isolated) != 1 || isolated[0] != "c" {
		t.Errorf("isolatedElements = %v, want [c]", isolated)
	}
}

func TestVisualReasoning_GraphDensity(t *testing.T) {
	p := NewVisualReasoningPattern()

	// 3 elements, 3 relationships — max edges = 3*(3-1)/2 = 3 → density = 1.0
	inputFull := map[string]any{
		"operation": "density-check",
		"elements": []any{
			map[string]any{"id": "x", "type": "node"},
			map[string]any{"id": "y", "type": "node"},
			map[string]any{"id": "z", "type": "node"},
		},
		"relationships": []any{
			map[string]any{"from": "x", "to": "y"},
			map[string]any{"from": "y", "to": "z"},
			map[string]any{"from": "x", "to": "z"},
		},
	}
	validated, _ := p.Validate(inputFull)
	resultFull, _ := p.Handle(validated, "s1")
	densityFull, ok := resultFull.Data["density"].(float64)
	if !ok {
		t.Fatalf("density missing or wrong type: %T", resultFull.Data["density"])
	}
	if densityFull != 1.0 {
		t.Errorf("full graph density = %.4f, want 1.0", densityFull)
	}

	// 3 elements, 1 relationship → density = 1/3 ≈ 0.333
	inputSparse := map[string]any{
		"operation": "density-check",
		"elements": []any{
			map[string]any{"id": "x", "type": "node"},
			map[string]any{"id": "y", "type": "node"},
			map[string]any{"id": "z", "type": "node"},
		},
		"relationships": []any{
			map[string]any{"from": "x", "to": "y"},
		},
	}
	validated2, _ := p.Validate(inputSparse)
	resultSparse, _ := p.Handle(validated2, "s1")
	densitySparse := resultSparse.Data["density"].(float64)
	if densitySparse >= densityFull {
		t.Errorf("sparse density (%.4f) should be less than full density (%.4f)", densitySparse, densityFull)
	}
}
