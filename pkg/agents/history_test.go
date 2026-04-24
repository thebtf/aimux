package agents_test

import (
	"database/sql"
	"testing"

	"github.com/thebtf/aimux/pkg/agents"
	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestDispatchHistory_RecordAndSuccessRate(t *testing.T) {
	db := newTestDB(t)
	h, err := agents.NewDispatchHistory(db)
	if err != nil {
		t.Fatalf("NewDispatchHistory: %v", err)
	}

	// No records → neutral prior.
	if got := h.GetSuccessRate("implementer", "coding"); got != 0.5 {
		t.Errorf("no records: want 0.5, got %f", got)
	}

	// Record 3 successes and 1 failure.
	for i := 0; i < 3; i++ {
		if err := h.Record("implementer", "coding", "success", 1000, "proj1"); err != nil {
			t.Fatalf("Record success: %v", err)
		}
	}
	if err := h.Record("implementer", "coding", "failure", 500, "proj1"); err != nil {
		t.Fatalf("Record failure: %v", err)
	}

	// All records are fresh (age ≈ 0) so decay weight ≈ 1 for each.
	// Expected: 3/4 = 0.75, allow ±0.05 for floating-point.
	rate := h.GetSuccessRate("implementer", "coding")
	if rate < 0.70 || rate > 0.80 {
		t.Errorf("expected ≈0.75 success rate, got %f", rate)
	}
}

func TestDispatchHistory_SeparatesByCategory(t *testing.T) {
	db := newTestDB(t)
	h, err := agents.NewDispatchHistory(db)
	if err != nil {
		t.Fatalf("NewDispatchHistory: %v", err)
	}

	if err := h.Record("implementer", "coding", "success", 1000, ""); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := h.Record("implementer", "review", "failure", 500, ""); err != nil {
		t.Fatalf("Record: %v", err)
	}

	codingRate := h.GetSuccessRate("implementer", "coding")
	reviewRate := h.GetSuccessRate("implementer", "review")

	if codingRate < 0.9 {
		t.Errorf("coding rate should be ~1.0, got %f", codingRate)
	}
	if reviewRate > 0.1 {
		t.Errorf("review rate should be ~0.0, got %f", reviewRate)
	}
}

func TestDispatchHistory_SeparatesByAgent(t *testing.T) {
	db := newTestDB(t)
	h, err := agents.NewDispatchHistory(db)
	if err != nil {
		t.Fatalf("NewDispatchHistory: %v", err)
	}

	if err := h.Record("debugger", "debugging", "success", 1000, ""); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := h.Record("reviewer", "debugging", "failure", 500, ""); err != nil {
		t.Fatalf("Record: %v", err)
	}

	debuggerRate := h.GetSuccessRate("debugger", "debugging")
	reviewerRate := h.GetSuccessRate("reviewer", "debugging")

	if debuggerRate < 0.9 {
		t.Errorf("debugger rate should be ~1.0, got %f", debuggerRate)
	}
	if reviewerRate > 0.1 {
		t.Errorf("reviewer rate should be ~0.0, got %f", reviewerRate)
	}
}

func TestDispatchHistory_AllSuccessReturnsNearOne(t *testing.T) {
	db := newTestDB(t)
	h, err := agents.NewDispatchHistory(db)
	if err != nil {
		t.Fatalf("NewDispatchHistory: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := h.Record("researcher", "research", "success", 2000, "proj"); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	rate := h.GetSuccessRate("researcher", "research")
	if rate < 0.95 {
		t.Errorf("all-success rate should be ~1.0, got %f", rate)
	}
}

func TestDispatchHistory_AllFailuresReturnsNearZero(t *testing.T) {
	db := newTestDB(t)
	h, err := agents.NewDispatchHistory(db)
	if err != nil {
		t.Fatalf("NewDispatchHistory: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := h.Record("researcher", "research", "failure", 100, "proj"); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	rate := h.GetSuccessRate("researcher", "research")
	if rate > 0.05 {
		t.Errorf("all-failure rate should be ~0.0, got %f", rate)
	}
}
