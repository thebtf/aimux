package patterns

import (
	"math"
	"strings"
	"testing"
)

// --- ShannonEntropy ---

func TestMathutil_ShannonEntropy_Empty(t *testing.T) {
	if got := ShannonEntropy(nil); got != 0.0 {
		t.Errorf("ShannonEntropy(nil) = %v, want 0.0", got)
	}
}

func TestMathutil_ShannonEntropy_EmptyMap(t *testing.T) {
	if got := ShannonEntropy(map[string]int{}); got != 0.0 {
		t.Errorf("ShannonEntropy({}) = %v, want 0.0", got)
	}
}

func TestMathutil_ShannonEntropy_SingleElement(t *testing.T) {
	// Single key: only one outcome, zero uncertainty.
	if got := ShannonEntropy(map[string]int{"a": 5}); got != 0.0 {
		t.Errorf("ShannonEntropy(single) = %v, want 0.0", got)
	}
}

func TestMathutil_ShannonEntropy_Uniform4(t *testing.T) {
	// 4 equally distributed items: H = log2(4) = 2.0
	got := ShannonEntropy(map[string]int{"a": 10, "b": 10, "c": 10, "d": 10})
	if math.Abs(got-2.0) > 0.001 {
		t.Errorf("ShannonEntropy(uniform 4) = %v, want 2.0", got)
	}
}

func TestMathutil_ShannonEntropy_Skewed(t *testing.T) {
	// One dominant entry: entropy is near 0.
	got := ShannonEntropy(map[string]int{"a": 100, "b": 1})
	if got > 0.2 {
		t.Errorf("ShannonEntropy(skewed) = %v, want < 0.2", got)
	}
}

// --- LinearSlope ---

func TestMathutil_LinearSlope_Empty(t *testing.T) {
	if got := LinearSlope(nil); got != 0.0 {
		t.Errorf("LinearSlope(nil) = %v, want 0.0", got)
	}
}

func TestMathutil_LinearSlope_SinglePoint(t *testing.T) {
	if got := LinearSlope([]float64{5.0}); got != 0.0 {
		t.Errorf("LinearSlope(single) = %v, want 0.0", got)
	}
}

func TestMathutil_LinearSlope_PerfectIncreasing(t *testing.T) {
	// y = i+1 (values [1,2,3,4]): slope = 1.0
	got := LinearSlope([]float64{1, 2, 3, 4})
	if math.Abs(got-1.0) > 0.001 {
		t.Errorf("LinearSlope([1,2,3,4]) = %v, want 1.0", got)
	}
}

func TestMathutil_LinearSlope_Increasing2x(t *testing.T) {
	// Perfect line y = 2x: slope = 2.0
	got := LinearSlope([]float64{0, 2, 4, 6, 8})
	if math.Abs(got-2.0) > 0.001 {
		t.Errorf("LinearSlope(2x) = %v, want 2.0", got)
	}
}

func TestMathutil_LinearSlope_Decreasing(t *testing.T) {
	// Decreasing line [4,3,2,1]: slope = -1.0
	got := LinearSlope([]float64{4, 3, 2, 1})
	if got >= 0 {
		t.Errorf("LinearSlope(decreasing) = %v, want negative", got)
	}
	if math.Abs(got-(-1.0)) > 0.001 {
		t.Errorf("LinearSlope([4,3,2,1]) = %v, want -1.0", got)
	}
}

func TestMathutil_LinearSlope_Flat(t *testing.T) {
	got := LinearSlope([]float64{5, 5, 5, 5})
	if math.Abs(got) > 0.001 {
		t.Errorf("LinearSlope(flat) = %v, want 0.0", got)
	}
}

// --- MeanStdDev ---

func TestMathutil_MeanStdDev_Empty(t *testing.T) {
	mean, std := MeanStdDev(nil)
	if mean != 0 || std != 0 {
		t.Errorf("MeanStdDev(nil) = (%v, %v), want (0, 0)", mean, std)
	}
}

func TestMathutil_MeanStdDev_SingleValue(t *testing.T) {
	// len < 2 → (0, 0) per spec
	mean, std := MeanStdDev([]float64{7.0})
	if mean != 0.0 || std != 0.0 {
		t.Errorf("MeanStdDev(single) = (%v, %v), want (0, 0)", mean, std)
	}
}

func TestMathutil_MeanStdDev_KnownPopulation(t *testing.T) {
	// [2,4,4,4,5,5,7,9]: mean=5, population stddev=2.0
	// Sum of squared deviations: (9+1+1+1+0+0+4+16) = 32; var=32/8=4; stddev=2
	mean, std := MeanStdDev([]float64{2, 4, 4, 4, 5, 5, 7, 9})
	if math.Abs(mean-5.0) > 0.001 {
		t.Errorf("mean = %v, want 5.0", mean)
	}
	if math.Abs(std-2.0) > 0.001 {
		t.Errorf("stddev = %v, want 2.0", std)
	}
}

func TestMathutil_MeanStdDev_TwoValues(t *testing.T) {
	// [0, 4]: mean=2, stddev=2
	mean, std := MeanStdDev([]float64{0, 4})
	if math.Abs(mean-2.0) > 0.001 {
		t.Errorf("mean = %v, want 2.0", mean)
	}
	if math.Abs(std-2.0) > 0.001 {
		t.Errorf("stddev = %v, want 2.0", std)
	}
}

// --- NgramExtract ---

func TestMathutil_NgramExtract_EmptyText(t *testing.T) {
	got := NgramExtract("", 2, 5)
	if len(got) != 0 {
		t.Errorf("NgramExtract('') = %v, want empty", got)
	}
}

func TestMathutil_NgramExtract_NLessThanOne(t *testing.T) {
	got := NgramExtract("hello world", 0, 5)
	if len(got) != 0 {
		t.Errorf("NgramExtract(n=0) = %v, want empty", got)
	}
}

func TestMathutil_NgramExtract_TopKZero(t *testing.T) {
	got := NgramExtract("hello world foo", 1, 0)
	if len(got) != 0 {
		t.Errorf("NgramExtract(topK=0) = %v, want empty", got)
	}
}

func TestMathutil_NgramExtract_NegativeTopK(t *testing.T) {
	got := NgramExtract("hello world foo", 1, -1)
	if len(got) != 0 {
		t.Errorf("NgramExtract(topK=-1) = %v, want empty", got)
	}
}

func TestMathutil_NgramExtract_SingleWord_Bigram(t *testing.T) {
	got := NgramExtract("hello", 2, 5)
	if len(got) != 0 {
		t.Errorf("NgramExtract(1 word, bigram) = %v, want empty", got)
	}
}

func TestMathutil_NgramExtract_Bigrams(t *testing.T) {
	// "the cat" appears 2x; other bigrams appear once.
	// Expect "the cat" to be top-ranked (count 2 > count 1).
	text := "the cat sat on the cat mat"
	got := NgramExtract(text, 2, 3)
	if len(got) == 0 {
		t.Fatal("expected bigrams, got empty")
	}
	// "the cat" has count 2 and must be first (or at least in top 3).
	if got[0] != "the cat" {
		t.Errorf("top bigram = %q, want 'the cat'", got[0])
	}
}

func TestMathutil_NgramExtract_Unigrams(t *testing.T) {
	// n=1 returns top words by frequency.
	text := "go go go rust python rust go"
	got := NgramExtract(text, 1, 2)
	if len(got) == 0 {
		t.Fatal("expected unigrams, got empty")
	}
	// "go" appears 4x, must be first.
	if got[0] != "go" {
		t.Errorf("top unigram = %q, want 'go'", got[0])
	}
}

func TestMathutil_NgramExtract_TopKLimit(t *testing.T) {
	text := "a b c d e f g h i j"
	got := NgramExtract(text, 2, 3)
	if len(got) != 3 {
		t.Errorf("NgramExtract topK=3 returned %d, want 3", len(got))
	}
}

func TestMathutil_NgramExtract_NGreaterThanTokenCount(t *testing.T) {
	// n=5 but only 3 tokens: should return empty.
	got := NgramExtract("one two three", 5, 10)
	if len(got) != 0 {
		t.Errorf("NgramExtract(n > token count) = %v, want empty", got)
	}
}

func TestMathutil_NgramExtract_FiltersNumbers(t *testing.T) {
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

func TestMathutil_NgramExtract_TieBreakAlphabetical(t *testing.T) {
	// "augmented generation" and "retrieval augmented" both appear 2x.
	// Alphabetical tie-break: "augmented generation" < "retrieval augmented".
	text := "retrieval augmented generation for large language models and retrieval augmented generation"
	got := NgramExtract(text, 2, 3)
	if len(got) == 0 {
		t.Fatal("expected bigrams, got empty")
	}
	if got[0] != "augmented generation" {
		t.Errorf("top bigram = %q, want 'augmented generation'", got[0])
	}
}
