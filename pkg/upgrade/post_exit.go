package upgrade

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	PostExitUpgradeFlag       = "--aimux-post-exit-upgrade"
	defaultPostExitDaemonFlag = "--muxcore-daemon"
	postExitHelperDirEnv      = "AIMUX_POST_EXIT_HELPER_DIR"
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
		opts.DaemonFlag = daemonFlag
		timeoutMs := int(opts.WaitTimeout.Milliseconds())
		if timeoutMs <= 0 {
			timeoutMs = int(defaultControlRequestTimeout.Milliseconds())
		}

		helperExe, err := createPostExitHelperCopy(opts.StagedExe)
		if err != nil {
			return fmt.Errorf("prepare post-exit helper: %w", err)
		}

		cmd := exec.Command(helperExe, postExitInstallArgs(opts, timeoutMs)...)
		configurePostExitCommand(cmd)
		if err := cmd.Start(); err != nil {
			_ = os.Remove(helperExe)
			return fmt.Errorf("start post-exit installer: %w", err)
		}
		if err := cmd.Process.Release(); err != nil {
			return fmt.Errorf("release post-exit installer: %w", err)
		}
		return nil
	}
}

func createPostExitHelperCopy(stagedExe string) (string, error) {
	var failures []string
	for _, dir := range postExitHelperCandidateDirs(stagedExe) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			failures = append(failures, fmt.Sprintf("%s: mkdir: %v", dir, err))
			continue
		}
		cleanupStalePostExitHelpers(dir, stagedExe)

		helperExe := postExitHelperPath(dir, stagedExe)
		if err := copyPostExitHelperExecutable(stagedExe, helperExe); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		return helperExe, nil
	}
	return "", fmt.Errorf("write helper copy: %s", strings.Join(failures, "; "))
}

func copyPostExitHelperExecutable(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open staged helper source: %w", err)
	}
	defer srcFile.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create helper temp file: %w", err)
	}
	tmpPath := tmp.Name()
	keepTmp := false
	defer func() {
		if !keepTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, srcFile); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("copy helper payload: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close helper temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("chmod helper temp file: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("promote helper executable: %w", err)
	}
	keepTmp = true
	return nil
}

func postExitHelperCandidateDirs(stagedExe string) []string {
	var dirs []string
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		for _, existing := range dirs {
			if strings.EqualFold(existing, clean) {
				return
			}
		}
		dirs = append(dirs, clean)
	}

	add(os.Getenv(postExitHelperDirEnv))
	if cacheDir, err := os.UserCacheDir(); err == nil {
		add(filepath.Join(cacheDir, "aimux", "post-exit"))
	}
	if tempDir := os.TempDir(); tempDir != "" {
		add(filepath.Join(tempDir, "aimux-post-exit"))
	}
	if wd, err := os.Getwd(); err == nil {
		add(filepath.Join(wd, ".aimux-post-exit"))
	}
	add(filepath.Dir(stagedExe))
	return dirs
}

func postExitHelperPath(dir, stagedExe string) string {
	base := filepath.Base(stagedExe)
	name := fmt.Sprintf("%s.post-exit-helper.%d.%d.exe", base, os.Getpid(), time.Now().UnixNano())
	return filepath.Join(dir, name)
}

func cleanupStalePostExitHelpers(dir, stagedExe string) {
	base := filepath.Base(stagedExe)
	matches, err := filepath.Glob(filepath.Join(dir, base+".post-exit-helper.*.exe"))
	if err != nil {
		return
	}
	for _, match := range matches {
		_ = os.Remove(match)
	}
}

func postExitInstallArgs(opts PostExitInstallOptions, timeoutMs int) []string {
	return []string{
		PostExitUpgradeFlag,
		"--current-exe", opts.CurrentExe,
		"--staged-exe", opts.StagedExe,
		"--daemon-flag", opts.DaemonFlag,
		"--timeout-ms", strconv.Itoa(timeoutMs),
	}
}
