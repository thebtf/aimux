package server

import (
	"strings"
	"testing"
)

func TestBuildInstructions_WarmCLIs(t *testing.T) {
	output := buildInstructions(
		[]string{"gemini", "codex", "claude"},
		true,
		nil,
		0,
		map[string]string{"coding": "codex"},
	)

	for _, cli := range []string{"claude", "codex", "gemini"} {
		if !strings.Contains(output, cli) {
			t.Fatalf("expected output to contain warm CLI %q", cli)
		}
	}
}

func TestBuildInstructions_NoCLIs(t *testing.T) {
	output := buildInstructions(nil, true, nil, 0, nil)

	if !strings.Contains(output, "No CLIs available") {
		t.Fatalf("expected no CLI message, got:\n%s", output)
	}
}

func TestBuildInstructions_WarmupIncomplete(t *testing.T) {
	output := buildInstructions(nil, false, []string{"codex", "gemini"}, 0, nil)

	for _, expected := range []string{"codex", "gemini", "warmup in progress"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q", expected)
		}
	}
}

func TestBuildInstructions_TokenCount(t *testing.T) {
	output := buildInstructions(
		[]string{"claude", "codex", "gemini"},
		true,
		[]string{"claude", "codex", "gemini"},
		4,
		map[string]string{"coding": "codex", "review": "gemini"},
	)

	if len(output)/4 > 4000 {
		t.Fatalf("expected approximate token count <= 4000, got %d", len(output)/4)
	}
}

func TestBuildInstructions_LineCount(t *testing.T) {
	output := buildInstructions(
		[]string{"claude", "codex", "gemini"},
		true,
		nil,
		0,
		nil,
	)

	lineCount := strings.Count(output, "\n") + 1
	if lineCount > 120 {
		t.Fatalf("expected line count <= 120, got %d", lineCount)
	}
}

func TestBuildInstructions_HasAllSections(t *testing.T) {
	output := buildInstructions(
		[]string{"codex"},
		true,
		nil,
		1,
		map[string]string{"coding": "codex"},
	)

	for _, expected := range []string{"Anti-Patterns", "First Actions", "guide", "delegate"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q", expected)
		}
	}
}

func TestBuildInstructions_RoleMap(t *testing.T) {
	output := buildInstructions(
		[]string{"codex"},
		true,
		nil,
		0,
		map[string]string{"coding": "codex"},
	)

	if !strings.Contains(output, "- codex: coding") {
		t.Fatalf("expected warm CLI display to include mapped role, got:\n%s", output)
	}
}
