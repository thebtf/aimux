package gate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunGoProjectPassesBuildTypecheckTestsInSequence(t *testing.T) {
	dir := newGoFixture(t, map[string]string{
		"calc.go":      "package fixture\n\nfunc Add(a, b int) int { return a + b }\n",
		"calc_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal(\"bad add\") } }\n",
	})

	result := Run(context.Background(), Project{CWD: dir, PhaseTimeout: 30 * time.Second})

	if result.Status != StatusPassed {
		t.Fatalf("status = %s, want %s: %#v", result.Status, StatusPassed, result)
	}
	assertPhaseSequence(t, result, PhaseBuild, PhaseTypeCheck, PhaseTests)
	for _, phase := range result.Phases {
		if phase.Status != StatusPassed {
			t.Fatalf("%s status = %s, want passed: %s", phase.Name, phase.Status, phase.Log)
		}
		if phase.Log == "" {
			t.Fatalf("%s log is empty", phase.Name)
		}
	}
}

func TestRunGoProjectBuildFailureStopsAtBuild(t *testing.T) {
	dir := newGoFixture(t, map[string]string{
		"broken.go":      "package fixture\n\nfunc Broken() { missing }\n",
		"broken_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) {}\n",
	})

	result := Run(context.Background(), Project{CWD: dir, PhaseTimeout: 30 * time.Second})

	if result.Status != StatusFailed {
		t.Fatalf("status = %s, want %s: %#v", result.Status, StatusFailed, result)
	}
	if result.Reason != string(PhaseBuild) {
		t.Fatalf("reason = %q, want %q", result.Reason, PhaseBuild)
	}
	assertPhaseSequence(t, result, PhaseBuild)
	if result.Phases[0].Log == "" {
		t.Fatal("build failure log is empty")
	}
}

func TestRunGoProjectWithoutTestsSkipsWithWarning(t *testing.T) {
	dir := newGoFixture(t, map[string]string{
		"calc.go": "package fixture\n\nfunc Add(a, b int) int { return a + b }\n",
	})

	result := Run(context.Background(), Project{CWD: dir, PhaseTimeout: 30 * time.Second})

	if result.Status != StatusSkipped {
		t.Fatalf("status = %s, want %s: %#v", result.Status, StatusSkipped, result)
	}
	if result.Reason != string(PhaseTests) {
		t.Fatalf("reason = %q, want %q", result.Reason, PhaseTests)
	}
	if result.Warning == "" {
		t.Fatal("warning is empty")
	}
	assertPhaseSequence(t, result, PhaseBuild, PhaseTypeCheck, PhaseTests)
	if result.Phases[2].Status != StatusSkipped {
		t.Fatalf("tests phase status = %s, want skipped", result.Phases[2].Status)
	}
}

func TestDetectProjectTypeMarkerOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"fixture\"\nversion = \"0.1.0\"\n")
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"fixture\"\n")
	writeFile(t, dir, "package.json", "{}\n")
	writeFile(t, dir, "go.mod", "module example.com/fixture\n\ngo 1.21\n")

	projectType, err := DetectProjectType(dir)
	if err != nil {
		t.Fatalf("DetectProjectType returned error: %v", err)
	}
	if projectType != ProjectTypeGo {
		t.Fatalf("project type = %s, want %s", projectType, ProjectTypeGo)
	}
}

func newGoFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/gatefixture\n\ngo 1.21\n")
	for path, content := range files {
		writeFile(t, dir, path, content)
	}
	return dir
}

func writeFile(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertPhaseSequence(t *testing.T, result Result, phases ...Phase) {
	t.Helper()
	if len(result.Phases) != len(phases) {
		t.Fatalf("phase count = %d, want %d: %#v", len(result.Phases), len(phases), result.Phases)
	}
	for i, phase := range phases {
		if result.Phases[i].Name != phase {
			t.Fatalf("phase[%d] = %s, want %s", i, result.Phases[i].Name, phase)
		}
	}
}
