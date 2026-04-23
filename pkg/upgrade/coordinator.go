// Package upgrade orchestrates the aimux binary upgrade flow.
//
// The Coordinator type manages the full upgrade lifecycle: detection, download,
// checksum verification, and application. In Phase 1, it delegated to the
// existing updater.ApplyUpdate path (deferred restart behavior). Phase 3 adds
// the daemon-control seam for muxcore-backed graceful restart.
package upgrade

import (
	"context"
	"errors"
	"fmt"
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

	// ApplyUpdate installs the latest release. Defaults to updater.ApplyUpdate.
	ApplyUpdate ApplyUpdateFunc

	// Logger receives structured log output for upgrade lifecycle events.
	// May be nil; logging is skipped when nil.
	Logger *logger.Logger
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

var errHotSwapUnsupported = errors.New("hot-swap requires daemon-side muxcore graceful-restart seam")

// Apply downloads and applies the upgrade according to mode.
//
// ModeDeferred always uses the legacy non-live path.
// ModeHotSwap requires a daemon-side graceful restart seam and fails if unavailable.
// ModeAuto tries the daemon-side seam first and falls back to deferred with
// HandoffError populated when live restart cannot be completed.
func (c *Coordinator) Apply(ctx context.Context, mode Mode) (*Result, error) {
	applyUpdate := c.applyUpdateFunc()

	release, err := applyUpdate(ctx, c.Version)
	if err != nil {
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

func (c *Coordinator) applyUpdateFunc() ApplyUpdateFunc {
	if c.ApplyUpdate != nil {
		return c.ApplyUpdate
	}
	return updater.ApplyUpdate
}

func (c *Coordinator) afterDeferredInstall(release *updater.Release) *Result {
	if !c.EngineMode {
		if c.Logger != nil {
			c.Logger.Info("upgrade: non-engine manual restart required prev_version=%s new_version=%s",
				c.Version, release.Version)
		}
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
	if c.Logger != nil {
		c.Logger.Info("upgrade: deferred restart staged prev_version=%s new_version=%s",
			c.Version, release.Version)
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
	if err := c.GracefulRestart(ctx, defaultGracefulRestartDrainTimeout); err != nil {
		return nil, fmt.Errorf("daemon graceful restart: %w", err)
	}
	if c.Logger != nil {
		c.Logger.Info("upgrade: daemon graceful restart requested prev_version=%s new_version=%s",
			c.Version, release.Version)
	}
	return &Result{
		Method:          "hot_swap",
		PreviousVersion: c.Version,
		NewVersion:      release.Version,
		Message:         "Binary updated. Daemon graceful restart requested.",
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
