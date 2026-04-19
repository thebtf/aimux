package updater_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thebtf/aimux/pkg/updater"
)

// TestVerifyChecksum_NilRelease verifies that nil release returns an error.
func TestVerifyChecksum_NilRelease(t *testing.T) {
	err := updater.VerifyChecksum("/any/path", nil)
	if err == nil {
		t.Fatal("expected error for nil release, got nil")
	}
}

// TestVerifyChecksum_MissingFile verifies that a non-existent path returns an error.
func TestVerifyChecksum_MissingFile(t *testing.T) {
	r := &updater.Release{Version: "1.0.0"}
	err := updater.VerifyChecksum("/nonexistent/path/that/does/not/exist", r)
	if err == nil {
		t.Fatal("expected error for non-existent binary path, got nil")
	}
}

// TestVerifyChecksum_ValidFile verifies that an existing file with non-nil release succeeds.
func TestVerifyChecksum_ValidFile(t *testing.T) {
	// Create a temp file to simulate a downloaded binary.
	dir := t.TempDir()
	tmpFile := filepath.Join(dir, "aimux-test")
	if err := os.WriteFile(tmpFile, []byte("fake binary content"), 0o755); err != nil {
		t.Fatalf("create temp file: %v", err)
	}

	r := &updater.Release{
		Version:   "4.4.0",
		AssetName: "aimux_windows_amd64.zip",
	}
	err := updater.VerifyChecksum(tmpFile, r)
	if err != nil {
		t.Fatalf("VerifyChecksum with valid file and release: got error %v, want nil", err)
	}
}

// TestDownload_RequiresNetwork documents that Download requires GitHub access.
func TestDownload_RequiresNetwork(t *testing.T) {
	t.Skip("Download requires GitHub network access — skipped in offline/unit test environment")
}

// TestApplyUpdate_RequiresNetwork documents that ApplyUpdate requires GitHub access.
func TestApplyUpdate_RequiresNetwork(t *testing.T) {
	t.Skip("ApplyUpdate requires GitHub network access — skipped in offline/unit test environment")
}
