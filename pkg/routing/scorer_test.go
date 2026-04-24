package routing_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/routing"
)

// scoreText is a simple Scorable wrapper for tests.
type scoreText string

func (s scoreText) ScoreText() string { return string(s) }

func TestBM25Score_ExactMatchScoresHigherThanPartial(t *testing.T) {
	s := routing.NewBM25Scorer()

	exact := s.Score("Go routing", "Go routing engine for CLI tools")
	partial := s.Score("Go routing", "CLI tool dispatcher")

	if exact <= partial {
		t.Errorf("exact match score (%f) should be > partial match score (%f)", exact, partial)
	}
}

func TestBM25Rank_SecurityReviewerRanksAboveCodeReviewer(t *testing.T) {
	s := routing.NewBM25Scorer()

	items := []routing.Scorable{
		scoreText("code-reviewer: reviews Go code for quality and style"),
		scoreText("security-reviewer: reviews Go code for security vulnerabilities"),
	}

	ranked := s.Rank("review Go code for security", items)

	if len(ranked) < 2 {
		t.Fatalf("expected at least 2 ranked items, got %d", len(ranked))
	}

	// Index 1 = security-reviewer, Index 0 = code-reviewer.
	if ranked[0].Index != 1 {
		t.Errorf("expected security-reviewer (index 1) to rank first, got index %d (score %f vs %f)",
			ranked[0].Index, ranked[0].Score, ranked[1].Score)
	}
}

func TestBM25Rank_EmptyQueryReturnsEmptyList(t *testing.T) {
	s := routing.NewBM25Scorer()

	items := []routing.Scorable{
		scoreText("some description"),
		scoreText("another description"),
	}

	ranked := s.Rank("", items)
	if len(ranked) != 0 {
		t.Errorf("expected empty ranked list for empty query, got %d items", len(ranked))
	}
}

func TestBM25Score_ZeroForCompletelyUnrelatedText(t *testing.T) {
	s := routing.NewBM25Scorer()

	score := s.Score("security vulnerability scanner", "banana smoothie recipe blender")
	if score != 0 {
		t.Errorf("expected score 0 for unrelated text, got %f", score)
	}
}

func TestBM25Rank_OnlyPositiveScoresReturned(t *testing.T) {
	s := routing.NewBM25Scorer()

	items := []routing.Scorable{
		scoreText("completely unrelated fruit salad"),
		scoreText("Go code review security analysis"),
		scoreText("random unrelated words here"),
	}

	ranked := s.Rank("security code review", items)
	for _, r := range ranked {
		if r.Score <= 0 {
			t.Errorf("ranked item at index %d has non-positive score %f", r.Index, r.Score)
		}
	}

	// Only the security/code item should appear.
	if len(ranked) != 1 || ranked[0].Index != 1 {
		t.Errorf("expected only index 1 in ranked results, got %+v", ranked)
	}
}

// --- Benchmarks ---

func BenchmarkBM25Score(b *testing.B) {
	s := routing.NewBM25Scorer()
	query := "review Go code for security vulnerabilities"
	doc := "security-reviewer: performs deep analysis of Go code to detect security vulnerabilities and CVEs"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Score(query, doc)
	}
}

func BenchmarkBM25Rank200(b *testing.B) {
	s := routing.NewBM25Scorer()
	query := "review Go code for security vulnerabilities"

	// Build 200 synthetic agent descriptions.
	descriptions := []string{
		"security-reviewer: reviews code for vulnerabilities",
		"code-reviewer: checks Go code style and quality",
		"debugger: finds and fixes bugs in Go programs",
		"planner: plans architecture and implementation steps",
		"tdd-orchestrator: drives test-driven development cycles",
	}
	items := make([]routing.Scorable, 200)
	for i := range items {
		items[i] = scoreText(descriptions[i%len(descriptions)])
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Rank(query, items)
	}
}
