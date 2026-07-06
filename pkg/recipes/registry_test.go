package recipes

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/workflow"
)

func TestListReturnsDeterministicReadOnlyRecipes(t *testing.T) {
	got := List()
	wantIDs := []string{"code-review", "second-opinion", "security-audit", "debug-investigation"}
	if len(got) != len(wantIDs) {
		t.Fatalf("recipe count = %d, want %d", len(got), len(wantIDs))
	}
	for i, wantID := range wantIDs {
		if got[i].ID != wantID {
			t.Fatalf("recipe order[%d] = %q, want %q", i, got[i].ID, wantID)
		}
	}
	for _, recipe := range got {
		if recipe.Title == "" || recipe.Description == "" {
			t.Fatalf("recipe %s missing title/description: %#v", recipe.ID, recipe)
		}
		if recipe.TaskClass != TaskClassReview {
			t.Fatalf("recipe %s task_class = %q, want %q", recipe.ID, recipe.TaskClass, TaskClassReview)
		}
		if !recipe.ReadOnly {
			t.Fatalf("recipe %s ReadOnly = false, want true", recipe.ID)
		}
		if len(recipe.Phases) == 0 {
			t.Fatalf("recipe %s phases empty", recipe.ID)
		}
		if len(recipe.PolicyNeeds) == 0 {
			t.Fatalf("recipe %s policy needs empty", recipe.ID)
		}
		if !containsString(recipe.PolicyNeeds, PolicyReadOnly) {
			t.Fatalf("recipe %s policy needs = %#v, want read_only", recipe.ID, recipe.PolicyNeeds)
		}
		if !containsString(recipe.RequiredArgs, "target") {
			t.Fatalf("recipe %s required args = %#v, want target", recipe.ID, recipe.RequiredArgs)
		}
		if !stringSlicesEqual(recipe.OutputResources, []string{"task_snapshot", "task_events", "task_progress"}) {
			t.Fatalf("recipe %s output resources = %#v, want read-only task resources", recipe.ID, recipe.OutputResources)
		}
	}
}

func TestWorkflowBackedRecipesExposeCompiledWorkflowMetadata(t *testing.T) {
	tests := []struct {
		id         string
		workflowID string
		steps      []workflow.WorkflowStep
	}{
		{id: "security-audit", workflowID: "secaudit", steps: workflow.SecurityAuditSteps()},
		{id: "debug-investigation", workflowID: "debug", steps: workflow.DebugSteps()},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			recipe, ok := Resolve(tt.id)
			if !ok {
				t.Fatalf("Resolve(%q) ok = false", tt.id)
			}
			payload := recipeJSONPayload(t, recipe)
			if payload["recipe_workflow_id"] != tt.workflowID {
				t.Fatalf("recipe_workflow_id = %v, want %s; payload=%v", payload["recipe_workflow_id"], tt.workflowID, payload)
			}
			if source, ok := payload["recipe_workflow_source"].(string); !ok || !strings.Contains(source, "pkg/workflow/") {
				t.Fatalf("recipe_workflow_source = %#v, want pkg/workflow source", payload["recipe_workflow_source"])
			}
			if !stringSlicesEqual(jsonStringSlice(t, payload["recipe_workflow_steps"]), workflowStepNames(tt.steps)) {
				t.Fatalf("recipe_workflow_steps = %#v, want workflow %s step names %#v", payload["recipe_workflow_steps"], tt.workflowID, workflowStepNames(tt.steps))
			}
		})
	}
}

func TestListReturnsCopies(t *testing.T) {
	got := List()
	got[0].ID = "mutated"
	got[0].Phases[0] = "mutated"

	again := List()
	if again[0].ID != "code-review" {
		t.Fatalf("registry ID mutated through caller-owned slice: %q", again[0].ID)
	}
	if again[0].Phases[0] == "mutated" {
		t.Fatalf("registry phases mutated through caller-owned nested slice: %#v", again[0].Phases)
	}
}

func TestResolveKnownAndUnknownRecipe(t *testing.T) {
	recipe, ok := Resolve(" code-review ")
	if !ok {
		t.Fatal("Resolve(code-review) ok = false")
	}
	if recipe.ID != "code-review" {
		t.Fatalf("recipe ID = %q, want code-review", recipe.ID)
	}
	if !recipe.GateDefault {
		t.Fatalf("code-review GateDefault = false, want true")
	}

	if _, ok := Resolve("missing"); ok {
		t.Fatal("Resolve(missing) ok = true, want false")
	}
}

func TestAvailableIDs(t *testing.T) {
	got := AvailableIDs()
	want := []string{"code-review", "second-opinion", "security-audit", "debug-investigation"}
	if len(got) != len(want) {
		t.Fatalf("AvailableIDs len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AvailableIDs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	got[0] = "mutated"
	if again := AvailableIDs(); again[0] != "code-review" {
		t.Fatalf("AvailableIDs returned mutable backing array: %#v", again)
	}
}

func recipeJSONPayload(t *testing.T, recipe Recipe) map[string]any {
	t.Helper()
	data, err := json.Marshal(recipe)
	if err != nil {
		t.Fatalf("marshal recipe %s: %v", recipe.ID, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal recipe %s: %v", recipe.ID, err)
	}
	return payload
}

func jsonStringSlice(t *testing.T, value any) []string {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("value = %#v, want JSON string array", value)
	}
	out := make([]string, len(items))
	for i, item := range items {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("value[%d] = %#v, want string", i, item)
		}
		out[i] = text
	}
	return out
}

func workflowStepNames(steps []workflow.WorkflowStep) []string {
	out := make([]string, len(steps))
	for i, step := range steps {
		out[i] = step.Name
	}
	return out
}

func stringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
