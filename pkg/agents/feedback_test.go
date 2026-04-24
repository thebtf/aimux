package agents_test

import (
	"math"
	"testing"

	"github.com/thebtf/aimux/pkg/agents"
)

func TestFeedbackTracker_AdjustScore_NoHistory(t *testing.T) {
	db := newTestDB(t)
	h, err := agents.NewDispatchHistory(db)
	if err != nil {
		t.Fatalf("NewDispatchHistory: %v", err)
	}
	ft := agents.NewFeedbackTracker(h)

	// No history → success rate = 0.5 (neutral prior).
	// adjusted = 0.7*0.8 + 0.3*0.5 = 0.56 + 0.15 = 0.71
	got := ft.AdjustScore(0.8, "implementer", "coding")
	want := 0.7*0.8 + 0.3*0.5
	if math.Abs(got-want) > 0.001 {
		t.Errorf("AdjustScore(0.8, no history) = %f, want %f", got, want)
	}
}

func TestFeedbackTracker_AdjustScore_HighSuccessRate(t *testing.T) {
	db := newTestDB(t)
	h, err := agents.NewDispatchHistory(db)
	if err != nil {
		t.Fatalf("NewDispatchHistory: %v", err)
	}

	// Record 10 successes — success rate ≈ 1.0.
	for i := 0; i < 10; i++ {
		if err := h.Record("implementer", "coding", "success", 1000, ""); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	ft := agents.NewFeedbackTracker(h)

	got := ft.AdjustScore(0.5, "implementer", "coding")
	// adjusted ≈ 0.7*0.5 + 0.3*1.0 = 0.35 + 0.30 = 0.65
	if got < 0.60 || got > 0.70 {
		t.Errorf("AdjustScore(0.5, high success) = %f, want ≈0.65", got)
	}
}

func TestFeedbackTracker_AdjustScore_LowSuccessRate(t *testing.T) {
	db := newTestDB(t)
	h, err := agents.NewDispatchHistory(db)
	if err != nil {
		t.Fatalf("NewDispatchHistory: %v", err)
	}

	// Record 10 failures — success rate ≈ 0.0.
	for i := 0; i < 10; i++ {
		if err := h.Record("implementer", "coding", "failure", 100, ""); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	ft := agents.NewFeedbackTracker(h)

	got := ft.AdjustScore(0.5, "implementer", "coding")
	// adjusted ≈ 0.7*0.5 + 0.3*0.0 = 0.35; above floor 0.1
	if got < 0.30 || got > 0.40 {
		t.Errorf("AdjustScore(0.5, low success) = %f, want ≈0.35", got)
	}
}

func TestFeedbackTracker_AdjustScore_Floor(t *testing.T) {
	db := newTestDB(t)
	h, err := agents.NewDispatchHistory(db)
	if err != nil {
		t.Fatalf("NewDispatchHistory: %v", err)
	}

	// Record 10 failures — success rate ≈ 0.0.
	for i := 0; i < 10; i++ {
		if err := h.Record("failing-agent", "category", "failure", 100, ""); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	ft := agents.NewFeedbackTracker(h)

	// base = 0 — formula gives 0.7*0 + 0.3*0 = 0, but floor at 0.1.
	got := ft.AdjustScore(0.0, "failing-agent", "category")
	if got < 0.1 {
		t.Errorf("AdjustScore floor violated: got %f, want >= 0.1", got)
	}
}

func TestFeedbackTracker_OnDispatchComplete_RecordsOutcome(t *testing.T) {
	db := newTestDB(t)
	h, err := agents.NewDispatchHistory(db)
	if err != nil {
		t.Fatalf("NewDispatchHistory: %v", err)
	}
	ft := agents.NewFeedbackTracker(h)

	// Before any records, rate is 0.5.
	before := h.GetSuccessRate("reviewer", "review")
	if before != 0.5 {
		t.Errorf("initial rate = %f, want 0.5", before)
	}

	// Record several successes through the tracker.
	for i := 0; i < 5; i++ {
		ft.OnDispatchComplete("reviewer", "review", "success")
	}

	after := h.GetSuccessRate("reviewer", "review")
	if after < 0.9 {
		t.Errorf("after 5 successes, rate = %f, want > 0.9", after)
	}
}

func TestFeedbackTracker_NilHistory(t *testing.T) {
	// FeedbackTracker with nil history must not panic and must honour floor.
	ft := agents.NewFeedbackTracker(nil)

	got := ft.AdjustScore(0.6, "any-agent", "any-category")
	if math.Abs(got-0.6) > 0.001 {
		t.Errorf("nil history: AdjustScore(0.6) = %f, want 0.6 (pass-through)", got)
	}

	// floor
	got2 := ft.AdjustScore(0.0, "any-agent", "any-category")
	if got2 < 0.1 {
		t.Errorf("nil history floor: AdjustScore(0.0) = %f, want >= 0.1", got2)
	}

	// must not panic
	ft.OnDispatchComplete("x", "y", "success")
}
