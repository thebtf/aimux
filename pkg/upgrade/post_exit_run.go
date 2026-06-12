package upgrade

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

// RunPostExitInstall is executed by the staged binary after the old daemon has
// been asked to exit. It waits for the stable binary path to become replaceable,
// installs the staged binary, then starts the stable path in daemon mode.
func RunPostExitInstall(opts PostExitInstallOptions) error {
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
	timeout := opts.WaitTimeout
	if timeout <= 0 {
		timeout = defaultControlRequestTimeout
	}

	deadline := time.Now().Add(timeout)
	for {
		err := atomicReplaceBinary(opts.CurrentExe, opts.StagedExe)
		if err == nil {
			break
		}
		if !IsCurrentBinaryLocked(err) && !IsOldSlotLocked(err) {
			return fmt.Errorf("post-exit install: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("post-exit install timed out after %s: %w", timeout, err)
		}
		time.Sleep(250 * time.Millisecond)
	}

	_ = os.Remove(opts.StagedExe)

	cmd := exec.Command(opts.CurrentExe, daemonFlag)
	configurePostExitCommand(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start replacement daemon: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release replacement daemon: %w", err)
	}
	return nil
}
