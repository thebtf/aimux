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
			HeadlessFlags:     []string{"--full-auto"},
			PromptFlag:        "-p",
			StdinThreshold:    6000,
			StdinSentinel:     "-",
			CompletionPattern: `turn\.completed`,
			TimeoutSeconds:    10,
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
	// Prompt always goes via stdin, not args — no prompt flag in args
	assertSliceEqual(t, sa.Args, []string{})
	if sa.Stdin != "hello world" {
		t.Errorf("Stdin = %q, want %q", sa.Stdin, "hello world")
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

	// Prompt goes via stdin, not as positional arg
	assertSliceEqual(t, sa.Args, []string{})
	if sa.Stdin != "fix bug" {
		t.Errorf("Stdin = %q, want %q", sa.Stdin, "fix bug")
	}
}

func TestProfileResolver_LongFlagPrompt(t *testing.T) {
	r := NewProfileResolver(testProfiles())

	sa, err := r.ResolveSpawnArgs("aider", "fix the bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Prompt via stdin, no --message flag in args
	assertSliceEqual(t, sa.Args, []string{})
	if sa.Stdin != "fix the bug" {
		t.Errorf("Stdin = %q, want %q", sa.Stdin, "fix the bug")
	}
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
	// base args (codex --json --full-auto) only — prompt goes via stdin
	assertSliceEqual(t, sa.Args, []string{"codex", "--json", "--full-auto"})
	if sa.Stdin != "hello" {
		t.Errorf("Stdin = %q, want %q", sa.Stdin, "hello")
	}
}

func TestProfileResolver_StdinAlwaysUsed(t *testing.T) {
	r := NewProfileResolver(testProfiles())

	// Short prompt — still goes via stdin; args contain headless flags + stdin sentinel only
	sa, err := r.ResolveSpawnArgs("codex", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sa.Stdin != "hello" {
		t.Errorf("Stdin = %q, want %q", sa.Stdin, "hello")
	}
	assertSliceEqual(t, sa.Args, []string{"--full-auto", "-"})

	// Long prompt — also via stdin; prompt must not appear in Args
	longPrompt := strings.Repeat("x", 50000)
	sa2, err := r.ResolveSpawnArgs("codex", longPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sa2.Stdin != longPrompt {
		t.Error("expected stdin to contain the long prompt")
	}
	assertSliceEqual(t, sa2.Args, []string{"--full-auto", "-"})
	if strings.Contains(strings.Join(sa2.Args, " "), longPrompt) {
		t.Error("long prompt should not be in Args")
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

func TestProfileResolver_TimeoutSeconds(t *testing.T) {
	r := NewProfileResolver(testProfiles())

	sa, err := r.ResolveSpawnArgs("codex", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sa.TimeoutSeconds != 10 {
		t.Errorf("TimeoutSeconds = %d, want 10", sa.TimeoutSeconds)
	}

	sa2, err := r.ResolveSpawnArgs("gemini", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sa2.TimeoutSeconds != 0 {
		t.Errorf("TimeoutSeconds should be 0 for gemini, got %d", sa2.TimeoutSeconds)
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
