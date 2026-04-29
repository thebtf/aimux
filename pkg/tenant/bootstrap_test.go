package tenant

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBootstrap_FreshInstall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tenants.yaml")

	if err := WriteBootstrapFile(path, 1001); err != nil {
		t.Fatalf("WriteBootstrapFile: %v", err)
	}

	// File must exist.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	// Must parse successfully.
	snap, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile after bootstrap: %v", err)
	}

	// Must contain exactly one entry with the correct uid and operator role.
	cfg, ok := snap.byUID[1001]
	if !ok {
		t.Fatal("uid 1001 not found in bootstrap snapshot")
	}
	if cfg.Role != RoleOperator {
		t.Errorf("bootstrap role = %q, want %q", cfg.Role, RoleOperator)
	}
}

func TestBootstrap_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tenants.yaml")

	// Write an existing file with different content.
	existingContent := `
tenants:
  - name: original
    uid: 9001
    role: plain
`
	if err := os.WriteFile(path, []byte(existingContent), 0o600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	// Bootstrap should be a no-op.
	if err := WriteBootstrapFile(path, 1001); err != nil {
		t.Fatalf("WriteBootstrapFile on existing file: %v", err)
	}

	// File content must be unchanged.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != existingContent {
		t.Errorf("file content changed after bootstrap no-op:\ngot:  %q\nwant: %q", string(data), existingContent)
	}
}

func TestBootstrap_EmptyExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tenants.yaml")

	// Create an empty file.
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("create empty file: %v", err)
	}

	// Bootstrap must treat it as "exists" and be a no-op.
	if err := WriteBootstrapFile(path, 1001); err != nil {
		t.Fatalf("WriteBootstrapFile on empty file: %v", err)
	}

	// File should still be empty.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("empty file was written to by bootstrap: %q", string(data))
	}
}

func TestBootstrap_FileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce POSIX file mode bits — mode check deferred to v2")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "tenants.yaml")

	if err := WriteBootstrapFile(path, 1001); err != nil {
		t.Fatalf("WriteBootstrapFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("file mode = %o, want 0600", mode)
	}
}
