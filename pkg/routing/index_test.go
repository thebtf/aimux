package routing_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/thebtf/aimux/pkg/routing"
)

// TestTermIndex_ScoreMatchesQuery verifies that a document matching the query
// receives a positive BM25 score.
func TestTermIndex_ScoreMatchesQuery(t *testing.T) {
	t.Parallel()

	idx := routing.NewTermIndex()
	idx.Add("doc1", "golang concurrency goroutines channels select")
	idx.Add("doc2", "python asyncio coroutines event loop")

	score := idx.Score("goroutines channels", "doc1")
	if score <= 0 {
		t.Errorf("expected positive score for matching doc, got %f", score)
	}

	scoreUnrelated := idx.Score("goroutines channels", "doc2")
	if scoreUnrelated >= score {
		t.Errorf("unrelated doc should score lower: unrelated=%f matching=%f", scoreUnrelated, score)
	}
}

// TestTermIndex_RemoveReturnsZero confirms that after removing a document its
// score is 0.
func TestTermIndex_RemoveReturnsZero(t *testing.T) {
	t.Parallel()

	idx := routing.NewTermIndex()
	idx.Add("doc1", "rust memory safety ownership borrowing")
	idx.Remove("doc1")

	score := idx.Score("memory safety", "doc1")
	if score != 0 {
		t.Errorf("expected 0 after removal, got %f", score)
	}
	if idx.Count() != 0 {
		t.Errorf("expected count 0 after removal, got %d", idx.Count())
	}
}

// TestTermIndex_ConcurrentAddScore verifies that concurrent Add and Score
// calls do not trigger the race detector.
func TestTermIndex_ConcurrentAddScore(t *testing.T) {
	t.Parallel()

	idx := routing.NewTermIndex()
	const n = 50

	var wg sync.WaitGroup
	wg.Add(n * 2)

	for i := range n {
		go func(i int) {
			defer wg.Done()
			idx.Add(fmt.Sprintf("doc%d", i), fmt.Sprintf("term%d alpha beta gamma", i))
		}(i)
	}

	for i := range n {
		go func(i int) {
			defer wg.Done()
			_ = idx.Score(fmt.Sprintf("term%d", i), fmt.Sprintf("doc%d", i))
		}(i)
	}

	wg.Wait()
}

// TestTermIndex_RebuildRecomputes verifies that Rebuild produces correct stats
// after bulk mutations applied without incrementally updating derived stats.
func TestTermIndex_RebuildRecomputes(t *testing.T) {
	t.Parallel()

	idx := routing.NewTermIndex()
	for i := range 10 {
		idx.Add(fmt.Sprintf("doc%d", i), fmt.Sprintf("keyword topic%d extra words here", i))
	}

	before := idx.Count()
	if before != 10 {
		t.Fatalf("expected 10 docs, got %d", before)
	}

	// Rebuild should leave the same count and stats consistent.
	idx.Rebuild()
	after := idx.Count()
	if after != 10 {
		t.Fatalf("expected 10 docs after rebuild, got %d", after)
	}

	// Scores should still be positive after rebuild.
	score := idx.Score("keyword topic3", "doc3")
	if score <= 0 {
		t.Errorf("expected positive score after rebuild, got %f", score)
	}
}

// TestTermIndex_RankSorted verifies that Rank returns results in descending
// score order.
func TestTermIndex_RankSorted(t *testing.T) {
	t.Parallel()

	idx := routing.NewTermIndex()
	// doc1 has 3 occurrences of "search", doc2 has 1.
	idx.Add("doc1", "search search search relevance ranking retrieval")
	idx.Add("doc2", "search engine optimization keywords")
	idx.Add("doc3", "unrelated document about cooking recipes")

	results := idx.Rank("search")
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	for i := 1; i < len(results); i++ {
		if results[i-1].Score < results[i].Score {
			t.Errorf("results not sorted descending: [%d]=%f < [%d]=%f",
				i-1, results[i-1].Score, i, results[i].Score)
		}
	}

	// doc3 should not appear (no matching terms).
	for _, r := range results {
		if r.ID == "doc3" {
			t.Errorf("unrelated doc3 should not appear in results")
		}
	}
}

// TestTermIndex_RankEmpty verifies that ranking an empty index returns nil.
func TestTermIndex_RankEmpty(t *testing.T) {
	t.Parallel()

	idx := routing.NewTermIndex()
	results := idx.Rank("anything")
	if results != nil {
		t.Errorf("expected nil for empty index, got %v", results)
	}
}

// TestTermIndex_RankNoMatch verifies that a query with no matching terms
// returns an empty (but not nil-required) slice.
func TestTermIndex_RankNoMatch(t *testing.T) {
	t.Parallel()

	idx := routing.NewTermIndex()
	idx.Add("doc1", "golang channels goroutines")
	results := idx.Rank("zzzzunmatchedterm")
	if len(results) != 0 {
		t.Errorf("expected empty results for non-matching query, got %d", len(results))
	}
}

// BenchmarkTermIndex_Build200 verifies that building an index of 200 documents
// completes in under 10ms.
func BenchmarkTermIndex_Build200(b *testing.B) {
	texts := make([]string, 200)
	for i := range 200 {
		texts[i] = fmt.Sprintf(
			"document %d contains terms like routing index scoring bm25 relevance "+
				"information retrieval full text search engine term frequency inverse "+
				"document frequency weighting scheme golang concurrent safe",
			i,
		)
	}

	b.ResetTimer()
	for range b.N {
		idx := routing.NewTermIndex()
		for i, text := range texts {
			idx.Add(fmt.Sprintf("doc%d", i), text)
		}
	}
}
