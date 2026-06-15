package upgrade

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	muxengine "github.com/thebtf/mcp-mux/muxcore/engine"
)

func TestCoordinatorApply_AutoUsesPostExitInstallWhenRequired(t *testing.T) {
	restore := setPostExitInstallRequiredForTest(func() bool { return true })
	defer restore()

	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-next.exe")
	writeTestFile(t, binaryPath, "v1")
	writeTestFile(t, sourcePath, "v2")

	mock := &testSessionHandler{}
	var postExitCalled bool
	var got PostExitInstallOptions
	var muxcoreCalled bool
	coord := &Coordinator{
		Version:        "5.14.2",
		BinaryPath:     binaryPath,
		Source:         sourcePath,
		EngineMode:     true,
		SessionHandler: mock,
		PostExitInstall: func(ctx context.Context, opts PostExitInstallOptions) error {
			postExitCalled = true
			got = opts
			return nil
		},
		ApplyUpdateAndRestart: func(ctx context.Context, opts muxengine.UpdateAndRestartOptions) (muxengine.UpdateAndRestartResult, error) {
			muxcoreCalled = true
			return muxengine.UpdateAndRestartResult{}, nil
		},
	}

	result, err := coord.Apply(context.Background(), ModeAuto, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !postExitCalled {
		t.Fatal("expected post-exit installer to be called")
	}
	if muxcoreCalled {
		t.Fatal("did not expect muxcore swap-first helper on post-exit platform")
	}
	if got.CurrentExe != binaryPath {
		t.Fatalf("CurrentExe = %q, want %q", got.CurrentExe, binaryPath)
	}
	resolvedSource, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		t.Fatalf("EvalSymlinks source: %v", err)
	}
	if got.StagedExe != resolvedSource {
		t.Fatalf("StagedExe = %q, want %q", got.StagedExe, resolvedSource)
	}
	if got.DaemonFlag == "" {
		t.Fatal("expected daemon flag for replacement start")
	}
	if !mock.pendingCalled {
		t.Fatal("expected SetUpdatePending after scheduling post-exit install")
	}
	if result.Method != "deferred" {
		t.Fatalf("Method = %q, want deferred", result.Method)
	}
	if result.HandoffError == "" {
		t.Fatal("expected handoff error explaining post-exit reconnect")
	}
}

func TestCoordinatorApply_HotSwapRejectsPostExitInstall(t *testing.T) {
	restore := setPostExitInstallRequiredForTest(func() bool { return true })
	defer restore()

	coord := &Coordinator{
		Version:         "5.14.2",
		BinaryPath:      filepath.Join(t.TempDir(), "aimux.exe"),
		EngineMode:      true,
		PostExitInstall: func(context.Context, PostExitInstallOptions) error { return nil },
	}

	_, err := coord.Apply(context.Background(), ModeHotSwap, true)
	if err == nil {
		t.Fatal("expected hot_swap to reject post-exit install")
	}
	if got := err.Error(); got == "" || !containsAll(got, "hot-swap", "post-exit") {
		t.Fatalf("error = %q, want hot-swap/post-exit detail", got)
	}
}

func TestCreatePostExitHelperCopy_UsesDistinctExecutable(t *testing.T) {
	dir := t.TempDir()
	stagedPath := filepath.Join(dir, "aimux-next.exe")
	stagedContent := "replacement-payload"
	writeTestFile(t, stagedPath, stagedContent)

	helperDir := filepath.Join(dir, "helpers")
	t.Setenv(postExitHelperDirEnv, helperDir)

	staleHelper := filepath.Join(helperDir, filepath.Base(stagedPath)+".post-exit-helper.1.2.exe")
	if err := os.MkdirAll(helperDir, 0o700); err != nil {
		t.Fatalf("MkdirAll helperDir: %v", err)
	}
	writeTestFile(t, staleHelper, "stale-helper")

	helperPath, err := createPostExitHelperCopy(stagedPath)
	if err != nil {
		t.Fatalf("createPostExitHelperCopy: %v", err)
	}
	defer func() { _ = os.Remove(helperPath) }()

	if helperPath == stagedPath {
		t.Fatal("helper copy must not be the staged payload itself")
	}
	if filepath.Dir(helperPath) != helperDir {
		t.Fatalf("helper dir = %q, want %q", filepath.Dir(helperPath), helperDir)
	}
	if filepath.Ext(helperPath) != ".exe" {
		t.Fatalf("helper extension = %q, want .exe", filepath.Ext(helperPath))
	}
	if !strings.Contains(filepath.Base(helperPath), filepath.Base(stagedPath)+".post-exit-helper.") {
		t.Fatalf("helper path %q does not include post-exit-helper marker", helperPath)
	}

	got, err := os.ReadFile(helperPath)
	if err != nil {
		t.Fatalf("ReadFile helper: %v", err)
	}
	if string(got) != stagedContent {
		t.Fatalf("helper content = %q, want staged payload", got)
	}
	if _, err := os.Stat(staleHelper); !os.IsNotExist(err) {
		t.Fatalf("stale helper should be removed, stat err=%v", err)
	}
}

func TestSameExecutablePath_NormalizesEquivalentPaths(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aimux-next.exe")
	writeTestFile(t, exePath, "payload")

	equivalent := filepath.Join(dir, ".", "aimux-next.exe")
	if !sameExecutablePath(exePath, equivalent) {
		t.Fatalf("sameExecutablePath(%q, %q) = false, want true", exePath, equivalent)
	}
	if sameExecutablePath(exePath, filepath.Join(dir, "other.exe")) {
		t.Fatal("sameExecutablePath returned true for different paths")
	}
}

func TestMoveStagedBinaryIntoPlace_ConsumesStagedPayload(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "aimux.exe")
	stagedPath := filepath.Join(dir, "aimux-next.exe")

	writeTestFile(t, currentPath, "current-v1")
	writeTestFile(t, stagedPath, "staged-v2")

	if err := moveStagedBinaryIntoPlace(currentPath, stagedPath); err != nil {
		t.Fatalf("moveStagedBinaryIntoPlace: %v", err)
	}

	got, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("ReadFile currentPath: %v", err)
	}
	if string(got) != "staged-v2" {
		t.Fatalf("currentPath = %q, want staged-v2", got)
	}

	if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
		t.Fatalf("staged payload should be consumed by move, stat err=%v", err)
	}

	old, err := os.ReadFile(currentPath + ".old")
	if err != nil {
		t.Fatalf("ReadFile old rollback slot: %v", err)
	}
	if string(old) != "current-v1" {
		t.Fatalf("old rollback slot = %q, want current-v1", old)
	}
}

type testSessionHandler struct {
	pendingCalled bool
}

func (h *testSessionHandler) SetUpdatePending() {
	h.pendingCalled = true
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
