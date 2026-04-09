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
			wantName: "debugger",
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

func TestAutoSelectAgent_EmptyPromptFallsBackToImplementer(t *testing.T) {
	reg := agents.NewRegistry()
	agents.RegisterBuiltins(reg)

	got, score := agents.AutoSelectAgent(reg, "")
	if got == nil {
		t.Fatal("expected fallback to implementer, got nil")
	}
	if got.Name != "implementer" {
		t.Errorf("fallback agent = %q, want implementer", got.Name)
	}
	if score != 0 {
		t.Errorf("fallback score = %d, want 0", score)
	}
}

func TestAutoSelectAgent_NoMatchFallsBackToImplementer(t *testing.T) {
	reg := agents.NewRegistry()
	agents.RegisterBuiltins(reg)

	got, score := agents.AutoSelectAgent(reg, "xyzzy quux frobnicate zap")
	if got == nil {
		t.Fatal("expected fallback to implementer, got nil")
	}
	if got.Name != "implementer" {
		t.Errorf("fallback agent = %q, want implementer", got.Name)
	}
	_ = score
}
