package patterns

import (
	"math"
	"strings"
	"testing"
)

func TestShannonEntropy_Empty(t *testing.T) {
	if got := ShannonEntropy(nil); got != 0.0 {
		t.Errorf("ShannonEntropy(nil) = %v, want 0.0", got)
	}
}

func TestShannonEntropy_SingleElement(t *testing.T) {
	if got := ShannonEntropy(map[string]int{"a": 5}); got != 0.0 {
		t.Errorf("ShannonEntropy(single) = %v, want 0.0", got)
	}
}

func TestShannonEntropy_Uniform(t *testing.T) {
	// 4 equally distributed items: entropy = log2(4) = 2.0
	got := ShannonEntropy(map[string]int{"a": 10, "b": 10, "c": 10, "d": 10})
	if math.Abs(got-2.0) > 0.001 {
		t.Errorf("ShannonEntropy(uniform 4) = %v, want 2.0", got)
	}
}

func TestShannonEntropy_Skewed(t *testing.T) {
	// One dominant: entropy near 0
	got := ShannonEntropy(map[string]int{"a": 100, "b": 1})
	if got > 0.2 {
		t.Errorf("ShannonEntropy(skewed) = %v, want < 0.2", got)
	}
}

func TestLinearSlope_Empty(t *testing.T) {
	if got := LinearSlope(nil); got != 0.0 {
		t.Errorf("LinearSlope(nil) = %v, want 0.0", got)
	}
}

func TestLinearSlope_SinglePoint(t *testing.T) {
	if got := LinearSlope([]float64{5.0}); got != 0.0 {
		t.Errorf("LinearSlope(single) = %v, want 0.0", got)
	}
}

func TestLinearSlope_Increasing(t *testing.T) {
	// Perfect line y = 2x: slope = 2.0
	got := LinearSlope([]float64{0, 2, 4, 6, 8})
	if math.Abs(got-2.0) > 0.001 {
		t.Errorf("LinearSlope(increasing) = %v, want 2.0", got)
	}
}

func TestLinearSlope_Flat(t *testing.T) {
	got := LinearSlope([]float64{5, 5, 5, 5})
	if math.Abs(got) > 0.001 {
		t.Errorf("LinearSlope(flat) = %v, want 0.0", got)
	}
}

func TestMeanStdDev_Empty(t *testing.T) {
	mean, std := MeanStdDev(nil)
	if mean != 0 || std != 0 {
		t.Errorf("MeanStdDev(nil) = (%v, %v), want (0, 0)", mean, std)
	}
}

func TestMeanStdDev_SingleValue(t *testing.T) {
	mean, std := MeanStdDev([]float64{7.0})
	if mean != 7.0 || std != 0.0 {
		t.Errorf("MeanStdDev(7) = (%v, %v), want (7, 0)", mean, std)
	}
}

func TestMeanStdDev_Known(t *testing.T) {
	// [2, 4, 4, 4, 5, 5, 7, 9] mean=5, stddev=2.0
	mean, std := MeanStdDev([]float64{2, 4, 4, 4, 5, 5, 7, 9})
	if math.Abs(mean-5.0) > 0.001 {
		t.Errorf("mean = %v, want 5.0", mean)
	}
	if math.Abs(std-2.0) > 0.001 {
		t.Errorf("stddev = %v, want 2.0", std)
	}
}

func TestNgramExtract_Empty(t *testing.T) {
	if got := NgramExtract("", 2, 5); got != nil {
		t.Errorf("NgramExtract('') = %v, want nil", got)
	}
}

func TestNgramExtract_SingleWord(t *testing.T) {
	if got := NgramExtract("hello", 2, 5); got != nil {
		t.Errorf("NgramExtract(1 word, bigram) = %v, want nil", got)
	}
}

func TestNgramExtract_Bigrams(t *testing.T) {
	// "retrieval augmented" appears 2x, "augmented generation" appears 2x.
	// Tie-break is lexicographic: "augmented generation" < "retrieval augmented".
	text := "retrieval augmented generation for large language models and retrieval augmented generation"
	got := NgramExtract(text, 2, 3)
	if len(got) == 0 {
		t.Fatal("expected bigrams, got empty")
	}
	// Top bigram is "augmented generation" due to lex tie-break with count=2.
	if got[0] != "augmented generation" {
		t.Errorf("top bigram = %q, want 'augmented generation'", got[0])
	}
	// Both high-frequency bigrams must appear in top 3.
	found := map[string]bool{}
	for _, g := range got {
		found[g] = true
	}
	for _, want := range []string{"retrieval augmented", "augmented generation"} {
		if !found[want] {
			t.Errorf("expected bigram %q in top 3, got %v", want, got)
		}
	}
}

func TestNgramExtract_TopK(t *testing.T) {
	text := "a b c d e f g h i j"
	got := NgramExtract(text, 2, 3)
	if len(got) != 3 {
		t.Errorf("NgramExtract topK=3 returned %d, want 3", len(got))
	}
}

func TestNgramExtract_FiltersNumbers(t *testing.T) {
	text := "improved accuracy by 23 percent from 0.75 to 0.95 on 10M documents"
	got := NgramExtract(text, 2, 5)
	for _, ng := range got {
		for _, word := range strings.Fields(ng) {
			isNumeric := true
			for _, r := range word {
				if r < '0' || r > '9' {
					isNumeric = false
					break
				}
			}
			if isNumeric {
				t.Errorf("n-gram %q contains pure-numeric token %q", ng, word)
			}
		}
	}
}
