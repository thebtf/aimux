package updater_test

import (
	"errors"
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

// TestVerifyChecksum_ValidFile verifies Phase 1 behavior: file existence + non-nil release metadata.
// Note: Cryptographic checksum re-verification is deferred to Phase 3 (the go-selfupdate
// ChecksumValidator already verifies SHA256 during Download via UpdateTo).
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

func TestVerifyChecksum_ClassifiesFailure(t *testing.T) {
	err := updater.VerifyChecksum("/nonexistent/path/that/does/not/exist", &updater.Release{Version: "1.0.0"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, updater.ErrChecksumVerification) {
		t.Fatalf("error = %v, want ErrChecksumVerification", err)
	}
}

func TestInstall_ClassifiesDiskFull(t *testing.T) {
	updater.SetTestHooks(nil, nil, nil, func(newBinaryPath string, currentExePath string) error {
		return errors.New("no space left on device")
	})
	defer updater.SetTestHooks(nil, nil, nil, nil)

	err := updater.Install("ignored-new-binary", "ignored-current-exe")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, updater.ErrDiskFull) {
		t.Fatalf("error = %v, want ErrDiskFull", err)
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
