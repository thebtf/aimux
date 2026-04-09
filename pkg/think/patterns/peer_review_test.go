package patterns

import (
	"context"
	"errors"
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
)

// callTrackingProvider tracks whether RequestSampling was invoked.
type callTrackingProvider struct {
	called bool
}

func (c *callTrackingProvider) RequestSampling(_ context.Context, _ []think.SamplingMessage, _ int) (string, error) {
	c.called = true
	return "", errors.New("intentionally not called for this test")
}

// TestPeerReview_Sampling verifies that when a SamplingProvider is set and the
// artifact is substantial (>100 chars), the LLM-detected objections are merged
// into the result and marked with source="sampling".
func TestPeerReview_Sampling(t *testing.T) {
	samplingResp := `{
		"objections": [
			{"severity": "P1", "category": "security", "description": "SQL injection risk in query builder", "suggestion": "Use parameterized queries"},
			{"severity": "P2", "category": "performance", "description": "N+1 query pattern detected", "suggestion": "Use eager loading"}
		],
		"strengths": ["Good separation of concerns", "Clear error handling"]
	}`

	p := &peerReviewPattern{}
	p.SetSampling(&mockSamplingProvider{response: samplingResp})

	artifact := "This is a code review artifact that is longer than one hundred characters to trigger the sampling path in the peer review pattern handler."
	input := map[string]any{
		"artifact":    artifact,
		"methodology": "unit testing",
		"novelty":     "new approach to caching",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-sampling")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	objections, ok := result.Data["objections"].([]map[string]any)
	if !ok || len(objections) == 0 {
		t.Fatalf("expected non-empty objections, got %v (%T)", result.Data["objections"], result.Data["objections"])
	}

	// Verify sampling objections are present and tagged.
	foundSamplingObjCount := 0
	for _, o := range objections {
		if o["source"] == "sampling" {
			foundSamplingObjCount++
		}
	}
	if foundSamplingObjCount != 2 {
		t.Errorf("expected 2 sampling objections, got %d (objections: %v)", foundSamplingObjCount, objections)
	}

	// Strengths from sampling must be merged.
	strengths, _ := result.Data["strengths"].([]string)
	foundStrength := false
	for _, s := range strengths {
		if s == "Good separation of concerns" {
			foundStrength = true
		}
	}
	if !foundStrength {
		t.Errorf("expected sampling strength in strengths list, got %v", strengths)
	}

	// reviewSource must indicate sampling was used.
	if result.Data["reviewSource"] != "sampling" {
		t.Errorf("expected reviewSource=sampling, got %v", result.Data["reviewSource"])
	}
}

// TestPeerReview_NoSampling verifies that when no SamplingProvider is set,
// the existing keyword-based behavior is preserved unchanged.
func TestPeerReview_NoSampling(t *testing.T) {
	p := &peerReviewPattern{} // no SetSampling call

	input := map[string]any{
		"artifact": "A research paper on distributed consensus",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-no-sampling")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	// Existing structural checks must still produce objections.
	objections, ok := result.Data["objections"].([]map[string]any)
	if !ok || len(objections) == 0 {
		t.Fatal("expected non-empty objections without sampling")
	}

	// No sampling source tags on any objection.
	for _, o := range objections {
		if o["source"] == "sampling" {
			t.Errorf("unexpected sampling-tagged objection when no provider set: %v", o)
		}
	}

	// reviewSource must be keyword-analysis.
	if result.Data["reviewSource"] != "keyword-analysis" {
		t.Errorf("expected reviewSource=keyword-analysis, got %v", result.Data["reviewSource"])
	}

	// Standard fields must be present.
	if _, ok := result.Data["reviewVerdict"]; !ok {
		t.Error("expected reviewVerdict field")
	}
	if _, ok := result.Data["guidance"]; !ok {
		t.Error("expected guidance field")
	}
}

// TestPeerReview_SamplingFailureFallback verifies that when the SamplingProvider
// returns an error, Handle falls back to keyword-based review without error.
func TestPeerReview_SamplingFailureFallback(t *testing.T) {
	p := &peerReviewPattern{}
	p.SetSampling(&mockSamplingProvider{err: errors.New("sampling unavailable")})

	artifact := "A code artifact that is long enough to trigger the sampling path because it exceeds one hundred characters total."
	input := map[string]any{"artifact": artifact}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-sampling-fail")
	if err != nil {
		t.Fatalf("Handle must not return error on sampling failure, got: %v", err)
	}

	// Should still have keyword-based objections.
	objections, ok := result.Data["objections"].([]map[string]any)
	if !ok || len(objections) == 0 {
		t.Fatal("expected keyword-based objections on sampling failure")
	}

	// reviewSource should fall back to keyword-analysis.
	if result.Data["reviewSource"] != "keyword-analysis" {
		t.Errorf("expected reviewSource=keyword-analysis on failure, got %v", result.Data["reviewSource"])
	}
}

// TestPeerReview_ShortArtifactSkipsSampling verifies that artifacts ≤100 chars
// do not trigger sampling even when a provider is available.
func TestPeerReview_ShortArtifactSkipsSampling(t *testing.T) {
	tracker := &callTrackingProvider{}

	p := &peerReviewPattern{}
	p.SetSampling(tracker)

	input := map[string]any{"artifact": "Short artifact."} // well under 100 chars

	validated, _ := p.Validate(input)
	_, err := p.Handle(validated, "test-short")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	if tracker.called {
		t.Error("sampling must not be called for artifacts ≤100 characters")
	}
}
