package server

import (
	"strings"
	"testing"
)

func TestBuildInstructions_WarmCLIs(t *testing.T) {
	output := buildInstructions(
		[]string{"gemini", "codex", "claude"},
		true,
		[]string{"codex", "claude", "gemini"},
		2,
		map[string]string{"coding": "codex", "review": "claude"},
	)

	for _, cli := range []string{"codex", "claude", "gemini"} {
		if !strings.Contains(output, cli) {
			t.Fatalf("expected output to contain %q", cli)
		}
	}
}

func TestBuildInstructions_NoCLIs(t *testing.T) {
	output := buildInstructions(nil, true, []string{"codex"}, 0, nil)

	if !strings.Contains(output, "No CLIs available") {
		t.Fatalf("expected no-CLI message, got:\n%s", output)
	}
}

func TestBuildInstructions_WarmupIncomplete(t *testing.T) {
	output := buildInstructions(nil, false, []string{"gemini", "codex"}, 1, nil)

	for _, snippet := range []string{"codex", "gemini", "warmup in progress"} {
		if !strings.Contains(output, snippet) {
			t.Fatalf("expected output to contain %q", snippet)
		}
	}
}

func TestBuildInstructions_TokenCount(t *testing.T) {
	output := buildInstructions(
		[]string{"codex", "claude", "gemini"},
		true,
		[]string{"codex", "claude", "gemini"},
		4,
		map[string]string{"coding": "codex", "review": "claude", "analysis": "gemini"},
	)

	if len(output)/4 > 4000 {
		t.Fatalf("expected approximate token count <= 4000, got %d", len(output)/4)
	}
}

func TestBuildInstructions_LineCount(t *testing.T) {
	output := buildInstructions(
		[]string{"codex", "claude", "gemini"},
		true,
		[]string{"codex", "claude", "gemini"},
		3,
		map[string]string{"coding": "codex"},
	)

	lineCount := strings.Count(output, "\n") + 1
	if lineCount > 120 {
		t.Fatalf("expected <= 120 lines, got %d", lineCount)
	}
}

func TestBuildInstructions_HasAllSections(t *testing.T) {
	output := buildInstructions(
		[]string{"codex"},
		true,
		[]string{"codex"},
		1,
		map[string]string{"coding": "codex"},
	)

	for _, snippet := range []string{"Anti-Patterns", "First Actions", "guide", "delegate"} {
		if !strings.Contains(output, snippet) {
			t.Fatalf("expected output to contain %q", snippet)
		}
	}
}

func TestBuildInstructions_RoleMap(t *testing.T) {
	output := buildInstructions(
		[]string{"codex"},
		true,
		[]string{"codex"},
		1,
		map[string]string{"coding": "codex"},
	)

	if !strings.Contains(output, "- codex: coding") {
		t.Fatalf("expected role-mapped CLI entry, got:\n%s", output)
	}
}
