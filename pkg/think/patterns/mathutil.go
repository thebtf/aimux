package patterns

import (
	"math"
	"sort"
	"strings"
)

// ShannonEntropy computes Shannon entropy over a frequency distribution.
// H = -Σ(p_i * log2(p_i)) where p_i = count_i / total.
// Returns 0.0 for empty or single-element maps.
func ShannonEntropy(counts map[string]int) float64 {
	total := 0
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return 0.0
	}
	entropy := 0.0
	for _, c := range counts {
		if c <= 0 {
			continue
		}
		p := float64(c) / float64(total)
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// LinearSlope computes the slope of a simple linear regression y = mx + b
// over equally-spaced values (index as x). Returns 0.0 for <2 data points.
func LinearSlope(values []float64) float64 {
	n := len(values)
	if n < 2 {
		return 0.0
	}
	nf := float64(n)
	var sumX, sumY, sumXY, sumX2 float64
	for i, y := range values {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}
	denom := nf*sumX2 - sumX*sumX
	if denom == 0 {
		return 0.0
	}
	return (nf*sumXY - sumX*sumY) / denom
}

// MeanStdDev computes mean and population standard deviation of a float64 slice.
// Returns (0, 0) for slices with fewer than 2 elements.
func MeanStdDev(values []float64) (mean, stddev float64) {
	n := len(values)
	if n < 2 {
		return 0, 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean = sum / float64(n)
	variance := 0.0
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	variance /= float64(n)
	stddev = math.Sqrt(variance)
	return mean, stddev
}

// NgramExtract extracts n-grams from text and returns top-k by frequency.
// Words are lowercased and split on non-alphanumeric characters; pure-numeric tokens
// are filtered out. Ties broken alphabetically. Returns empty slice for n < 1 or topK < 1.
func NgramExtract(text string, n, topK int) []string {
	if n < 1 || topK < 1 {
		return []string{}
	}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
	})
	// Filter out pure-numeric tokens to prevent numbers from appearing in themes.
	filtered := words[:0]
	for _, w := range words {
		isNumeric := true
		for _, r := range w {
			if !('0' <= r && r <= '9') {
				isNumeric = false
				break
			}
		}
		if !isNumeric {
			filtered = append(filtered, w)
		}
	}
	words = filtered
	if len(words) < n {
		return nil
	}

	freq := make(map[string]int)
	for i := 0; i <= len(words)-n; i++ {
		ngram := strings.Join(words[i:i+n], " ")
		freq[ngram]++
	}

	type entry struct {
		ngram string
		count int
	}
	entries := make([]entry, 0, len(freq))
	for ng, c := range freq {
		entries = append(entries, entry{ng, c})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].ngram < entries[j].ngram
	})

	limit := len(entries)
	if topK > 0 && topK < limit {
		limit = topK
	}
	result := make([]string, limit)
	for i := 0; i < limit; i++ {
		result[i] = entries[i].ngram
	}
	return result
}
