package agents_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/agents"
)

func TestAutoSelectAgent(t *testing.T) {
	reg := agents.NewRegistry()
	agents.RegisterBuiltins(reg)

	tests := []struct {
		prompt   string
		wantName string
	}{
		{
			prompt:   "review this code for security issues",
			wantName: "reviewer",
		},
		{
			prompt:   "investigate the memory leak",
			wantName: "generic", // "investigate" only matches debugger content (score 1), below threshold 3
		},
		{
			prompt:   "debugger trace the crash",
			wantName: "debugger", // "debugger" matches name exactly (score 3)
		},
		{
			prompt:   "research best practices for caching",
			wantName: "researcher",
		},
	}

	for _, tc := range tests {
		t.Run(tc.prompt, func(t *testing.T) {
			got, score := agents.AutoSelectAgent(reg, tc.prompt)
			if got == nil {
				t.Fatalf("AutoSelectAgent returned nil for prompt %q", tc.prompt)
			}
			if got.Name != tc.wantName {
				t.Errorf("AutoSelectAgent(%q) = %q (score %d), want %q", tc.prompt, got.Name, score, tc.wantName)
			}
		})
	}
}

func TestAutoSelectAgent_EmptyPromptFallsBackToGeneral(t *testing.T) {
	reg := agents.NewRegistry()
	agents.RegisterBuiltins(reg)

	got, score := agents.AutoSelectAgent(reg, "")
	if got == nil {
		t.Fatal("expected fallback to general, got nil")
	}
	if got.Name != "generic" {
		t.Errorf("fallback agent = %q, want general", got.Name)
	}
	if score != 0 {
		t.Errorf("fallback score = %d, want 0", score)
	}
}

func TestAutoSelectAgent_NoMatchFallsBackToGeneral(t *testing.T) {
	reg := agents.NewRegistry()
	agents.RegisterBuiltins(reg)

	got, score := agents.AutoSelectAgent(reg, "xyzzy quux frobnicate zap")
	if got == nil {
		t.Fatal("expected fallback to general, got nil")
	}
	if got.Name != "generic" {
		t.Errorf("fallback agent = %q, want general", got.Name)
	}
	_ = score
}

// TestAutoSelectAgent_LowScoreFallsBack verifies that a score below the
// minimum threshold (3) causes the fallback to be returned. The agent below
// only matches on content (score 1) — name, domain, and role are all unrelated
// — so the total is 1 and must not beat the threshold.
func TestAutoSelectAgent_LowScoreFallsBack(t *testing.T) {
	reg := agents.NewRegistry()
	// "alpha-bravo" has nothing in common with the prompt except the word
	// "foxtrot" tucked in its content. Score = 1 (content match), below threshold.
	reg.Register(&agents.Agent{
		Name:    "alpha-bravo",
		Role:    "ops",
		Domain:  "infra",
		Content: "Handles foxtrot deployments.",
		Source:  "test",
	})
	agents.RegisterBuiltins(reg)

	// "foxtrot" matches only the content of "alpha-bravo" (score 1 < threshold 3).
	got, score := agents.AutoSelectAgent(reg, "foxtrot")
	if got == nil {
		t.Fatal("AutoSelectAgent returned nil")
	}
	if got.Name == "alpha-bravo" {
		t.Errorf("low-score agent alpha-bravo was selected (score %d), expected general/implementer fallback", score)
	}
	if got.Name != "generic" && got.Name != "implementer" {
		t.Errorf("fallback agent = %q (score %d), want general or implementer", got.Name, score)
	}
}

// TestAutoSelectAgent_HighScoreSelected verifies that a name-level match
// (score 3 per keyword) beats the fallback.
func TestAutoSelectAgent_HighScoreSelected(t *testing.T) {
	reg := agents.NewRegistry()
	agents.RegisterBuiltins(reg)

	// "reviewer" is a builtin whose name exactly matches the keyword.
	got, score := agents.AutoSelectAgent(reg, "review this code for security issues")
	if got == nil {
		t.Fatal("AutoSelectAgent returned nil")
	}
	if got.Name != "reviewer" {
		t.Errorf("AutoSelectAgent = %q (score %d), want reviewer", got.Name, score)
	}
	if score < 3 {
		t.Errorf("score = %d, expected >= 3 for name match", score)
	}
}

// TestListCandidates_WhenFieldPreferredOverDescription verifies that ListCandidates
// returns the agent's When field (not Description) in the candidate's When slot,
// and that a builtin agent's When field is non-empty and surfaced to callers.
func TestListCandidates_WhenFieldPreferredOverDescription(t *testing.T) {
	reg := agents.NewRegistry()
	reg.Register(&agents.Agent{
		Name:        "with-when",
		Description: "short description",
		When:        "Use when you need the when-field agent",
		Role:        "coding",
		Source:      "test",
	})
	reg.Register(&agents.Agent{
		Name:        "without-when",
		Description: "fallback description",
		Role:        "coding",
		Source:      "test",
	})

	candidates := agents.ListCandidates(reg, "", 0)

	byName := make(map[string]agents.AgentCandidate, len(candidates))
	for _, c := range candidates {
		byName[c.Name] = c
	}

	// Agent with When set: the When field must appear verbatim in the candidate.
	if got := byName["with-when"].When; got != "Use when you need the when-field agent" {
		t.Errorf("with-when candidate When = %q, want explicit When value", got)
	}

	// Agent without When: falls back to Description.
	if got := byName["without-when"].When; got != "fallback description" {
		t.Errorf("without-when candidate When = %q, want fallback to Description", got)
	}
}

// TestListCandidates_BuiltinWhenNonEmpty verifies that every builtin agent has a
// non-empty When field that propagates into ListCandidates output.
func TestListCandidates_BuiltinWhenNonEmpty(t *testing.T) {
	reg := agents.NewRegistry()
	agents.RegisterBuiltins(reg)

	candidates := agents.ListCandidates(reg, "", 0)
	if len(candidates) == 0 {
		t.Fatal("expected at least one builtin candidate")
	}
	for _, c := range candidates {
		if c.When == "" {
			t.Errorf("builtin agent %q has empty When in ListCandidates output", c.Name)
		}
	}
}

// TestAutoSelectAgent_GenericPromptUsesGeneral verifies that the canonical
// false-positive case — "Respond with 'test'" — does not match "incident-responder"
// or any other agent and falls back to "generic".
func TestAutoSelectAgent_GenericPromptUsesGeneral(t *testing.T) {
	reg := agents.NewRegistry()
	// Simulate a plugin agent that previously caused a false positive.
	reg.Register(&agents.Agent{
		Name:    "incident-responder",
		Role:    "ops",
		Domain:  "incidents",
		Content: "Responds to incidents and outages by triaging alerts.",
		Source:  "plugin",
	})
	agents.RegisterBuiltins(reg)

	got, score := agents.AutoSelectAgent(reg, "Respond with 'test'")
	if got == nil {
		t.Fatal("AutoSelectAgent returned nil")
	}
	if got.Name == "incident-responder" {
		t.Errorf("false positive: AutoSelectAgent matched incident-responder (score %d) for generic prompt", score)
	}
	if got.Name != "generic" && got.Name != "implementer" {
		t.Errorf("expected general/implementer fallback, got %q (score %d)", got.Name, score)
	}
}
