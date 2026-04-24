package think

import (
	"testing"
)

func TestAdvisor_NilInputs(t *testing.T) {
	a := NewPatternAdvisor()

	r := a.Evaluate(nil, &ThinkResult{Pattern: "think", Data: map[string]any{}})
	if r.Action != "continue" {
		t.Errorf("nil session: want continue, got %q", r.Action)
	}

	sess := &ThinkSession{ID: "s1", Pattern: "think", State: map[string]any{}}
	r = a.Evaluate(sess, nil)
	if r.Action != "continue" {
		t.Errorf("nil result: want continue, got %q", r.Action)
	}
}

func TestAdvisor_DefaultContinue(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	a := NewPatternAdvisor()
	sess := GetOrCreateSession("s1", "think", map[string]any{})
	result := &ThinkResult{
		Pattern: "think",
		Summary: "some thought",
		Data:    map[string]any{"thought": "reasoning about problem X"},
	}

	rec := a.Evaluate(sess, result)
	if rec.Action != "continue" {
		t.Errorf("first call: want continue, got %q (%s)", rec.Action, rec.Reason)
	}
}

func TestAdvisor_MaxSwitches_AlwaysContinue(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	a := NewPatternAdvisor()
	GetOrCreateSession("s1", "think", map[string]any{
		"_advisorSwitches": maxSwitches,
	})
	sess := GetSession("s1")

	result := &ThinkResult{
		Pattern: "think",
		Data:    map[string]any{"thought": "x"},
	}
	rec := a.Evaluate(sess, result)
	if rec.Action != "continue" {
		t.Errorf("max switches reached: want continue, got %q", rec.Action)
	}
}

func TestAdvisor_ConvergenceTriggersSwitch(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	// Register a minimal set of fake patterns so suggestAlternative has candidates.
	RegisterPattern(&fakeAdvisorPattern{name: "debugging_approach", desc: "debugging bug hypothesis"})
	RegisterPattern(&fakeAdvisorPattern{name: "scientific_method", desc: "hypothesis experiment observation analysis"})
	defer ClearPatterns()

	a := NewPatternAdvisor()
	GetOrCreateSession("s1", "sequential_thinking", map[string]any{})

	// Identical summary repeated 3 times should exceed Jaccard threshold.
	repeatedSummary := "reasoning about the bug in the authentication module service"

	for i := 0; i < 3; i++ {
		sess := GetSession("s1")
		result := &ThinkResult{
			Pattern: "sequential_thinking",
			Summary: repeatedSummary,
			Data:    map[string]any{"thought": repeatedSummary},
		}
		rec := a.Evaluate(sess, result)
		// Apply the state patch so history accumulates across iterations.
		if rec.StatePatch != nil {
			UpdateSessionState("s1", rec.StatePatch)
		}
		if i < 2 {
			// First two calls: history not yet 3 long → can't conclude switch
			_ = rec
		} else {
			// Third call: should detect convergence
			if rec.Action != "switch" {
				t.Errorf("call %d: want switch on convergence, got %q (%s)", i+1, rec.Action, rec.Reason)
			}
		}
	}
}

func TestAdvisor_NoConvergence_WithDifferentResults(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	a := NewPatternAdvisor()
	GetOrCreateSession("s1", "think", map[string]any{})

	summaries := []string{
		"reasoning about authentication design patterns and security protocols",
		"analyzing database query performance bottlenecks and indexing strategies",
		"evaluating distributed cache consistency models and trade-offs",
	}

	for i, summary := range summaries {
		sess := GetSession("s1")
		result := &ThinkResult{
			Pattern: "think",
			Summary: summary,
			Data:    map[string]any{"thought": summary},
		}
		rec := a.Evaluate(sess, result)
		// Apply the state patch so history accumulates across iterations.
		if rec.StatePatch != nil {
			UpdateSessionState("s1", rec.StatePatch)
		}
		if i == 2 && rec.Action == "switch" {
			// Unlikely with diverse summaries — verify reason is NOT convergence
			if rec.Reason == "last 3 results are too similar (convergence stall); try a different approach" {
				t.Errorf("diverse results incorrectly flagged as converged")
			}
		}
	}
}

func TestJaccardConverged_Identical(t *testing.T) {
	h := []string{"alpha beta gamma", "alpha beta gamma", "alpha beta gamma"}
	if !jaccardConverged(h, 0.8) {
		t.Error("identical strings should be converged")
	}
}

func TestJaccardConverged_Diverse(t *testing.T) {
	h := []string{"alpha beta gamma", "delta epsilon zeta", "eta theta iota"}
	if jaccardConverged(h, 0.8) {
		t.Error("diverse strings should not be converged")
	}
}

func TestJaccard_FullOverlap(t *testing.T) {
	a := tokenSet("foo bar baz")
	b := tokenSet("foo bar baz")
	if j := jaccard(a, b); j != 1.0 {
		t.Errorf("full overlap: want 1.0, got %f", j)
	}
}

func TestJaccard_NoOverlap(t *testing.T) {
	a := tokenSet("foo bar")
	b := tokenSet("baz qux")
	if j := jaccard(a, b); j != 0.0 {
		t.Errorf("no overlap: want 0.0, got %f", j)
	}
}

func TestRecordSwitch(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	GetOrCreateSession("s1", "think", map[string]any{})
	sess := GetSession("s1")

	RecordSwitch(sess)
	updated := GetSession("s1")
	if count := switchCountFromState(updated.State); count != 1 {
		t.Errorf("after RecordSwitch: want 1, got %d", count)
	}
}

// fakeAdvisorPattern is a minimal PatternHandler for advisor tests that need
// registered patterns without importing think/patterns (import cycle).
type fakeAdvisorPattern struct {
	name string
	desc string
}

func (f *fakeAdvisorPattern) Name() string        { return f.name }
func (f *fakeAdvisorPattern) Description() string { return f.desc }
func (f *fakeAdvisorPattern) Category() string    { return "test" }
func (f *fakeAdvisorPattern) SchemaFields() map[string]FieldSchema {
	return map[string]FieldSchema{}
}
func (f *fakeAdvisorPattern) Validate(input map[string]any) (map[string]any, error) {
	return input, nil
}
func (f *fakeAdvisorPattern) Handle(validInput map[string]any, sessionID string) (*ThinkResult, error) {
	return MakeThinkResult(f.name, map[string]any{}, sessionID, nil, "", nil), nil
}
