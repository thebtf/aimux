package loom

import (
	"testing"
)

func TestGate_EmptyOutput_Rejects(t *testing.T) {
	gate := NewQualityGate()
	task := &Task{ID: "t1"}
	result := &WorkerResult{Content: ""}

	decision := gate.Check(task, result)
	if decision.Accept {
		t.Error("should reject empty output")
	}
	if decision.Reason != "empty_output" {
		t.Errorf("reason: got %q want empty_output", decision.Reason)
	}
	if !decision.Retry {
		t.Error("empty output should be retryable")
	}
}

func TestGate_WhitespaceOnly_Rejects(t *testing.T) {
	gate := NewQualityGate()
	task := &Task{ID: "t1"}
	result := &WorkerResult{Content: "   \n\t  "}

	decision := gate.Check(task, result)
	if decision.Accept {
		t.Error("should reject whitespace-only output")
	}
	if decision.Reason != "empty_output" {
		t.Errorf("reason: got %q want empty_output", decision.Reason)
	}
}

func TestGate_RateLimitError_Rejects(t *testing.T) {
	gate := NewQualityGate()
	task := &Task{ID: "t1"}

	patterns := []string{
		"Error: rate limit exceeded",
		"HTTP 429 Too Many Requests",
		"Quota exceeded for model gpt-4",
		"Request was throttled",
	}

	for _, p := range patterns {
		result := &WorkerResult{Content: p}
		decision := gate.Check(task, result)
		if decision.Accept {
			t.Errorf("should reject rate limit: %q", p)
		}
		if decision.Reason != "rate_limit_error" {
			t.Errorf("reason for %q: got %q want rate_limit_error", p, decision.Reason)
		}
		if !decision.Retry {
			t.Errorf("rate limit should be retryable: %q", p)
		}
	}
}

func TestGate_ValidContent_Accepts(t *testing.T) {
	gate := NewQualityGate()
	task := &Task{ID: "t1"}
	result := &WorkerResult{Content: "Here is the implementation of the requested feature."}

	decision := gate.Check(task, result)
	if !decision.Accept {
		t.Errorf("should accept valid content, got reason: %q", decision.Reason)
	}
	if decision.Reason != "pass" {
		t.Errorf("reason: got %q want pass", decision.Reason)
	}
}

func TestGate_Thrashing_StopsRetry(t *testing.T) {
	gate := NewQualityGateWithOpts(WithThreshold(0.8), WithWindowSize(3))
	task := &Task{ID: "t1"}

	// Submit 2 identical results that get accepted (building history).
	// On 3rd identical result, all pairwise Jaccard = 1.0 > 0.8 → thrashing.
	r1 := &WorkerResult{Content: "The quick brown fox jumps over the lazy dog"}
	r2 := &WorkerResult{Content: "The quick brown fox jumps over the lazy dog"}

	d1 := gate.Check(task, r1)
	if !d1.Accept {
		t.Fatal("r1 should be accepted")
	}

	d2 := gate.Check(task, r2)
	if !d2.Accept {
		t.Fatal("r2 should be accepted")
	}

	// Third identical result → thrashing detected (Jaccard = 1.0 > 0.8).
	r3 := &WorkerResult{Content: "The quick brown fox jumps over the lazy dog"}
	d3 := gate.Check(task, r3)
	if d3.Accept {
		t.Error("r3 should be rejected (thrashing)")
	}
	if d3.Reason != "thrashing" {
		t.Errorf("reason: got %q want thrashing", d3.Reason)
	}
	if d3.Retry {
		t.Error("thrashing should NOT be retryable")
	}
}

func TestGate_DifferentContent_NoThrashing(t *testing.T) {
	gate := NewQualityGateWithOpts(WithThreshold(0.8), WithWindowSize(3))
	task := &Task{ID: "t1"}

	contents := []string{
		"The quick brown fox jumps over the lazy dog",
		"Completely different content about databases",
		"Another unrelated topic about networking",
	}

	for i, c := range contents {
		d := gate.Check(task, &WorkerResult{Content: c})
		if !d.Accept {
			t.Errorf("result %d should be accepted: %q", i, d.Reason)
		}
	}
}

func TestGate_MaxRetries_WithGate(t *testing.T) {
	// Test that gate correctly signals retry vs no-retry.
	gate := NewQualityGate()
	task := &Task{ID: "t-retry"}

	// Empty output → retry=true.
	d := gate.Check(task, &WorkerResult{Content: ""})
	if d.Retry != true {
		t.Error("empty output should signal retry")
	}

	// Valid content → accept.
	d = gate.Check(task, &WorkerResult{Content: "valid output"})
	if !d.Accept {
		t.Error("valid content should be accepted")
	}
}

// ---- Jaccard similarity tests ----

func TestJaccardWordSimilarity_Identical(t *testing.T) {
	got := jaccardWordSimilarity("hello world", "hello world")
	if got != 1.0 {
		t.Errorf("identical strings: got %f want 1.0", got)
	}
}

func TestJaccardWordSimilarity_CompletelyDifferent(t *testing.T) {
	got := jaccardWordSimilarity("hello world", "foo bar")
	if got != 0.0 {
		t.Errorf("completely different: got %f want 0.0", got)
	}
}

func TestJaccardWordSimilarity_Partial(t *testing.T) {
	// "hello world foo" vs "hello world bar"
	// intersection: {hello, world} = 2
	// union: {hello, world, foo, bar} = 4
	// similarity: 2/4 = 0.5
	got := jaccardWordSimilarity("hello world foo", "hello world bar")
	if got != 0.5 {
		t.Errorf("partial overlap: got %f want 0.5", got)
	}
}

func TestJaccardWordSimilarity_Empty(t *testing.T) {
	if got := jaccardWordSimilarity("", ""); got != 1.0 {
		t.Errorf("both empty: got %f want 1.0", got)
	}
	if got := jaccardWordSimilarity("hello", ""); got != 0.0 {
		t.Errorf("one empty: got %f want 0.0", got)
	}
}

func TestJaccardWordSimilarity_CaseInsensitive(t *testing.T) {
	got := jaccardWordSimilarity("Hello World", "hello world")
	if got != 1.0 {
		t.Errorf("case insensitive: got %f want 1.0", got)
	}
}
