package driver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDiscoverBinary_InPATH(t *testing.T) {
	// "echo" (or "cmd" on Windows) should always be found via PATH
	name := "echo"
	if runtime.GOOS == "windows" {
		name = "cmd"
	}

	result := DiscoverBinary(name, nil)
	if result == "" {
		t.Errorf("expected to find %q in PATH, got empty", name)
	}
}

func TestDiscoverBinary_NotFound(t *testing.T) {
	result := DiscoverBinary("nonexistent_binary_xyz_99999", nil)
	if result != "" {
		t.Errorf("expected empty for missing binary, got %q", result)
	}
}

func TestDiscoverBinary_InWellKnownDir(t *testing.T) {
	// Create a temp dir and a fake binary
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "testcli")
	if runtime.GOOS == "windows" {
		fakeBin += ".exe"
	}
	if err := os.WriteFile(fakeBin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Monkey-patch: test probeInDir directly since we can't modify wellKnownDirs
	result := probeInDir(dir, "testcli")
	if result == "" {
		t.Error("expected to find testcli in dir, got empty")
	}
}

func TestDiscoverBinary_ViaSearchPaths(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "mycli")
	if runtime.GOOS == "windows" {
		fakeBin += ".exe"
	}
	if err := os.WriteFile(fakeBin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Should find via profile search_paths
	result := DiscoverBinary("mycli", []string{dir})
	if result == "" {
		t.Error("expected to find mycli via search_paths, got empty")
	}
	if result != fakeBin {
		t.Errorf("result = %q, want %q", result, fakeBin)
	}
}

func TestDiscoverBinary_GlobSearchPaths(t *testing.T) {
	// Create structure: base/v1/bin/mycli
	base := t.TempDir()
	binDir := filepath.Join(base, "v1", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(binDir, "mycli")
	if runtime.GOOS == "windows" {
		fakeBin += ".exe"
	}
	if err := os.WriteFile(fakeBin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Glob pattern should match
	pattern := filepath.Join(base, "*", "bin")
	result := DiscoverBinary("mycli", []string{pattern})
	if result == "" {
		t.Error("expected to find mycli via glob search_paths, got empty")
	}
}

func TestProbeInDir_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "mycli")
	os.MkdirAll(subdir, 0o755)

	result := probeInDir(dir, "mycli")
	if result != "" {
		t.Errorf("expected empty for directory match, got %q", result)
	}
}

func TestBinaryCandidates(t *testing.T) {
	candidates := binaryCandidates("test")
	if runtime.GOOS == "windows" {
		if len(candidates) < 2 {
			t.Errorf("Windows should have multiple candidates, got %d", len(candidates))
		}
		if candidates[0] != "test.exe" {
			t.Errorf("first candidate = %q, want test.exe", candidates[0])
		}
	} else {
		if len(candidates) != 1 || candidates[0] != "test" {
			t.Errorf("Unix candidates = %v, want [test]", candidates)
		}
	}
}

func TestWellKnownDirs_NotEmpty(t *testing.T) {
	dirs := wellKnownDirs()
	if len(dirs) == 0 {
		t.Error("expected at least some well-known dirs")
	}
}

func TestVersionManagerGlobs_NotEmpty(t *testing.T) {
	globs := versionManagerGlobs()
	if len(globs) == 0 {
		t.Error("expected at least some version manager globs")
	}
}
