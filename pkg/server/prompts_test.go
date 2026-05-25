package server

import (
	"context"
	"os"
	"path/filepath"
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
