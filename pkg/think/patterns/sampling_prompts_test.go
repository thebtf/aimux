package patterns

import (
	"strings"
	"testing"
)

func TestGetSamplingPrompt_Exists(t *testing.T) {
	p := GetSamplingPrompt("problem_decomposition")
	if p == nil {
		t.Fatal("expected non-nil SamplingPrompt for problem_decomposition")
	}
	if p.SystemRole == "" {
		t.Error("expected non-empty SystemRole")
	}
	if p.UserPrompt == "" {
		t.Error("expected non-empty UserPrompt")
	}
	if p.MaxTokens <= 0 {
		t.Errorf("expected positive MaxTokens, got %d", p.MaxTokens)
	}
}

func TestGetSamplingPrompt_Missing(t *testing.T) {
	p := GetSamplingPrompt("nonexistent")
	if p != nil {
		t.Fatalf("expected nil for unknown pattern, got %+v", p)
	}
}

func TestFormatSamplingPrompt(t *testing.T) {
	prompt := &SamplingPrompt{
		SystemRole: "You are an expert.",
		UserPrompt: "Analyze this: {input}\n\nBe thorough.",
		MaxTokens:  1000,
	}

	system, user := FormatSamplingPrompt(prompt, "my test input")

	if system != "You are an expert." {
		t.Errorf("unexpected SystemRole: %q", system)
	}
	if strings.Contains(user, "{input}") {
		t.Error("expected {input} placeholder to be replaced")
	}
	if !strings.Contains(user, "my test input") {
		t.Errorf("expected user prompt to contain input text, got: %q", user)
	}
}
