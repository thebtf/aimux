package upgrade_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/upgrade"
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

// SessionHandler interface conformance check — aimuxHandler (from pkg/server)
// satisfies upgrade.SessionHandler via SetUpdatePending method.
func TestSessionHandler_InterfaceShape(t *testing.T) {
	var _ upgrade.SessionHandler = (*mockSessionHandler)(nil)
}
