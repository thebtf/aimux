package think

import (
	"testing"
)

func TestEnforcementGate_UnknownPattern_Complete(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "unknown_pattern", State: map[string]any{}}
	d := g.Check("unknown_pattern", sess)
	if d.Status != "complete" {
		t.Errorf("unknown pattern: want complete, got %q (%s)", d.Status, d.Reason)
	}
}

func TestEnforcementGate_NilSession_Incomplete(t *testing.T) {
	g := NewEnforcementGate()
	d := g.Check("debugging_approach", nil)
	if d.Status != "incomplete" {
		t.Errorf("nil session: want incomplete, got %q", d.Status)
	}
}

// --- debugging_approach: minSteps=3, minEvidence=2 ---

func TestEnforcementGate_DebuggingApproach_Incomplete_NoSteps(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "debugging_approach", State: map[string]any{
		"steps": []any{},
	}}
	d := g.Check("debugging_approach", sess)
	if d.Status != "incomplete" {
		t.Errorf("want incomplete (0 steps), got %q", d.Status)
	}
}

func TestEnforcementGate_DebuggingApproach_Incomplete_InsufficientEvidence(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "debugging_approach", State: map[string]any{
		"steps":    []any{"s1", "s2", "s3"},
		"evidence": []any{"e1"},
	}}
	d := g.Check("debugging_approach", sess)
	if d.Status != "incomplete" {
		t.Errorf("want incomplete (1 evidence), got %q", d.Status)
	}
}

func TestEnforcementGate_DebuggingApproach_Complete(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "debugging_approach", State: map[string]any{
		"steps":    []any{"s1", "s2", "s3"},
		"evidence": []any{"e1", "e2"},
	}}
	d := g.Check("debugging_approach", sess)
	if d.Status != "complete" {
		t.Errorf("want complete, got %q: %s", d.Status, d.Reason)
	}
}

// --- scientific_method: 5 stages required ---

func TestEnforcementGate_ScientificMethod_Incomplete_MissingStages(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "scientific_method", State: map[string]any{
		"stageHistory": []any{"observation", "hypothesis"},
	}}
	d := g.Check("scientific_method", sess)
	if d.Status != "incomplete" {
		t.Errorf("want incomplete, got %q", d.Status)
	}
}

func TestEnforcementGate_ScientificMethod_Complete(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "scientific_method", State: map[string]any{
		"stageHistory": []any{"observation", "hypothesis", "prediction", "experiment", "analysis"},
	}}
	d := g.Check("scientific_method", sess)
	if d.Status != "complete" {
		t.Errorf("want complete, got %q: %s", d.Status, d.Reason)
	}
}

// --- decision_framework: all criteria must have scores ---

func TestEnforcementGate_DecisionFramework_Incomplete_NoCriteria(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "decision_framework", State: map[string]any{}}
	d := g.Check("decision_framework", sess)
	if d.Status != "incomplete" {
		t.Errorf("want incomplete (no criteria), got %q", d.Status)
	}
}

func TestEnforcementGate_DecisionFramework_Incomplete_UnscoredCriterion(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "decision_framework", State: map[string]any{
		"criteria": []any{
			map[string]any{"name": "cost", "score": 3},
			map[string]any{"name": "speed"},
		},
	}}
	d := g.Check("decision_framework", sess)
	if d.Status != "incomplete" {
		t.Errorf("want incomplete (unscored criterion), got %q", d.Status)
	}
}

func TestEnforcementGate_DecisionFramework_Complete(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "decision_framework", State: map[string]any{
		"criteria": []any{
			map[string]any{"name": "cost", "score": 3},
			map[string]any{"name": "speed", "score": 5},
		},
	}}
	d := g.Check("decision_framework", sess)
	if d.Status != "complete" {
		t.Errorf("want complete, got %q: %s", d.Status, d.Reason)
	}
}

// --- source_comparison: 3 sources minimum ---

func TestEnforcementGate_SourceComparison_Incomplete(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "source_comparison", State: map[string]any{
		"sources": []any{"s1", "s2"},
	}}
	d := g.Check("source_comparison", sess)
	if d.Status != "incomplete" {
		t.Errorf("want incomplete (2 sources), got %q", d.Status)
	}
}

func TestEnforcementGate_SourceComparison_Complete(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "source_comparison", State: map[string]any{
		"sources": []any{"s1", "s2", "s3"},
	}}
	d := g.Check("source_comparison", sess)
	if d.Status != "complete" {
		t.Errorf("want complete, got %q: %s", d.Status, d.Reason)
	}
}

// --- sequential_thinking: convergence check ---

func TestEnforcementGate_SequentialThinking_Incomplete_NoConvergence(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "sequential_thinking", State: map[string]any{
		"thoughts": []any{"t1"},
	}}
	d := g.Check("sequential_thinking", sess)
	if d.Status != "incomplete" {
		t.Errorf("want incomplete (no convergence), got %q", d.Status)
	}
}

func TestEnforcementGate_SequentialThinking_Complete(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "sequential_thinking", State: map[string]any{
		"thoughts":    []any{"t1"},
		"convergence": true,
	}}
	d := g.Check("sequential_thinking", sess)
	if d.Status != "complete" {
		t.Errorf("want complete, got %q: %s", d.Status, d.Reason)
	}
}

// --- experimental_loop: minIterations=1 ---

func TestEnforcementGate_ExperimentalLoop_Incomplete(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "experimental_loop", State: map[string]any{
		"iteration": 0,
	}}
	d := g.Check("experimental_loop", sess)
	if d.Status != "incomplete" {
		t.Errorf("want incomplete (0 iterations), got %q", d.Status)
	}
}

func TestEnforcementGate_ExperimentalLoop_Complete(t *testing.T) {
	g := NewEnforcementGate()
	sess := &ThinkSession{ID: "s1", Pattern: "experimental_loop", State: map[string]any{
		"iteration": 1,
	}}
	d := g.Check("experimental_loop", sess)
	if d.Status != "complete" {
		t.Errorf("want complete, got %q: %s", d.Status, d.Reason)
	}
}
