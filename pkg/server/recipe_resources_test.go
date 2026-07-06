package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/workflow"
)

func TestRecipeResourceTemplatesRegistered(t *testing.T) {
	srv := testServerWithLoom(t)

	response := srv.mcp.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"resources/templates/list","params":{}}`))
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response %s: %v", raw, err)
	}
	result, ok := decoded["result"].(map[string]any)
	if !ok {
		t.Fatalf("resources/templates/list result missing or wrong type: %s", raw)
	}
	templates, ok := result["resourceTemplates"].([]any)
	if !ok {
		t.Fatalf("resourceTemplates missing or wrong type: %s", raw)
	}

	want := map[string]bool{
		"aimux://recipes":             false,
		"aimux://recipes/{recipe_id}": false,
	}
	for _, item := range templates {
		template, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("resource template item type = %T, want map", item)
		}
		if uri, ok := template["uriTemplate"].(string); ok {
			if _, tracked := want[uri]; tracked {
				want[uri] = true
			}
		}
	}
	for uri, found := range want {
		if !found {
			t.Fatalf("resource template %q not registered; templates=%v", uri, templates)
		}
	}
}

func TestRecipeListResourceReturnsCompactDeterministicCatalog(t *testing.T) {
	srv := testServerWithLoom(t)

	got := readRecipeListResource(t, srv, "aimux://recipes")
	wantIDs := []string{"code-review", "second-opinion", "security-audit", "debug-investigation"}
	if got["count"] != float64(len(wantIDs)) {
		t.Fatalf("count = %v, want %d; payload=%v", got["count"], len(wantIDs), got)
	}
	items, ok := got["recipes"].([]any)
	if !ok || len(items) != len(wantIDs) {
		t.Fatalf("recipes = %#v, want %d entries", got["recipes"], len(wantIDs))
	}
	for i, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("recipes[%d] type = %T, want map", i, item)
		}
		if entry["id"] != wantIDs[i] {
			t.Fatalf("recipe order[%d] = %v, want %s", i, entry["id"], wantIDs[i])
		}
		assertRecipeResourceReadOnlyShape(t, entry)
	}
}

func TestRecipeDetailResourceReturnsRecipe(t *testing.T) {
	srv := testServerWithLoom(t)

	got := readRecipeDetailResource(t, srv, "aimux://recipes/code-review")
	if got["id"] != "code-review" {
		t.Fatalf("id = %v, want code-review; payload=%v", got["id"], got)
	}
	assertRecipeResourceReadOnlyShape(t, got)
	if got["gate_default"] != true {
		t.Fatalf("gate_default = %v, want true for code-review", got["gate_default"])
	}
}

func TestRecipeDetailResourceReturnsWorkflowBackedMetadata(t *testing.T) {
	srv := testServerWithLoom(t)
	tests := []struct {
		uri        string
		id         string
		workflowID string
		steps      []workflow.WorkflowStep
	}{
		{uri: "aimux://recipes/security-audit", id: "security-audit", workflowID: "secaudit", steps: workflow.SecurityAuditSteps()},
		{uri: "aimux://recipes/debug-investigation", id: "debug-investigation", workflowID: "debug", steps: workflow.DebugSteps()},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := readRecipeDetailResource(t, srv, tt.uri)
			if got["id"] != tt.id {
				t.Fatalf("id = %v, want %s; payload=%v", got["id"], tt.id, got)
			}
			assertRecipeResourceReadOnlyShape(t, got)
			if got["recipe_workflow_id"] != tt.workflowID {
				t.Fatalf("recipe_workflow_id = %v, want %s; payload=%v", got["recipe_workflow_id"], tt.workflowID, got)
			}
			if source, ok := got["recipe_workflow_source"].(string); !ok || !strings.Contains(source, "pkg/workflow/") {
				t.Fatalf("recipe_workflow_source = %#v, want pkg/workflow source", got["recipe_workflow_source"])
			}
			if gotSteps := resourceStringSlice(t, got["recipe_workflow_steps"]); !stringSlicesEqual(gotSteps, recipeWorkflowStepNames(tt.steps)) {
				t.Fatalf("recipe_workflow_steps = %#v, want %#v", gotSteps, recipeWorkflowStepNames(tt.steps))
			}
		})
	}
}

func TestRecipeDetailResourceUnknownReturnsAvailableRecipes(t *testing.T) {
	srv := testServerWithLoom(t)

	got := readRecipeDetailResource(t, srv, "aimux://recipes/missing")
	if got["status"] != "not_found" {
		t.Fatalf("status = %v, want not_found; payload=%v", got["status"], got)
	}
	if got["error"] != "recipe not found" {
		t.Fatalf("error = %v, want recipe not found; payload=%v", got["error"], got)
	}
	if got["recipe_id"] != "missing" {
		t.Fatalf("recipe_id = %v, want missing", got["recipe_id"])
	}
	available := resourceStringSlice(t, got["available_recipes"])
	want := []string{"code-review", "second-opinion", "security-audit", "debug-investigation"}
	if !stringSlicesEqual(available, want) {
		t.Fatalf("available_recipes = %#v, want deterministic IDs %#v", available, want)
	}
}

func readRecipeListResource(t *testing.T, srv *Server, uri string) map[string]any {
	t.Helper()
	contents, err := srv.handleRecipeListResource(context.Background(), mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	})
	return decodeTaskResourceContents(t, contents, err, uri)
}

func readRecipeDetailResource(t *testing.T, srv *Server, uri string) map[string]any {
	t.Helper()
	contents, err := srv.handleRecipeDetailResource(context.Background(), mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	})
	return decodeTaskResourceContents(t, contents, err, uri)
}

func assertRecipeResourceReadOnlyShape(t *testing.T, item map[string]any) {
	t.Helper()
	if item["description"] == "" {
		t.Fatalf("recipe %v missing description: %v", item["id"], item)
	}
	if item["task_class"] != "review" {
		t.Fatalf("recipe %v task_class = %v, want review", item["id"], item["task_class"])
	}
	if item["read_only"] != true {
		t.Fatalf("recipe %v read_only = %v, want true", item["id"], item["read_only"])
	}
	if phases, ok := item["phases"].([]any); !ok || len(phases) == 0 {
		t.Fatalf("recipe %v phases = %#v, want non-empty list", item["id"], item["phases"])
	}
	if !stringSliceContains(resourceStringSlice(t, item["policy_needs"]), "read_only") {
		t.Fatalf("recipe %v policy_needs = %#v, want read_only", item["id"], item["policy_needs"])
	}
	if !stringSlicesEqual(resourceStringSlice(t, item["output_resources"]), []string{"task_snapshot", "task_events", "task_progress"}) {
		t.Fatalf("recipe %v output_resources = %#v, want read-only task resources", item["id"], item["output_resources"])
	}
	for _, forbidden := range []string{"prompt", "env", "transcript"} {
		if _, leaked := item[forbidden]; leaked {
			t.Fatalf("recipe %v exposed %q in resource payload: %v", item["id"], forbidden, item)
		}
	}
}

func resourceStringSlice(t *testing.T, value any) []string {
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

func recipeWorkflowStepNames(steps []workflow.WorkflowStep) []string {
	out := make([]string, len(steps))
	for i, step := range steps {
		out[i] = step.Name
	}
	return out
}
