package resolve

import (
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/types"
)

func testProfiles() map[string]*config.CLIProfile {
	return map[string]*config.CLIProfile{
		"codex": {
			Name: "codex",
			Command: config.CommandConfig{
				Base: "codex",
			},
			Features:          types.CLIFeatures{Headless: true},
			PromptFlag:        "-p",
			StdinThreshold:    6000,
			CompletionPattern: `turn\.completed`,
		},
		"gemini": {
			Name: "gemini",
			Command: config.CommandConfig{
				Base: "gemini",
			},
			PromptFlag:     "-p",
			StdinThreshold: 6000,
		},
		"aider": {
			Name: "aider",
			Command: config.CommandConfig{
				Base: "aider",
			},
			PromptFlag: "--message",
		},
		"testcli-codex": {
			Name: "testcli-codex",
			Command: config.CommandConfig{
				Base: "testcli codex --json --full-auto",
			},
			Features:          types.CLIFeatures{Headless: true},
			PromptFlag:        "-p",
			StdinThreshold:    6000,
			CompletionPattern: `turn\.completed`,
		},
	}
}

func TestProfileResolver_BasicResolution(t *testing.T) {
	r := NewProfileResolver(testProfiles())

	sa, err := r.ResolveSpawnArgs("gemini", "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sa.CLI != "gemini" {
		t.Errorf("CLI = %q, want %q", sa.CLI, "gemini")
	}
	if sa.Command != "gemini" {
		t.Errorf("Command = %q, want %q", sa.Command, "gemini")
	}
	assertSliceEqual(t, sa.Args, []string{"-p", "hello world"})
	if sa.Stdin != "" {
		t.Errorf("Stdin should be empty, got %q", sa.Stdin)
	}
}

func TestProfileResolver_PositionalPrompt(t *testing.T) {
	profiles := map[string]*config.CLIProfile{
		"crush": {
			Name: "crush",
			Command: config.CommandConfig{
				Base: "crush",
			},
			PromptFlag: "", // positional
		},
	}
	r := NewProfileResolver(profiles)

	sa, err := r.ResolveSpawnArgs("crush", "fix bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSliceEqual(t, sa.Args, []string{"fix bug"})
}

func TestProfileResolver_LongFlagPrompt(t *testing.T) {
	r := NewProfileResolver(testProfiles())

	sa, err := r.ResolveSpawnArgs("aider", "fix the bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSliceEqual(t, sa.Args, []string{"--message", "fix the bug"})
}

func TestProfileResolver_MultiWordCommandBase(t *testing.T) {
	r := NewProfileResolver(testProfiles())

	sa, err := r.ResolveSpawnArgs("testcli-codex", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sa.Command != "testcli" {
		t.Errorf("Command = %q, want %q", sa.Command, "testcli")
	}
	// base args (codex --json --full-auto) + --full-auto (headless codex) + -p hello
	// Note: profile Name is "testcli-codex", not "codex", so headless --full-auto is NOT added
	// because the headless check is `profile.Name == "codex"`
	assertSliceEqual(t, sa.Args, []string{"codex", "--json", "--full-auto", "-p", "hello"})
}

func TestProfileResolver_StdinPiping(t *testing.T) {
	r := NewProfileResolver(testProfiles())

	longPrompt := strings.Repeat("x", 7000) // exceeds 6000 threshold
	sa, err := r.ResolveSpawnArgs("codex", longPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sa.Stdin != longPrompt {
		t.Error("expected stdin to contain the long prompt")
	}
	// Args should NOT contain the prompt (piped via stdin)
	for _, arg := range sa.Args {
		if arg == longPrompt {
			t.Error("long prompt should not be in Args when piped via stdin")
		}
	}
}

func TestProfileResolver_StdinNotTriggeredBelowThreshold(t *testing.T) {
	r := NewProfileResolver(testProfiles())

	shortPrompt := "hello"
	sa, err := r.ResolveSpawnArgs("codex", shortPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sa.Stdin != "" {
		t.Errorf("Stdin should be empty for short prompts, got %d chars", len(sa.Stdin))
	}
}

func TestProfileResolver_StdinNotTriggeredZeroThreshold(t *testing.T) {
	r := NewProfileResolver(testProfiles())

	longPrompt := strings.Repeat("x", 7000)
	sa, err := r.ResolveSpawnArgs("aider", longPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// aider has StdinThreshold=0, so no stdin piping
	if sa.Stdin != "" {
		t.Error("Stdin should be empty when threshold is 0")
	}
}

func TestProfileResolver_CompletionPattern(t *testing.T) {
	r := NewProfileResolver(testProfiles())

	sa, err := r.ResolveSpawnArgs("codex", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sa.CompletionPattern != `turn\.completed` {
		t.Errorf("CompletionPattern = %q, want %q", sa.CompletionPattern, `turn\.completed`)
	}
}

func TestProfileResolver_EmptyCompletionPattern(t *testing.T) {
	r := NewProfileResolver(testProfiles())

	sa, err := r.ResolveSpawnArgs("gemini", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sa.CompletionPattern != "" {
		t.Errorf("CompletionPattern should be empty, got %q", sa.CompletionPattern)
	}
}

func TestProfileResolver_UnknownCLI(t *testing.T) {
	r := NewProfileResolver(testProfiles())

	_, err := r.ResolveSpawnArgs("nonexistent", "hello")
	if err == nil {
		t.Fatal("expected error for unknown CLI")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error should mention 'not configured', got: %v", err)
	}
}

func TestProfileResolver_ImplementsCLIResolver(t *testing.T) {
	var _ types.CLIResolver = (*ProfileResolver)(nil)
}
