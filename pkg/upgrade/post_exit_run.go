package upgrade

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	tracePostExit("start current=%q staged=%q daemon_flag=%q timeout=%s", opts.CurrentExe, opts.StagedExe, opts.DaemonFlag, opts.WaitTimeout)

	if runningFromStagedPayload(opts.StagedExe) {
		tracePostExit("running from staged payload; preparing helper relaunch")
		if err := prepareGenerationForPostExitHelperRelaunch(opts); err != nil {
			tracePostExit("prepare helper relaunch failed: %v", err)
			return err
		}
		err := relaunchPostExitHelperCopy(opts, timeout)
		if err != nil {
			tracePostExit("helper relaunch failed: %v", err)
		} else {
			tracePostExit("helper relaunch started")
		}
		return err
	}
	if err := ensureOrBootstrapPostExitGeneration(opts); err != nil {
		tracePostExit("generation check failed before move loop: %v", err)
		return err
	}
	installed := false
	defer func() {
		if installed {
			clearPostExitGenerationIfCurrent(opts)
			return
		}
		if err := ensurePostExitGenerationCurrent(opts); err == nil {
			clearPostExitGenerationIfCurrent(opts)
			_ = os.Remove(opts.StagedExe)
		}
	}()

	deadline := time.Now().Add(timeout)
	for {
		if err := ensurePostExitGenerationCurrent(opts); err != nil {
			tracePostExit("generation check failed during move loop: %v", err)
			return err
		}
		err := moveStagedBinary(opts.CurrentExe, opts.StagedExe)
		if err == nil {
			tracePostExit("move staged binary succeeded")
			installed = true
			break
		}
		if !IsCurrentBinaryLocked(err) && !IsOldSlotLocked(err) && !IsStagedBinaryLocked(err) {
			tracePostExit("move staged binary failed non-retriably: %v", err)
			return fmt.Errorf("post-exit install: %w", err)
		}
		if time.Now().After(deadline) {
			tracePostExit("move staged binary timed out after %s: %v", timeout, err)
			return fmt.Errorf("post-exit install timed out after %s: %w", timeout, err)
		}
		tracePostExit("move staged binary locked; retrying: %v", err)
		time.Sleep(250 * time.Millisecond)
	}

	cmd := exec.Command(opts.CurrentExe, daemonFlag)
	configurePostExitCommand(cmd)
	if err := cmd.Start(); err != nil {
		tracePostExit("start replacement daemon failed: %v", err)
		return fmt.Errorf("start replacement daemon: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		tracePostExit("release replacement daemon failed: %v", err)
		return fmt.Errorf("release replacement daemon: %w", err)
	}
	tracePostExit("replacement daemon started")
	return nil
}

var moveStagedBinary = moveStagedBinaryIntoPlace
var executablePath = os.Executable

func prepareGenerationForPostExitHelperRelaunch(opts PostExitInstallOptions) error {
	if err := ensurePostExitGenerationCurrent(opts); err == nil {
		return nil
	} else if errors.Is(err, os.ErrNotExist) {
		return writePostExitGeneration(opts)
	} else {
		return err
	}
}

func ensureOrBootstrapPostExitGeneration(opts PostExitInstallOptions) error {
	err := ensurePostExitGenerationCurrent(opts)
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) && runningFromLegacyPostExitHelperCopy(opts.StagedExe) {
		tracePostExit("legacy helper copy missing generation marker; bootstrapping")
		return writePostExitGeneration(opts)
	}
	return err
}

func runningFromStagedPayload(stagedExe string) bool {
	runningExe, err := executablePath()
	if err != nil {
		return false
	}
	return sameExecutablePath(runningExe, stagedExe)
}

func runningFromLegacyPostExitHelperCopy(stagedExe string) bool {
	runningExe, err := executablePath()
	if err != nil {
		return false
	}
	return isLegacyPostExitHelperCopyPath(runningExe, stagedExe)
}

func isLegacyPostExitHelperCopyPath(runningExe, stagedExe string) bool {
	runningName := filepath.Base(runningExe)
	stagedName := filepath.Base(stagedExe)
	if stagedName == "." || stagedName == string(filepath.Separator) || stagedName == "" {
		return false
	}
	return strings.HasPrefix(runningName, stagedName+".post-exit-helper.") && strings.HasSuffix(runningName, ".exe")
}

func sameExecutablePath(a, b string) bool {
	aAbs, aErr := filepath.Abs(a)
	bAbs, bErr := filepath.Abs(b)
	if aErr != nil || bErr != nil {
		return false
	}
	aClean := filepath.Clean(aAbs)
	bClean := filepath.Clean(bAbs)
	if executablePathEqual(aClean, bClean) {
		return true
	}
	aEval, aEvalErr := filepath.EvalSymlinks(aClean)
	bEval, bEvalErr := filepath.EvalSymlinks(bClean)
	return aEvalErr == nil && bEvalErr == nil && executablePathEqual(filepath.Clean(aEval), filepath.Clean(bEval))
}

func executablePathEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func relaunchPostExitHelperCopy(opts PostExitInstallOptions, timeout time.Duration) error {
	helperExe, err := createPostExitHelperCopy(opts.StagedExe)
	if err != nil {
		return fmt.Errorf("prepare post-exit helper copy: %w", err)
	}
	tracePostExit("created helper copy %q", helperExe)

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
	tracePostExit("released helper copy pid=%d", cmd.Process.Pid)
	return nil
}

func tracePostExit(format string, args ...any) {
	path := strings.TrimSpace(os.Getenv("AIMUX_POST_EXIT_TRACE"))
	if path == "" {
		return
	}
	exe, _ := os.Executable()
	line := fmt.Sprintf("%s pid=%d exe=%q %s\n",
		time.Now().Format(time.RFC3339Nano),
		os.Getpid(),
		exe,
		fmt.Sprintf(format, args...),
	)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line)
}
