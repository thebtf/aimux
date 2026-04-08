package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverCallerSkills_ClaudeSkills(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tdd.md", "review.md"} {
		if err := os.WriteFile(filepath.Join(skillsDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := DiscoverCallerSkills(dir)
	want := []string{"review", "tdd"}
	assertStringSlice(t, want, got)
}

func TestDiscoverCallerSkills_AgentsDir(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "coding.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got := DiscoverCallerSkills(dir)
	want := []string{"coding"}
	assertStringSlice(t, want, got)
}

func TestDiscoverCallerSkills_EmptyCwd(t *testing.T) {
	got := DiscoverCallerSkills("")
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestDiscoverCallerSkills_MissingDirs(t *testing.T) {
	got := DiscoverCallerSkills("/nonexistent-path-that-does-not-exist")
	if got != nil {
		t.Fatalf("expected nil for nonexistent dir, got %v", got)
	}
}

func TestDiscoverCallerSkills_AGENTS_MD(t *testing.T) {
	dir := t.TempDir()
	content := "### reviewer\n### debugger\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got := DiscoverCallerSkills(dir)
	want := []string{"debugger", "reviewer"}
	assertStringSlice(t, want, got)
}

// assertStringSlice compares two string slices for equality.
func assertStringSlice(t *testing.T, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("length mismatch: want %v, got %v", want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("index %d: want %q, got %q", i, want[i], got[i])
		}
	}
}
