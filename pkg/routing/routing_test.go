package routing_test

import (
	"os"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/types"
)

func TestRouter_Resolve_Defaults(t *testing.T) {
	defaults := map[string]types.RolePreference{
		"coding":     {CLI: "codex", Model: "gpt-5.3-codex"},
		"codereview": {CLI: "codex", Model: "gpt-5.4", ReasoningEffort: "high"},
		"analyze":    {CLI: "gemini"},
	}

	r := routing.NewRouter(defaults, []string{"codex", "gemini"})

	tests := []struct {
		role    string
		wantCLI string
	}{
		{"coding", "codex"},
		{"codereview", "codex"},
		{"analyze", "gemini"},
	}

	for _, tt := range tests {
		pref, err := r.Resolve(tt.role)
		if err != nil {
			t.Errorf("Resolve(%q): unexpected error: %v", tt.role, err)
			continue
		}
		if pref.CLI != tt.wantCLI {
			t.Errorf("Resolve(%q).CLI = %q, want %q", tt.role, pref.CLI, tt.wantCLI)
		}
	}
}

func TestRouter_Resolve_EnvOverride(t *testing.T) {
	defaults := map[string]types.RolePreference{
		"coding": {CLI: "codex"},
	}

	r := routing.NewRouter(defaults, []string{"codex", "gemini"})

	os.Setenv("AIMUX_ROLE_CODING", "gemini:gemini-2.5-pro:high")
	defer os.Unsetenv("AIMUX_ROLE_CODING")

	pref, err := r.Resolve("coding")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if pref.CLI != "gemini" {
		t.Errorf("CLI = %q, want gemini", pref.CLI)
	}
	if pref.Model != "gemini-2.5-pro" {
		t.Errorf("Model = %q, want gemini-2.5-pro", pref.Model)
	}
	if pref.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %q, want high", pref.ReasoningEffort)
	}
}

func TestRouter_Resolve_EnvOverride_HyphenatedRole(t *testing.T) {
	defaults := map[string]types.RolePreference{
		"backend-architect": {CLI: "codex"},
	}

	r := routing.NewRouter(defaults, []string{"codex", "gemini"})

	os.Setenv("AIMUX_ROLE_BACKEND_ARCHITECT", "gemini")
	defer os.Unsetenv("AIMUX_ROLE_BACKEND_ARCHITECT")

	pref, err := r.Resolve("backend-architect")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if pref.CLI != "gemini" {
		t.Errorf("CLI = %q, want gemini", pref.CLI)
	}
}

// TestRouter_Resolve_UnavailableCLI_NoCapabilityFallback verifies that when the
// configured CLI for a role is not enabled AND no other CLI declares capability
// for that role, Resolve returns an error (no silent alphabetical fallback).
// Phase 7 removed the silent "first enabled CLI" fallback clause (T028).
func TestRouter_Resolve_UnavailableCLI_NoCapabilityFallback(t *testing.T) {
	defaults := map[string]types.RolePreference{
		"coding": {CLI: "codex"},
	}

	// Only gemini available, codex not — and gemini has no capability for "coding".
	r := routing.NewRouter(defaults, []string{"gemini"})

	_, err := r.Resolve("coding")
	if err == nil {
		t.Fatal("expected error when configured CLI is unavailable and no capability match, got nil")
	}
	// Error must name the role.
	if errStr := err.Error(); errStr == "" {
		t.Error("expected non-empty error message")
	}
}

func TestRouter_Resolve_NoCLIAvailable(t *testing.T) {
	r := routing.NewRouter(nil, []string{})

	_, err := r.Resolve("coding")
	if err == nil {
		t.Fatal("expected error when no CLIs available")
	}
}

// --- T024: TestResolve_UnknownRole_ReturnsError ---

// TestResolve_UnknownRole_ReturnsError verifies that Resolve returns a non-nil error
// and a zero-value RolePreference when the role is unknown and no CLI declares
// capability for it.
func TestResolve_UnknownRole_ReturnsError(t *testing.T) {
	defaults := map[string]types.RolePreference{
		"coding": {CLI: "codex"},
	}

	r := routing.NewRouter(defaults, []string{"codex", "gemini"})

	pref, err := r.Resolve("nonexistent_xyz")
	if err == nil {
		t.Fatal("expected error for unknown role, got nil")
	}
	if pref.CLI != "" || pref.Model != "" || pref.ReasoningEffort != "" {
		t.Errorf("expected zero-value RolePreference, got %+v", pref)
	}
}

// --- T024: TestResolve_KnownRole_UsesPriorityOrder ---

// TestResolve_KnownRole_UsesPriorityOrder verifies that the capability-match fallback
// respects CLIPriority — not alphabetical order. When two CLIs both declare capability
// for a role, the one earlier in CLIPriority is selected.
func TestResolve_KnownRole_UsesPriorityOrder(t *testing.T) {
	// No defaults for "analyze" — force capability-match fallback.
	defaults := map[string]types.RolePreference{}

	profiles := map[string]*config.CLIProfile{
		"claude": {
			Name:         "claude",
			Capabilities: []string{"analyze"},
		},
		"gemini": {
			Name:         "gemini",
			Capabilities: []string{"analyze"},
		},
	}

	// CLIPriority: gemini first, then claude.
	// Alphabetical order would give claude first — so this proves priority is used.
	cliPriority := []string{"gemini", "claude"}

	r := routing.NewRouterWithPriority(defaults, []string{"claude", "gemini"}, profiles, cliPriority)

	pref, err := r.Resolve("analyze")
	if err != nil {
		t.Fatalf("Resolve(analyze): unexpected error: %v", err)
	}
	if pref.CLI != "gemini" {
		t.Errorf("expected gemini (first in CLIPriority), got %q (alphabetical would give claude)", pref.CLI)
	}
}

func TestIsAdvisory(t *testing.T) {
	advisory := []string{
		"thinkdeep", "codereview", "secaudit", "challenge", "planner",
		"backend-architect", "security-auditor",
	}
	for _, role := range advisory {
		if !routing.IsAdvisory(role) {
			t.Errorf("expected %q to be advisory", role)
		}
	}

	nonAdvisory := []string{"coding", "default", "refactor", "testgen"}
	for _, role := range nonAdvisory {
		if routing.IsAdvisory(role) {
			t.Errorf("expected %q to not be advisory", role)
		}
	}
}
