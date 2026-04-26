package patterns

import (
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
)

// --- source_comparison ---

func TestSourceComparison_ValidateSuccess(t *testing.T) {
	p := NewSourceComparisonPattern()
	out, err := p.Validate(map[string]any{
		"topic":   "climate change",
		"sources": []any{"IPCC", "NASA"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["topic"] != "climate change" {
		t.Fatalf("expected topic='climate change', got %v", out["topic"])
	}
}

func TestSourceComparison_ValidateFailure(t *testing.T) {
	p := NewSourceComparisonPattern()

	_, err := p.Validate(map[string]any{"sources": []any{"A", "B"}})
	if err == nil {
		t.Fatal("expected error for missing topic")
	}

	_, err = p.Validate(map[string]any{"topic": "x", "sources": []any{"only one"}})
	if err == nil {
		t.Fatal("expected error for fewer than 2 sources")
	}

	_, err = p.Validate(map[string]any{"topic": "x"})
	if err == nil {
		t.Fatal("expected error for missing sources")
	}
}

func TestSourceComparison_TwoSources(t *testing.T) {
	p := NewSourceComparisonPattern()
	input, err := p.Validate(map[string]any{
		"topic": "LLM accuracy",
		"sources": []any{
			map[string]any{"name": "Paper A", "claim": "quantum computing enables exponential speedup"},
			map[string]any{"name": "Paper B", "claim": "traditional CPUs remain faster for most workloads"},
		},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	r, err := p.Handle(input, "sc-test-1")
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	if r.Data["sourceCount"] != 2 {
		t.Fatalf("expected sourceCount=2, got %v", r.Data["sourceCount"])
	}
	matrix, ok := r.Data["comparisonMatrix"].([]map[string]any)
	if !ok || len(matrix) != 1 {
		t.Fatalf("expected 1 matrix entry, got %v", r.Data["comparisonMatrix"])
	}
	if matrix[0]["agreement"] != "disagree" {
		t.Fatalf("expected disagreement between different claims, got %v", matrix[0]["agreement"])
	}
	if r.Data["overallConsensus"] != 0.0 {
		t.Fatalf("expected 0%% consensus, got %v", r.Data["overallConsensus"])
	}
}

func TestSourceComparison_AgreeingSources(t *testing.T) {
	p := NewSourceComparisonPattern()
	input, _ := p.Validate(map[string]any{
		"topic": "gravity",
		"sources": []any{
			map[string]any{"name": "Newton", "claim": "objects attract"},
			map[string]any{"name": "Einstein", "claim": "objects attract"},
		},
	})
	r, err := p.Handle(input, "sc-agree-1")
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["overallConsensus"] != 100.0 {
		t.Fatalf("expected 100%% consensus, got %v", r.Data["overallConsensus"])
	}
}

// --- literature_review ---

func TestLiteratureReview_ValidateSuccess(t *testing.T) {
	p := NewLiteratureReviewPattern()
	out, err := p.Validate(map[string]any{"topic": "neural networks"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["topic"] != "neural networks" {
		t.Fatalf("expected topic='neural networks', got %v", out["topic"])
	}
}

func TestLiteratureReview_ValidateFailure(t *testing.T) {
	p := NewLiteratureReviewPattern()
	_, err := p.Validate(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing topic")
	}
}

func TestLiteratureReview_WithPapers(t *testing.T) {
	p := NewLiteratureReviewPattern()
	input, _ := p.Validate(map[string]any{
		"topic":  "transformer models",
		"papers": []any{"Attention Is All You Need", "BERT: Pre-training"},
	})
	r, err := p.Handle(input, "lr-test-1")
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["paperCount"] != 2 {
		t.Fatalf("expected paperCount=2, got %v", r.Data["paperCount"])
	}
	if r.SuggestedNextPattern != "source_comparison" {
		t.Fatalf("expected suggestedNext=source_comparison, got %s", r.SuggestedNextPattern)
	}
	if _, ok := r.Data["themes"]; !ok {
		t.Fatal("expected 'themes' in result data")
	}
}

func TestLiteratureReview_WithoutPapers(t *testing.T) {
	p := NewLiteratureReviewPattern()
	input, _ := p.Validate(map[string]any{"topic": "quantum computing"})
	r, err := p.Handle(input, "lr-empty-1")
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["paperCount"] != 0 {
		t.Fatalf("expected paperCount=0, got %v", r.Data["paperCount"])
	}
	gaps, ok := r.Data["identifiedGaps"].([]string)
	if !ok || len(gaps) == 0 {
		t.Fatal("expected non-empty identifiedGaps when no papers provided")
	}
}

// --- peer_review ---

func TestPeerReview_ValidateSuccess(t *testing.T) {
	p := NewPeerReviewPattern()
	out, err := p.Validate(map[string]any{"artifact": "A new sorting algorithm"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["artifact"] != "A new sorting algorithm" {
		t.Fatalf("expected artifact, got %v", out["artifact"])
	}
}

func TestPeerReview_ValidateFailure(t *testing.T) {
	p := NewPeerReviewPattern()
	_, err := p.Validate(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing artifact")
	}
	_, err = p.Validate(map[string]any{"artifact": ""})
	if err == nil {
		t.Fatal("expected error for empty artifact")
	}
}

func TestPeerReview_ReturnsVerdict(t *testing.T) {
	p := NewPeerReviewPattern()
	input, _ := p.Validate(map[string]any{
		"artifact":    "Federated learning paper",
		"claims":      []any{"Privacy is preserved", "Accuracy matches centralized training"},
		"methodology": "Differential privacy with epsilon=0.1",
		"novelty":     "First application to on-device NLP",
	})
	r, err := p.Handle(input, "pr-test-1")
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	verdict, ok := r.Data["reviewVerdict"].(string)
	if !ok || verdict == "" {
		t.Fatalf("expected non-empty reviewVerdict, got %v", r.Data["reviewVerdict"])
	}
	validVerdicts := map[string]bool{
		"accept": true, "minor_revision": true, "major_revision": true, "reject": true,
	}
	if !validVerdicts[verdict] {
		t.Fatalf("unexpected verdict: %s", verdict)
	}
	if _, ok := r.Data["objections"]; !ok {
		t.Fatal("expected objections in result")
	}
	if _, ok := r.Data["revisionPlan"]; !ok {
		t.Fatal("expected revisionPlan in result")
	}
	if _, ok := r.Data["strengths"]; !ok {
		t.Fatal("expected strengths in result")
	}
}

func TestPeerReview_MinimalInput(t *testing.T) {
	p := NewPeerReviewPattern()
	input, _ := p.Validate(map[string]any{"artifact": "A claim with no supporting detail"})
	r, err := p.Handle(input, "pr-minimal-1")
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	// Minimal input should yield at least minor or major revision.
	verdict, _ := r.Data["reviewVerdict"].(string)
	if verdict == "accept" {
		t.Fatal("expected revision verdict for minimal artifact, got accept")
	}
}

// --- replication_analysis ---

func TestReplicationAnalysis_ValidateSuccess(t *testing.T) {
	p := NewReplicationAnalysisPattern()
	out, err := p.Validate(map[string]any{"claim": "LLMs pass bar exam"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["claim"] != "LLMs pass bar exam" {
		t.Fatalf("expected claim, got %v", out["claim"])
	}
}

func TestReplicationAnalysis_ValidateFailure(t *testing.T) {
	p := NewReplicationAnalysisPattern()
	_, err := p.Validate(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing claim")
	}
}

func TestReplicationAnalysis_Handle(t *testing.T) {
	p := NewReplicationAnalysisPattern()
	input, _ := p.Validate(map[string]any{
		"claim":          "Model X achieves 95% accuracy on benchmark Y",
		"originalMethod": "Standard fine-tuning with AdamW",
		"resources":      []any{"GPU cluster", "original dataset"},
	})
	r, err := p.Handle(input, "ra-test-1")
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	feasibility, ok := r.Data["replicationFeasibility"].(string)
	if !ok || feasibility == "" {
		t.Fatalf("expected replicationFeasibility, got %v", r.Data["replicationFeasibility"])
	}
	validFeasibility := map[string]bool{"feasible": true, "partial": true, "infeasible": true}
	if !validFeasibility[feasibility] {
		t.Fatalf("unexpected feasibility: %s", feasibility)
	}

	effort, ok := r.Data["estimatedEffort"].(string)
	if !ok || effort == "" {
		t.Fatalf("expected estimatedEffort, got %v", r.Data["estimatedEffort"])
	}

	if _, ok := r.Data["requirements"]; !ok {
		t.Fatal("expected requirements in result")
	}
	if _, ok := r.Data["risks"]; !ok {
		t.Fatal("expected risks in result")
	}
}

// --- experimental_loop ---

func TestExperimentalLoop_ValidateSuccess(t *testing.T) {
	p := NewExperimentalLoopPattern()
	out, err := p.Validate(map[string]any{
		"hypothesis": "Larger batch size improves throughput",
		"metric":     128.5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["hypothesis"] != "Larger batch size improves throughput" {
		t.Fatalf("expected hypothesis, got %v", out["hypothesis"])
	}
}

func TestExperimentalLoop_ValidateFailure(t *testing.T) {
	p := NewExperimentalLoopPattern()
	_, err := p.Validate(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing hypothesis")
	}
	_, err = p.Validate(map[string]any{"hypothesis": ""})
	if err == nil {
		t.Fatal("expected error for empty hypothesis")
	}
}

func TestExperimentalLoop_StatefulTwoCalls(t *testing.T) {
	think.ClearSessions()
	p := NewExperimentalLoopPattern()
	sid := "el-stateful-1"

	// First call — baseline experiment.
	input1, _ := p.Validate(map[string]any{
		"hypothesis":  "Batch size 32 is optimal",
		"observation": "Training stable",
		"result":      "accuracy=0.82",
		"metric":      0.82,
	})
	r1, err := p.Handle(input1, sid)
	if err != nil {
		t.Fatalf("handle 1: %v", err)
	}
	if r1.Data["experimentCount"] != 1 {
		t.Fatalf("expected experimentCount=1, got %v", r1.Data["experimentCount"])
	}
	if r1.Data["isImprovement"] != true {
		t.Fatalf("expected first experiment to be an improvement over 0.0")
	}
	if r1.SuggestedNextPattern != "experimental_loop" {
		t.Fatalf("expected suggestedNext=experimental_loop, got %s", r1.SuggestedNextPattern)
	}

	// Second call — improved metric.
	input2, _ := p.Validate(map[string]any{
		"hypothesis":  "Batch size 64 is better",
		"observation": "Training faster",
		"result":      "accuracy=0.91",
		"metric":      0.91,
	})
	r2, err := p.Handle(input2, sid)
	if err != nil {
		t.Fatalf("handle 2: %v", err)
	}
	if r2.Data["experimentCount"] != 2 {
		t.Fatalf("expected experimentCount=2, got %v", r2.Data["experimentCount"])
	}
	if r2.Data["isImprovement"] != true {
		t.Fatalf("expected second experiment to improve over 0.82")
	}
	if r2.Data["bestMetric"] != 0.91 {
		t.Fatalf("expected bestMetric=0.91, got %v", r2.Data["bestMetric"])
	}
}

func TestExperimentalLoop_NoImprovementPivot(t *testing.T) {
	think.ClearSessions()
	p := NewExperimentalLoopPattern()
	sid := "el-pivot-1"

	// Seed with a good baseline.
	input1, _ := p.Validate(map[string]any{"hypothesis": "baseline", "metric": 0.9})
	_, err := p.Handle(input1, sid)
	if err != nil {
		t.Fatalf("handle 1: %v", err)
	}

	// Three subsequent calls with no improvement.
	for i := 2; i <= 4; i++ {
		input, _ := p.Validate(map[string]any{
			"hypothesis": "no improvement experiment",
			"metric":     0.5,
		})
		r, err := p.Handle(input, sid)
		if err != nil {
			t.Fatalf("handle %d: %v", i, err)
		}
		if i == 4 {
			action, _ := r.Data["suggestedAction"].(string)
			if action != "pivot" {
				t.Fatalf("expected suggestedAction=pivot after 3 non-improving experiments, got %s", action)
			}
		}
	}
}

// --- research_synthesis ---

func TestResearchSynthesis_ValidateSuccess(t *testing.T) {
	p := NewResearchSynthesisPattern()
	out, err := p.Validate(map[string]any{
		"topic":    "attention mechanisms",
		"findings": []any{"Attention improves translation", "Attention helps summarization"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["topic"] != "attention mechanisms" {
		t.Fatalf("expected topic, got %v", out["topic"])
	}
}

func TestResearchSynthesis_ValidateFailure(t *testing.T) {
	p := NewResearchSynthesisPattern()
	_, err := p.Validate(map[string]any{"findings": []any{"f1"}})
	if err == nil {
		t.Fatal("expected error for missing topic")
	}
	_, err = p.Validate(map[string]any{"topic": "x"})
	if err == nil {
		t.Fatal("expected error for missing findings")
	}
	_, err = p.Validate(map[string]any{"topic": "x", "findings": []any{}})
	if err == nil {
		t.Fatal("expected error for empty findings list")
	}
	_, err = p.Validate(map[string]any{"topic": "x", "findings": []any{42}})
	if err == nil {
		t.Fatal("expected error for non-string finding")
	}
}

func TestResearchSynthesis_ClaimGrouping(t *testing.T) {
	p := NewResearchSynthesisPattern()
	input, _ := p.Validate(map[string]any{
		"topic": "neural network training",
		"findings": []any{
			"Neural networks benefit from regularization",
			"Neural networks require large datasets",
			"Training stability improves with learning rate schedules",
		},
	})
	r, err := p.Handle(input, "rs-test-1")
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	if r.Data["findingCount"] != 3 {
		t.Fatalf("expected findingCount=3, got %v", r.Data["findingCount"])
	}
	claims, ok := r.Data["synthesizedClaims"].([]map[string]any)
	if !ok || len(claims) == 0 {
		t.Fatalf("expected non-empty synthesizedClaims, got %v", r.Data["synthesizedClaims"])
	}
	// Each claim must have required fields.
	for _, c := range claims {
		if _, ok := c["claim"]; !ok {
			t.Fatal("expected 'claim' in synthesized claim")
		}
		if _, ok := c["supportingFindings"]; !ok {
			t.Fatal("expected 'supportingFindings' in synthesized claim")
		}
		if _, ok := c["confidenceLevel"]; !ok {
			t.Fatal("expected 'confidenceLevel' in synthesized claim")
		}
	}
	if _, ok := r.Data["overallConclusion"]; !ok {
		t.Fatal("expected overallConclusion in result")
	}
	if _, ok := r.Data["openQuestions"]; !ok {
		t.Fatal("expected openQuestions in result")
	}
}
