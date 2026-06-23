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
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/updater"
	"github.com/thebtf/mcp-mux/muxcore/control"
	muxengine "github.com/thebtf/mcp-mux/muxcore/engine"
)

const (
	defaultApplyModeMessage            = "Binary updated. Restart aimux to load the new version."
	defaultGracefulRestartDrainTimeout = 10000
	defaultControlRequestTimeout       = 45 * time.Second
	postExitInstallWatchdogTimeout     = defaultControlRequestTimeout
	postExitStopDelay                  = 500 * time.Millisecond
	activeEngineFileEnv                = "MCPMUX_ACTIVE_ENGINE_FILE"
	sourceStagingDirEnv                = "AIMUX_UPGRADE_SOURCE_DIR"
	allowSourceOutsideBinDirEnv        = "AIMUX_ALLOW_UPGRADE_SOURCE_OUTSIDE_BIN_DIR"
	stagedExecutablePrefix             = "aimux-stage-"
	legacyStagedExecutablePrefix       = "aimux-update-"
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

// ApplyUpdateAndRestartFunc swaps a staged binary into place and restarts the
// muxcore daemon namespace using the provider-owned restart choreography.
type ApplyUpdateAndRestartFunc func(ctx context.Context, opts muxengine.UpdateAndRestartOptions) (muxengine.UpdateAndRestartResult, error)

// RestartWithSuccessorFunc restarts the muxcore daemon namespace with an
// already-staged successor executable. Consumers with stable launcher +
// versioned engine topology update their active pointer before calling this.
type RestartWithSuccessorFunc func(ctx context.Context, opts muxengine.RestartWithSuccessorOptions) (muxengine.UpdateAndRestartResult, error)

// PostExitInstallFunc starts an out-of-process installer from the staged binary.
// The helper waits until the current daemon exits, then replaces CurrentExe and
// starts the replacement daemon. This is required on platforms that cannot
// rename the currently running executable in-process.
type PostExitInstallFunc func(ctx context.Context, opts PostExitInstallOptions) error

// PostExitInstallOptions describes the deferred self-replacement helper.
type PostExitInstallOptions struct {
	CurrentExe  string
	StagedExe   string
	DaemonFlag  string
	WaitTimeout time.Duration
}

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

type updatePendingLauncher interface {
	SetUpdatePendingLauncher(func() error)
}

type updatePendingStopScheduler interface {
	StopForUpdatePendingRestartAfter(time.Duration)
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

	// ApplyUpdateAndRestart installs a staged binary and restarts the muxcore
	// daemon in one provider-owned operation. Nil means the helper is unavailable.
	ApplyUpdateAndRestart ApplyUpdateAndRestartFunc

	// RestartWithSuccessor restarts the muxcore daemon after the caller has
	// staged a successor executable and updated the active engine pointer.
	// Nil means the successor topology helper is unavailable.
	RestartWithSuccessor RestartWithSuccessorFunc

	// PostExitInstall installs a staged binary after the current process exits.
	// Used for Windows self-replacement, where swap-before-exit fails because
	// the running executable is locked.
	PostExitInstall PostExitInstallFunc

	// Logger receives structured log output for upgrade lifecycle events.
	// May be nil; logging is skipped when nil.
	Logger *logger.Logger

	// Source is an optional path to a local binary to install.
	// When set, the coordinator skips GitHub download and uses this file directly.
	Source string

	// ActiveEngineFile is the stable-launcher pointer file to update before
	// RestartWithSuccessor. Empty falls back to MCPMUX_ACTIVE_ENGINE_FILE.
	ActiveEngineFile string

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

	// Topology captures step-level update/restart evidence for product-owned
	// health and smoke assertions.
	Topology UpdateTopology
}

// UpdateTopology describes the observable update/restart path.
type UpdateTopology struct {
	UpdateMethod       string
	RestartTopology    string
	DaemonWasRunning   bool
	LockAcquired       bool
	GracefulRestarted  bool
	FallbackShutdown   bool
	ReplacementStarted bool
	ReplacementReady   bool
	CleanedStale       int
	FailurePhase       string
	Warnings           []string
}

func (t UpdateTopology) IsZero() bool {
	return t.UpdateMethod == "" &&
		t.RestartTopology == "" &&
		!t.DaemonWasRunning &&
		!t.LockAcquired &&
		!t.GracefulRestarted &&
		!t.FallbackShutdown &&
		!t.ReplacementStarted &&
		!t.ReplacementReady &&
		t.CleanedStale == 0 &&
		t.FailurePhase == "" &&
		len(t.Warnings) == 0
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

	if c.shouldUsePostExitInstall(mode) {
		return c.applyWithPostExitInstall(ctx, normalizeMode(mode), force)
	}
	if c.shouldUseRestartWithSuccessor(mode) {
		return c.applyWithSuccessorRestart(ctx, normalizeMode(mode), force)
	}
	if c.shouldUseApplyUpdateAndRestart(mode) {
		return c.applyWithMuxcoreRestart(ctx, normalizeMode(mode), force)
	}

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

func normalizeMode(mode Mode) Mode {
	if mode == "" {
		return ModeAuto
	}
	return mode
}

func (c *Coordinator) shouldUseApplyUpdateAndRestart(mode Mode) bool {
	mode = normalizeMode(mode)
	return c.EngineMode &&
		c.ApplyUpdateAndRestart != nil &&
		(mode == ModeAuto || mode == ModeHotSwap) &&
		c.ApplyUpdate == nil
}

func (c *Coordinator) shouldUseRestartWithSuccessor(mode Mode) bool {
	mode = normalizeMode(mode)
	return c.EngineMode &&
		c.RestartWithSuccessor != nil &&
		c.activeEngineFilePath() != "" &&
		(mode == ModeAuto || mode == ModeHotSwap) &&
		c.ApplyUpdate == nil
}

func (c *Coordinator) shouldUsePostExitInstall(mode Mode) bool {
	mode = normalizeMode(mode)
	return c.EngineMode &&
		c.PostExitInstall != nil &&
		postExitInstallRequired() &&
		c.activeEngineFilePath() == "" &&
		(mode == ModeAuto || mode == ModeHotSwap) &&
		c.ApplyUpdate == nil
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
			Topology: UpdateTopology{
				UpdateMethod:    "deferred",
				RestartTopology: "direct_deferred",
			},
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
		Topology: UpdateTopology{
			UpdateMethod:    "deferred",
			RestartTopology: "session_drain",
		},
	}
}

func (c *Coordinator) applyWithSuccessorRestart(ctx context.Context, mode Mode, force bool) (*Result, error) {
	release, successorPath, cleanupStaged, err := c.prepareStagedUpdate(ctx, force)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return &Result{
			Method:          "up_to_date",
			PreviousVersion: c.Version,
			NewVersion:      c.Version,
			Message:         "Already up to date.",
		}, nil
	}

	activeEngineFile := c.activeEngineFilePath()
	if err := writeActiveEnginePointer(activeEngineFile, successorPath); err != nil {
		if cleanupStaged && successorPath != "" {
			_ = os.Remove(successorPath)
		}
		return nil, err
	}

	restart, err := c.RestartWithSuccessor(ctx, muxengine.RestartWithSuccessorOptions{
		SuccessorExe: successorPath,
		DrainTimeout: time.Duration(defaultGracefulRestartDrainTimeout) * time.Millisecond,
	})
	if err != nil {
		if mode == ModeHotSwap {
			return nil, err
		}
		fallback := c.afterDeferredInstall(release)
		fallback.HandoffError = err.Error()
		partial := muxengine.UpdateAndRestartResult{}
		var updateErr *muxengine.UpdateAndRestartError
		if errors.As(err, &updateErr) {
			partial = updateErr.Result
		}
		fallback.Topology = topologyFromMuxcoreRestart("deferred", partial, updateAndRestartFailurePhase(err))
		if fallback.Message != "" {
			fallback.Message += " Hot-swap unavailable: " + err.Error()
		}
		return fallback, nil
	}

	if restart.GracefulRestarted && restart.ReplacementReady && !restart.FallbackShutdown {
		return &Result{
			Method:          "hot_swap",
			PreviousVersion: c.Version,
			NewVersion:      release.Version,
			Message:         "Binary updated. Daemon handoff completed successfully.",
			Topology:        topologyFromMuxcoreRestart("hot_swap", restart, ""),
		}, nil
	}

	reason := restartFallbackReason(restart)
	if mode == ModeHotSwap {
		return nil, fmt.Errorf("%w: %s", errHotSwapUnsupported, reason)
	}
	return &Result{
		Method:          "deferred",
		PreviousVersion: c.Version,
		NewVersion:      release.Version,
		HandoffError:    reason,
		Message:         "Binary updated. Daemon restarted without live handoff. Hot-swap unavailable: " + reason,
		Topology:        topologyFromMuxcoreRestart("deferred", restart, ""),
	}, nil
}

func (c *Coordinator) applyWithMuxcoreRestart(ctx context.Context, mode Mode, force bool) (*Result, error) {
	release, stagedPath, cleanupStaged, err := c.prepareStagedUpdate(ctx, force)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return &Result{
			Method:          "up_to_date",
			PreviousVersion: c.Version,
			NewVersion:      c.Version,
			Message:         "Already up to date.",
		}, nil
	}
	if cleanupStaged && stagedPath != "" {
		defer func() { _ = os.Remove(stagedPath) }()
	}

	restart, err := c.ApplyUpdateAndRestart(ctx, muxengine.UpdateAndRestartOptions{
		CurrentExe:   c.BinaryPath,
		StagedExe:    stagedPath,
		DrainTimeout: time.Duration(defaultGracefulRestartDrainTimeout) * time.Millisecond,
		CleanStale:   true,
	})
	if err != nil {
		if !updateAndRestartReachedInstall(err) {
			return nil, err
		}
		if mode == ModeHotSwap {
			return nil, err
		}
		fallback := c.afterDeferredInstall(release)
		fallback.HandoffError = err.Error()
		partial := muxengine.UpdateAndRestartResult{}
		var updateErr *muxengine.UpdateAndRestartError
		if errors.As(err, &updateErr) {
			partial = updateErr.Result
		}
		fallback.Topology = topologyFromMuxcoreRestart("deferred", partial, updateAndRestartFailurePhase(err))
		if fallback.Message != "" {
			fallback.Message += " Hot-swap unavailable: " + err.Error()
		}
		return fallback, nil
	}

	if restart.GracefulRestarted && restart.ReplacementReady && !restart.FallbackShutdown {
		return &Result{
			Method:          "hot_swap",
			PreviousVersion: c.Version,
			NewVersion:      release.Version,
			Message:         "Binary updated. Daemon handoff completed successfully.",
			Topology:        topologyFromMuxcoreRestart("hot_swap", restart, ""),
		}, nil
	}

	reason := restartFallbackReason(restart)
	if mode == ModeHotSwap {
		return nil, fmt.Errorf("%w: %s", errHotSwapUnsupported, reason)
	}
	return &Result{
		Method:          "deferred",
		PreviousVersion: c.Version,
		NewVersion:      release.Version,
		HandoffError:    reason,
		Message:         "Binary updated. Daemon restarted without live handoff. Hot-swap unavailable: " + reason,
		Topology:        topologyFromMuxcoreRestart("deferred", restart, ""),
	}, nil
}

func (c *Coordinator) activeEngineFilePath() string {
	if c == nil {
		return ""
	}
	if active := strings.TrimSpace(c.ActiveEngineFile); active != "" {
		return active
	}
	return strings.TrimSpace(os.Getenv(activeEngineFileEnv))
}

func writeActiveEnginePointer(pointerPath, successorPath string) error {
	if strings.TrimSpace(pointerPath) == "" {
		return fmt.Errorf("%s is required for successor restart", activeEngineFileEnv)
	}
	if strings.TrimSpace(successorPath) == "" {
		return fmt.Errorf("successor executable path is required")
	}
	pointerAbs, err := filepath.Abs(pointerPath)
	if err != nil {
		return fmt.Errorf("resolve active engine pointer: %w", err)
	}
	successorAbs, err := filepath.Abs(successorPath)
	if err != nil {
		return fmt.Errorf("resolve successor executable: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(pointerAbs), 0o700); err != nil {
		return fmt.Errorf("prepare active engine pointer directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(pointerAbs), filepath.Base(pointerAbs)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create active engine pointer temp file: %w", err)
	}
	tmpPath := tmp.Name()
	keepTmp := false
	defer func() {
		if !keepTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.WriteString(successorAbs + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write active engine pointer: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close active engine pointer temp file: %w", err)
	}
	renameErr := renameActiveEnginePointer(tmpPath, pointerAbs)
	if renameErr == nil {
		keepTmp = true
		return nil
	}

	// Windows: os.Rename refuses to overwrite an existing destination, so the
	// retry path below removes pointerAbs first. That remove must never leave the
	// live pointer missing — back up the existing content and restore it if the
	// retry rename also fails (e.g. a third process re-locked the path between the
	// remove and the retry). Without this, a double failure permanently destroys
	// the only active-engine pointer and the launcher cannot locate the engine on
	// next boot (PRC 2026-06-23 F1).
	backup, backupErr := readActiveEnginePointer(pointerAbs)
	// Distinguish "pointer absent" (ErrNotExist — nothing to preserve) from
	// "pointer exists but unreadable" (ACL/EACCES/transient I/O). On POSIX,
	// os.Remove below would still succeed on an unreadable-but-present file
	// (Remove needs write on the parent dir, not read on the file), so a
	// non-ErrNotExist read failure must abort BEFORE the remove — otherwise the
	// live pointer is deleted with no backup to restore (adversarial-verify
	// 2026-06-23: backupErr==nil conflated unreadable with absent).
	if backupErr != nil && !errors.Is(backupErr, os.ErrNotExist) {
		return fmt.Errorf("read active engine pointer before replace (rename: %v): %w", renameErr, backupErr)
	}
	haveBackup := backupErr == nil

	if removeErr := os.Remove(pointerAbs); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return fmt.Errorf("replace active engine pointer (rename: %v): %w", renameErr, removeErr)
	}

	if retryErr := renameActiveEnginePointer(tmpPath, pointerAbs); retryErr != nil {
		if haveBackup {
			if restoreErr := os.WriteFile(pointerAbs, backup, 0o600); restoreErr != nil {
				return fmt.Errorf("promote active engine pointer (rename: %v) and restore prior pointer failed: %w", retryErr, restoreErr)
			}
			return fmt.Errorf("promote active engine pointer (prior pointer restored): %w", retryErr)
		}
		return fmt.Errorf("promote active engine pointer: %w", retryErr)
	}
	keepTmp = true
	return nil
}

// renameActiveEnginePointer and readActiveEnginePointer are the os seams for
// writeActiveEnginePointer. Overridable in tests to exercise the
// non-destructive double-failure and unreadable-pointer paths.
//
// writeActiveEnginePointer is only reached through Coordinator.Apply, which
// serializes concurrent invocations via the applyInProgress CAS guard
// (errAlreadyInProgress). The backup-read → remove → retry-rename window below
// therefore runs single-flight per Coordinator; no additional lock is required.
var (
	renameActiveEnginePointer = os.Rename
	readActiveEnginePointer   = os.ReadFile
)

func (c *Coordinator) applyWithPostExitInstall(ctx context.Context, mode Mode, force bool) (*Result, error) {
	if mode == ModeHotSwap {
		return nil, fmt.Errorf("%w: post-exit install requires deferred reconnect", errHotSwapUnsupported)
	}

	release, stagedPath, cleanupStaged, err := c.prepareStagedUpdate(ctx, force)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return &Result{
			Method:          "up_to_date",
			PreviousVersion: c.Version,
			NewVersion:      c.Version,
			Message:         "Already up to date.",
		}, nil
	}

	opts := PostExitInstallOptions{
		CurrentExe:  c.BinaryPath,
		StagedExe:   stagedPath,
		DaemonFlag:  defaultPostExitDaemonFlag,
		WaitTimeout: postExitInstallWatchdogTimeout,
	}
	launchCtx := context.WithoutCancel(ctx)
	var launchOnce sync.Once
	var launchErr error
	launch := func() error {
		launchOnce.Do(func() {
			launchErr = c.PostExitInstall(launchCtx, opts)
		})
		return launchErr
	}
	if launcher, ok := c.SessionHandler.(updatePendingLauncher); ok {
		launcher.SetUpdatePendingLauncher(launch)
	}
	if err := launch(); err != nil {
		if cleanupStaged && stagedPath != "" {
			_ = os.Remove(stagedPath)
		}
		return nil, err
	}

	if c.SessionHandler != nil {
		c.SessionHandler.SetUpdatePending()
	}
	if stopper, ok := c.SessionHandler.(updatePendingStopScheduler); ok {
		stopper.StopForUpdatePendingRestartAfter(postExitStopDelay)
	}
	return &Result{
		Method:          "deferred",
		PreviousVersion: c.Version,
		NewVersion:      release.Version,
		HandoffError:    "post-exit install scheduled",
		Message:         "Binary update scheduled. Post-exit helper will stop and restart the daemon.",
		Topology: UpdateTopology{
			UpdateMethod:       "deferred",
			RestartTopology:    "post_exit",
			ReplacementStarted: true,
		},
	}, nil
}

func (c *Coordinator) prepareStagedUpdate(ctx context.Context, force bool) (*updater.Release, string, bool, error) {
	if c.Source != "" {
		sourcePath, err := c.validateLocalSource(c.Source)
		if err != nil {
			return nil, "", false, err
		}
		stagedPath, err := c.newStagedUpdatePath()
		if err != nil {
			return nil, "", false, err
		}
		if err := copyExecutableDirect(sourcePath, stagedPath); err != nil {
			_ = os.Remove(stagedPath)
			return nil, "", false, fmt.Errorf("stage local source binary: %w", err)
		}
		return &updater.Release{
			Version:      "local-dev",
			AssetName:    filepath.Base(sourcePath),
			ReleaseNotes: fmt.Sprintf("Installed from local source: %s", sourcePath),
		}, stagedPath, true, nil
	}

	effectiveVersion := c.Version
	if force {
		effectiveVersion = "0.0.0"
	}
	stagedPath, err := c.newStagedUpdatePath()
	if err != nil {
		return nil, "", false, err
	}
	release, err := updater.Download(ctx, effectiveVersion, stagedPath)
	if err != nil {
		_ = os.Remove(stagedPath)
		return nil, "", false, err
	}
	if release == nil {
		_ = os.Remove(stagedPath)
		return nil, "", false, nil
	}
	if err := updater.VerifyChecksum(stagedPath, release); err != nil {
		_ = os.Remove(stagedPath)
		return nil, "", false, err
	}
	return release, stagedPath, true, nil
}

func (c *Coordinator) newStagedUpdatePath() (string, error) {
	if strings.TrimSpace(c.BinaryPath) == "" {
		return "", fmt.Errorf("current binary path is required for staged upgrade")
	}
	binaryAbs, err := filepath.Abs(c.BinaryPath)
	if err != nil {
		return "", fmt.Errorf("resolve current binary path: %w", err)
	}
	dir := filepath.Dir(binaryAbs)
	cleanupStaleStagedUpdates(dir)
	ext := stagedExecutableExtension(binaryAbs)
	for attempt := 0; attempt < 100; attempt++ {
		name := fmt.Sprintf("%s%d-%d-%d%s", stagedExecutablePrefix, os.Getpid(), time.Now().UnixNano(), attempt, ext)
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		} else if err != nil {
			return "", fmt.Errorf("check staged update path: %w", err)
		}
	}
	return "", fmt.Errorf("allocate staged update path in %s: exhausted unique candidates", dir)
}

func stagedExecutableExtension(binaryPath string) string {
	if ext := filepath.Ext(binaryPath); ext != "" {
		return ext
	}
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ".bin"
}

func cleanupStaleStagedUpdates(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		candidate := strings.TrimPrefix(name, ".")
		if (strings.HasPrefix(candidate, stagedExecutablePrefix) ||
			strings.HasPrefix(candidate, legacyStagedExecutablePrefix)) &&
			(strings.HasSuffix(candidate, ".bin") ||
				strings.Contains(candidate, ".bin.") ||
				strings.HasSuffix(candidate, ".exe") ||
				strings.Contains(candidate, ".exe.")) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func copyExecutableDirect(sourcePath, stagedPath string) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read local source: %w", err)
	}
	if err := os.WriteFile(stagedPath, data, 0o755); err != nil {
		return fmt.Errorf("write staged source: %w", err)
	}
	return nil
}

func updateAndRestartReachedInstall(err error) bool {
	var updateErr *muxengine.UpdateAndRestartError
	if !errors.As(err, &updateErr) {
		return false
	}
	return updateErr.Phase != muxengine.UpdatePhaseValidate && updateErr.Phase != muxengine.UpdatePhaseSwap
}

func updateAndRestartFailurePhase(err error) string {
	var updateErr *muxengine.UpdateAndRestartError
	if !errors.As(err, &updateErr) {
		return ""
	}
	return string(updateErr.Phase)
}

func topologyFromMuxcoreRestart(method string, result muxengine.UpdateAndRestartResult, failurePhase string) UpdateTopology {
	return UpdateTopology{
		UpdateMethod:       method,
		RestartTopology:    restartTopology(result),
		DaemonWasRunning:   result.DaemonWasRunning,
		LockAcquired:       result.LockAcquired,
		GracefulRestarted:  result.GracefulRestarted,
		FallbackShutdown:   result.FallbackShutdown,
		ReplacementStarted: result.ReplacementStarted,
		ReplacementReady:   result.ReplacementReady,
		CleanedStale:       result.CleanedStale,
		FailurePhase:       failurePhase,
		Warnings:           result.Warnings,
	}
}

func restartTopology(result muxengine.UpdateAndRestartResult) string {
	switch {
	case result.GracefulRestarted && result.ReplacementReady && !result.FallbackShutdown:
		return "graceful_restart"
	case result.FallbackShutdown:
		return "fallback_shutdown"
	case result.ReplacementStarted:
		return "replacement_restart"
	default:
		return "deferred"
	}
}

func restartFallbackReason(result muxengine.UpdateAndRestartResult) string {
	switch {
	case !result.DaemonWasRunning:
		return "daemon was not running after binary swap"
	case result.FallbackShutdown:
		return "daemon graceful restart fell back to shutdown restart"
	case !result.ReplacementReady:
		return "replacement daemon was not ready after restart"
	default:
		return "daemon graceful restart did not complete live handoff"
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
	validatedSource, err := c.validateLocalSource(sourcePath)
	if err != nil {
		return nil, err
	}

	if err := atomicReplaceBinary(c.BinaryPath, validatedSource); err != nil {
		return nil, fmt.Errorf("rename current binary: %w", err)
	}

	return &updater.Release{
		Version:      "local-dev",
		AssetName:    filepath.Base(validatedSource),
		ReleaseNotes: fmt.Sprintf("Installed from local source: %s", validatedSource),
	}, nil
}

func (c *Coordinator) validateLocalSource(sourcePath string) (string, error) {
	if strings.TrimSpace(sourcePath) == "" {
		return "", fmt.Errorf("source binary path is required")
	}
	if strings.TrimSpace(c.BinaryPath) == "" {
		return "", fmt.Errorf("current binary path is required for local-source upgrade")
	}

	sourceAbs, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	binaryAbs, err := filepath.Abs(c.BinaryPath)
	if err != nil {
		return "", fmt.Errorf("resolve current binary path: %w", err)
	}
	sourceResolved, err := filepath.EvalSymlinks(sourceAbs)
	if err != nil {
		return "", fmt.Errorf("source binary not found: %w", err)
	}
	binaryResolved, err := filepath.EvalSymlinks(binaryAbs)
	if err != nil {
		return "", fmt.Errorf("current binary path not found: %w", err)
	}

	info, err := os.Stat(sourceResolved)
	if err != nil {
		return "", fmt.Errorf("source binary not found: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("source path is a directory, not a binary: %s", sourceResolved)
	}

	binaryExt := filepath.Ext(binaryResolved)
	sourceExt := filepath.Ext(sourceResolved)
	if !strings.EqualFold(sourceExt, binaryExt) {
		return "", fmt.Errorf("source binary extension %q does not match current binary extension %q", sourceExt, binaryExt)
	}

	if os.Getenv(allowSourceOutsideBinDirEnv) == "1" {
		return sourceResolved, nil
	}

	allowedDirs := []string{filepath.Dir(binaryResolved)}
	if stagingDir := strings.TrimSpace(os.Getenv(sourceStagingDirEnv)); stagingDir != "" {
		stagingAbs, err := filepath.Abs(stagingDir)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", sourceStagingDirEnv, err)
		}
		stagingResolved, err := filepath.EvalSymlinks(stagingAbs)
		if err != nil {
			return "", fmt.Errorf("resolve %s symlinks: %w", sourceStagingDirEnv, err)
		}
		allowedDirs = append(allowedDirs, stagingResolved)
	}

	for _, dir := range allowedDirs {
		if pathWithinDir(sourceResolved, dir) {
			return sourceResolved, nil
		}
	}
	return "", fmt.Errorf("local source %s is outside trusted upgrade directories; place it beside the running binary, set %s, or explicitly set %s=1 for local development", sourceResolved, sourceStagingDirEnv, allowSourceOutsideBinDirEnv)
}

func pathWithinDir(path, dir string) bool {
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(resolvedDir), filepath.Clean(resolvedPath))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
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
