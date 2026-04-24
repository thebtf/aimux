package routing

import (
	"sort"
	"strings"
	"sync"
	"unicode"
)

// RankedResult is a document ID with its BM25 score.
type RankedResult struct {
	ID    string
	Score float64
}

// TermIndex is a thread-safe in-memory inverted index for BM25 scoring.
// Documents are added by ID; the index pre-computes term frequencies and
// document lengths for efficient scoring.
type TermIndex struct {
	mu        sync.RWMutex
	docs      map[string]*docEntry // id → document entry
	df        map[string]int       // term → document frequency
	totalDocs int
	avgDL     float64
	scorer    *BM25Scorer
}

type docEntry struct {
	id     string
	terms  map[string]int // term → count
	length int            // total terms
	text   string         // original text for re-indexing
}

// NewTermIndex creates a TermIndex with default BM25 parameters.
func NewTermIndex() *TermIndex {
	return &TermIndex{
		docs:   make(map[string]*docEntry),
		df:     make(map[string]int),
		scorer: NewBM25Scorer(),
	}
}

// Add tokenizes text, computes term frequencies for the document, updates the
// document-frequency map, and recomputes avgDL.
// If a document with the given ID already exists it is replaced.
func (idx *TermIndex) Add(id, text string) {
	entry := buildEntry(id, text)

	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Remove old entry's df contribution if it exists.
	if old, exists := idx.docs[id]; exists {
		for term := range old.terms {
			idx.df[term]--
			if idx.df[term] <= 0 {
				delete(idx.df, term)
			}
		}
	} else {
		idx.totalDocs++
	}

	idx.docs[id] = entry
	for term := range entry.terms {
		idx.df[term]++
	}
	idx.recomputeAvgDL()
}

// Remove deletes the document from the index and recomputes derived stats.
// No-op if the document does not exist.
func (idx *TermIndex) Remove(id string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	old, exists := idx.docs[id]
	if !exists {
		return
	}

	for term := range old.terms {
		idx.df[term]--
		if idx.df[term] <= 0 {
			delete(idx.df, term)
		}
	}
	delete(idx.docs, id)
	idx.totalDocs--
	idx.recomputeAvgDL()
}

// Score returns the BM25 score for query against the document with the given ID.
// Returns 0 if the document does not exist or no query terms match.
func (idx *TermIndex) Score(query, id string) float64 {
	tokens := tokenizeText(query)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	entry, exists := idx.docs[id]
	if !exists {
		return 0
	}

	var total float64
	for _, term := range tokens {
		tf := entry.terms[term]
		df := idx.df[term]
		total += idx.scorer.ScoreTerm(tf, df, idx.totalDocs, entry.length, idx.avgDL)
	}
	return total
}

// Rank scores all documents against query and returns results sorted by
// descending score. Documents with score == 0 are omitted.
func (idx *TermIndex) Rank(query string) []RankedResult {
	tokens := tokenizeText(query)
	if len(tokens) == 0 {
		return nil
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.totalDocs == 0 {
		return nil
	}

	results := make([]RankedResult, 0, idx.totalDocs)
	for id, entry := range idx.docs {
		var sc float64
		for _, term := range tokens {
			tf := entry.terms[term]
			df := idx.df[term]
			sc += idx.scorer.ScoreTerm(tf, df, idx.totalDocs, entry.length, idx.avgDL)
		}
		if sc > 0 {
			results = append(results, RankedResult{ID: id, Score: sc})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})
	return results
}

// Rebuild recomputes all derived statistics (df map, totalDocs, avgDL) from
// the stored documents. Use after bulk mutations to ensure consistency.
func (idx *TermIndex) Rebuild() {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Recompute df from scratch.
	newDF := make(map[string]int, len(idx.df))
	for _, entry := range idx.docs {
		for term := range entry.terms {
			newDF[term]++
		}
	}
	idx.df = newDF
	idx.totalDocs = len(idx.docs)
	idx.recomputeAvgDL()
}

// Count returns the number of indexed documents.
func (idx *TermIndex) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.totalDocs
}

// recomputeAvgDL recalculates the average document length.
// Must be called with idx.mu held (write).
func (idx *TermIndex) recomputeAvgDL() {
	if idx.totalDocs == 0 {
		idx.avgDL = 0
		return
	}
	var sum int
	for _, e := range idx.docs {
		sum += e.length
	}
	idx.avgDL = float64(sum) / float64(idx.totalDocs)
}

// buildEntry tokenizes text and builds a docEntry without holding any lock.
func buildEntry(id, text string) *docEntry {
	tokens := tokenizeText(text)
	tf := make(map[string]int, len(tokens))
	for _, t := range tokens {
		tf[t]++
	}
	return &docEntry{
		id:     id,
		terms:  tf,
		length: len(tokens),
		text:   text,
	}
}

// tokenizeText lowercases and splits text into alphabetic tokens, discarding
// stop-words and single-character tokens for cleaner BM25 scoring.
func tokenizeText(s string) []string {
	s = strings.ToLower(s)
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	result := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) > 1 {
			result = append(result, f)
		}
	}
	return result
}
