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
	if got.StagedExe != sourcePath {
		t.Fatalf("StagedExe = %q, want %q", got.StagedExe, sourcePath)
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
