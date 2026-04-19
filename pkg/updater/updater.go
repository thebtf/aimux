// Package updater provides binary self-update from GitHub releases.
//
// Uses creativeprojects/go-selfupdate for release detection, download,
// checksum verification, and atomic binary replacement.
//
// The three-step API (Download, VerifyChecksum, Install) supports the
// hot-swap upgrade flow (Phase 3) where the binary is downloaded to a
// temp path, verified, and installed atomically. ApplyUpdate is preserved
// as a backwards-compatible wrapper for Phase 1.
package updater

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/creativeprojects/go-selfupdate"
	selfupdateUpdate "github.com/creativeprojects/go-selfupdate/update"
)

const (
	// DefaultSlug is the GitHub repository slug for aimux releases.
	DefaultSlug = "thebtf/aimux"
)

// Release holds information about an available update.
type Release struct {
	Version      string `json:"version"`
	AssetName    string `json:"asset_name"`
	AssetURL     string `json:"asset_url"`
	ReleaseNotes string `json:"release_notes"`
	PublishedAt  string `json:"published_at"`
}

// CheckUpdate detects the latest release and compares it with the current version.
// Returns nil release if already up to date or no release found.
func CheckUpdate(ctx context.Context, currentVersion string) (*Release, error) {
	u, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
	})
	if err != nil {
		return nil, fmt.Errorf("create updater: %w", err)
	}

	latest, found, err := u.DetectLatest(ctx, selfupdate.ParseSlug(DefaultSlug))
	if err != nil {
		return nil, fmt.Errorf("detect latest: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("no release found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	if latest.LessOrEqual(currentVersion) {
		return nil, nil // already up to date
	}

	return &Release{
		Version:      latest.Version(),
		AssetName:    latest.AssetName,
		AssetURL:     latest.AssetURL,
		ReleaseNotes: latest.ReleaseNotes,
		PublishedAt:  latest.PublishedAt.Format("2006-01-02T15:04:05Z"),
	}, nil
}

// Download detects the latest release and downloads it to targetPath.
//
// Checksum validation is performed by the embedded ChecksumValidator
// during the UpdateTo call — the file at targetPath is already verified
// when this function returns successfully.
//
// Returns nil release if currentVersion is already up to date (no download performed).
// The caller is responsible for cleaning up targetPath on error.
func Download(ctx context.Context, currentVersion string, targetPath string) (*Release, error) {
	u, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
	})
	if err != nil {
		return nil, fmt.Errorf("create updater: %w", err)
	}

	latest, found, err := u.DetectLatest(ctx, selfupdate.ParseSlug(DefaultSlug))
	if err != nil {
		return nil, fmt.Errorf("detect latest: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("no release found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if latest.LessOrEqual(currentVersion) {
		return nil, nil // already up to date
	}

	// UpdateTo downloads the asset, verifies the checksum via ChecksumValidator,
	// decompresses the archive, and writes the binary to targetPath.
	// targetPath can be any writable path — it does not need to be the current exe.
	if err := u.UpdateTo(ctx, latest, targetPath); err != nil {
		return nil, fmt.Errorf("download to %s: %w", targetPath, err)
	}

	return &Release{
		Version:      latest.Version(),
		AssetName:    latest.AssetName,
		AssetURL:     latest.AssetURL,
		ReleaseNotes: latest.ReleaseNotes,
		PublishedAt:  latest.PublishedAt.Format("2006-01-02T15:04:05Z"),
	}, nil
}

// VerifyChecksum validates that a downloaded binary exists and the release
// metadata is present.
//
// Note: the go-selfupdate ChecksumValidator already verifies the SHA256 checksum
// against checksums.txt during Download via UpdateTo. This function is the explicit
// post-download verification hook in the three-step flow. For Phase 1, it confirms
// file existence and non-nil release metadata. Full cryptographic re-verification
// (re-downloading checksums.txt) is reserved for Phase 3 where the temp path is
// separate from the install destination.
func VerifyChecksum(binaryPath string, release *Release) error {
	if release == nil {
		return fmt.Errorf("verify checksum: release is nil")
	}
	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("verify checksum: binary not found at %s: %w", binaryPath, err)
	}
	return nil
}

// Install atomically replaces currentExePath with the binary at newBinaryPath.
//
// Uses go-selfupdate/update.Apply for cross-platform safe replacement:
// on Windows, running executables cannot be overwritten directly
// (ERROR_ACCESS_DENIED), so the library renames the old binary before
// placing the new one. The old binary is hidden rather than deleted on Windows.
//
// newBinaryPath must be readable; currentExePath is the installation target.
func Install(newBinaryPath string, currentExePath string) error {
	f, err := os.Open(newBinaryPath)
	if err != nil {
		return fmt.Errorf("open new binary %s: %w", newBinaryPath, err)
	}
	defer f.Close()

	if err := selfupdateUpdate.Apply(f, selfupdateUpdate.Options{
		TargetPath: currentExePath,
	}); err != nil {
		return fmt.Errorf("install to %s: %w", currentExePath, err)
	}
	return nil
}

// ApplyUpdate downloads and installs the latest release, replacing the current
// executable. Preserved as a backwards-compatible wrapper calling
// Download → VerifyChecksum → Install.
//
// Phase 3 callers should prefer Download + VerifyChecksum + Install directly
// to control the download destination and enable the muxcore hot-swap flow.
func ApplyUpdate(ctx context.Context, currentVersion string) (*Release, error) {
	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return nil, fmt.Errorf("locate executable: %w", err)
	}

	// Download to a temp path first so the three-step flow is exercised
	// even in the backwards-compat wrapper. This ensures the split functions
	// are used in production and avoids the current-exe-is-target edge case.
	tmpPath := exe + ".update.tmp"
	defer os.Remove(tmpPath) // clean up temp regardless of outcome

	release, err := Download(ctx, currentVersion, tmpPath)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, nil // already up to date
	}

	if err := VerifyChecksum(tmpPath, release); err != nil {
		return nil, fmt.Errorf("checksum verification: %w", err)
	}

	if err := Install(tmpPath, exe); err != nil {
		return nil, fmt.Errorf("install binary: %w", err)
	}

	return release, nil
}
