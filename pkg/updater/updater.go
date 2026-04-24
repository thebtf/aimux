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
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/creativeprojects/go-selfupdate"
	selfupdateUpdate "github.com/creativeprojects/go-selfupdate/update"
)

const (
	// DefaultSlug is the GitHub repository slug for aimux releases.
	DefaultSlug          = "thebtf/aimux"
	mockUpdateBaseURLEnv = "AIMUX_TEST_UPDATE_BASE_URL"
)

var testHooks = struct {
	check          func(ctx context.Context, currentVersion string) (*Release, error)
	download       func(ctx context.Context, currentVersion string, targetPath string) (*Release, error)
	verifyChecksum func(binaryPath string, release *Release) error
	install        func(newBinaryPath string, currentExePath string) error
}{
	check:          nil,
	download:       nil,
	verifyChecksum: nil,
	install:        nil,
}

var (
	ErrChecksumVerification = errors.New("checksum_verification_failed")
	ErrDiskFull             = errors.New("disk_full")
)

// ApplyError carries the discovered release when apply/install fails after download.
type ApplyError struct {
	Release *Release
	Err     error
}

func (e *ApplyError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *ApplyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ReleaseFromError extracts release metadata from an apply error chain.
func ReleaseFromError(err error) (*Release, bool) {
	var applyErr *ApplyError
	if !errors.As(err, &applyErr) || applyErr == nil || applyErr.Release == nil {
		return nil, false
	}
	return applyErr.Release, true
}

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
	if baseURL := os.Getenv(mockUpdateBaseURLEnv); baseURL != "" {
		return checkMockUpdate(ctx, currentVersion, baseURL)
	}
	if testHooks.check != nil {
		return testHooks.check(ctx, currentVersion)
	}

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
	if baseURL := os.Getenv(mockUpdateBaseURLEnv); baseURL != "" {
		return downloadMockUpdate(ctx, currentVersion, targetPath, baseURL)
	}
	if testHooks.download != nil {
		return testHooks.download(ctx, currentVersion, targetPath)
	}

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
	if testHooks.verifyChecksum != nil {
		return testHooks.verifyChecksum(binaryPath, release)
	}
	if release == nil {
		return fmt.Errorf("verify checksum: %w: release is nil", ErrChecksumVerification)
	}
	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("verify checksum: %w: binary not found at %s: %w", ErrChecksumVerification, binaryPath, err)
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
	var err error
	if testHooks.install != nil {
		err = testHooks.install(newBinaryPath, currentExePath)
	} else {
		f, openErr := os.Open(newBinaryPath)
		if openErr != nil {
			return fmt.Errorf("open new binary %s: %w", newBinaryPath, openErr)
		}
		defer f.Close()

		err = selfupdateUpdate.Apply(f, selfupdateUpdate.Options{
			TargetPath: currentExePath,
		})
	}
	if err != nil {
		if isDiskFullError(err) {
			return fmt.Errorf("install to %s: %w: %w", currentExePath, ErrDiskFull, err)
		}
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
	return ApplyUpdateAt(ctx, currentVersion, exe)
}

// ApplyUpdateAt downloads and installs the latest release over currentExePath.
// This variant exists so the upgrade coordinator can hot-swap a known binary path
// without relying on selfupdate.ExecutablePath().
func ApplyUpdateAt(ctx context.Context, currentVersion string, currentExePath string) (*Release, error) {
	// Download to a temp directory with the binary name matching the expected
	// filename inside the release zip. go-selfupdate's UpdateTo extracts the
	// archive entry whose name matches filepath.Base(targetPath). If the target
	// has a random temp suffix, extraction fails with "executable not found in
	// zip file". Using a temp DIR with the real binary name inside it avoids
	// this while still placing the temp on the same filesystem for atomic rename.
	tmpDir, err := os.MkdirTemp(filepath.Dir(currentExePath), "aimux-update-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	tmpPath := filepath.Join(tmpDir, filepath.Base(currentExePath))

	release, err := Download(ctx, currentVersion, tmpPath)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, nil // already up to date
	}

	if err := VerifyChecksum(tmpPath, release); err != nil {
		return nil, &ApplyError{
			Release: release,
			Err:     fmt.Errorf("checksum verification: %w", err),
		}
	}

	if err := Install(tmpPath, currentExePath); err != nil {
		return nil, &ApplyError{
			Release: release,
			Err:     fmt.Errorf("install binary: %w", err),
		}
	}

	return release, nil
}

// SetTestHooks installs deterministic hooks for tests that need to control the
// update source without network access. Passing nil resets hooks to production.
func SetTestHooks(
	check func(ctx context.Context, currentVersion string) (*Release, error),
	download func(ctx context.Context, currentVersion string, targetPath string) (*Release, error),
	verifyChecksum func(binaryPath string, release *Release) error,
	install func(newBinaryPath string, currentExePath string) error,
) {
	testHooks.check = check
	testHooks.download = download
	testHooks.verifyChecksum = verifyChecksum
	testHooks.install = install
}

func isDiskFullError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ENOSPC) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no space left on device") ||
		strings.Contains(message, "disk full") ||
		strings.Contains(message, "not enough space")
}

func checkMockUpdate(ctx context.Context, currentVersion string, baseURL string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/release.json", nil)
	if err != nil {
		return nil, fmt.Errorf("mock release request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mock release fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mock release status: %s", resp.Status)
	}
	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("mock release decode: %w", err)
	}
	if compareVersionStrings(release.Version, currentVersion) <= 0 {
		return nil, nil
	}
	return &release, nil
}

func downloadMockUpdate(ctx context.Context, currentVersion string, targetPath string, baseURL string) (*Release, error) {
	release, err := checkMockUpdate(ctx, currentVersion, baseURL)
	if err != nil || release == nil {
		return release, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, release.AssetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("mock asset request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mock asset fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mock asset status: %s", resp.Status)
	}
	zipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mock asset read: %w", err)
	}
	binary, err := extractSingleBinary(zipBytes)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(targetPath, binary, 0o755); err != nil {
		return nil, fmt.Errorf("write mock binary: %w", err)
	}
	return release, nil
}

func extractSingleBinary(zipBytes []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("mock asset zip: %w", err)
	}
	if len(zr.File) != 1 {
		return nil, fmt.Errorf("mock asset expected 1 zip entry, got %d", len(zr.File))
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		return nil, fmt.Errorf("mock asset open: %w", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("mock asset read entry: %w", err)
	}
	return data, nil
}

func compareVersionStrings(a string, b string) int {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for len(as) < 3 {
		as = append(as, "0")
	}
	for len(bs) < 3 {
		bs = append(bs, "0")
	}
	for i := 0; i < 3; i++ {
		ai, _ := strconv.Atoi(as[i])
		bi, _ := strconv.Atoi(bs[i])
		if ai > bi {
			return 1
		}
		if ai < bi {
			return -1
		}
	}
	return 0
}
