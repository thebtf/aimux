package picker

import (
	"strings"
	"testing"
)

func TestErrNoHealthyCLI_Error_Empty(t *testing.T) {
	err := &ErrNoHealthyCLI{}
	got := err.Error()
	if !strings.Contains(got, "no healthy CLI available") {
		t.Errorf("expected 'no healthy CLI available' in error, got: %q", got)
	}
	if !strings.Contains(got, "no CLIs configured") {
		t.Errorf("expected 'no CLIs configured' in error, got: %q", got)
	}
}

func TestErrNoHealthyCLI_Error_SingleReason(t *testing.T) {
	err := &ErrNoHealthyCLI{
		Reasons: []CLIFailureReason{
			{CLI: "codex", Reason: "binary not found in PATH"},
		},
	}
	got := err.Error()
	if !strings.Contains(got, "codex") {
		t.Errorf("expected CLI name 'codex' in error, got: %q", got)
	}
	if !strings.Contains(got, "binary not found in PATH") {
		t.Errorf("expected reason in error, got: %q", got)
	}
}

func TestErrNoHealthyCLI_Error_MultipleReasons(t *testing.T) {
	err := &ErrNoHealthyCLI{
		Reasons: []CLIFailureReason{
			{CLI: "codex", Reason: "binary not found in PATH"},
			{CLI: "claude", Reason: "binary not found in PATH"},
			{CLI: "gemini", Reason: "binary not found in PATH"},
		},
	}
	got := err.Error()
	for _, cli := range []string{"codex", "claude", "gemini"} {
		if !strings.Contains(got, cli) {
			t.Errorf("expected CLI name %q in error, got: %q", cli, got)
		}
	}
}

func TestErrNoHealthyCLI_ImplementsError(t *testing.T) {
	var _ error = &ErrNoHealthyCLI{}
}
