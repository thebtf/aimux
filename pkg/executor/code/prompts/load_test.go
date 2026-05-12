package prompts

import (
	"strings"
	"testing"
)

func TestRenderDriverProducesReadonlyUnifiedDiffPrompt(t *testing.T) {
	rendered, err := RenderDriver(fixtureData())
	if err != nil {
		t.Fatalf("RenderDriver returned error: %v", err)
	}
	assertContains(t, rendered, "rename old to new")
	assertContains(t, rendered, "Do NOT write any files")
	assertContains(t, rendered, "unified diff")
}

func TestRenderNavigatorProducesVerdictPrompt(t *testing.T) {
	rendered, err := RenderNavigator(fixtureData())
	if err != nil {
		t.Fatalf("RenderNavigator returned error: %v", err)
	}
	assertContains(t, rendered, "rename old to new")
	assertContains(t, rendered, "diff --git")
	assertContains(t, rendered, "TestsPass")
	assertContains(t, rendered, `"verdict"`)
	assertContains(t, rendered, "APPLY|REVISE|RETRY|ESCALATE")
}

func fixtureData() RenderData {
	return RenderData{
		Prompt:         "rename old to new",
		ProjectContext: "CWD=/workspace",
		Diff:           "diff --git a/note.txt b/note.txt\n-old\n+new",
		CriteriaList: []CriterionView{
			{Name: "BuildClean", Description: "build exits 0", Weight: 0.30},
			{Name: "TestsPass", Description: "tests pass", Weight: 0.30},
		},
	}
}

func assertContains(t *testing.T, text string, want string) {
	t.Helper()
	if strings.TrimSpace(text) == "" {
		t.Fatal("rendered prompt is empty")
	}
	if !strings.Contains(text, want) {
		t.Fatalf("rendered prompt missing %q:\n%s", want, text)
	}
}
