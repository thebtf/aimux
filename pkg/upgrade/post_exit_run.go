package upgrade

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	opts.WaitTimeout = timeout
	if daemonFlag != opts.DaemonFlag {
		opts.DaemonFlag = daemonFlag
	}

	if runningFromStagedPayload(opts.StagedExe) {
		return relaunchPostExitHelperCopy(opts, timeout)
	}

	deadline := time.Now().Add(timeout)
	for {
		err := moveStagedBinaryIntoPlace(opts.CurrentExe, opts.StagedExe)
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

func runningFromStagedPayload(stagedExe string) bool {
	runningExe, err := os.Executable()
	if err != nil {
		return false
	}
	return sameExecutablePath(runningExe, stagedExe)
}

func sameExecutablePath(a, b string) bool {
	aAbs, aErr := filepath.Abs(a)
	bAbs, bErr := filepath.Abs(b)
	if aErr != nil || bErr != nil {
		return false
	}
	aClean := filepath.Clean(aAbs)
	bClean := filepath.Clean(bAbs)
	if strings.EqualFold(aClean, bClean) {
		return true
	}
	aEval, aEvalErr := filepath.EvalSymlinks(aClean)
	bEval, bEvalErr := filepath.EvalSymlinks(bClean)
	return aEvalErr == nil && bEvalErr == nil && strings.EqualFold(filepath.Clean(aEval), filepath.Clean(bEval))
}

func relaunchPostExitHelperCopy(opts PostExitInstallOptions, timeout time.Duration) error {
	helperExe, err := createPostExitHelperCopy(opts.StagedExe)
	if err != nil {
		return fmt.Errorf("prepare post-exit helper copy: %w", err)
	}

	timeoutMs := int(timeout.Milliseconds())
	if timeoutMs <= 0 {
		timeoutMs = int(defaultControlRequestTimeout.Milliseconds())
	}
	cmd := exec.Command(helperExe, postExitInstallArgs(opts, timeoutMs)...)
	configurePostExitCommand(cmd)
	if err := cmd.Start(); err != nil {
		_ = os.Remove(helperExe)
		return fmt.Errorf("start post-exit helper copy: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release post-exit helper copy: %w", err)
	}
	return nil
}
