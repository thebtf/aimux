package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestGuideResourceTemplatesRegistered(t *testing.T) {
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
		"aimux://guides":        false,
		"aimux://guides/caller": false,
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

func TestGuideCatalogResourceReturnsCallerGuide(t *testing.T) {
	srv := testServerWithLoom(t)

	got := readGuideCatalogResource(t, srv, "aimux://guides")
	if got["status"] != "ok" {
		t.Fatalf("status = %v, want ok; payload=%v", got["status"], got)
	}
	guides, ok := got["guides"].([]any)
	if !ok || len(guides) != 1 {
		t.Fatalf("guides = %#v, want one caller guide", got["guides"])
	}
	caller := guides[0].(map[string]any)
	if caller["id"] != "caller" || caller["uri"] != "aimux://guides/caller" {
		t.Fatalf("caller guide entry = %v", caller)
	}
}

func TestCallerGuideResourceDocumentsSupportedSurface(t *testing.T) {
	srv := testServerWithLoom(t)

	guide := readCallerGuideResource(t, srv, "aimux://guides/caller")
	for _, want := range []string{
		"# aimux Caller Guide",
		"task",
		"think(action=start|step|finalize)",
		"aimux://tasks",
		"aimux://tasks/{task_id}/viewer",
		"aimux://recipes",
		"code-review",
		"second-opinion",
		"recipe_replay_cache_hit",
		"recipe_replay_source_task_id",
		"worktree_path",
		"mcp-launcher -mode tool -tool task",
		"workflow",
	} {
		if !strings.Contains(guide, want) {
			t.Fatalf("caller guide missing %q:\n%s", want, guide)
		}
	}
}

func readGuideCatalogResource(t *testing.T, srv *Server, uri string) map[string]any {
	t.Helper()
	contents, err := srv.handleGuideCatalogResource(context.Background(), mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	})
	return decodeTaskResourceContents(t, contents, err, uri)
}

func readCallerGuideResource(t *testing.T, srv *Server, uri string) string {
	t.Helper()
	contents, err := srv.handleCallerGuideResource(context.Background(), mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	})
	if err != nil {
		t.Fatalf("caller guide read(%s): %v", uri, err)
	}
	if len(contents) != 1 {
		t.Fatalf("caller guide contents len = %d, want 1", len(contents))
	}
	text, ok := contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("caller guide content type = %T, want TextResourceContents", contents[0])
	}
	if text.URI != uri {
		t.Fatalf("caller guide URI = %q, want %q", text.URI, uri)
	}
	if text.MIMEType != callerGuideMIMEType {
		t.Fatalf("caller guide MIMEType = %q, want %s", text.MIMEType, callerGuideMIMEType)
	}
	return text.Text
}
