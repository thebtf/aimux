package upgrade_test

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/updater"
	"github.com/thebtf/aimux/pkg/upgrade"
	"github.com/thebtf/mcp-mux/muxcore/control"
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
		ApplyUpdate: func(ctx context.Context, currentVersion string) (*updater.Release, error) {
			return &updater.Release{Version: "4.4.0"}, nil
		},
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeDeferred)
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
	}

	result, err := coord.Apply(context.Background(), upgrade.ModeAuto)
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

	result, err := coord.Apply(context.Background(), upgrade.ModeAuto)
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

	result, err := coord.Apply(context.Background(), upgrade.ModeAuto)
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
	}

	_, err := coord.Apply(context.Background(), upgrade.ModeHotSwap)
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

	_, err := coord.Apply(context.Background(), upgrade.ModeHotSwap)
	if err == nil {
		t.Fatal("expected ModeHotSwap to fail outside engine mode")
	}
	if !strings.Contains(err.Error(), "outside engine mode") {
		t.Fatalf("error = %q, want outside engine mode message", err)
	}
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

// SessionHandler interface conformance check — aimuxHandler (from pkg/server)
// satisfies upgrade.SessionHandler via SetUpdatePending method.
func TestSessionHandler_InterfaceShape(t *testing.T) {
	var _ upgrade.SessionHandler = (*mockSessionHandler)(nil)
}

func controlSocketTestPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "aimux-gr.sock")
	}
	return filepath.Join(t.TempDir(), "aimux-gr.sock")
}

type mockControlHandler struct {
	shutdownCalls        int
	lastDrainTimeoutMs   int
	gracefulRestartCalls int
}

func (m *mockControlHandler) HandleShutdown(drainTimeoutMs int) string {
	m.shutdownCalls++
	m.lastDrainTimeoutMs = drainTimeoutMs
	return "ok"
}

func (m *mockControlHandler) HandleStatus() map[string]interface{} {
	return map[string]interface{}{"status": "ok"}
}

func (m *mockControlHandler) HandleSpawn(req control.Request) (ipcPath, serverID, token string, err error) {
	return "", "", "", nil
}

func (m *mockControlHandler) HandleRemove(serverID string) error {
	return nil
}

func (m *mockControlHandler) HandleGracefulRestart(drainTimeoutMs int) (snapshotPath string, err error) {
	m.gracefulRestartCalls++
	m.lastDrainTimeoutMs = drainTimeoutMs
	return "", nil
}

func (m *mockControlHandler) HandleRefreshSessionToken(prevToken string) (newToken string, err error) {
	return "", nil
}

func (m *mockControlHandler) HandleReconnectGiveUp(reason string) error {
	return nil
}
