package routing_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/types"
)

// makeProfiles builds a minimal profiles map for capability-based tests.
func makeProfiles(caps map[string][]string) map[string]*config.CLIProfile {
	profiles := make(map[string]*config.CLIProfile, len(caps))
	for name, c := range caps {
		profiles[name] = &config.CLIProfile{
			Name:         name,
			Capabilities: c,
		}
	}
	return profiles
}

// cliNames extracts CLI names from a preference slice.
func cliNames(prefs []types.RolePreference) []string {
	names := make([]string, len(prefs))
	for i, p := range prefs {
		names[i] = p.CLI
	}
	return names
}

// TestResolveWithFallback_PrimaryFirst verifies the resolved primary CLI is
// always the first entry in the returned list.
func TestResolveWithFallback_PrimaryFirst(t *testing.T) {
	defaults := map[string]types.RolePreference{
		"coding": {CLI: "codex"},
	}
	profiles := makeProfiles(map[string][]string{
		"codex":  {"coding", "review"},
		"claude": {"coding", "review", "analysis"},
		"gemini": {"analysis", "review"},
	})

	r := routing.NewRouterWithProfiles(defaults, []string{"codex", "claude", "gemini"}, profiles)
	result := r.ResolveWithFallback("coding")

	if len(result) == 0 {
		t.Fatal("expected at least one candidate, got none")
	}
	if result[0].CLI != "codex" {
		t.Errorf("first candidate = %q, want codex", result[0].CLI)
	}
}

// TestResolveWithFallback_CapabilityOrderedFirst verifies that CLIs whose
// Capabilities include the role name appear before those that don't.
func TestResolveWithFallback_CapabilityOrderedFirst(t *testing.T) {
	defaults := map[string]types.RolePreference{
		"coding": {CLI: "codex"},
	}
	profiles := makeProfiles(map[string][]string{
		"codex":  {"coding", "review"},
		"gemini": {"analysis"},         // does NOT have "coding"
		"claude": {"coding", "review"}, // has "coding"
	})

	r := routing.NewRouterWithProfiles(defaults, []string{"codex", "claude", "gemini"}, profiles)
	result := r.ResolveWithFallback("coding")

	if len(result) < 3 {
		t.Fatalf("expected 3 candidates, got %d: %v", len(result), cliNames(result))
	}
	// Primary is codex (index 0). Among fallbacks claude has "coding" and gemini does not,
	// so claude must come before gemini.
	fallbacks := result[1:]
	claudeIdx, geminiIdx := -1, -1
	for i, p := range fallbacks {
		switch p.CLI {
		case "claude":
			claudeIdx = i
		case "gemini":
			geminiIdx = i
		}
	}
	if claudeIdx == -1 || geminiIdx == -1 {
		t.Fatalf("claude or gemini missing from fallbacks: %v", cliNames(fallbacks))
	}
	if claudeIdx > geminiIdx {
		t.Errorf("capability-matching claude (idx %d) should come before gemini (idx %d)", claudeIdx, geminiIdx)
	}
}

// TestResolveWithFallback_AllBroken verifies that when all CLIs except the
// primary are filtered out by the caller (simulating circuit-open), only the
// primary is returned. The router itself does not check breakers — breaker
// filtering happens in buildFallbackCandidates inside the server. Here we test
// that the list still returns the primary even with no fallbacks.
func TestResolveWithFallback_AllBroken(t *testing.T) {
	defaults := map[string]types.RolePreference{
		"coding": {CLI: "codex"},
	}
	// Only codex is enabled — the others are "broken" (not in enabled list).
	profiles := makeProfiles(map[string][]string{
		"codex":  {"coding"},
		"claude": {"coding"},
		"gemini": {"analysis"},
	})

	r := routing.NewRouterWithProfiles(defaults, []string{"codex"}, profiles)
	result := r.ResolveWithFallback("coding")

	if len(result) != 1 {
		t.Errorf("expected 1 candidate (primary only), got %d: %v", len(result), cliNames(result))
	}
	if result[0].CLI != "codex" {
		t.Errorf("CLI = %q, want codex", result[0].CLI)
	}
}

// TestResolveWithFallback_NoCapabilityMatch verifies that even when no CLI
// declares the requested capability, all enabled CLIs are still returned as
// fallbacks (last-resort behaviour).
func TestResolveWithFallback_NoCapabilityMatch(t *testing.T) {
	defaults := map[string]types.RolePreference{
		"rare-role": {CLI: "codex"},
	}
	// No CLI has "rare-role" in its capabilities.
	profiles := makeProfiles(map[string][]string{
		"codex":  {"coding"},
		"claude": {"analysis"},
		"gemini": {"analysis"},
	})

	r := routing.NewRouterWithProfiles(defaults, []string{"codex", "claude", "gemini"}, profiles)
	result := r.ResolveWithFallback("rare-role")

	// Should still return all 3 CLIs (primary + 2 fallbacks without capability).
	if len(result) != 3 {
		t.Errorf("expected 3 candidates, got %d: %v", len(result), cliNames(result))
	}
	if result[0].CLI != "codex" {
		t.Errorf("primary = %q, want codex", result[0].CLI)
	}
}

// TestResolveWithFallback_NoProfiles verifies that when profiles are not
// loaded (NewRouter, not NewRouterWithProfiles), fallbacks still include all
// enabled CLIs, just without capability ordering.
func TestResolveWithFallback_NoProfiles(t *testing.T) {
	defaults := map[string]types.RolePreference{
		"coding": {CLI: "codex"},
	}

	r := routing.NewRouter(defaults, []string{"codex", "claude", "gemini"})
	result := r.ResolveWithFallback("coding")

	if len(result) == 0 {
		t.Fatal("expected at least one candidate")
	}
	if result[0].CLI != "codex" {
		t.Errorf("primary = %q, want codex", result[0].CLI)
	}
	// All 3 CLIs should be present.
	if len(result) != 3 {
		t.Errorf("expected 3 candidates, got %d: %v", len(result), cliNames(result))
	}
}

// TestResolveWithFallback_NoPrimaryAvailable verifies behaviour when the
// primary CLI for the role is not in the enabled list. The router falls back to
// the first enabled CLI as primary, so the list is still non-empty.
func TestResolveWithFallback_NoPrimaryAvailable(t *testing.T) {
	defaults := map[string]types.RolePreference{
		"coding": {CLI: "codex"}, // codex NOT in enabled list
	}
	profiles := makeProfiles(map[string][]string{
		"claude": {"coding", "analysis"},
		"gemini": {"analysis"},
	})

	r := routing.NewRouterWithProfiles(defaults, []string{"claude", "gemini"}, profiles)
	result := r.ResolveWithFallback("coding")

	if len(result) == 0 {
		t.Fatal("expected at least one candidate when primary unavailable")
	}
	// The resolved primary should be one of the enabled CLIs (first available).
	names := cliNames(result)
	found := false
	for _, n := range names {
		if n == "claude" || n == "gemini" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected enabled CLI in candidates, got %v", names)
	}
}
