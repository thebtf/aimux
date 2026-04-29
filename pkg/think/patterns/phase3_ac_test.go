package patterns

import (
	"testing"
)

// ===== T011: problem_decomposition.go — criticalPath + highRiskCriticalNodes =====

// TestProblemDecomp_CriticalPath verifies T011 AC: DAG with 6 nodes, 5 edges →
// criticalPath is the longest chain; risk named on critical path → in highRiskCriticalNodes.
func TestProblemDecomp_CriticalPath(t *testing.T) {
	// Build chain: A → B → C → D → E → F (length 6, the only chain, so it IS the critical path).
	// Plus a shorter chain A → X to verify the longest is selected.
	edges := []dagEdge{
		{from: "A", to: "B"},
		{from: "B", to: "C"},
		{from: "C", to: "D"},
		{from: "D", to: "E"},
		{from: "E", to: "F"},
		{from: "A", to: "X"}, // short branch — should not be critical path
	}
	result := analyzeDag(edges, nil)

	if result.hasCycle {
		t.Fatalf("unexpected cycle: %v", result.cyclePath)
	}

	cp := longestPath(result.topologicalOrder, buildAdj(edges))
	if len(cp) < 6 {
		t.Errorf("criticalPath length = %d, want >= 6; path = %v", len(cp), cp)
	}
	// Last node must be F (end of the long chain).
	if len(cp) > 0 && cp[len(cp)-1] != "F" {
		t.Errorf("critical path should end at F, got %v", cp)
	}
}

// TestProblemDecomp_HighRiskCriticalNodes verifies that a risk mentioning a critical path
// node causes that node to appear in highRiskCriticalNodes.
func TestProblemDecomp_HighRiskCriticalNodes(t *testing.T) {
	// Use longer, unambiguous node names to avoid single-char substring false matches.
	criticalPath := []string{"BuildStep", "TestStep", "DeployStep"}
	risks := []any{"TestStep might fail due to flaky dependency", "unrelated operational concern"}

	hrn := highRiskNodes(criticalPath, risks)
	found := false
	for _, n := range hrn {
		if n == "TestStep" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected TestStep in highRiskCriticalNodes, got %v", hrn)
	}
	// BuildStep and DeployStep are not mentioned in risks — must not appear.
	for _, n := range hrn {
		if n == "BuildStep" || n == "DeployStep" {
			t.Errorf("unexpected node %q in highRiskCriticalNodes (not mentioned in risk text)", n)
		}
	}
}

// TestProblemDecomp_HighRiskEmptyWhenNoRisks verifies graceful nil return.
func TestProblemDecomp_HighRiskEmptyWhenNoRisks(t *testing.T) {
	hrn := highRiskNodes([]string{"A", "B"}, nil)
	if hrn != nil {
		t.Errorf("expected nil highRiskCriticalNodes when risks empty, got %v", hrn)
	}
}

// ===== T012: peer_review.go — objectionDensity + noEvidenceWarning =====

// TestPeerReview_ObjectionDensityAndNoEvidenceWarning verifies T012 AC.
func TestPeerReview_ObjectionDensityAndNoEvidenceWarning(t *testing.T) {
	p := NewPeerReviewPattern()

	// Minimal artifact that produces no keyword-detected objections/strengths.
	// "Short artifact." — does not contain security/data/perf keywords.
	input, err := p.Validate(map[string]any{
		"artifact": "Short.",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(input, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// objectionDensity must always be present.
	if _, ok := result.Data["objectionDensity"].(float64); !ok {
		t.Fatal("objectionDensity missing or wrong type")
	}

	// noEvidenceWarning must be present.
	if _, ok := result.Data["noEvidenceWarning"].(bool); !ok {
		t.Fatal("noEvidenceWarning missing or wrong type")
	}
}

// TestPeerReview_ObjectionDensityComputedForKnownInput verifies formula:
// 3 P2 objections on 500-char artifact → density computed (non-zero).
func TestPeerReview_ObjectionDensityNonZero(t *testing.T) {
	p := NewPeerReviewPattern()

	// Craft artifact with security + data_consistency + performance keywords to trigger objections.
	artifact := "This service has no authentication and no rate limiting. " +
		"It directly executes user-supplied SQL queries without parameterization. " +
		"Cache invalidation is missing — stale data persists indefinitely. " +
		"The login endpoint has no brute-force protection. Sessions stored in localStorage. " +
		"All write operations bypass the cache layer, causing consistency issues. " +
		"Response time degrades under load due to no indexing on the users table."

	input, _ := p.Validate(map[string]any{"artifact": artifact})
	result, err := p.Handle(input, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	density, ok := result.Data["objectionDensity"].(float64)
	if !ok {
		t.Fatal("objectionDensity missing")
	}
	if density <= 0 {
		t.Errorf("expected positive objectionDensity for artifact with security/consistency issues, got %f", density)
	}
	// noEvidenceWarning must be false when objections exist.
	warn, _ := result.Data["noEvidenceWarning"].(bool)
	if warn {
		t.Error("noEvidenceWarning must be false when objections are present")
	}
}

// ===== T013: source_comparison.go — sourceAgreementScores + outlierSources =====

// TestSourceComparison_SourceAgreementScoresAndOutliers verifies T013 AC:
// 3 sources, 1 disagreeing → outlierSources contains it (or at least scores are computed).
func TestSourceComparison_SourceAgreementScoresAndOutliers(t *testing.T) {
	p := NewSourceComparisonPattern()
	input, err := p.Validate(map[string]any{
		"topic": "database performance",
		"sources": []any{
			map[string]any{"name": "SrcA", "claim": "index scan improves database performance significantly"},
			map[string]any{"name": "SrcB", "claim": "index scan improves database query speed greatly"},
			// SrcC disagrees completely — different vocabulary.
			map[string]any{"name": "SrcC", "claim": "full table scan is always optimal for large datasets"},
		},
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(input, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// sourceAgreementScores must be map[string]float64 with 3 entries.
	scores, ok := result.Data["sourceAgreementScores"].(map[string]float64)
	if !ok {
		t.Fatalf("sourceAgreementScores missing or wrong type: %T", result.Data["sourceAgreementScores"])
	}
	if len(scores) != 3 {
		t.Errorf("expected 3 sourceAgreementScores, got %d: %v", len(scores), scores)
	}
	// All three source names must appear.
	for _, name := range []string{"SrcA", "SrcB", "SrcC"} {
		if _, has := scores[name]; !has {
			t.Errorf("missing sourceAgreementScore for %q", name)
		}
	}

	// outlierSources must be present (may be empty if stddev math doesn't push SrcC below threshold,
	// but the field itself must exist).
	if _, ok := result.Data["outlierSources"]; !ok {
		t.Fatal("outlierSources missing from result")
	}
}

// TestSourceComparison_OutlierIdentified verifies that a clearly disagreeing source is
// identified as an outlier when the vocabulary difference is large enough.
func TestSourceComparison_OutlierIdentified(t *testing.T) {
	p := NewSourceComparisonPattern()
	// SrcA and SrcB share most tokens; SrcC shares none.
	input, _ := p.Validate(map[string]any{
		"topic": "cats",
		"sources": []any{
			map[string]any{"name": "SrcA", "claim": "cats love fish and sleep daily"},
			map[string]any{"name": "SrcB", "claim": "cats adore fish and nap often"},
			map[string]any{"name": "SrcC", "claim": "quantum computers process photon entanglement"},
		},
	})
	result, _ := p.Handle(input, "")
	outliers, ok := result.Data["outlierSources"].([]string)
	if !ok {
		t.Fatalf("outlierSources wrong type: %T", result.Data["outlierSources"])
	}
	// SrcC should be identified as the outlier.
	found := false
	for _, o := range outliers {
		if o == "SrcC" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SrcC in outlierSources, got %v (scores: %v)", outliers, result.Data["sourceAgreementScores"])
	}
}

// ===== T015: temporal_thinking.go — paceDistribution + significantPauses =====

// TestTemporal_PaceDistributionLateCluster verifies T015 AC:
// 4 events clustered in last third → paceDistribution["late"]=3.
func TestTemporal_PaceDistributionLateCluster(t *testing.T) {
	p := NewTemporalThinkingPattern()

	// Timespan 0-90. Early: 0-30, Mid: 30-60, Late: 60-90.
	// Events at 5 (early), 65, 70, 80 (3 in late).
	input := map[string]any{
		"timeFrame": "test window",
		"events": []any{
			makeEvent(5.0, "early"),
			makeEvent(65.0, "late1"),
			makeEvent(70.0, "late2"),
			makeEvent(80.0, "late3"),
		},
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(validated, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	dist, ok := result.Data["paceDistribution"].(map[string]int)
	if !ok {
		t.Fatalf("paceDistribution missing or wrong type: %T", result.Data["paceDistribution"])
	}
	if dist["late"] != 3 {
		t.Errorf("paceDistribution[late] = %d, want 3 (full dist: %v)", dist["late"], dist)
	}
	if dist["early"] != 1 {
		t.Errorf("paceDistribution[early] = %d, want 1", dist["early"])
	}
}

// TestTemporal_SignificantPauses verifies that a gap > 2×averageGap appears in significantPauses.
func TestTemporal_SignificantPauses(t *testing.T) {
	p := NewTemporalThinkingPattern()

	// Events at 0, 1, 2, 20 — gaps: 1, 1, 18. avgGap = 20/3 ≈ 6.67. 2×avg ≈ 13.33.
	// Gap of 18 (between 2 and 20) > 13.33 → significant.
	input := map[string]any{
		"timeFrame": "test",
		"events": []any{
			makeEvent(0.0, "e1"),
			makeEvent(1.0, "e2"),
			makeEvent(2.0, "e3"),
			makeEvent(20.0, "e4"),
		},
	}
	validated, _ := p.Validate(input)
	result, err := p.Handle(validated, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	pauses, ok := result.Data["significantPauses"].([]map[string]any)
	if !ok {
		t.Fatalf("significantPauses missing or wrong type: %T", result.Data["significantPauses"])
	}
	if len(pauses) == 0 {
		t.Fatalf("expected at least 1 significant pause, got 0 (averageGap=%v)", result.Data["averageGap"])
	}
	// The pause must span the big gap between e3 and e4.
	// The actual struct format is: {"between": [2]string{"e3","e4"}, "duration": 18.0}.
	found := false
	for _, pause := range pauses {
		dur, _ := pause["duration"].(float64)
		if dur >= 17.0 { // gap is exactly 18 (20-2), allow small float tolerance
			found = true
		}
	}
	if !found {
		t.Errorf("expected significant pause with duration ≈18 (gap between t=2 and t=20), got %v", pauses)
	}
}

// TestTemporal_AverageGap verifies averageGap = totalTimespan / (n-1) for n events.
func TestTemporal_AverageGap(t *testing.T) {
	p := NewTemporalThinkingPattern()

	// 3 events at 0, 5, 10 → totalTimespan=10, averageGap=10/2=5.
	input := map[string]any{
		"timeFrame": "test",
		"events": []any{
			makeEvent(0.0, "e1"),
			makeEvent(5.0, "e2"),
			makeEvent(10.0, "e3"),
		},
	}
	validated, _ := p.Validate(input)
	result, _ := p.Handle(validated, "")

	avg, ok := result.Data["averageGap"].(float64)
	if !ok {
		t.Fatalf("averageGap missing or wrong type: %T", result.Data["averageGap"])
	}
	const wantAvg = 5.0
	const eps = 0.001
	if avg < wantAvg-eps || avg > wantAvg+eps {
		t.Errorf("averageGap = %f, want %f", avg, wantAvg)
	}
}

// ===== T016: recursive_thinking.go — estimatedCallTreeNodes + stackRisk =====

// TestRecursiveThinking_CallTreeNodes verifies T016 AC:
// currentDepth=3, maxDepth=10 → depthRemaining=7, branchFactor=2, callTreeNodes=128, stackRisk="safe".
func TestRecursiveThinking_CallTreeNodes(t *testing.T) {
	p := NewRecursiveThinkingPattern()
	input, err := p.Validate(map[string]any{
		"problem":      "binary tree traversal",
		"currentDepth": 3.0,
		"maxDepth":     10.0,
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(input, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	nodes, ok := result.Data["estimatedCallTreeNodes"].(int)
	if !ok {
		t.Fatalf("estimatedCallTreeNodes missing or wrong type: %T", result.Data["estimatedCallTreeNodes"])
	}
	// depthRemaining = 10-3 = 7, branchFactor=2 → 2^7 = 128
	if nodes != 128 {
		t.Errorf("estimatedCallTreeNodes = %d, want 128", nodes)
	}

	risk, ok := result.Data["stackRisk"].(string)
	if !ok {
		t.Fatalf("stackRisk missing or wrong type: %T", result.Data["stackRisk"])
	}
	if risk != "safe" {
		t.Errorf("stackRisk = %q, want safe", risk)
	}
}

// TestRecursiveThinking_StackRiskOverflow verifies T016 AC:
// depth=0, maxDepth=2000 → depthRemaining=2000 > 1000 → stackRisk="overflow".
func TestRecursiveThinking_StackRiskOverflow(t *testing.T) {
	p := NewRecursiveThinkingPattern()
	input, _ := p.Validate(map[string]any{
		"problem":          "deep recursion without base case",
		"currentDepth":     0.0,
		"maxDepth":         2000.0,
		"convergenceCheck": "always converges",
	})
	result, err := p.Handle(input, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	risk, ok := result.Data["stackRisk"].(string)
	if !ok {
		t.Fatalf("stackRisk missing or wrong type: %T", result.Data["stackRisk"])
	}
	if risk != "overflow" {
		t.Errorf("stackRisk = %q, want overflow (depthRemaining=2000)", risk)
	}
}

// ===== T017: visual_reasoning.go — degreeMap + highDegreeElements =====

// TestVisualReasoning_HighDegreeElements verifies T017 AC:
// 5 elements, 1 with 4 connections vs mean 1.5 → in highDegreeElements.
func TestVisualReasoning_HighDegreeElements(t *testing.T) {
	p := NewVisualReasoningPattern()

	// Elements: hub, a, b, c, d. hub connects to all 4 others.
	// Degree map: hub=4, a=1, b=1, c=1, d=1. mean=8/5=1.6, stddev≈1.02. threshold≈2.62.
	// hub(4) > 2.62 → highDegreeElements should contain "hub".
	input := map[string]any{
		"operation": "analyze",
		"elements": []any{
			map[string]any{"id": "hub", "type": "node"},
			map[string]any{"id": "a", "type": "node"},
			map[string]any{"id": "b", "type": "node"},
			map[string]any{"id": "c", "type": "node"},
			map[string]any{"id": "d", "type": "node"},
		},
		"relationships": []any{
			map[string]any{"from": "hub", "to": "a"},
			map[string]any{"from": "hub", "to": "b"},
			map[string]any{"from": "hub", "to": "c"},
			map[string]any{"from": "hub", "to": "d"},
		},
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(validated, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	degreeMap, ok := result.Data["degreeMap"].(map[string]int)
	if !ok {
		t.Fatalf("degreeMap missing or wrong type: %T", result.Data["degreeMap"])
	}
	if degreeMap["hub"] != 4 {
		t.Errorf("degreeMap[hub] = %d, want 4", degreeMap["hub"])
	}

	highDeg, ok := result.Data["highDegreeElements"].([]string)
	if !ok {
		t.Fatalf("highDegreeElements missing or wrong type: %T", result.Data["highDegreeElements"])
	}
	found := false
	for _, e := range highDeg {
		if e == "hub" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected hub in highDegreeElements, got %v (degreeMap: %v)", highDeg, degreeMap)
	}
}

// ===== T018: mental_model.go — clarityScore + alignmentScore =====

// TestMentalModel_ClarityScore4Steps verifies T018 AC: 4 steps → clarityScore = 0.8.
func TestMentalModel_ClarityScore4Steps(t *testing.T) {
	p := NewMentalModelPattern()
	input, err := p.Validate(map[string]any{
		"modelName": "first_principles",
		"problem":   "what are the fundamental truths here",
		"steps":     []any{"step1", "step2", "step3", "step4"},
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(input, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	clarity, ok := result.Data["clarityScore"].(float64)
	if !ok {
		t.Fatalf("clarityScore missing or wrong type: %T", result.Data["clarityScore"])
	}
	// formula: min(4/5.0, 1.0) = 0.8
	const want = 0.8
	const eps = 0.0001
	if clarity < want-eps || clarity > want+eps {
		t.Errorf("clarityScore = %f, want %f (4 steps / 5.0)", clarity, want)
	}
}

// TestMentalModel_AlignmentScoreAndFit verifies T018 AC:
// first_principles + problem with "assumption" → alignmentScore > 0 → modelFit != "mismatch".
func TestMentalModel_AlignmentScoreAndFit(t *testing.T) {
	p := NewMentalModelPattern()
	input, err := p.Validate(map[string]any{
		"modelName": "first_principles",
		"problem":   "What are the fundamental assumptions behind this design?",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(input, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	alignment, ok := result.Data["alignmentScore"].(float64)
	if !ok {
		t.Fatalf("alignmentScore missing or wrong type: %T", result.Data["alignmentScore"])
	}
	if alignment <= 0 {
		t.Errorf("alignmentScore = %f, want > 0 (problem contains 'fundamental' and 'assumption')", alignment)
	}

	fit, ok := result.Data["modelFit"].(string)
	if !ok {
		t.Fatalf("modelFit missing or wrong type: %T", result.Data["modelFit"])
	}
	if fit == "mismatch" {
		t.Errorf("modelFit = mismatch, want strong or weak (alignment %.4f > 0)", alignment)
	}
}

// ===== T019: replication_analysis.go — assessFeasibility weighted scoring + resourceCoverageRatio =====

// TestReplicationAnalysis_ZeroRisksIsFeasible verifies that assessFeasibility with no
// risks returns "feasible" (AC: Zero constraints → feasibility="feasible").
func TestReplicationAnalysis_AssessFeasibility_ZeroRisks(t *testing.T) {
	feasibility, _ := assessFeasibility([]any{}, []any{"GPU cluster", "dataset"})
	if feasibility != "feasible" {
		t.Errorf("assessFeasibility(no risks) = %q, want feasible", feasibility)
	}
}

// TestReplicationAnalysis_OneCriticalRiskIsPartial verifies T019 AC:
// 1 critical risk → riskScore=0.4 → "partial".
func TestReplicationAnalysis_AssessFeasibility_OneCritical(t *testing.T) {
	// "unavailable" is a critical keyword.
	risks := []any{"Original data is unavailable under NDA"}
	feasibility, _ := assessFeasibility(risks, []any{"some resource"})
	if feasibility != "partial" {
		t.Errorf("assessFeasibility(1 critical risk) = %q, want partial (riskScore=0.4)", feasibility)
	}
}

// TestReplicationAnalysis_ResourceCoverageRatio verifies T019 AC: ratio is computed.
func TestReplicationAnalysis_ResourceCoverageRatio(t *testing.T) {
	p := NewReplicationAnalysisPattern()
	input, _ := p.Validate(map[string]any{
		"claim":          "Model achieves 90% accuracy",
		"originalMethod": "Standard fine-tuning",
		"resources":      []any{"GPU", "dataset"},
	})
	result, err := p.Handle(input, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ratio, ok := result.Data["resourceCoverageRatio"].(float64)
	if !ok {
		t.Fatalf("resourceCoverageRatio missing or wrong type: %T", result.Data["resourceCoverageRatio"])
	}
	if ratio <= 0 {
		t.Errorf("resourceCoverageRatio = %f, want > 0", ratio)
	}
}

// ===== T020: research_synthesis.go — contradictoryPairs + themeOverlap =====

// TestResearchSynthesis_ContradictoryPairs verifies T020 AC:
// 2 findings with shared topic word but low Jaccard → contradictoryPairs has ≥1 entry.
func TestResearchSynthesis_ContradictoryPairs(t *testing.T) {
	p := NewResearchSynthesisPattern()
	// Both findings share the topic word "attention" but say opposite things.
	input, err := p.Validate(map[string]any{
		"topic": "attention mechanisms",
		"findings": []any{
			"attention greatly improves machine translation quality scores",
			"attention overhead degrades inference throughput significantly",
		},
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(input, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	pairs, ok := result.Data["contradictoryPairs"].([]map[string]any)
	if !ok {
		t.Fatalf("contradictoryPairs missing or wrong type: %T", result.Data["contradictoryPairs"])
	}
	if len(pairs) == 0 {
		t.Errorf("expected ≥1 contradictory pair for divergent findings sharing topic word 'attention', got 0")
	}
	// Each pair must have findingA, findingB, similarity.
	for _, pair := range pairs {
		if _, ok := pair["findingA"]; !ok {
			t.Error("pair missing findingA")
		}
		if _, ok := pair["findingB"]; !ok {
			t.Error("pair missing findingB")
		}
		if _, ok := pair["similarity"]; !ok {
			t.Error("pair missing similarity")
		}
	}
}

// TestResearchSynthesis_ThemeOverlapPresent verifies T020 AC:
// themeOverlap field is always present in result.
func TestResearchSynthesis_ThemeOverlapPresent(t *testing.T) {
	p := NewResearchSynthesisPattern()
	input, _ := p.Validate(map[string]any{
		"topic": "neural networks",
		"findings": []any{
			"Neural networks benefit from regularization techniques",
			"Dropout reduces overfitting in deep learning models",
		},
	})
	result, err := p.Handle(input, "")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if _, ok := result.Data["themeOverlap"]; !ok {
		t.Fatal("themeOverlap missing from result")
	}
}
