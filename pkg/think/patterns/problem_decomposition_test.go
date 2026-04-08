package patterns

import (
	"testing"
)

// TestProblemDecomp_AcyclicDAG verifies that a simple 3-node, 2-edge acyclic graph
// produces the correct topological order with no cycle detected.
func TestProblemDecomp_AcyclicDAG(t *testing.T) {
	// Graph: A → B → C
	edges := []dagEdge{
		{from: "A", to: "B"},
		{from: "B", to: "C"},
	}
	result := analyzeDag(edges, nil)

	if result.hasCycle {
		t.Fatalf("expected no cycle, got hasCycle=true (cyclePath=%v)", result.cyclePath)
	}
	if len(result.cyclePath) != 0 {
		t.Errorf("expected empty cyclePath, got %v", result.cyclePath)
	}
	// Topological order must respect all edges: A before B before C.
	order := result.topologicalOrder
	if len(order) != 3 {
		t.Fatalf("expected 3 nodes in topologicalOrder, got %d: %v", len(order), order)
	}
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	if pos["A"] >= pos["B"] {
		t.Errorf("A must appear before B in topological order, got %v", order)
	}
	if pos["B"] >= pos["C"] {
		t.Errorf("B must appear before C in topological order, got %v", order)
	}
}

// TestProblemDecomp_CyclicDAG verifies that A→B→C→A is detected as a cycle
// and cyclePath contains the repeated node.
func TestProblemDecomp_CyclicDAG(t *testing.T) {
	edges := []dagEdge{
		{from: "A", to: "B"},
		{from: "B", to: "C"},
		{from: "C", to: "A"},
	}
	result := analyzeDag(edges, nil)

	if !result.hasCycle {
		t.Fatal("expected hasCycle=true, got false")
	}
	if len(result.cyclePath) == 0 {
		t.Fatal("expected non-empty cyclePath")
	}
	// The cycle path must start and end with the same node.
	first := result.cyclePath[0]
	last := result.cyclePath[len(result.cyclePath)-1]
	if first != last {
		t.Errorf("cyclePath should start and end with same node, got %v", result.cyclePath)
	}
	// topologicalOrder must be nil/empty when a cycle exists.
	if len(result.topologicalOrder) != 0 {
		t.Errorf("expected empty topologicalOrder when cycle detected, got %v", result.topologicalOrder)
	}
}

// TestProblemDecomp_Orphans verifies that a sub-problem not present in any edge
// is reported in orphanSubProblems.
func TestProblemDecomp_Orphans(t *testing.T) {
	// Graph: A → B; sub-problem "D" is not in any edge.
	edges := []dagEdge{
		{from: "A", to: "B"},
	}
	subProblems := []any{"A", "B", "D"}
	result := analyzeDag(edges, subProblems)

	if result.hasCycle {
		t.Fatalf("unexpected cycle: %v", result.cyclePath)
	}
	if len(result.orphanSubProblems) != 1 || result.orphanSubProblems[0] != "D" {
		t.Errorf("expected orphanSubProblems=[\"D\"], got %v", result.orphanSubProblems)
	}
}

// TestProblemDecomp_NoDependencies verifies backward compatibility: when no
// dependencies are provided the Handle output contains no "dag" key.
func TestProblemDecomp_NoDependencies(t *testing.T) {
	p := &problemDecompositionPattern{}
	input := map[string]any{
		"problem":      "plan a feature",
		"subProblems":  []any{"design", "implement"},
		"risks":        []any{"scope creep"},
		"stakeholders": []any{"product"},
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-session")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
	if _, hasDag := result.Data["dag"]; hasDag {
		t.Error("expected no 'dag' key in output when dependencies are absent")
	}
	// Existing counts must still be present.
	if result.Data["subProblemCount"] != 2 {
		t.Errorf("expected subProblemCount=2, got %v", result.Data["subProblemCount"])
	}
	if result.Data["totalComponents"] != 4 {
		t.Errorf("expected totalComponents=4, got %v", result.Data["totalComponents"])
	}
}

// TestProblemDecomp_AntiStub verifies the implementation is not a stub:
// different inputs must produce different hasCycle and topologicalOrder values.
func TestProblemDecomp_AntiStub(t *testing.T) {
	acyclicEdges := []dagEdge{{from: "X", to: "Y"}}
	cyclicEdges := []dagEdge{{from: "X", to: "Y"}, {from: "Y", to: "X"}}

	acyclic := analyzeDag(acyclicEdges, nil)
	cyclic := analyzeDag(cyclicEdges, nil)

	if acyclic.hasCycle == cyclic.hasCycle {
		t.Error("anti-stub: acyclic and cyclic graphs must produce different hasCycle values")
	}
	if len(acyclic.topologicalOrder) == 0 {
		t.Error("anti-stub: acyclic graph must produce a non-empty topologicalOrder")
	}
	if len(cyclic.topologicalOrder) != 0 {
		t.Error("anti-stub: cyclic graph must produce an empty topologicalOrder")
	}
}

// TestExtractDagDependencies_Valid verifies correct edge extraction from map objects.
func TestExtractDagDependencies_Valid(t *testing.T) {
	deps := []any{
		map[string]any{"from": "A", "to": "B"},
		map[string]any{"from": "B", "to": "C"},
	}
	edges := extractDagDependencies(deps)
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}
	if edges[0].from != "A" || edges[0].to != "B" {
		t.Errorf("unexpected first edge: %+v", edges[0])
	}
}

// TestExtractDagDependencies_InvalidReturnsNil verifies that non-object entries
// cause extractDagDependencies to return nil (not a partial result).
func TestExtractDagDependencies_InvalidReturnsNil(t *testing.T) {
	deps := []any{"not-an-object"}
	edges := extractDagDependencies(deps)
	if edges != nil {
		t.Errorf("expected nil for invalid input, got %v", edges)
	}
}
