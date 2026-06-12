package upgrade

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
)

const (
	PostExitUpgradeFlag       = "--aimux-post-exit-upgrade"
	defaultPostExitDaemonFlag = "--muxcore-daemon"
)

var postExitInstallRequired = func() bool {
	return runtime.GOOS == "windows"
}

func setPostExitInstallRequiredForTest(fn func() bool) func() {
	prev := postExitInstallRequired
	postExitInstallRequired = fn
	return func() { postExitInstallRequired = prev }
}

// NewPostExitInstallFunc returns the production post-exit installer launcher.
func NewPostExitInstallFunc() PostExitInstallFunc {
	return func(ctx context.Context, opts PostExitInstallOptions) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if opts.CurrentExe == "" {
			return fmt.Errorf("current executable path is required")
		}
		if opts.StagedExe == "" {
			return fmt.Errorf("staged executable path is required")
		}
		daemonFlag := opts.DaemonFlag
		if daemonFlag == "" {
			daemonFlag = defaultPostExitDaemonFlag
		}
		timeoutMs := int(opts.WaitTimeout.Milliseconds())
		if timeoutMs <= 0 {
			timeoutMs = int(defaultControlRequestTimeout.Milliseconds())
		}

		cmd := exec.Command(opts.StagedExe,
			PostExitUpgradeFlag,
			"--current-exe", opts.CurrentExe,
			"--staged-exe", opts.StagedExe,
			"--daemon-flag", daemonFlag,
			"--timeout-ms", strconv.Itoa(timeoutMs),
		)
		configurePostExitCommand(cmd)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start post-exit installer: %w", err)
		}
		if err := cmd.Process.Release(); err != nil {
			return fmt.Errorf("release post-exit installer: %w", err)
		}
		return nil
	}
}
