package gate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasTestsNodeRequiresScriptsTest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"test":"1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := HasTests(root, ProjectTypeNode)
	if err != nil {
		t.Fatalf("HasTests node without script: %v", err)
	}
	if got {
		t.Fatal("HasTests node without scripts.test = true, want false")
	}

	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"vitest run"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = HasTests(root, ProjectTypeNode)
	if err != nil {
		t.Fatalf("HasTests node with script: %v", err)
	}
	if !got {
		t.Fatal("HasTests node with scripts.test = false, want true")
	}
}

func TestHasTestsPythonRequiresPythonTestFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test_notes.txt"), []byte("not python"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := HasTests(root, ProjectTypePython)
	if err != nil {
		t.Fatalf("HasTests python with txt: %v", err)
	}
	if got {
		t.Fatal("HasTests python with test_notes.txt = true, want false")
	}

	if err := os.WriteFile(filepath.Join(root, "test_sample.py"), []byte("def test_ok(): pass"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = HasTests(root, ProjectTypePython)
	if err != nil {
		t.Fatalf("HasTests python with py: %v", err)
	}
	if !got {
		t.Fatal("HasTests python with test_sample.py = false, want true")
	}
}

func TestHasTestsPythonIgnoresEmptyTestsDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := HasTests(root, ProjectTypePython)
	if err != nil {
		t.Fatalf("HasTests python with empty tests dir: %v", err)
	}
	if got {
		t.Fatal("HasTests python with empty tests dir = true, want false")
	}

	if err := os.WriteFile(filepath.Join(root, "tests", "conftest.py"), []byte("import pytest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = HasTests(root, ProjectTypePython)
	if err != nil {
		t.Fatalf("HasTests python with conftest: %v", err)
	}
	if !got {
		t.Fatal("HasTests python with conftest.py = false, want true")
	}
}

func TestDetectProjectTypeEmptyCWDIsUnknown(t *testing.T) {
	got, err := DetectProjectType("")
	if err != nil {
		t.Fatalf("DetectProjectType empty cwd: %v", err)
	}
	if got != ProjectTypeUnknown {
		t.Fatalf("DetectProjectType empty cwd = %s, want %s", got, ProjectTypeUnknown)
	}
}
