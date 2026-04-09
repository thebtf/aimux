package patterns

import (
	"slices"
	"testing"
)

// TestExtractKeywords verifies that stop words are filtered, words are
// deduplicated, and the result is sorted alphabetically.
func TestExtractKeywords(t *testing.T) {
	got := ExtractKeywords("Design authentication system with OAuth2")
	want := []string{"authentication", "design", "oauth2", "system"}

	if !slices.Equal(got, want) {
		t.Errorf("ExtractKeywords mismatch\n  got:  %v\n  want: %v", got, want)
	}
}

// TestExtractKeywords_StopWords verifies that common stop words are fully
// filtered and only meaningful content words survive.
func TestExtractKeywords_StopWords(t *testing.T) {
	got := ExtractKeywords("The system is designed to handle requests")
	want := []string{"designed", "handle", "requests", "system"}

	if !slices.Equal(got, want) {
		t.Errorf("ExtractKeywords stop-word filtering mismatch\n  got:  %v\n  want: %v", got, want)
	}
}

// TestBuildGuidance verifies that all fields are populated and the example
// string contains the pattern name.
func TestBuildGuidance(t *testing.T) {
	enrichments := []string{"subProblems", "dependencies", "risks"}
	g := BuildGuidance("problem_decomposition", "basic", enrichments)

	if g.CurrentDepth != "basic" {
		t.Errorf("expected CurrentDepth='basic', got %q", g.CurrentDepth)
	}
	if g.NextLevel == "" {
		t.Error("NextLevel must not be empty")
	}
	if g.Example == "" {
		t.Error("Example must not be empty")
	}
	if !containsString(g.Example, "problem_decomposition") {
		t.Errorf("Example must contain the pattern name 'problem_decomposition', got: %q", g.Example)
	}
	if len(g.Enrichments) != len(enrichments) {
		t.Errorf("expected %d enrichments, got %d", len(enrichments), len(g.Enrichments))
	}

	// Anti-stub: different depths must produce different NextLevel text.
	gEnriched := BuildGuidance("problem_decomposition", "enriched", enrichments)
	gFull := BuildGuidance("problem_decomposition", "full", enrichments)

	if g.NextLevel == gEnriched.NextLevel {
		t.Error("anti-stub: 'basic' and 'enriched' depths must produce different NextLevel text")
	}
	if gEnriched.NextLevel == gFull.NextLevel {
		t.Error("anti-stub: 'enriched' and 'full' depths must produce different NextLevel text")
	}
}

// containsString is a small helper so we avoid importing strings in test output.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
