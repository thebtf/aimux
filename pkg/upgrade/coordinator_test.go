package upgrade_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/updater"
	"github.com/thebtf/aimux/pkg/upgrade"
	"github.com/thebtf/mcp-mux/muxcore/control"
	muxengine "github.com/thebtf/mcp-mux/muxcore/engine"
)

type mockSessionHandler struct {
	pendingCalled bool
}

func (m *mockSessionHandler) SetUpdatePending() {
	m.pendingCalled = true
}

func TestCoordinator_Compile(t *testing.T) {
	mock := &mockSessionHandler{}
	coord := &upgrade.Coordinator{
		Version:        "4.3.0",
		BinaryPath:     "/usr/local/bin/aimux",
		SessionHandler: mock,
		EngineMode:     false,
		Logger:         nil,
	}
	if coord.Version != "4.3.0" {
		t.Fatalf("Version field: got %q, want %q", coord.Version, "4.3.0")
	}
	if coord.BinaryPath != "/usr/local/bin/aimux" {
		t.Fatalf("BinaryPath field: got %q, want %q", coord.BinaryPath, "/usr/local/bin/aimux")
	}
}

func TestMode_Values(t *testing.T) {
	tests := []struct {
		mode upgrade.Mode
		want string
	}{
		{upgrade.ModeAuto, "auto"},
		{upgrade.ModeHotSwap, "hot_swap"},
		{upgrade.ModeDeferred, "deferred"},
	}
	for _, tc := range tests {
		if string(tc.mode) != tc.want {
			t.Errorf("Mode %v: got %q, want %q", tc.mode, string(tc.mode), tc.want)
		}
	}
}

func TestResult_Fields(t *testing.T) {
	r := &upgrade.Result{}
	if r.Method != "" {
		t.Error("Method should default to empty string")
	}
	if r.HandoffTransferred != nil {
		t.Error("HandoffTransferred should default to nil")
	}
	if r.HandoffDurationMs != 0 {
		t.Error("HandoffDurationMs should default to 0")
	}
}

func TestCoordinatorApply_DeferredSignalsSessionHandler(t *testing.T) {
	mock := &mockSessionHandler{}
	coord := &upgrade.Coordinator{
		Version:        "4.3.0",
		SessionHandler: mock,
		EngineMode:     true,
		ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
			return &updater.Release{Version: "4.4.0"}, nil
		},
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeDeferred, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !mock.pendingCalled {
		t.Fatal("expected SetUpdatePending to be called for deferred mode")
	}
	if result.Method != "deferred" {
		t.Fatalf("Method = %q, want deferred", result.Method)
	}
}

func TestCoordinatorApply_AutoUsesGracefulRestartInEngineMode(t *testing.T) {
	var called bool
	var gotDrain int
	coord := &upgrade.Coordinator{
		Version:    "4.3.0",
		EngineMode: true,
		ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
			return &updater.Release{Version: "4.4.0"}, nil
		},
		GracefulRestart: func(ctx context.Context, drainTimeoutMs int) error {
			called = true
			gotDrain = drainTimeoutMs
			return nil
		},
		HandoffStatus: func(ctx context.Context) (upgrade.HandoffStatus, error) {
			return upgrade.HandoffStatus{Fallback: 7}, nil
		},
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeAuto, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !called {
		t.Fatal("expected graceful restart seam to be invoked")
	}
	if gotDrain != 10000 {
		t.Fatalf("drain timeout = %d, want 10000", gotDrain)
	}
	if result.Method != "hot_swap" {
		t.Fatalf("Method = %q, want hot_swap", result.Method)
	}
	if result.Message == "" {
		t.Fatal("expected non-empty message")
	}
}

func TestCoordinatorApply_LocalSourceUsesMuxcoreApplyUpdateAndRestart(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-next.exe")
	if err := os.WriteFile(binaryPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("v2"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	var called bool
	var got muxengine.UpdateAndRestartOptions
	coord := &upgrade.Coordinator{
		Version:    "5.14.2",
		BinaryPath: binaryPath,
		Source:     sourcePath,
		EngineMode: true,
		ApplyUpdateAndRestart: func(ctx context.Context, opts muxengine.UpdateAndRestartOptions) (muxengine.UpdateAndRestartResult, error) {
			called = true
			got = opts
			stagedPayload, err := os.ReadFile(opts.StagedExe)
			if err != nil {
				t.Fatalf("ReadFile StagedExe: %v", err)
			}
			if string(stagedPayload) != "v2" {
				t.Fatalf("StagedExe payload = %q, want v2", stagedPayload)
			}
			return muxengine.UpdateAndRestartResult{
				DaemonWasRunning:   true,
				GracefulRestarted:  true,
				ReplacementStarted: true,
				ReplacementReady:   true,
			}, nil
		},
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeAuto, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !called {
		t.Fatal("expected muxcore ApplyUpdateAndRestart helper to be called")
	}
	if got.CurrentExe != binaryPath {
		t.Fatalf("CurrentExe = %q, want %q", got.CurrentExe, binaryPath)
	}
	resolvedSource, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		t.Fatalf("EvalSymlinks source: %v", err)
	}
	if got.StagedExe == resolvedSource {
		t.Fatalf("StagedExe = %q, want copied staging path distinct from source", got.StagedExe)
	}
	if filepath.Dir(got.StagedExe) != filepath.Dir(binaryPath) {
		t.Fatalf("StagedExe dir = %q, want binary dir %q", filepath.Dir(got.StagedExe), filepath.Dir(binaryPath))
	}
	if _, err := os.Stat(resolvedSource); err != nil {
		t.Fatalf("source path should remain available after staging: %v", err)
	}
	if got.DrainTimeout != 10*time.Second {
		t.Fatalf("DrainTimeout = %s, want 10s", got.DrainTimeout)
	}
	if !got.CleanStale {
		t.Fatal("expected CleanStale=true")
	}
	if result.Method != "hot_swap" {
		t.Fatalf("Method = %q, want hot_swap", result.Method)
	}
	if result.NewVersion != "local-dev" {
		t.Fatalf("NewVersion = %q, want local-dev", result.NewVersion)
	}
}

func TestCoordinatorApply_RemoteDownloadUsesMuxcoreStagedPath(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux.exe")
	if err := os.WriteFile(binaryPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}

	var gotCurrentVersion string
	updater.SetTestHooks(nil, func(ctx context.Context, currentVersion string, targetPath string) (*updater.Release, error) {
		gotCurrentVersion = currentVersion
		if filepath.Dir(targetPath) != dir {
			t.Fatalf("download target dir = %q, want %q", filepath.Dir(targetPath), dir)
		}
		if err := os.WriteFile(targetPath, []byte("downloaded-v2"), 0o755); err != nil {
			t.Fatalf("WriteFile staged target: %v", err)
		}
		return &updater.Release{Version: "5.14.3", AssetName: filepath.Base(targetPath)}, nil
	}, nil, nil)
	t.Cleanup(func() { updater.SetTestHooks(nil, nil, nil, nil) })

	var got muxengine.UpdateAndRestartOptions
	coord := &upgrade.Coordinator{
		Version:    "5.14.2",
		BinaryPath: binaryPath,
		EngineMode: true,
		ApplyUpdateAndRestart: func(ctx context.Context, opts muxengine.UpdateAndRestartOptions) (muxengine.UpdateAndRestartResult, error) {
			got = opts
			if _, err := os.Stat(opts.StagedExe); err != nil {
				t.Fatalf("staged exe should exist while helper runs: %v", err)
			}
			return muxengine.UpdateAndRestartResult{
				DaemonWasRunning:   true,
				GracefulRestarted:  true,
				ReplacementStarted: true,
				ReplacementReady:   true,
			}, nil
		},
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeAuto, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if gotCurrentVersion != "0.0.0" {
		t.Fatalf("download currentVersion = %q, want force version 0.0.0", gotCurrentVersion)
	}
	if got.CurrentExe != binaryPath {
		t.Fatalf("CurrentExe = %q, want %q", got.CurrentExe, binaryPath)
	}
	if got.StagedExe == "" {
		t.Fatal("expected non-empty staged exe")
	}
	if _, err := os.Stat(got.StagedExe); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged exe cleanup = %v, want os.ErrNotExist", err)
	}
	if result.Method != "hot_swap" {
		t.Fatalf("Method = %q, want hot_swap", result.Method)
	}
	if result.NewVersion != "5.14.3" {
		t.Fatalf("NewVersion = %q, want 5.14.3", result.NewVersion)
	}
}

func TestCoordinatorApply_MuxcoreFallbackShutdownReturnsUpdatedDeferred(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-next.exe")
	if err := os.WriteFile(binaryPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("v2"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	mock := &mockSessionHandler{}
	coord := &upgrade.Coordinator{
		Version:        "5.14.2",
		BinaryPath:     binaryPath,
		Source:         sourcePath,
		EngineMode:     true,
		SessionHandler: mock,
		ApplyUpdateAndRestart: func(ctx context.Context, opts muxengine.UpdateAndRestartOptions) (muxengine.UpdateAndRestartResult, error) {
			return muxengine.UpdateAndRestartResult{
				DaemonWasRunning:   true,
				FallbackShutdown:   true,
				ReplacementStarted: true,
				ReplacementReady:   true,
			}, nil
		},
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeAuto, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Method != "deferred" {
		t.Fatalf("Method = %q, want deferred", result.Method)
	}
	if !strings.Contains(result.HandoffError, "shutdown restart") {
		t.Fatalf("HandoffError = %q, want shutdown restart detail", result.HandoffError)
	}
	if mock.pendingCalled {
		t.Fatal("fallback shutdown already restarted the daemon; SetUpdatePending should not be called")
	}
	if result.Topology.UpdateMethod != "deferred" {
		t.Fatalf("Topology.UpdateMethod = %q, want deferred", result.Topology.UpdateMethod)
	}
	if result.Topology.RestartTopology != "fallback_shutdown" {
		t.Fatalf("Topology.RestartTopology = %q, want fallback_shutdown", result.Topology.RestartTopology)
	}
	if !result.Topology.FallbackShutdown || !result.Topology.ReplacementStarted || !result.Topology.ReplacementReady {
		t.Fatalf("Topology missing muxcore restart flags: %+v", result.Topology)
	}
}

func TestCoordinatorApply_HotSwapFailsOnMuxcoreFallbackShutdown(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-next.exe")
	if err := os.WriteFile(binaryPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("v2"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	coord := &upgrade.Coordinator{
		Version:    "5.14.2",
		BinaryPath: binaryPath,
		Source:     sourcePath,
		EngineMode: true,
		ApplyUpdateAndRestart: func(ctx context.Context, opts muxengine.UpdateAndRestartOptions) (muxengine.UpdateAndRestartResult, error) {
			return muxengine.UpdateAndRestartResult{
				DaemonWasRunning:   true,
				FallbackShutdown:   true,
				ReplacementStarted: true,
				ReplacementReady:   true,
			}, nil
		},
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeHotSwap, true)
	if err == nil {
		t.Fatal("expected hot_swap mode to fail on fallback shutdown")
	}
	if !strings.Contains(err.Error(), "shutdown restart") {
		t.Fatalf("error = %q, want shutdown restart detail", err)
	}
}

func TestCoordinatorApply_AutoFallsBackWhenMuxcoreRestartFailsAfterSwap(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-next.exe")
	if err := os.WriteFile(binaryPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("v2"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	mock := &mockSessionHandler{}
	coord := &upgrade.Coordinator{
		Version:        "5.14.2",
		BinaryPath:     binaryPath,
		Source:         sourcePath,
		EngineMode:     true,
		SessionHandler: mock,
		ApplyUpdateAndRestart: func(ctx context.Context, opts muxengine.UpdateAndRestartOptions) (muxengine.UpdateAndRestartResult, error) {
			return muxengine.UpdateAndRestartResult{}, &muxengine.UpdateAndRestartError{
				Phase: muxengine.UpdatePhaseRestart,
				Err:   errors.New("control socket failed"),
			}
		},
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeAuto, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Method != "deferred" {
		t.Fatalf("Method = %q, want deferred", result.Method)
	}
	if !strings.Contains(result.HandoffError, "control socket failed") {
		t.Fatalf("HandoffError = %q, want muxcore restart error", result.HandoffError)
	}
	if !mock.pendingCalled {
		t.Fatal("expected deferred fallback to call SetUpdatePending after post-swap restart failure")
	}
	if result.Topology.FailurePhase != string(muxengine.UpdatePhaseRestart) {
		t.Fatalf("Topology.FailurePhase = %q, want %q", result.Topology.FailurePhase, muxengine.UpdatePhaseRestart)
	}
}

func TestCoordinatorApply_MuxcoreSwapFailureIsHardErrorInAuto(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-next.exe")
	if err := os.WriteFile(binaryPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("v2"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	mock := &mockSessionHandler{}
	coord := &upgrade.Coordinator{
		Version:        "5.14.2",
		BinaryPath:     binaryPath,
		Source:         sourcePath,
		EngineMode:     true,
		SessionHandler: mock,
		ApplyUpdateAndRestart: func(ctx context.Context, opts muxengine.UpdateAndRestartOptions) (muxengine.UpdateAndRestartResult, error) {
			return muxengine.UpdateAndRestartResult{}, &muxengine.UpdateAndRestartError{
				Phase: muxengine.UpdatePhaseSwap,
				Err:   errors.New("rename failed"),
			}
		},
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeAuto, true)
	if err == nil {
		t.Fatal("expected swap failure to be a hard error")
	}
	if !strings.Contains(err.Error(), "rename failed") {
		t.Fatalf("error = %q, want swap error", err)
	}
	if mock.pendingCalled {
		t.Fatal("did not expect deferred fallback before binary install was reached")
	}
}

func TestCoordinatorApply_AutoFallsBackWhenEngineModeSeamUnavailable(t *testing.T) {
	mock := &mockSessionHandler{}
	coord := &upgrade.Coordinator{
		Version:        "4.3.0",
		EngineMode:     true,
		SessionHandler: mock,
		ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
			return &updater.Release{Version: "4.4.0"}, nil
		},
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeAuto, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Method != "deferred" {
		t.Fatalf("Method = %q, want deferred fallback", result.Method)
	}
	if result.HandoffError == "" {
		t.Fatal("expected HandoffError on fallback")
	}
	if !strings.Contains(result.HandoffError, "control seam") {
		t.Fatalf("HandoffError = %q, want control seam detail", result.HandoffError)
	}
	if !mock.pendingCalled {
		t.Fatal("expected deferred fallback to call SetUpdatePending")
	}
}

func TestCoordinatorApply_AutoBypassesGracefulRestartOutsideEngineMode(t *testing.T) {
	var called bool
	coord := &upgrade.Coordinator{
		Version:    "4.3.0",
		EngineMode: false,
		ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
			return &updater.Release{Version: "4.4.0"}, nil
		},
		GracefulRestart: func(ctx context.Context, drainTimeoutMs int) error {
			called = true
			return nil
		},
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeAuto, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if called {
		t.Fatal("graceful restart seam should not be called outside engine mode")
	}
	if result.Method != "deferred" {
		t.Fatalf("Method = %q, want deferred", result.Method)
	}
	if !strings.Contains(result.Message, "Restart aimux") {
		t.Fatalf("message = %q, want manual restart guidance", result.Message)
	}
}

func TestCoordinatorApply_PropagatesGracefulRestartFailureInHotSwapMode(t *testing.T) {
	coord := &upgrade.Coordinator{
		Version:    "4.3.0",
		EngineMode: true,
		ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
			return &updater.Release{Version: "4.4.0"}, nil
		},
		GracefulRestart: func(ctx context.Context, drainTimeoutMs int) error {
			return errors.New("dial failed")
		},
		HandoffStatus: func(ctx context.Context) (upgrade.HandoffStatus, error) {
			return upgrade.HandoffStatus{}, nil
		},
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeHotSwap, false)
	if err == nil {
		t.Fatal("expected graceful restart error")
	}
	if !strings.Contains(err.Error(), "dial failed") {
		t.Fatalf("error = %q, want propagated seam failure", err)
	}
}

func TestCoordinatorApply_HotSwapFailsOutsideEngineMode(t *testing.T) {
	coord := &upgrade.Coordinator{
		Version:    "4.3.0",
		EngineMode: false,
		ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
			return &updater.Release{Version: "4.4.0"}, nil
		},
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeHotSwap, false)
	if err == nil {
		t.Fatal("expected ModeHotSwap to fail outside engine mode")
	}
	if !strings.Contains(err.Error(), "outside engine mode") {
		t.Fatalf("error = %q, want outside engine mode message", err)
	}
}

func TestCoordinatorApply_ConcurrentCallsReturnAlreadyInProgress(t *testing.T) {
	started := make(chan struct{})
	releaseFirst := make(chan struct{})
	coord := &upgrade.Coordinator{
		Version: "4.3.0",
		ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
			close(started)
			<-releaseFirst
			return &updater.Release{Version: "4.4.0"}, nil
		},
	}

	var wg sync.WaitGroup
	wg.Add(1)
	var firstErr error
	go func() {
		defer wg.Done()
		_, firstErr = coord.Apply(context.Background(), upgrade.ModeDeferred, false)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first apply to start")
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeDeferred, false)
	if err == nil {
		t.Fatal("expected already_in_progress error")
	}
	if !strings.Contains(err.Error(), "already_in_progress") {
		t.Fatalf("error = %q, want already_in_progress", err)
	}

	close(releaseFirst)
	wg.Wait()
	if firstErr != nil {
		t.Fatalf("first apply error = %v, want nil", firstErr)
	}
}

func TestCoordinatorApply_AutoFallsBackWithDiskFullClass(t *testing.T) {
	mock := &mockSessionHandler{}
	coord := &upgrade.Coordinator{
		Version:        "4.3.0",
		BinaryPath:     filepath.Join(t.TempDir(), "aimux-test.exe"),
		EngineMode:     true,
		SessionHandler: mock,
		ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
			return nil, &updater.ApplyError{
				Release: &updater.Release{Version: "4.4.0"},
				Err:     fmt.Errorf("install: %w: no space left on device", updater.ErrDiskFull),
			}
		},
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeAuto, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Method != "deferred" {
		t.Fatalf("Method = %q, want deferred", result.Method)
	}
	if result.HandoffError != "disk_full" {
		t.Fatalf("HandoffError = %q, want disk_full", result.HandoffError)
	}
	if !strings.Contains(result.Message, "disk_full") {
		t.Fatalf("message = %q, want disk_full detail", result.Message)
	}
	if !mock.pendingCalled {
		t.Fatal("expected deferred fallback to call SetUpdatePending")
	}
}

func TestCoordinatorApply_ChecksumFailureIsHardErrorInAuto(t *testing.T) {
	mock := &mockSessionHandler{}
	coord := &upgrade.Coordinator{
		Version:        "4.3.0",
		BinaryPath:     filepath.Join(t.TempDir(), "aimux-test.exe"),
		EngineMode:     true,
		SessionHandler: mock,
		ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
			return nil, fmt.Errorf("apply update: %w", updater.ErrChecksumVerification)
		},
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeAuto, false)
	if err == nil {
		t.Fatal("expected checksum failure to be a hard error")
	}
	if !errors.Is(err, updater.ErrChecksumVerification) {
		t.Fatalf("error = %v, want checksum verification classification", err)
	}
	if mock.pendingCalled {
		t.Fatal("did not expect deferred fallback on checksum failure")
	}
}

func TestCoordinatorApply_LogsInfoOnHotSwapSuccess(t *testing.T) {
	line := runApplyAndReadSingleLogLine(t, func(log *logger.Logger) *upgrade.Coordinator {
		return &upgrade.Coordinator{
			Version:    "4.3.0",
			EngineMode: true,
			Logger:     log,
			ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
				return &updater.Release{Version: "4.4.0"}, nil
			},
			GracefulRestart: func(ctx context.Context, drainTimeoutMs int) error {
				return nil
			},
			HandoffStatus: func(ctx context.Context) (upgrade.HandoffStatus, error) {
				return upgrade.HandoffStatus{Fallback: 3}, nil
			},
		}
	}, func(coord *upgrade.Coordinator) {
		result, err := coord.Apply(context.Background(), upgrade.ModeHotSwap, false)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if result.Method != "hot_swap" {
			t.Fatalf("Method = %q, want hot_swap", result.Method)
		}
	})

	assertLogContainsAll(t, line,
		"[INFO]",
		"module=server.upgrade",
		"event=upgrade_complete",
		"prev_version=4.3.0",
		"new_version=4.4.0",
		"method=hot_swap",
		"duration_ms=",
		"transferred_ids=[]",
	)
	assertLogNotContains(t, line, "handoff_error=")
	assertLogNotContains(t, line, "error=")
}

func TestCoordinatorApply_LogsWarnOnDeferredFallback(t *testing.T) {
	line := runApplyAndReadSingleLogLine(t, func(log *logger.Logger) *upgrade.Coordinator {
		return &upgrade.Coordinator{
			Version:    "4.3.0",
			EngineMode: true,
			Logger:     log,
			ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
				return &updater.Release{Version: "4.4.0"}, nil
			},
		}
	}, func(coord *upgrade.Coordinator) {
		result, err := coord.Apply(context.Background(), upgrade.ModeAuto, false)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if result.Method != "deferred" {
			t.Fatalf("Method = %q, want deferred", result.Method)
		}
		if result.HandoffError == "" {
			t.Fatal("expected handoff error")
		}
	})

	assertLogContainsAll(t, line,
		"[WARN]",
		"module=server.upgrade",
		"event=upgrade_complete",
		"prev_version=4.3.0",
		"new_version=4.4.0",
		"method=deferred",
		"duration_ms=",
		"transferred_ids=[]",
		"handoff_error=",
	)
	assertLogNotContains(t, line, "[INFO]")
	assertLogNotContains(t, line, " error=")
}

func TestCoordinatorApply_LogsErrorOnHardFailure(t *testing.T) {
	line := runApplyAndReadSingleLogLine(t, func(log *logger.Logger) *upgrade.Coordinator {
		return &upgrade.Coordinator{
			Version: "4.3.0",
			Logger:  log,
			ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
				return nil, errors.New("network unavailable")
			},
		}
	}, func(coord *upgrade.Coordinator) {
		_, err := coord.Apply(context.Background(), "", false)
		if err == nil {
			t.Fatal("expected hard error")
		}
	})

	assertLogContainsAll(t, line,
		"[ERROR]",
		"module=server.upgrade",
		"event=upgrade_complete",
		"prev_version=4.3.0",
		"new_version=4.3.0",
		"method=auto",
		"duration_ms=",
		"transferred_ids=[]",
		"error=",
	)
	assertLogNotContains(t, line, "handoff_error=")
}

func TestNewControlSocketGracefulRestartFunc_SendsGracefulRestart(t *testing.T) {
	socketPath := controlSocketTestPath(t)
	handler := &mockControlHandler{}
	server, err := control.NewServer(socketPath, handler, log.Default())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	restart := upgrade.NewControlSocketGracefulRestartFunc(server.SocketPath())
	if restart == nil {
		t.Fatal("expected non-nil graceful restart func")
	}
	if err := restart(context.Background(), 4321); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if handler.lastDrainTimeoutMs != 4321 {
		t.Fatalf("drain timeout = %d, want 4321", handler.lastDrainTimeoutMs)
	}
	if handler.gracefulRestartCalls != 1 {
		t.Fatalf("graceful restart calls = %d, want 1", handler.gracefulRestartCalls)
	}
	if handler.shutdownCalls != 0 {
		t.Fatalf("shutdown calls = %d, want 0", handler.shutdownCalls)
	}
}

func TestCoordinatorApply_AutoFallsBackWhenDaemonReportsFallback(t *testing.T) {
	mock := &mockSessionHandler{}
	statusCalls := 0
	coord := &upgrade.Coordinator{
		Version:        "4.3.0",
		EngineMode:     true,
		SessionHandler: mock,
		ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
			return &updater.Release{Version: "4.4.0"}, nil
		},
		GracefulRestart: func(ctx context.Context, drainTimeoutMs int) error {
			return nil
		},
		HandoffStatus: func(ctx context.Context) (upgrade.HandoffStatus, error) {
			statusCalls++
			if statusCalls == 1 {
				return upgrade.HandoffStatus{Fallback: 10}, nil
			}
			return upgrade.HandoffStatus{Fallback: 11}, nil
		},
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeAuto, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Method != "deferred" {
		t.Fatalf("Method = %q, want deferred fallback", result.Method)
	}
	if result.HandoffError == "" {
		t.Fatal("expected HandoffError on daemon-reported fallback")
	}
	if !strings.Contains(result.HandoffError, "fell back to deferred restart") {
		t.Fatalf("HandoffError = %q, want daemon fallback detail", result.HandoffError)
	}
	if !mock.pendingCalled {
		t.Fatal("expected deferred fallback to call SetUpdatePending")
	}
}

func TestCoordinatorApply_HotSwapFailsWhenDaemonReportsFallback(t *testing.T) {
	statusCalls := 0
	coord := &upgrade.Coordinator{
		Version:    "4.3.0",
		EngineMode: true,
		ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
			return &updater.Release{Version: "4.4.0"}, nil
		},
		GracefulRestart: func(ctx context.Context, drainTimeoutMs int) error {
			return nil
		},
		HandoffStatus: func(ctx context.Context) (upgrade.HandoffStatus, error) {
			statusCalls++
			if statusCalls == 1 {
				return upgrade.HandoffStatus{Fallback: 2}, nil
			}
			return upgrade.HandoffStatus{Fallback: 3}, nil
		},
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeHotSwap, false)
	if err == nil {
		t.Fatal("expected hot_swap mode to fail when daemon reports fallback")
	}
	if !strings.Contains(err.Error(), "fell back to deferred restart") {
		t.Fatalf("error = %q, want daemon fallback detail", err)
	}
}

func TestNewControlSocketHandoffStatusFunc_ReadsFallbackCounter(t *testing.T) {
	socketPath := controlSocketTestPath(t)
	handler := &mockControlHandler{statusHandoffFallback: 12}
	server, err := control.NewServer(socketPath, handler, log.Default())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	statusFn := upgrade.NewControlSocketHandoffStatusFunc(server.SocketPath())
	if statusFn == nil {
		t.Fatal("expected non-nil handoff status func")
	}
	status, err := statusFn(context.Background())
	if err != nil {
		t.Fatalf("statusFn: %v", err)
	}
	if status.Fallback != 12 {
		t.Fatalf("Fallback = %d, want 12", status.Fallback)
	}
}

// SessionHandler interface conformance check — aimuxHandler (from pkg/server)
// satisfies upgrade.SessionHandler via SetUpdatePending method.
func TestSessionHandler_InterfaceShape(t *testing.T) {
	var _ upgrade.SessionHandler = (*mockSessionHandler)(nil)
}

func TestCoordinator_ApplyFromLocal(t *testing.T) {
	dir := t.TempDir()

	// Create a fake source binary with known content.
	srcPath := filepath.Join(dir, "aimux-new.exe")
	srcContent := []byte("fake-binary-content-v2")
	if err := os.WriteFile(srcPath, srcContent, 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	// Create a fake current binary that will be replaced.
	binaryPath := filepath.Join(dir, "aimux.exe")
	if err := os.WriteFile(binaryPath, []byte("fake-binary-content-v1"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}

	coord := &upgrade.Coordinator{
		Version:    "4.3.0",
		BinaryPath: binaryPath,
		Source:     srcPath,
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeDeferred, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.NewVersion != "local-dev" {
		t.Fatalf("NewVersion = %q, want local-dev", result.NewVersion)
	}

	// Verify the binary at BinaryPath now contains the source content.
	got, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("ReadFile binary after apply: %v", err)
	}
	if string(got) != string(srcContent) {
		t.Fatalf("binary content = %q, want %q", got, srcContent)
	}

	// .old file should exist (original backup).
	oldPath := binaryPath + ".old"
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("expected .old backup to exist: %v", err)
	}
}

func TestCoordinator_ApplyFromLocal_MissingSource(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux.exe")
	if err := os.WriteFile(binaryPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	coord := &upgrade.Coordinator{
		Version:    "4.3.0",
		BinaryPath: binaryPath,
		Source:     filepath.Join(dir, "nonexistent.exe"),
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeDeferred, false)
	if err == nil {
		t.Fatal("expected error for missing source binary")
	}
	if !strings.Contains(err.Error(), "source binary not found") {
		t.Fatalf("error = %q, want source binary not found", err)
	}

	// Verify the original binary is untouched (no rename should have occurred).
	if _, statErr := os.Stat(binaryPath); statErr != nil {
		t.Fatalf("original binary should still exist: %v", statErr)
	}
}

func TestCoordinator_ApplyFromLocal_SourceIsDirectory(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux.exe")
	if err := os.WriteFile(binaryPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	coord := &upgrade.Coordinator{
		Version:    "4.3.0",
		BinaryPath: binaryPath,
		Source:     dir, // a directory, not a file
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeDeferred, false)
	if err == nil {
		t.Fatal("expected error when source is a directory")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Fatalf("error = %q, want directory error", err)
	}
}

func TestCoordinator_ApplyFromLocal_RejectsSourceOutsideTrustedDirs(t *testing.T) {
	binDir := t.TempDir()
	sourceDir := t.TempDir()
	binaryPath := filepath.Join(binDir, "aimux.exe")
	sourcePath := filepath.Join(sourceDir, "aimux-next.exe")
	if err := os.WriteFile(binaryPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("v2"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	coord := &upgrade.Coordinator{
		Version:    "4.3.0",
		BinaryPath: binaryPath,
		Source:     sourcePath,
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeDeferred, false)
	if err == nil {
		t.Fatal("expected source outside trusted directories to be denied")
	}
	if !strings.Contains(err.Error(), "outside trusted upgrade directories") {
		t.Fatalf("error = %q, want trusted directory denial", err)
	}
}

func TestCoordinator_ApplyFromLocal_RejectsSymlinkToSourceOutsideTrustedDirs(t *testing.T) {
	binDir := t.TempDir()
	sourceDir := t.TempDir()
	binaryPath := filepath.Join(binDir, "aimux.exe")
	outsideSource := filepath.Join(sourceDir, "aimux-next.exe")
	sourceLink := filepath.Join(binDir, "aimux-next.exe")
	if err := os.WriteFile(binaryPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}
	if err := os.WriteFile(outsideSource, []byte("v2"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	if err := os.Symlink(outsideSource, sourceLink); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	coord := &upgrade.Coordinator{
		Version:    "4.3.0",
		BinaryPath: binaryPath,
		Source:     sourceLink,
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeDeferred, false)
	if err == nil {
		t.Fatal("expected symlinked source outside trusted directories to be denied")
	}
	if !strings.Contains(err.Error(), "outside trusted upgrade directories") {
		t.Fatalf("error = %q, want trusted directory denial", err)
	}
}

func TestCoordinator_ApplyFromLocal_AllowsConfiguredStagingDir(t *testing.T) {
	binDir := t.TempDir()
	stagingDir := t.TempDir()
	t.Setenv("AIMUX_UPGRADE_SOURCE_DIR", stagingDir)
	binaryPath := filepath.Join(binDir, "aimux.exe")
	sourcePath := filepath.Join(stagingDir, "aimux-next.exe")
	sourceContent := []byte("v2")
	if err := os.WriteFile(binaryPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}
	if err := os.WriteFile(sourcePath, sourceContent, 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	coord := &upgrade.Coordinator{
		Version:    "4.3.0",
		BinaryPath: binaryPath,
		Source:     sourcePath,
	}

	if _, err := coord.Apply(context.Background(), upgrade.ModeDeferred, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("ReadFile binary after apply: %v", err)
	}
	if string(got) != string(sourceContent) {
		t.Fatalf("binary content = %q, want %q", got, sourceContent)
	}
}

func TestCoordinator_ApplyFromLocal_RejectsExtensionMismatch(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "aimux.exe")
	sourcePath := filepath.Join(dir, "aimux-next.bin")
	if err := os.WriteFile(binaryPath, []byte("v1"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("v2"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	coord := &upgrade.Coordinator{
		Version:    "4.3.0",
		BinaryPath: binaryPath,
		Source:     sourcePath,
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeDeferred, false)
	if err == nil {
		t.Fatal("expected extension mismatch to be denied")
	}
	if !strings.Contains(err.Error(), "extension") {
		t.Fatalf("error = %q, want extension detail", err)
	}
}

func runApplyAndReadSingleLogLine(t *testing.T, buildCoordinator func(*logger.Logger) *upgrade.Coordinator, runApply func(*upgrade.Coordinator)) string {
	t.Helper()

	logPath := filepath.Join(t.TempDir(), "upgrade.log")
	upgradeLogger, err := logger.New(logPath, logger.LevelDebug, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}

	coord := buildCoordinator(upgradeLogger)
	runApply(coord)

	if err := upgradeLogger.Close(); err != nil {
		t.Fatalf("logger.Close: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, line := range lines {
		if strings.Contains(line, "event=upgrade_complete") {
			return line
		}
	}
	t.Fatalf("no upgrade_complete event found in log (%d lines); content=%q", len(lines), string(data))
	return ""
}

func assertLogContainsAll(t *testing.T, line string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(line, want) {
			t.Fatalf("log line = %q, missing %q", line, want)
		}
	}
}

func assertLogNotContains(t *testing.T, line string, notWant string) {
	t.Helper()
	if strings.Contains(line, notWant) {
		t.Fatalf("log line = %q, must not contain %q", line, notWant)
	}
}

func controlSocketTestPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "aimux-gr.sock")
	}
	// macOS sun_path is 104 bytes (struct sockaddr_un); Linux is 108. Go's
	// t.TempDir() roots under /var/folders/<short-hash>/T/<test-name>NNN/...
	// which combined with the test name + "aimux-gr.sock" (13 bytes) can push
	// the address past 104 on darwin and yield EINVAL intermittently when
	// other goroutines (race detector / parallel tests) claim the same path.
	// Use a short unique suffix under TMPDIR that fits in sun_path even
	// under deepest-name test cases.
	//
	// DEF-12 root cause: t.TempDir() name length × race scheduling × sun_path
	// limit produced the macOS pkg/upgrade flake on CI.
	if runtime.GOOS == "darwin" {
		// /tmp/amx-<pid>-<nano>.sock — typically ≤ 40 bytes, safe within 104.
		// Hardcode /tmp (not os.TempDir()): on macOS os.TempDir() resolves to
		// /var/folders/... which can exceed the 104-byte sun_path limit.
		path := filepath.Join("/tmp", fmt.Sprintf("amx-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
		t.Cleanup(func() { _ = os.Remove(path) })
		return path
	}
	return filepath.Join(t.TempDir(), "aimux-gr.sock")
}

type mockControlHandler struct {
	shutdownCalls         int
	lastDrainTimeoutMs    int
	gracefulRestartCalls  int
	statusHandoffFallback uint64
}

func (m *mockControlHandler) HandleShutdown(drainTimeoutMs int) string {
	m.shutdownCalls++
	m.lastDrainTimeoutMs = drainTimeoutMs
	return "ok"
}

func (m *mockControlHandler) HandleStatus() map[string]interface{} {
	return map[string]interface{}{
		"status": "ok",
		"handoff": map[string]any{
			"fallback": m.statusHandoffFallback,
		},
	}
}

func (m *mockControlHandler) HandleSpawn(req control.Request) (ipcPath, serverID, token string, err error) {
	return "", "", "", nil
}

func (m *mockControlHandler) HandleRemove(serverID string) error {
	return nil
}

func (m *mockControlHandler) HandleGracefulRestart(drainTimeoutMs int) (snapshotPath string, afterResponse func(), err error) {
	m.gracefulRestartCalls++
	m.lastDrainTimeoutMs = drainTimeoutMs
	return "", nil, nil
}

func (m *mockControlHandler) HandleRefreshSessionToken(prevToken string) (newToken string, err error) {
	return "", nil
}

func (m *mockControlHandler) HandleReconnectGiveUp(reason string) error {
	return nil
}

func (m *mockControlHandler) HandleListOwners(req control.Request) (control.ListOwnersResponse, error) {
	return control.ListOwnersResponse{}, nil
}
