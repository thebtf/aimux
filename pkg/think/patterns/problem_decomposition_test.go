package patterns

import (
	"context"
	"errors"
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
)

// mockSamplingProvider is a test double for think.SamplingProvider.
type mockSamplingProvider struct {
	response string
	err      error
}

func (m *mockSamplingProvider) RequestSampling(_ context.Context, _ []think.SamplingMessage, _ int) (string, error) {
	return m.response, m.err
}

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

// TestProblemDecomp_SamplingDecompose verifies that when no subProblems are
// provided but a SamplingProvider is set, the pattern calls the LLM, parses
// the JSON response, and runs DAG analysis on the generated data.
func TestProblemDecomp_SamplingDecompose(t *testing.T) {
	samplingResp := `{
		"subProblems": [
			{"id": "sp1", "description": "Define requirements"},
			{"id": "sp2", "description": "Design architecture"},
			{"id": "sp3", "description": "Implement core"}
		],
		"dependencies": [
			{"from": "sp1", "to": "sp2"},
			{"from": "sp2", "to": "sp3"}
		]
	}`

	p := &problemDecompositionPattern{}
	p.SetSampling(&mockSamplingProvider{response: samplingResp})

	input := map[string]any{"problem": "build a new feature"}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-sampling")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	// DAG analysis must have run on the LLM-generated data.
	dagVal, hasDag := result.Data["dag"]
	if !hasDag {
		t.Fatal("expected 'dag' key in output when sampling provides subProblems and dependencies")
	}
	dag, ok := dagVal.(map[string]any)
	if !ok {
		t.Fatalf("expected dag to be map[string]any, got %T", dagVal)
	}
	if dag["hasCycle"] != false {
		t.Errorf("expected acyclic graph, got hasCycle=%v", dag["hasCycle"])
	}
	order, ok := dag["topologicalOrder"].([]string)
	if !ok || len(order) == 0 {
		t.Errorf("expected non-empty topologicalOrder, got %v", dag["topologicalOrder"])
	}
	// Verify sp1 precedes sp2 and sp2 precedes sp3.
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	if pos["sp1"] >= pos["sp2"] {
		t.Errorf("sp1 must precede sp2 in topological order, got %v", order)
	}
	if pos["sp2"] >= pos["sp3"] {
		t.Errorf("sp2 must precede sp3 in topological order, got %v", order)
	}
	// Counts must reflect the generated data.
	if result.Data["subProblemCount"] != 3 {
		t.Errorf("expected subProblemCount=3, got %v", result.Data["subProblemCount"])
	}
}

// TestProblemDecomp_SamplingFailure verifies that when the SamplingProvider
// returns an error, Handle gracefully degrades to a basic result with no DAG.
func TestProblemDecomp_SamplingFailure(t *testing.T) {
	p := &problemDecompositionPattern{}
	p.SetSampling(&mockSamplingProvider{err: errors.New("sampling unavailable")})

	input := map[string]any{"problem": "complex problem"}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-sampling-fail")
	if err != nil {
		t.Fatalf("Handle must not return error on sampling failure, got: %v", err)
	}
	// No DAG — sampling failed, graceful degradation.
	if _, hasDag := result.Data["dag"]; hasDag {
		t.Error("expected no 'dag' key when sampling fails")
	}
	if result.Data["subProblemCount"] != 0 {
		t.Errorf("expected subProblemCount=0 on sampling failure, got %v", result.Data["subProblemCount"])
	}
}

// TestProblemDecomp_AutoAnalysis_DomainTemplate verifies that when no subProblems or
// dependencies are provided and no sampling provider is set, Handle uses the domain
// template engine to produce suggestedSubProblems, suggestedDependencies, autoAnalysis,
// and guidance fields.
func TestProblemDecomp_AutoAnalysis_DomainTemplate(t *testing.T) {
	p := &problemDecompositionPattern{} // no sampling

	input := map[string]any{
		"problem": "build an authentication system with jwt login",
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-auto")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	// suggestedSubProblems must be present and non-empty (auth template has 5 items).
	ssp, ok := result.Data["suggestedSubProblems"].([]string)
	if !ok || len(ssp) == 0 {
		t.Errorf("expected non-empty suggestedSubProblems, got %v (%T)", result.Data["suggestedSubProblems"], result.Data["suggestedSubProblems"])
	}

	// autoAnalysis source must be "domain-template".
	aa, ok := result.Data["autoAnalysis"].(map[string]any)
	if !ok {
		t.Fatalf("expected autoAnalysis map, got %T", result.Data["autoAnalysis"])
	}
	if aa["source"] != "domain-template" {
		t.Errorf("expected autoAnalysis.source=domain-template, got %v", aa["source"])
	}

	// guidance must be present.
	if _, ok := result.Data["guidance"]; !ok {
		t.Error("expected guidance field in result")
	}
}

// TestProblemDecomp_AutoAnalysis_KeywordFallback verifies that when no domain template
// matches, autoAnalysis.source is "keyword-analysis".
func TestProblemDecomp_AutoAnalysis_KeywordFallback(t *testing.T) {
	p := &problemDecompositionPattern{}

	input := map[string]any{
		"problem": "organize a birthday party for my cat",
	}
	validated, _ := p.Validate(input)
	result, err := p.Handle(validated, "test-auto-fallback")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	aa, ok := result.Data["autoAnalysis"].(map[string]any)
	if !ok {
		t.Fatalf("expected autoAnalysis map, got %T", result.Data["autoAnalysis"])
	}
	if aa["source"] != "keyword-analysis" {
		t.Errorf("expected autoAnalysis.source=keyword-analysis, got %v", aa["source"])
	}
}

// TestProblemDecomp_AutoAnalysis_BackwardCompat verifies that when subProblems ARE
// provided, the existing behavior is preserved (no suggestedSubProblems).
func TestProblemDecomp_AutoAnalysis_BackwardCompat(t *testing.T) {
	p := &problemDecompositionPattern{}

	input := map[string]any{
		"problem":     "plan a feature",
		"subProblems": []any{"design", "implement"},
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-compat")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	// suggestedSubProblems must NOT be present when user supplied subProblems.
	if _, ok := result.Data["suggestedSubProblems"]; ok {
		t.Error("suggestedSubProblems must not appear when subProblems are provided")
	}
	// Existing counts must still be correct.
	if result.Data["subProblemCount"] != 2 {
		t.Errorf("expected subProblemCount=2, got %v", result.Data["subProblemCount"])
	}
	// Guidance must still be present.
	if _, ok := result.Data["guidance"]; !ok {
		t.Error("expected guidance field in result")
	}
}

// TestProblemDecomp_NoSampling verifies that when SetSampling is never called,
// Handle behaves exactly as before: no DAG when subProblems are absent.
func TestProblemDecomp_NoSampling(t *testing.T) {
	p := &problemDecompositionPattern{} // no SetSampling call

	input := map[string]any{
		"problem":     "plan a migration",
		"risks":       []any{"data loss"},
		"methodology": "iterative",
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-no-sampling")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
	if _, hasDag := result.Data["dag"]; hasDag {
		t.Error("expected no 'dag' key when sampling is not configured")
	}
	if result.Data["subProblemCount"] != 0 {
		t.Errorf("expected subProblemCount=0 without sampling, got %v", result.Data["subProblemCount"])
	}
	if result.Data["riskCount"] != 1 {
		t.Errorf("expected riskCount=1, got %v", result.Data["riskCount"])
	}
}
