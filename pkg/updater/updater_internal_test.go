package updater

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/creativeprojects/go-selfupdate"
)

func TestDownloadReleaseBinaryExtractsCanonicalArchiveBinaryToGeneratedTarget(t *testing.T) {
	targetPath := filepath.Join(t.TempDir(), "aimux-update-12345-0.bin")

	assetDir := t.TempDir()
	zipPath := filepath.Join(assetDir, "asset.zip")
	makeTestZip(t, zipPath, "bin/aimux.exe", "binary-content")
	checksumPath := filepath.Join(assetDir, "checksums.txt")
	writeChecksumFile(t, checksumPath, "aimux_9.9.9_windows_amd64.zip", zipPath)
	server := httptest.NewServer(http.FileServer(http.Dir(assetDir)))
	defer server.Close()

	release := &selfupdate.Release{
		AssetName: "aimux_9.9.9_windows_amd64.zip",
		AssetURL:  server.URL + "/asset.zip",
		ValidationChain: []struct {
			ValidationAssetID                       int64
			ValidationAssetName, ValidationAssetURL string
		}{
			{
				ValidationAssetName: "checksums.txt",
				ValidationAssetURL:  server.URL + "/checksums.txt",
			},
		},
	}
	if err := downloadReleaseBinary(t.Context(), release, targetPath); err != nil {
		t.Fatalf("downloadReleaseBinary: %v", err)
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "binary-content" {
		t.Fatalf("target content = %q, want binary-content", string(data))
	}
}

func makeTestZip(t *testing.T, path, entryName, content string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(file)
	entry, err := zw.Create(entryName)
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := entry.Write([]byte(content)); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}
}

func writeChecksumFile(t *testing.T, path, assetName, assetPath string) {
	t.Helper()
	data, err := os.ReadFile(assetPath)
	if err != nil {
		t.Fatalf("read asset for checksum: %v", err)
	}
	sum := sha256.Sum256(data)
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%x  %s\n", sum, assetName)), 0o644); err != nil {
		t.Fatalf("write checksum file: %v", err)
	}
}
