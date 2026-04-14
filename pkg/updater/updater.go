// Package updater provides binary self-update from GitHub releases.
//
// Uses creativeprojects/go-selfupdate for release detection, download,
// checksum verification, and atomic binary replacement.
package updater

import (
	"context"
	"fmt"
	"runtime"

	"github.com/creativeprojects/go-selfupdate"
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
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
	})
	if err != nil {
		return nil, fmt.Errorf("create updater: %w", err)
	}

	latest, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug(DefaultSlug))
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

// ApplyUpdate downloads and installs the specified release, replacing the
// current executable. The caller is responsible for restarting the process.
func ApplyUpdate(ctx context.Context, currentVersion string) (*Release, error) {
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
	})
	if err != nil {
		return nil, fmt.Errorf("create updater: %w", err)
	}

	latest, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug(DefaultSlug))
	if err != nil {
		return nil, fmt.Errorf("detect latest: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("no release found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if latest.LessOrEqual(currentVersion) {
		return nil, nil // already up to date
	}

	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return nil, fmt.Errorf("locate executable: %w", err)
	}

	if err := updater.UpdateTo(ctx, latest, exe); err != nil {
		return nil, fmt.Errorf("update binary: %w", err)
	}

	return &Release{
		Version:      latest.Version(),
		AssetName:    latest.AssetName,
		ReleaseNotes: latest.ReleaseNotes,
		PublishedAt:  latest.PublishedAt.Format("2006-01-02T15:04:05Z"),
	}, nil
}
