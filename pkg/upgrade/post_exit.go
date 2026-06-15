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
	postExitHelperPrefix      = "aimux-postexit-helper-"
	postExitGenerationSuffix  = ".post-exit-active"
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
		if err := writePostExitGeneration(opts); err != nil {
			_ = os.Remove(helperExe)
			return fmt.Errorf("write post-exit generation marker: %w", err)
		}

		cmd := exec.Command(helperExe, postExitInstallArgs(opts, timeoutMs)...)
		configurePostExitCommand(cmd)
		if err := cmd.Start(); err != nil {
			clearPostExitGenerationIfCurrent(opts)
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
		if err := copyExecutable(stagedExe, helperExe); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		return helperExe, nil
	}
	return "", fmt.Errorf("write helper copy: %s", strings.Join(failures, "; "))
}

func copyExecutable(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open executable source: %w", err)
	}
	defer srcFile.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create executable temp file: %w", err)
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
		return fmt.Errorf("copy executable payload: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close executable temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("chmod executable temp file: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		if directErr := copyExecutableDirect(src, dst); directErr == nil {
			return nil
		} else {
			return fmt.Errorf("promote executable: %w; direct copy fallback: %v", err, directErr)
		}
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
	add(filepath.Dir(stagedExe))
	return dirs
}

func postExitHelperPath(dir, stagedExe string) string {
	name := fmt.Sprintf("%s%d-%d.exe", postExitHelperPrefix, os.Getpid(), time.Now().UnixNano())
	return filepath.Join(dir, name)
}

func cleanupStalePostExitHelpers(dir, stagedExe string) {
	base := filepath.Base(stagedExe)
	legacyPrefix := base + ".post-exit-helper."
	const suffix = ".exe"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, suffix) &&
			(strings.HasPrefix(name, postExitHelperPrefix) || strings.HasPrefix(name, legacyPrefix)) {
			_ = os.Remove(filepath.Join(dir, name))
		}
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

func postExitGenerationPath(currentExe string) string {
	return currentExe + postExitGenerationSuffix
}

func writePostExitGeneration(opts PostExitInstallOptions) error {
	if opts.CurrentExe == "" || opts.StagedExe == "" {
		return fmt.Errorf("current and staged executable paths are required")
	}
	currentAbs, err := filepath.Abs(opts.CurrentExe)
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	stagedAbs, err := filepath.Abs(opts.StagedExe)
	if err != nil {
		return fmt.Errorf("resolve staged executable: %w", err)
	}
	payload := stagedAbs + "\n"
	return os.WriteFile(postExitGenerationPath(currentAbs), []byte(payload), 0o600)
}

func ensurePostExitGenerationCurrent(opts PostExitInstallOptions) error {
	currentAbs, err := filepath.Abs(opts.CurrentExe)
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	markerPath := postExitGenerationPath(currentAbs)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return fmt.Errorf("read post-exit generation marker: %w", err)
	}
	activeStaged := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	if activeStaged == "" {
		return fmt.Errorf("post-exit generation marker is empty")
	}
	if !sameExecutablePath(activeStaged, opts.StagedExe) {
		_ = os.Remove(opts.StagedExe)
		return fmt.Errorf("post-exit install superseded: active staged %q differs from helper staged %q", activeStaged, opts.StagedExe)
	}
	return nil
}

func clearPostExitGenerationIfCurrent(opts PostExitInstallOptions) {
	if err := ensurePostExitGenerationCurrent(opts); err != nil {
		return
	}
	currentAbs, err := filepath.Abs(opts.CurrentExe)
	if err != nil {
		return
	}
	_ = os.Remove(postExitGenerationPath(currentAbs))
}
