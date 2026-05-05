package updater_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestApplyError_NilInnerErrorStillDescribesFailure(t *testing.T) {
	err := (&updater.ApplyError{}).Error()
	if err == "" {
		t.Fatal("ApplyError with nil inner error returned empty message")
	}
}

func TestDownload_MockUpdateSelectsExpectedBinaryFromMultiEntryZip(t *testing.T) {
	targetPath := filepath.Join(t.TempDir(), "aimux-dev-next.exe")
	zipBytes := makeZip(t, map[string]string{
		"README.txt":                       "not the binary",
		"bin/" + filepath.Base(targetPath): "binary-content",
	})

	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/release.json", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(updater.Release{
			Version:  "9.9.9",
			AssetURL: server.URL + "/asset.zip",
		}); err != nil {
			t.Fatalf("encode release: %v", err)
		}
	})
	mux.HandleFunc("/asset.zip", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(zipBytes); err != nil {
			t.Fatalf("write asset: %v", err)
		}
	})
	server = httptest.NewServer(mux)
	defer server.Close()
	t.Setenv("AIMUX_TEST_UPDATE_BASE_URL", server.URL)

	release, err := updater.Download(context.Background(), "0.0.1", targetPath)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if release == nil {
		t.Fatal("Download returned nil release")
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "binary-content" {
		t.Fatalf("target content = %q; want binary-content", string(data))
	}
}

func makeZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// TestDownload_RequiresNetwork documents that Download requires GitHub access.
func TestDownload_RequiresNetwork(t *testing.T) {
	t.Skip("Download requires GitHub network access — skipped in offline/unit test environment")
}

// TestApplyUpdate_RequiresNetwork documents that ApplyUpdate requires GitHub access.
func TestApplyUpdate_RequiresNetwork(t *testing.T) {
	t.Skip("ApplyUpdate requires GitHub network access — skipped in offline/unit test environment")
}
