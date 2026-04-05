package routing_test

import (
	"os"
	"testing"

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

func TestRouter_Resolve_UnavailableCLI(t *testing.T) {
	defaults := map[string]types.RolePreference{
		"coding": {CLI: "codex"},
	}

	// Only gemini available, codex not
	r := routing.NewRouter(defaults, []string{"gemini"})

	pref, err := r.Resolve("coding")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Should fallback to first enabled
	if pref.CLI != "gemini" {
		t.Errorf("CLI = %q, want gemini (fallback)", pref.CLI)
	}
}

func TestRouter_Resolve_NoCLIAvailable(t *testing.T) {
	r := routing.NewRouter(nil, []string{})

	_, err := r.Resolve("coding")
	if err == nil {
		t.Fatal("expected error when no CLIs available")
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
