package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
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
	if got["count"] != float64(2) {
		t.Fatalf("count = %v, want 2; payload=%v", got["count"], got)
	}
	items, ok := got["recipes"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("recipes = %#v, want two entries", got["recipes"])
	}
	first := items[0].(map[string]any)
	second := items[1].(map[string]any)
	if first["id"] != "code-review" || second["id"] != "second-opinion" {
		t.Fatalf("recipe order = [%v %v], want [code-review second-opinion]", first["id"], second["id"])
	}
	for _, item := range []map[string]any{first, second} {
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
		if policyNeeds, ok := item["policy_needs"].([]any); !ok || len(policyNeeds) == 0 {
			t.Fatalf("recipe %v policy_needs = %#v, want non-empty list", item["id"], item["policy_needs"])
		}
		if outputResources, ok := item["output_resources"].([]any); !ok || len(outputResources) == 0 {
			t.Fatalf("recipe %v output_resources = %#v, want non-empty list", item["id"], item["output_resources"])
		}
		for _, forbidden := range []string{"prompt", "env", "transcript"} {
			if _, leaked := item[forbidden]; leaked {
				t.Fatalf("recipe %v exposed %q in compact catalog: %v", item["id"], forbidden, item)
			}
		}
	}
}

func TestRecipeDetailResourceReturnsRecipe(t *testing.T) {
	srv := testServerWithLoom(t)

	got := readRecipeDetailResource(t, srv, "aimux://recipes/code-review")
	if got["id"] != "code-review" {
		t.Fatalf("id = %v, want code-review; payload=%v", got["id"], got)
	}
	if got["title"] == "" || got["description"] == "" {
		t.Fatalf("detail missing title/description: %v", got)
	}
	if got["task_class"] != "review" {
		t.Fatalf("task_class = %v, want review", got["task_class"])
	}
	if got["read_only"] != true {
		t.Fatalf("read_only = %v, want true", got["read_only"])
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
	available, ok := got["available_recipes"].([]any)
	if !ok || len(available) != 2 {
		t.Fatalf("available_recipes = %#v, want two recipe IDs", got["available_recipes"])
	}
	if available[0] != "code-review" || available[1] != "second-opinion" {
		t.Fatalf("available_recipes = %#v, want deterministic IDs", available)
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
