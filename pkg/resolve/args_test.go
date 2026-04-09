package resolve

import (
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/types"
)

func TestCommandBinary(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{"single word", "codex", "codex"},
		{"two words", "testcli codex", "testcli"},
		{"multi-word with flags", "testcli codex --json --full-auto", "testcli"},
		{"empty string", "", ""},
		{"leading space", " codex", " codex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CommandBinary(tt.base)
			if got != tt.want {
				t.Errorf("CommandBinary(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

func TestCommandBaseArgs(t *testing.T) {
	tests := []struct {
		name string
		base string
		want []string
	}{
		{"single word", "codex", nil},
		{"two words", "testcli codex", []string{"codex"}},
		{"multi-word with flags", "testcli codex --json", []string{"codex", "--json"}},
		{"empty string", "", nil},
		{"extra whitespace", "testcli   codex   --json", []string{"codex", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CommandBaseArgs(tt.base)
			if !strSliceEqual(got, tt.want) {
				t.Errorf("CommandBaseArgs(%q) = %v, want %v", tt.base, got, tt.want)
			}
		})
	}
}

func TestBuildPromptArgs_PositionalPrompt(t *testing.T) {
	profile := &config.CLIProfile{
		Name: "codex",
		Command: config.CommandConfig{
			Base: "codex",
		},
		Features:      types.CLIFeatures{Headless: true},
		HeadlessFlags: []string{"--full-auto"},
		PromptFlag:    "",
	}

	args := BuildPromptArgs(profile, "", "", false, "hello world")

	// codex headless → --full-auto + positional prompt
	assertSliceEqual(t, args, []string{"--full-auto", "hello world"})
}

func TestBuildPromptArgs_FlagPrompt(t *testing.T) {
	profile := &config.CLIProfile{
		Name: "gemini",
		Command: config.CommandConfig{
			Base: "gemini",
		},
		PromptFlag: "-p",
	}

	args := BuildPromptArgs(profile, "", "", false, "hello world")

	assertSliceEqual(t, args, []string{"-p", "hello world"})
}

func TestBuildPromptArgs_LongFlagPrompt(t *testing.T) {
	profile := &config.CLIProfile{
		Name: "aider",
		Command: config.CommandConfig{
			Base: "aider",
		},
		PromptFlag: "--message",
	}

	args := BuildPromptArgs(profile, "", "", false, "fix the bug")

	assertSliceEqual(t, args, []string{"--message", "fix the bug"})
}

func TestBuildPromptArgs_MultiWordBase(t *testing.T) {
	profile := &config.CLIProfile{
		Name: "codex",
		Command: config.CommandConfig{
			Base: "testcli codex --json",
		},
		Features:      types.CLIFeatures{Headless: true},
		HeadlessFlags: []string{"--full-auto"},
		PromptFlag:    "",
	}

	args := BuildPromptArgs(profile, "", "", false, "prompt")

	// base args + --full-auto (headless) + positional prompt
	assertSliceEqual(t, args, []string{"codex", "--json", "--full-auto", "prompt"})
}

func TestBuildPromptArgs_WithModel(t *testing.T) {
	profile := &config.CLIProfile{
		Name: "gemini",
		Command: config.CommandConfig{
			Base: "gemini",
		},
		PromptFlag: "-p",
		ModelFlag:  "--model",
	}

	args := BuildPromptArgs(profile, "gemini-2.5-pro", "", false, "hello")

	assertSliceEqual(t, args, []string{"--model", "gemini-2.5-pro", "-p", "hello"})
}

func TestBuildPromptArgs_WithReasoning(t *testing.T) {
	profile := &config.CLIProfile{
		Name: "droid",
		Command: config.CommandConfig{
			Base: "droid",
		},
		PromptFlag: "-p",
		Reasoning: &config.ReasoningConfig{
			Flag:   "-r",
			Levels: []string{"low", "medium", "high"},
		},
	}

	args := BuildPromptArgs(profile, "", "high", false, "hello")

	assertSliceEqual(t, args, []string{"-r", "high", "-p", "hello"})
}

func TestBuildPromptArgs_WithReasoningTemplate(t *testing.T) {
	profile := &config.CLIProfile{
		Name: "codex",
		Command: config.CommandConfig{
			Base: "codex",
		},
		Features:      types.CLIFeatures{Headless: true},
		HeadlessFlags: []string{"--full-auto"},
		PromptFlag:    "-p",
		Reasoning: &config.ReasoningConfig{
			Flag:              "-c",
			FlagValueTemplate: `model_reasoning_effort=%s`,
			Levels:            []string{"low", "medium", "high", "xhigh"},
		},
	}

	args := BuildPromptArgs(profile, "", "high", false, "hello")

	assertSliceEqual(t, args, []string{"--full-auto", "-c", `model_reasoning_effort=high`, "-p", "hello"})
}

func TestBuildPromptArgs_ReadOnly(t *testing.T) {
	profile := &config.CLIProfile{
		Name: "codex",
		Command: config.CommandConfig{
			Base: "codex",
		},
		Features:      types.CLIFeatures{Headless: true},
		HeadlessFlags: []string{"--full-auto"},
		PromptFlag:    "-p",
		ReadOnlyFlags: []string{"--sandbox", "read-only"},
	}

	args := BuildPromptArgs(profile, "", "", true, "hello")

	assertSliceEqual(t, args, []string{"--full-auto", "--sandbox", "read-only", "-p", "hello"})
}

func TestBuildPromptArgs_EmptyPrompt(t *testing.T) {
	profile := &config.CLIProfile{
		Name: "gemini",
		Command: config.CommandConfig{
			Base: "gemini",
		},
		PromptFlag: "-p",
	}

	args := BuildPromptArgs(profile, "", "", false, "")

	// No prompt flag when prompt is empty (stdin piping case)
	assertSliceEqual(t, args, []string{})
}

func TestBuildPromptArgs_HeadlessNoFlags(t *testing.T) {
	// Headless=true but no HeadlessFlags configured → no extra flags added
	profile := &config.CLIProfile{
		Name: "gemini",
		Command: config.CommandConfig{
			Base: "gemini",
		},
		Features:   types.CLIFeatures{Headless: true},
		PromptFlag: "-p",
	}

	args := BuildPromptArgs(profile, "", "", false, "hello")

	assertSliceEqual(t, args, []string{"-p", "hello"})
}

// --- helpers ---

func strSliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertSliceEqual(t *testing.T, got, want []string) {
	t.Helper()
	if !strSliceEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
