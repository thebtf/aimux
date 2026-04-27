// Package upgrade orchestrates the aimux binary upgrade flow.
//
// The Coordinator type manages the full upgrade lifecycle: detection, download,
// checksum verification, and application. In Phase 1, it delegated to the
// existing updater.ApplyUpdate path (deferred restart behavior). Phase 3 adds
// the daemon-control seam for muxcore-backed graceful restart.
package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/updater"
	"github.com/thebtf/mcp-mux/muxcore/control"
)

const (
	defaultApplyModeMessage            = "Binary updated. Restart aimux to load the new version."
	defaultGracefulRestartDrainTimeout = 10000
	defaultControlRequestTimeout       = 45 * time.Second
)

// ApplyUpdateFunc installs the latest binary release for the current version.
type ApplyUpdateFunc func(ctx context.Context, currentVersion string) (*updater.Release, error)

// GracefulRestartFunc requests a daemon-side graceful restart after an upgrade.
type GracefulRestartFunc func(ctx context.Context, drainTimeoutMs int) error

// HandoffStatus describes the daemon handoff counters relevant to truthful
// hot-swap reporting.
type HandoffStatus struct {
	Fallback uint64
}

// HandoffStatusFunc reads the current daemon handoff counters.
type HandoffStatusFunc func(ctx context.Context) (HandoffStatus, error)

// Mode controls how the upgrade is applied.
type Mode string

const (
	// ModeAuto tries hot-swap first and falls back to deferred on any failure.
	// This is the default mode for upgrade(action="apply").
	ModeAuto Mode = "auto"

	// ModeHotSwap requires hot-swap and returns an error if handoff fails.
	// Used for testing and for operators who need hard confirmation of live upgrade.
	ModeHotSwap Mode = "hot_swap"

	// ModeDeferred skips hot-swap and uses the legacy deferred restart behavior.
	// Equivalent to v4.3.0 upgrade behavior.
	ModeDeferred Mode = "deferred"
)

// SessionHandler is the minimal interface Coordinator requires from the
// muxcore session handler for deferred upgrade signalling.
type SessionHandler interface {
	// SetUpdatePending signals that a binary update has been staged.
	// The daemon will exit when all CC sessions disconnect.
	SetUpdatePending()
}

// Coordinator orchestrates the full upgrade lifecycle.
type Coordinator struct {
	// Version is the currently running binary version string (e.g. "4.3.0").
	Version string

	// BinaryPath is the absolute path to the running executable.
	// Populated by the caller via os.Executable() or selfupdate.ExecutablePath().
	BinaryPath string

	// SessionHandler provides lifecycle signals to the muxcore session layer.
	// May be nil when running outside engine mode (standalone stdio transport).
	SessionHandler SessionHandler

	// EngineMode indicates the daemon is running under the muxcore engine.
	// When false, daemon-side graceful restart is unavailable.
	EngineMode bool

	// GracefulRestart requests daemon-side graceful restart over the control socket.
	// Nil means the seam is unavailable.
	GracefulRestart GracefulRestartFunc

	// HandoffStatus reads the daemon's handoff counters before and after the
	// graceful restart request so the coordinator can distinguish real hot-swap
	// success from FR-8 fallback.
	HandoffStatus HandoffStatusFunc

	// ApplyUpdate installs the latest release. Defaults to updater.ApplyUpdate.
	ApplyUpdate ApplyUpdateFunc

	// Logger receives structured log output for upgrade lifecycle events.
	// May be nil; logging is skipped when nil.
	Logger *logger.Logger

	// Source is an optional path to a local binary to install.
	// When set, the coordinator skips GitHub download and uses this file directly.
	Source string

	applyInProgress atomic.Bool
}

// Result describes the outcome of an Apply call.
type Result struct {
	// Method is one of "hot_swap", "deferred", or "up_to_date".
	Method string

	// PreviousVersion is the version string before the upgrade.
	PreviousVersion string

	// NewVersion is the version string after the upgrade.
	NewVersion string

	// HandoffTransferred contains IDs of FD groups transferred during hot-swap.
	// Populated on successful hot-swap only; nil on deferred path.
	HandoffTransferred []string

	// HandoffDurationMs is the wall-clock time for the handoff protocol in ms.
	// Populated on successful hot-swap only; zero on deferred path.
	HandoffDurationMs int64

	// HandoffError describes why hot-swap failed, triggering a deferred fallback.
	// Populated when Method=="deferred" as a result of a failed hot-swap attempt.
	HandoffError string

	// Message is the human-readable status suitable for MCP tool response.
	Message string
}

var (
	errHotSwapUnsupported = errors.New("hot-swap requires daemon-side muxcore graceful-restart seam")
	errAlreadyInProgress  = errors.New("already_in_progress")
)

// Apply downloads and applies the upgrade according to mode.
//
// ModeDeferred always uses the legacy non-live path.
// ModeHotSwap requires a daemon-side graceful restart seam and fails if unavailable.
// ModeAuto tries the daemon-side seam first and falls back to deferred with
// HandoffError populated when live restart cannot be completed.
func (c *Coordinator) Apply(ctx context.Context, mode Mode, force bool) (result *Result, err error) {
	startedAt := time.Now()
	var release *updater.Release
	defer func() {
		c.logApplyOutcome(startedAt, mode, release, result, err)
	}()

	if !c.applyInProgress.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("apply update: %w", errAlreadyInProgress)
	}
	defer c.applyInProgress.Store(false)

	// Local binary source: skip GitHub download and install from local file.
	if c.Source != "" {
		release, err = c.applyFromLocal(ctx, c.Source)
		if err != nil {
			return nil, fmt.Errorf("apply local binary: %w", err)
		}
	} else {
		applyUpdate := c.applyUpdateFunc()

		effectiveVersion := c.Version
		if force {
			effectiveVersion = "0.0.0"
		}

		release, err = applyUpdate(ctx, effectiveVersion)
		if err != nil {
			if errors.Is(err, updater.ErrChecksumVerification) {
				return nil, fmt.Errorf("apply update: %w", err)
			}
			if errors.Is(err, updater.ErrDiskFull) {
				failedRelease, ok := updater.ReleaseFromError(err)
				if !ok || failedRelease == nil {
					return nil, fmt.Errorf("apply update: %w", err)
				}
				release = failedRelease
				switch mode {
				case ModeAuto, "":
					fallback := c.afterDeferredInstall(failedRelease)
					fallback.HandoffError = "disk_full"
					if fallback.Message != "" {
						fallback.Message += " Hot-swap unavailable: disk_full"
					}
					return fallback, nil
				default:
					return nil, fmt.Errorf("apply update: %w", err)
				}
			}
			return nil, fmt.Errorf("apply update: %w", err)
		}
		if release == nil {
			return &Result{
				Method:          "up_to_date",
				PreviousVersion: c.Version,
				NewVersion:      c.Version,
				Message:         "Already up to date.",
			}, nil
		}
	}

	switch mode {
	case ModeDeferred:
		return c.afterDeferredInstall(release), nil
	case ModeHotSwap:
		return c.afterHotSwapInstall(ctx, release)
	case ModeAuto, "":
		result, hotSwapErr := c.afterHotSwapInstall(ctx, release)
		if hotSwapErr == nil {
			return result, nil
		}
		if errors.Is(hotSwapErr, updater.ErrChecksumVerification) {
			return nil, hotSwapErr
		}
		if errors.Is(hotSwapErr, updater.ErrDiskFull) {
			fallback := c.afterDeferredInstall(release)
			fallback.HandoffError = "disk_full"
			if fallback.Message != "" {
				fallback.Message += " Hot-swap unavailable: disk_full"
			}
			return fallback, nil
		}
		fallback := c.afterDeferredInstall(release)
		fallback.HandoffError = hotSwapErr.Error()
		if fallback.Message != "" {
			fallback.Message += " Hot-swap unavailable: " + hotSwapErr.Error()
		}
		return fallback, nil
	default:
		return nil, fmt.Errorf("unknown upgrade mode %q", mode)
	}
}

func (c *Coordinator) logApplyOutcome(startedAt time.Time, requestedMode Mode, release *updater.Release, result *Result, applyErr error) {
	if c.Logger == nil {
		return
	}

	prevVersion := c.Version
	newVersion := c.Version
	method := normalizeApplyMode(requestedMode)
	transferredIDs := []string{}
	var durationMs int64

	if result != nil {
		if result.PreviousVersion != "" {
			prevVersion = result.PreviousVersion
		}
		if result.NewVersion != "" {
			newVersion = result.NewVersion
		}
		if result.Method != "" {
			method = result.Method
		}
		if result.HandoffTransferred != nil {
			transferredIDs = result.HandoffTransferred
		}
		durationMs = result.HandoffDurationMs
	}
	if durationMs == 0 {
		durationMs = time.Since(startedAt).Milliseconds()
	}
	if applyErr != nil && release != nil && release.Version != "" {
		newVersion = release.Version
	}

	message := fmt.Sprintf(
		"module=server.upgrade event=upgrade_complete prev_version=%s new_version=%s method=%s duration_ms=%d transferred_ids=%v",
		prevVersion,
		newVersion,
		method,
		durationMs,
		transferredIDs,
	)

	switch {
	case applyErr != nil:
		c.Logger.Error("%s error=%q", message, applyErr.Error())
	case result != nil && result.HandoffError != "":
		c.Logger.Warn("%s handoff_error=%q", message, result.HandoffError)
	default:
		c.Logger.Info("%s", message)
	}
}

func normalizeApplyMode(mode Mode) string {
	if mode == "" {
		return string(ModeAuto)
	}
	return string(mode)
}

func (c *Coordinator) applyUpdateFunc() ApplyUpdateFunc {
	if c.ApplyUpdate != nil {
		return c.ApplyUpdate
	}
	return func(ctx context.Context, currentVersion string) (*updater.Release, error) {
		return updater.ApplyUpdateAt(ctx, currentVersion, c.BinaryPath)
	}
}

func (c *Coordinator) afterDeferredInstall(release *updater.Release) *Result {
	if !c.EngineMode {
		return &Result{
			Method:          "deferred",
			PreviousVersion: c.Version,
			NewVersion:      release.Version,
			Message:         defaultApplyModeMessage,
		}
	}

	if c.SessionHandler != nil {
		c.SessionHandler.SetUpdatePending()
	}
	return &Result{
		Method:          "deferred",
		PreviousVersion: c.Version,
		NewVersion:      release.Version,
		Message:         "Binary updated. Daemon will restart when all CC sessions disconnect.",
	}
}

func (c *Coordinator) afterHotSwapInstall(ctx context.Context, release *updater.Release) (*Result, error) {
	if !c.EngineMode {
		return nil, fmt.Errorf("%w: outside engine mode", errHotSwapUnsupported)
	}
	if c.GracefulRestart == nil {
		return nil, fmt.Errorf("%w: control seam not configured", errHotSwapUnsupported)
	}
	if c.HandoffStatus == nil {
		return nil, fmt.Errorf("%w: handoff status seam not configured", errHotSwapUnsupported)
	}

	before, err := c.HandoffStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("daemon handoff status before graceful restart: %w", err)
	}
	if err := c.GracefulRestart(ctx, defaultGracefulRestartDrainTimeout); err != nil {
		return nil, fmt.Errorf("daemon graceful restart: %w", err)
	}
	after, err := c.HandoffStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("daemon handoff status after graceful restart: %w", err)
	}
	if after.Fallback > before.Fallback {
		return nil, fmt.Errorf("daemon graceful restart fell back to deferred restart")
	}
	return &Result{
		Method:          "hot_swap",
		PreviousVersion: c.Version,
		NewVersion:      release.Version,
		Message:         "Binary updated. Daemon handoff completed successfully.",
	}, nil
}

// applyFromLocal installs a binary from a local file path instead of downloading
// from GitHub. It delegates the atomic replacement to atomicReplaceBinary, which
// provides platform-appropriate semantics (stage-then-swap on Windows; direct
// rename on Unix).
func (c *Coordinator) applyFromLocal(_ context.Context, sourcePath string) (*updater.Release, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("source binary not found: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("source path is a directory, not a binary: %s", sourcePath)
	}

	if err := atomicReplaceBinary(c.BinaryPath, sourcePath); err != nil {
		return nil, fmt.Errorf("rename current binary: %w", err)
	}

	return &updater.Release{
		Version:      "local-dev",
		AssetName:    filepath.Base(sourcePath),
		ReleaseNotes: fmt.Sprintf("Installed from local source: %s", sourcePath),
	}, nil
}

// NewControlSocketGracefulRestartFunc builds the production daemon-control seam.
func NewControlSocketGracefulRestartFunc(socketPath string) GracefulRestartFunc {
	if socketPath == "" {
		return nil
	}
	return func(ctx context.Context, drainTimeoutMs int) error {
		timeout := defaultControlRequestTimeout
		if deadline, ok := ctx.Deadline(); ok {
			if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
				timeout = remaining
			}
		}
		resp, err := control.SendWithTimeout(socketPath, control.Request{
			Cmd:            "graceful-restart",
			DrainTimeoutMs: drainTimeoutMs,
		}, timeout)
		if err != nil {
			return err
		}
		if resp == nil {
			return fmt.Errorf("empty control response")
		}
		if !resp.OK {
			if resp.Message != "" {
				return errors.New(resp.Message)
			}
			return fmt.Errorf("graceful restart rejected")
		}
		return nil
	}
}

// NewControlSocketHandoffStatusFunc builds a production status seam that reads
// daemon handoff counters over the control socket.
func NewControlSocketHandoffStatusFunc(socketPath string) HandoffStatusFunc {
	if socketPath == "" {
		return nil
	}
	return func(ctx context.Context) (HandoffStatus, error) {
		timeout := defaultControlRequestTimeout
		if deadline, ok := ctx.Deadline(); ok {
			if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
				timeout = remaining
			}
		}
		resp, err := control.SendWithTimeout(socketPath, control.Request{Cmd: "status"}, timeout)
		if err != nil {
			return HandoffStatus{}, err
		}
		if resp == nil {
			return HandoffStatus{}, fmt.Errorf("empty control response")
		}
		if !resp.OK {
			if resp.Message != "" {
				return HandoffStatus{}, errors.New(resp.Message)
			}
			return HandoffStatus{}, fmt.Errorf("status rejected")
		}
		var payload struct {
			Handoff struct {
				Fallback uint64 `json:"fallback"`
			} `json:"handoff"`
		}
		if err := json.Unmarshal(resp.Data, &payload); err != nil {
			return HandoffStatus{}, fmt.Errorf("decode status response: %w", err)
		}
		return HandoffStatus{Fallback: payload.Handoff.Fallback}, nil
	}
}
