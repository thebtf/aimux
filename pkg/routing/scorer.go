package routing

import (
	"math"
	"sort"
	"strings"
)

// Scorable is implemented by anything that can be scored for relevance.
type Scorable interface {
	ScoreText() string
}

// RankedItem holds a scored item with its relevance score.
type RankedItem struct {
	Index int
	Score float64
}

// Scorer scores text relevance.
type Scorer interface {
	Score(query, document string) float64
	Rank(query string, items []Scorable) []RankedItem
}

// BM25Scorer implements Scorer using the Okapi BM25 algorithm.
// Parameters: k1=1.2, b=0.75 (standard IR defaults).
type BM25Scorer struct {
	k1 float64
	b  float64
}

// NewBM25Scorer returns a BM25Scorer with standard IR parameters (k1=1.2, b=0.75).
func NewBM25Scorer() *BM25Scorer {
	return &BM25Scorer{k1: 1.2, b: 0.75}
}

// ScoreTerm computes BM25 score for a single term given pre-computed statistics.
// Used by TermIndex which maintains its own inverted index.
func (s *BM25Scorer) ScoreTerm(tf, df, totalDocs, docLen int, avgDL float64) float64 {
	if tf == 0 || totalDocs == 0 {
		return 0
	}
	N := float64(totalDocs)
	dfF := float64(df)
	idf := math.Log((N-dfF+0.5)/(dfF+0.5) + 1)
	tfF := float64(tf)
	dl := float64(docLen)
	return idf * (tfF * (s.k1 + 1)) / (tfF + s.k1*(1-s.b+s.b*dl/avgDL))
}

// Score computes the BM25 relevance of a single document against a query.
// IDF is computed in the single-document context (df=1, N=1 → IDF=log(1)=0 when term
// is present), which collapses to pure TF weighting.  For multi-document ranking,
// use Rank, which computes IDF across the whole corpus.
func (s *BM25Scorer) Score(query, document string) float64 {
	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		return 0
	}
	docTerms := tokenize(document)
	if len(docTerms) == 0 {
		return 0
	}

	// Single-document corpus: treat this as a 2-document corpus where the query
	// term either appears (df=1, N=2) or does not (df=0, N=2) so that IDF is
	// non-zero for matching terms.
	const N = 2
	docFreq := termFreqMap(docTerms)
	dl := float64(len(docTerms))

	var score float64
	seen := make(map[string]bool, len(queryTerms))
	for _, term := range queryTerms {
		if seen[term] {
			continue
		}
		seen[term] = true

		tf := float64(docFreq[term])
		if tf == 0 {
			continue
		}

		df := 1.0 // term appears in the document
		idf := math.Log((float64(N)-df+0.5)/(df+0.5) + 1)
		// b normalisation is identity in single-document mode (dl == avgDL by definition).
		// For multi-document ranking with length normalisation, use Rank() instead.
		score += idf * (tf * (s.k1 + 1)) / (tf + s.k1*(1-s.b+s.b*dl/dl))
	}
	return score
}

// Rank scores all items against the query using BM25 with corpus-level IDF,
// then returns items with score > 0 sorted by score descending.
func (s *BM25Scorer) Rank(query string, items []Scorable) []RankedItem {
	queryTerms := tokenize(query)
	if len(queryTerms) == 0 || len(items) == 0 {
		return nil
	}

	// Tokenize all documents once.
	docs := make([][]string, len(items))
	for i, item := range items {
		docs[i] = tokenize(item.ScoreText())
	}

	N := float64(len(docs))

	// Compute average document length.
	totalLen := 0
	for _, d := range docs {
		totalLen += len(d)
	}
	avgdl := float64(totalLen) / N
	if avgdl == 0 {
		return nil
	}

	// Compute document frequency for each unique query term.
	uniqueTerms := uniqueTokens(queryTerms)
	df := make(map[string]float64, len(uniqueTerms))
	for _, term := range uniqueTerms {
		for _, d := range docs {
			if containsTerm(d, term) {
				df[term]++
			}
		}
	}

	// Precompute IDF for each query term.
	idf := make(map[string]float64, len(uniqueTerms))
	for _, term := range uniqueTerms {
		idf[term] = math.Log((N-df[term]+0.5)/(df[term]+0.5) + 1)
	}

	// Score each document.
	ranked := make([]RankedItem, 0, len(items))
	for i, d := range docs {
		dl := float64(len(d))
		freqs := termFreqMap(d)

		var score float64
		for _, term := range uniqueTerms {
			tf := float64(freqs[term])
			if tf == 0 {
				continue
			}
			score += idf[term] * (tf * (s.k1 + 1)) / (tf + s.k1*(1-s.b+s.b*dl/avgdl))
		}

		if score > 0 {
			ranked = append(ranked, RankedItem{Index: i, Score: score})
		}
	}

	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].Score > ranked[j].Score
	})

	return ranked
}

// tokenize splits text into lowercase tokens on whitespace.
func tokenize(text string) []string {
	raw := strings.Fields(strings.ToLower(text))
	return raw
}

// termFreqMap counts occurrences of each token in the slice.
func termFreqMap(tokens []string) map[string]int {
	m := make(map[string]int, len(tokens))
	for _, t := range tokens {
		m[t]++
	}
	return m
}

// uniqueTokens returns deduplicated tokens preserving first-occurrence order.
func uniqueTokens(tokens []string) []string {
	seen := make(map[string]bool, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// containsTerm reports whether the token slice contains the given term.
func containsTerm(tokens []string, term string) bool {
	for _, t := range tokens {
		if t == term {
			return true
		}
	}
	return false
}
