package prompts

import (
	"strings"
	"testing"
)

func TestRenderStructuralIncludesTargetCriteriaAndFocus(t *testing.T) {
	rendered, err := RenderStructural(fixtureData())
	if err != nil {
		t.Fatalf("RenderStructural returned error: %v", err)
	}
	assertPromptContains(t, rendered, "HEAD~1..HEAD")
	assertPromptContains(t, rendered, "BuildClean")
	assertPromptContains(t, rendered, "naming")
	assertPromptContains(t, rendered, "abstraction")
}

func TestRenderBehaviouralIncludesTargetCriteriaAndFocus(t *testing.T) {
	rendered, err := RenderBehavioural(fixtureData())
	if err != nil {
		t.Fatalf("RenderBehavioural returned error: %v", err)
	}
	assertPromptContains(t, rendered, "HEAD~1..HEAD")
	assertPromptContains(t, rendered, "TestsPass")
	assertPromptContains(t, rendered, "happy path")
	assertPromptContains(t, rendered, "side effects")
}

func TestRenderAdversarialIncludesTargetCriteriaAndFocus(t *testing.T) {
	rendered, err := RenderAdversarial(fixtureData())
	if err != nil {
		t.Fatalf("RenderAdversarial returned error: %v", err)
	}
	assertPromptContains(t, rendered, "HEAD~1..HEAD")
	assertPromptContains(t, rendered, "NoSecretLeak")
	assertPromptContains(t, rendered, "security")
	assertPromptContains(t, rendered, "hostile inputs")
}

func fixtureData() RenderData {
	return RenderData{
		Target: "HEAD~1..HEAD",
		Criteria: []CriterionView{
			{Name: "BuildClean", Description: "go build exits 0"},
			{Name: "TestsPass", Description: "go test exits 0"},
			{Name: "NoSecretLeak", Description: "logs do not expose credentials"},
		},
	}
}

func assertPromptContains(t *testing.T, rendered string, want string) {
	t.Helper()
	if strings.TrimSpace(rendered) == "" {
		t.Fatal("rendered prompt is empty")
	}
	if !strings.Contains(rendered, want) {
		t.Fatalf("rendered prompt missing %q:\n%s", want, rendered)
	}
}
