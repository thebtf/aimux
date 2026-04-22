package upgrade

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/updater"
)

type mockSessionHandler struct {
	pendingCalled bool
}

func (m *mockSessionHandler) SetUpdatePending() {
	m.pendingCalled = true
}

func TestCoordinator_Compile(t *testing.T) {
	mock := &mockSessionHandler{}
	coord := &Coordinator{
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
		mode Mode
		want string
	}{
		{ModeAuto, "auto"},
		{ModeHotSwap, "hot_swap"},
		{ModeDeferred, "deferred"},
	}
	for _, tc := range tests {
		if string(tc.mode) != tc.want {
			t.Errorf("Mode %v: got %q, want %q", tc.mode, string(tc.mode), tc.want)
		}
	}
}

func TestResult_Fields(t *testing.T) {
	r := &Result{}
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

func TestCoordinator_Apply_ModeDeferred_UsesDeferredPath(t *testing.T) {
	mock := &mockSessionHandler{}
	coord := &Coordinator{
		Version:        "4.3.0",
		SessionHandler: mock,
	}
	setCoordinatorApplyUpdateFn(t, coord, func(ctx context.Context, currentVersion string) (*updater.Release, error) {
		return &updater.Release{Version: "4.4.0"}, nil
	})

	result, err := coord.Apply(context.Background(), ModeDeferred)
	if err != nil {
		t.Fatalf("Apply(ModeDeferred): %v", err)
	}
	if result.Method != "deferred" {
		t.Fatalf("Method = %q, want deferred", result.Method)
	}
	if result.HandoffError != "" {
		t.Fatalf("HandoffError = %q, want empty", result.HandoffError)
	}
	if !mock.pendingCalled {
		t.Fatal("SetUpdatePending was not called for ModeDeferred")
	}
}

func TestCoordinator_Apply_ModeHotSwap_FailsWithoutFallback(t *testing.T) {
	mock := &mockSessionHandler{}
	coord := &Coordinator{
		EngineMode:     true,
		SessionHandler: mock,
	}
	setCoordinatorApplyUpdateFn(t, coord, func(ctx context.Context, currentVersion string) (*updater.Release, error) {
		return &updater.Release{Version: "4.4.0"}, nil
	})

	_, err := coord.Apply(context.Background(), ModeHotSwap)
	if err == nil {
		t.Fatal("expected ModeHotSwap to fail")
	}
	if mock.pendingCalled {
		t.Fatal("ModeHotSwap must not fall back to deferred path")
	}
	if !strings.Contains(err.Error(), "ShutdownForHandoff") {
		t.Fatalf("error = %q, want ShutdownForHandoff evidence", err)
	}
}

func TestCoordinator_Apply_ModeAuto_FallsBackWithHandoffError(t *testing.T) {
	mock := &mockSessionHandler{}
	coord := &Coordinator{
		EngineMode:     true,
		SessionHandler: mock,
		Version:        "4.3.0",
	}
	setCoordinatorApplyUpdateFn(t, coord, func(ctx context.Context, currentVersion string) (*updater.Release, error) {
		return &updater.Release{Version: "4.4.0"}, nil
	})

	result, err := coord.Apply(context.Background(), ModeAuto)
	if err != nil {
		t.Fatalf("Apply(ModeAuto): %v", err)
	}
	if result.Method != "deferred" {
		t.Fatalf("Method = %q, want deferred fallback", result.Method)
	}
	if result.HandoffError == "" {
		t.Fatal("ModeAuto must populate HandoffError on fallback")
	}
	if !strings.Contains(result.HandoffError, "ShutdownForHandoff") {
		t.Fatalf("HandoffError = %q, want ShutdownForHandoff evidence", result.HandoffError)
	}
	if !strings.Contains(result.Message, "Hot-swap unavailable") {
		t.Fatalf("Message = %q, want hot-swap unavailable suffix", result.Message)
	}
	if !mock.pendingCalled {
		t.Fatal("ModeAuto fallback must call deferred path")
	}
}

func TestCoordinator_Apply_ModeAuto_PropagatesDeferredFailure(t *testing.T) {
	coord := &Coordinator{
		EngineMode:     true,
		SessionHandler: &mockSessionHandler{},
	}
	setCoordinatorApplyUpdateFn(t, coord, func(ctx context.Context, currentVersion string) (*updater.Release, error) {
		return nil, errors.New("apply update boom")
	})

	_, err := coord.Apply(context.Background(), ModeAuto)
	if err == nil {
		t.Fatal("expected ModeAuto to surface deferred failure")
	}
	if !strings.Contains(err.Error(), "apply update boom") {
		t.Fatalf("error = %q, want deferred failure cause", err)
	}
}

func TestCoordinator_TryHotSwap_BlockedWithoutEngineMode(t *testing.T) {
	coord := &Coordinator{}

	_, err := coord.Apply(context.Background(), ModeHotSwap)
	if err == nil {
		t.Fatal("expected ModeHotSwap to fail without real daemon-side handoff access")
	}
	if !strings.Contains(err.Error(), "daemon-side muxcore owner handoff") {
		t.Fatalf("error = %q, want daemon-side handoff blocker", err)
	}
	if !strings.Contains(err.Error(), "engine mode disabled") {
		t.Fatalf("error = %q, want engine mode blocker detail", err)
	}
}

func TestCoordinator_TryHotSwap_BlockedWithSessionOnlyAdapter(t *testing.T) {
	coord := &Coordinator{
		EngineMode:     true,
		SessionHandler: &mockSessionHandler{},
	}

	_, err := coord.Apply(context.Background(), ModeHotSwap)
	if err == nil {
		t.Fatal("expected ModeHotSwap to fail when only session-side adapter is available")
	}
	if !strings.Contains(err.Error(), "ShutdownForHandoff") {
		t.Fatalf("error = %q, want ShutdownForHandoff evidence", err)
	}
	if !strings.Contains(err.Error(), "ReceiveHandoff") {
		t.Fatalf("error = %q, want ReceiveHandoff evidence", err)
	}
}

// SessionHandler interface conformance check — aimuxHandler (from pkg/server)
// satisfies upgrade.SessionHandler via SetUpdatePending method.
func TestSessionHandler_InterfaceShape(t *testing.T) {
	var _ SessionHandler = (*mockSessionHandler)(nil)
}

func setCoordinatorApplyUpdateFn(t *testing.T, coord *Coordinator, fn func(context.Context, string) (*updater.Release, error)) {
	t.Helper()
	coord.applyUpdateFn = fn
}
