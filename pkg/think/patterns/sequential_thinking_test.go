package patterns

import (
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
)

// TestSequential_LinearChain verifies that three thoughts processed in sequence
// accumulate correctly (totalInSession) and that stage progresses through
// initial → middle → final as thoughtNumber advances.
func TestSequential_LinearChain(t *testing.T) {
	think.ClearSessions()
	p := NewSequentialThinkingPattern()
	sid := "seq-linear-1"

	steps := []struct {
		thoughtNumber int
		wantStage     string
		wantTotal     int
	}{
		{1, "initial", 1},
		{2, "middle", 2},
		{3, "final", 3},
	}

	for _, step := range steps {
		input, err := p.Validate(map[string]any{
			"thought":       "some reasoning thought",
			"thoughtNumber": step.thoughtNumber,
			"totalThoughts": 3,
		})
		if err != nil {
			t.Fatalf("validate thought %d: %v", step.thoughtNumber, err)
		}

		r, err := p.Handle(input, sid)
		if err != nil {
			t.Fatalf("handle thought %d: %v", step.thoughtNumber, err)
		}

		if r.Data["totalInSession"] != step.wantTotal {
			t.Errorf("thought %d: totalInSession = %v, want %d",
				step.thoughtNumber, r.Data["totalInSession"], step.wantTotal)
		}
		if r.Data["stage"] != step.wantStage {
			t.Errorf("thought %d: stage = %v, want %s",
				step.thoughtNumber, r.Data["stage"], step.wantStage)
		}
	}
}

// TestSequential_Branch verifies that providing a branchId causes hasBranches
// to be reported as true in the result.
func TestSequential_Branch(t *testing.T) {
	think.ClearSessions()
	p := NewSequentialThinkingPattern()
	sid := "seq-branch-2"

	input, err := p.Validate(map[string]any{
		"thought":           "alternative approach via caching",
		"branchId":          "cache-path",
		"branchFromThought": 1,
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	r, err := p.Handle(input, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	if r.Data["hasBranches"] != true {
		t.Errorf("hasBranches = %v, want true", r.Data["hasBranches"])
	}
}

// TestSequential_Contradiction verifies contradiction detection: when a second
// thought shares significant word overlap with an earlier thought (Jaccard > 0.6)
// AND contains a negation word, contradictionDetected must be true.
//
// Craft: T1 establishes words; T2 is an isRevision thought with the same core
// words plus a negation word so Jaccard(T1, T2) > 0.6.
func TestSequential_Contradiction(t *testing.T) {
	think.ClearSessions()
	p := NewSequentialThinkingPattern()
	sid := "seq-contra-1"

	// T1: establishes the base claim.
	// Word set: {the, cache, reduces, latency, for, all, user, requests} = 8 words.
	t1, err := p.Validate(map[string]any{
		"thought":       "the cache reduces latency for all user requests",
		"thoughtNumber": 1,
		"totalThoughts": 2,
	})
	if err != nil {
		t.Fatalf("validate T1: %v", err)
	}
	_, err = p.Handle(t1, sid)
	if err != nil {
		t.Fatalf("handle T1: %v", err)
	}

	// T2: refutes the claim — same core words + negation "not" + isRevision=true.
	// Word set: {the, cache, does, not, reduce, latency, for, all, user, requests} = 10 words.
	// Shared: {the, cache, latency, for, all, user, requests} = 7 words.
	// Union = 8 + 10 - 7 = 11. Jaccard = 7/11 ≈ 0.636 > 0.6 → contradiction triggered.
	t2, err := p.Validate(map[string]any{
		"thought":        "the cache does not reduce latency for all user requests",
		"thoughtNumber":  2,
		"totalThoughts":  2,
		"isRevision":     true,
		"revisesThought": 1,
	})
	if err != nil {
		t.Fatalf("validate T2: %v", err)
	}

	r, err := p.Handle(t2, sid)
	if err != nil {
		t.Fatalf("handle T2: %v", err)
	}

	if r.Data["contradictionDetected"] != true {
		t.Errorf("contradictionDetected = %v, want true", r.Data["contradictionDetected"])
	}
	if r.Data["contradictsWith"] != 1 {
		t.Errorf("contradictsWith = %v, want 1", r.Data["contradictsWith"])
	}
}

// TestSequential_StepNumber: step_number appears in output data when provided.
func TestSequential_StepNumber(t *testing.T) {
	think.ClearSessions()
	p := NewSequentialThinkingPattern()
	sid := "seq-stepnum-1"

	inp, err := p.Validate(map[string]any{
		"thought":       "analyzing the problem space",
		"thoughtNumber": 1,
		"totalThoughts": 3,
		"step_number":   float64(2),
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if inp["step_number"] != 2 {
		t.Fatalf("expected step_number=2 in validated input, got %v", inp["step_number"])
	}

	r, err := p.Handle(inp, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["step_number"] != 2 {
		t.Fatalf("expected step_number=2 in output data, got %v", r.Data["step_number"])
	}
}
