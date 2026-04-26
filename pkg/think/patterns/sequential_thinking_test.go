package patterns

import (
	"fmt"
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

// TestSequential_BranchAccumulates verifies that multiple calls with the same
// branchId accumulate entries in a slice rather than overwriting.
// branchFromThought must be set for branch tracking to activate (matches TS v1).
func TestSequential_BranchAccumulates(t *testing.T) {
	think.ClearSessions()
	p := NewSequentialThinkingPattern()
	sid := "seq-branch-accum-1"

	for i := 1; i <= 3; i++ {
		input, err := p.Validate(map[string]any{
			"thought":           fmt.Sprintf("branch thought %d", i),
			"thoughtNumber":     i,
			"totalThoughts":     5,
			"branchId":          "alt-path",
			"branchFromThought": 1,
		})
		if err != nil {
			t.Fatalf("validate thought %d: %v", i, err)
		}
		r, err := p.Handle(input, sid)
		if err != nil {
			t.Fatalf("handle thought %d: %v", i, err)
		}
		// After each call the branch count must be 1 (one branch ID, growing slice).
		if r.Data["branchCount"] != 1 {
			t.Errorf("thought %d: branchCount = %v, want 1", i, r.Data["branchCount"])
		}
	}

	// Verify the branch slice grew to 3 by inspecting the session state directly.
	sess := think.GetOrCreateSession(sid, "sequential_thinking", nil)
	branches, _ := sess.State["branches"].(map[string]any)
	entries, _ := branches["alt-path"].([]any)
	if len(entries) != 3 {
		t.Errorf("branch 'alt-path' has %d entries, want 3", len(entries))
	}
}

// TestSequential_BranchIdWithoutBranchFrom verifies that branchId alone (without
// branchFromThought) does NOT register a branch entry, matching TS v1 behaviour.
func TestSequential_BranchIdWithoutBranchFrom(t *testing.T) {
	think.ClearSessions()
	p := NewSequentialThinkingPattern()
	sid := "seq-branch-nofrom-1"

	input, err := p.Validate(map[string]any{
		"thought":  "thought with branchId but no branchFromThought",
		"branchId": "orphan-branch",
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	r, err := p.Handle(input, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["hasBranches"] != false {
		t.Errorf("hasBranches = %v, want false (branchFromThought not set)", r.Data["hasBranches"])
	}
	if r.Data["branchCount"] != 0 {
		t.Errorf("branchCount = %v, want 0", r.Data["branchCount"])
	}
}

// TestSequential_NextThoughtNeeded verifies that nextThoughtNeeded is present
// in the output and reflects whether more thoughts remain.
func TestSequential_NextThoughtNeeded(t *testing.T) {
	think.ClearSessions()
	p := NewSequentialThinkingPattern()
	sid := "seq-ntn-1"

	cases := []struct {
		thoughtNumber int
		wantNTN       bool
	}{
		{1, true},  // more thoughts remain
		{2, true},  // more thoughts remain
		{3, false}, // last thought
	}

	for _, tc := range cases {
		input, err := p.Validate(map[string]any{
			"thought":       "a step",
			"thoughtNumber": tc.thoughtNumber,
			"totalThoughts": 3,
		})
		if err != nil {
			t.Fatalf("validate thought %d: %v", tc.thoughtNumber, err)
		}
		r, err := p.Handle(input, sid)
		if err != nil {
			t.Fatalf("handle thought %d: %v", tc.thoughtNumber, err)
		}
		if r.Data["nextThoughtNeeded"] != tc.wantNTN {
			t.Errorf("thought %d: nextThoughtNeeded = %v, want %v",
				tc.thoughtNumber, r.Data["nextThoughtNeeded"], tc.wantNTN)
		}
	}
}

// TestSequential_BranchCount verifies that branchCount is a numeric count (not
// just the hasBranches bool present in earlier versions).
func TestSequential_BranchCount(t *testing.T) {
	think.ClearSessions()
	p := NewSequentialThinkingPattern()
	sid := "seq-bcount-1"

	// Add two distinct branch IDs.
	for _, bid := range []string{"branch-a", "branch-b"} {
		input, err := p.Validate(map[string]any{
			"thought":           "branching thought",
			"branchId":          bid,
			"branchFromThought": 1,
		})
		if err != nil {
			t.Fatalf("validate branch %s: %v", bid, err)
		}
		if _, err = p.Handle(input, sid); err != nil {
			t.Fatalf("handle branch %s: %v", bid, err)
		}
	}

	// Third call — no branch — just read back branchCount.
	input, err := p.Validate(map[string]any{"thought": "regular thought"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	r, err := p.Handle(input, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["branchCount"] != 2 {
		t.Errorf("branchCount = %v, want 2", r.Data["branchCount"])
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
