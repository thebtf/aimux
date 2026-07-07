package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/thebtf/mcp-mux/muxcore"
)

// TestBuildSkillData_UsesCallerProjectCWD verifies that buildSkillData uses the
// session/project CWD from ProjectContext for skill discovery, not the daemon's
// os.Getwd() result (engram #243 regression guard).
func TestBuildSkillData_UsesCallerProjectCWD(t *testing.T) {
	// Create a temp dir that looks like a caller's project: it has a .claude/skills dir
	// with a skill file that would NOT be present in the daemon CWD.
	callerDir := t.TempDir()
	skillsDir := filepath.Join(callerDir, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	markerSkill := "regression-marker-243"
	if err := os.WriteFile(filepath.Join(skillsDir, markerSkill+".md"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv := testServer(t)

	// Build a context that carries the caller's ProjectContext.
	pc := muxcore.ProjectContext{
		ID:  muxcore.ProjectContextID(callerDir),
		Cwd: callerDir,
	}
	ctx := context.WithValue(context.Background(), projectContextKey{}, pc)

	data := srv.buildSkillData(ctx, mcp.GetPromptRequest{})

	// The marker skill must appear in CallerSkills — proving the caller's CWD was used.
	found := false
	for _, name := range data.CallerSkills {
		if name == markerSkill {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("CallerSkills = %v; want %q to be present (caller CWD skill discovery failed)",
			data.CallerSkills, markerSkill)
	}
}

// TestBuildSkillData_FallsBackSafelyWithNoProjectContext verifies that buildSkillData
// does not call os.Getwd() and returns empty CallerSkills when no ProjectContext is
// present in ctx (engram #243: safe fallback, no daemon CWD leak).
func TestBuildSkillData_FallsBackSafelyWithNoProjectContext(t *testing.T) {
	srv := testServer(t)

	// Context carries no ProjectContext — simulates direct stdio mode.
	ctx := context.Background()

	data := srv.buildSkillData(ctx, mcp.GetPromptRequest{})

	// CallerSkills must be nil/empty: no project context → no discovery attempt.
	// The daemon CWD must NOT be leaked (whatever happens to be in aimux's own CWD
	// is not a valid caller skills list).
	if len(data.CallerSkills) != 0 {
		t.Errorf("CallerSkills = %v; want empty when no ProjectContext in ctx", data.CallerSkills)
	}
}

func TestGuidePromptDescriptionAdvertisesReducedGuideResource(t *testing.T) {
	srv := testServer(t)

	description := guidePromptDescription(t, srv)
	for _, want := range []string{"reduced", "aimux://guides/caller"} {
		if !strings.Contains(description, want) {
			t.Fatalf("guide prompt description missing %q: %q", want, description)
		}
	}
	if strings.Contains(description, "13 MCP tools") {
		t.Fatalf("guide prompt description must not use stale 13-tool wording: %q", description)
	}
}

func TestGuidePromptContentMentionsReviewFacadeAndReducedSurface(t *testing.T) {
	srv := testServer(t)

	result, err := srv.handleGuidePrompt(context.Background(), mcp.GetPromptRequest{})
	if err != nil {
		t.Fatalf("handleGuidePrompt: %v", err)
	}
	text := guidePromptText(t, result)
	lowerText := strings.ToLower(text)
	for _, want := range []string{"review code through the dedicated facade", "| review |", "don't expect exec/agent/workflow tools"} {
		if !strings.Contains(lowerText, want) {
			t.Fatalf("guide prompt content missing %q:\n%s", want, text)
		}
	}
	for _, want := range []string{"aimux://guides", "aimux://guides/caller"} {
		if !strings.Contains(text, want) {
			t.Fatalf("guide prompt content missing guide resource %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "13 MCP tools") {
		t.Fatalf("guide prompt content must not use stale 13-tool wording:\n%s", text)
	}
}

func guidePromptDescription(t *testing.T, srv *Server) string {
	t.Helper()
	response := srv.mcp.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"prompts/list","params":{}}`))
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
		t.Fatalf("prompts/list result missing or wrong type: %s", raw)
	}
	prompts, ok := result["prompts"].([]any)
	if !ok {
		t.Fatalf("prompts missing or wrong type: %s", raw)
	}
	for _, item := range prompts {
		prompt, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("prompt item type = %T, want map", item)
		}
		if prompt["name"] == "guide" {
			description, _ := prompt["description"].(string)
			return description
		}
	}
	t.Fatalf("guide prompt not registered; prompts=%v", prompts)
	return ""
}

func guidePromptText(t *testing.T, result *mcp.GetPromptResult) string {
	t.Helper()
	if result == nil || len(result.Messages) == 0 {
		t.Fatal("guide prompt returned no messages")
	}
	text, ok := result.Messages[0].Content.(mcp.TextContent)
	if !ok {
		t.Fatalf("guide prompt content type = %T, want TextContent", result.Messages[0].Content)
	}
	return text.Text
}
