// Package upgrade orchestrates the aimux binary upgrade flow.
//
// The Coordinator type manages the full upgrade lifecycle: detection, download,
// checksum verification, and application. In Phase 1, it delegates to the
// existing updater.ApplyUpdate path (deferred restart behavior). Phase 3 adds
// the hot-swap flow via muxcore handoff primitives.
package upgrade

import (
	"context"
	"errors"
	"fmt"

	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/updater"
)

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
// SetDraining will be added in Phase 3 for hot-swap graceful drain.
type SessionHandler interface {
	// SetUpdatePending signals that a binary update has been staged.
	// The daemon will exit when all CC sessions disconnect.
	SetUpdatePending()
}

// Coordinator orchestrates the full upgrade lifecycle.
// It owns the decision of whether to perform a hot-swap (Phase 3) or fall
// back to the deferred restart path (current v4.3.0 behavior).
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
	// When false, hot-swap is disabled — handoff requires engine IPC sockets.
	EngineMode bool

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

var errHotSwapUnsupported = errors.New("hot-swap requires daemon-side muxcore owner handoff; current coordinator only has session-side update adapter")

// Apply downloads and applies the upgrade according to mode.
//
// T007 boundary in the current codebase shape:
//   - real muxcore hot-swap requires daemon-side access to Owner.ShutdownForHandoff()
//     so the predecessor can transfer FD-bearing HandoffUpstream payloads to the
//     successor daemon's ReceiveHandoff()/NewOwnerFromHandoff() path.
//   - Coordinator currently receives only the session-side SetUpdatePending adapter,
//     so it cannot enumerate owners or produce real handoff payloads.
//
// Therefore ModeAuto attempts hot-swap, records the concrete blocker, and falls back
// to deferred. ModeHotSwap returns an error instead of faking a handoff.
func (c *Coordinator) Apply(ctx context.Context, mode Mode) (*Result, error) {
	switch mode {
	case ModeDeferred:
		return c.applyDeferred(ctx)
	case ModeHotSwap:
		_, err := c.tryHotSwap(ctx)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("tryHotSwap returned success without result")
	case ModeAuto, "":
		hotSwapResult, err := c.tryHotSwap(ctx)
		if err == nil {
			return hotSwapResult, nil
		}
		result, deferredErr := c.applyDeferred(ctx)
		if deferredErr != nil {
			return nil, deferredErr
		}
		result.HandoffError = err.Error()
		if result.Message != "" {
			result.Message += " Hot-swap unavailable: " + err.Error()
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unknown upgrade mode %q", mode)
	}
}

func (c *Coordinator) tryHotSwap(ctx context.Context) (*Result, error) {
	_ = ctx

	if !c.EngineMode {
		return nil, fmt.Errorf("%w: engine mode disabled", errHotSwapUnsupported)
	}
	if c.SessionHandler == nil {
		return nil, fmt.Errorf("%w: session handler unavailable", errHotSwapUnsupported)
	}

	return nil, fmt.Errorf("%w: coordinator cannot call daemon owner ShutdownForHandoff or successor ReceiveHandoff/NewOwnerFromHandoff", errHotSwapUnsupported)
}

// applyDeferred executes the legacy v4.3.0 upgrade path: download + install
// the new binary via updater.ApplyUpdate, then signal SetUpdatePending on the
// session handler. The daemon will exit and restart when all CC sessions disconnect.
func (c *Coordinator) applyDeferred(ctx context.Context) (*Result, error) {
	release, err := updater.ApplyUpdate(ctx, c.Version)
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

	// Signal deferred restart — daemon exits when all CC sessions disconnect.
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
	}, nil
}
