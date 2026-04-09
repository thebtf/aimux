package patterns

import (
	"strings"
	"testing"
)

// --- AnalyzeText tests ---

func TestAnalyzeText_Entities(t *testing.T) {
	analysis := AnalyzeText("Design auth with OAuth2 and JWT")
	has := func(want string) bool {
		for _, e := range analysis.Entities {
			if e == want {
				return true
			}
		}
		return false
	}
	for _, expected := range []string{"OAuth2", "JWT"} {
		if !has(expected) {
			t.Errorf("expected entity %q in %v", expected, analysis.Entities)
		}
	}
}

func TestAnalyzeText_Negations(t *testing.T) {
	analysis := AnalyzeText("Build a service without database dependency")
	found := false
	for _, n := range analysis.Negations {
		if strings.Contains(strings.ToLower(n), "database") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected negation containing 'database dependency', got %v", analysis.Negations)
	}
}

func TestAnalyzeText_Questions(t *testing.T) {
	analysis := AnalyzeText("should we use JWT or sessions?")
	if len(analysis.Questions) < 1 {
		t.Errorf("expected at least 1 question, got %v", analysis.Questions)
	}
}

func TestAnalyzeText_Complexity_Low(t *testing.T) {
	analysis := AnalyzeText("Build a login page.")
	if analysis.Complexity != "low" {
		t.Errorf("expected 'low' complexity, got %q", analysis.Complexity)
	}
}

func TestAnalyzeText_Complexity_High(t *testing.T) {
	// 7 sentences, 4 conjunctions → "high"
	long := "Design auth. Add login flow. Implement token management. Handle sessions. " +
		"Support RBAC and MFA. Add password reset and audit logging. Configure rate-limiting."
	analysis := AnalyzeText(long)
	if analysis.Complexity != "high" {
		t.Errorf("expected 'high' complexity, got %q (text: %q)", analysis.Complexity, long)
	}
}

// --- DetectGaps tests ---

func TestDetectGaps_AuthDomain(t *testing.T) {
	// Only "login" is mentioned; tokens, sessions, logout, roles, password-reset, mfa should appear as gaps.
	detected := []string{"login", "OAuth2"}
	domain := MatchDomainTemplate("auth login authentication")
	if domain == nil {
		t.Fatal("expected auth domain template, got nil")
	}

	gaps := DetectGaps(detected, domain)

	mustBeGap := []string{"tokens", "sessions", "logout", "roles"}
	gapSet := make(map[string]struct{}, len(gaps))
	for _, g := range gaps {
		gapSet[g.Expected] = struct{}{}
	}
	for _, want := range mustBeGap {
		if _, ok := gapSet[want]; !ok {
			t.Errorf("expected gap %q but it was not reported; all gaps: %v", want, gaps)
		}
	}

	// Verify "login" is NOT reported as a gap.
	if _, ok := gapSet["login"]; ok {
		t.Errorf("'login' should not be a gap since it was detected")
	}
}

func TestDetectGaps_NoDomain(t *testing.T) {
	gaps := DetectGaps([]string{"anything"}, nil)
	if gaps != nil {
		t.Errorf("expected nil gaps for nil domain, got %v", gaps)
	}
}
