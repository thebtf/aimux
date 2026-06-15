package server

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/upgrade"
	"github.com/thebtf/mcp-mux/muxcore/engine"
)

// TestUpgrade_EngineMode_DetectionWhenSessionHandlerSet verifies that the
// engineMode boolean computed by handleUpgrade reflects the result of
// `SessionHandler()` having been called. This is the smoking-gun test for
// engram issue #174 (hot-swap false-deferred).
//
// Sequence:
//  1. Construct a Server via the same path the daemon uses (NewDaemon → registerTools).
//  2. Call srv.SessionHandler() the same way main.go does before engine.New.
//  3. Assert s.sessionHandler is now a non-nil *aimuxHandler.
//  4. Assert that handleUpgrade's type assertion returns engineMode=true.
//
// If step 3 passes and step 4 also passes — the bug is NOT in handleUpgrade
// detection. The bug must be in integration (separate Server instance, or
// runtime mutation we have not found).
//
// If step 3 passes but step 4 fails — the type assertion itself is broken
// in a way no static review found.
func TestUpgrade_EngineMode_DetectionWhenSessionHandlerSet(t *testing.T) {
	srv := testServer(t)

	// Mirror cmd/aimux/main.go default branch wiring.
	h := srv.SessionHandler()
	if h == nil {
		t.Fatal("SessionHandler() returned nil")
	}

	// Step 3: assert sessionHandler is set as expected.
	if srv.sessionHandler == nil {
		t.Fatal("after SessionHandler(): srv.sessionHandler is nil")
	}
	concrete, ok := srv.sessionHandler.(*aimuxHandler)
	if !ok {
		t.Fatalf("after SessionHandler(): srv.sessionHandler is %T, expected *aimuxHandler", srv.sessionHandler)
	}
	if concrete == nil {
		t.Fatal("after SessionHandler(): concrete *aimuxHandler is nil pointer")
	}

	// Step 4: replicate the exact engineMode detection from handleUpgrade.
	hUp, engineMode := srv.sessionHandler.(*aimuxHandler)
	if !engineMode {
		t.Fatalf("type assertion on srv.sessionHandler returned engineMode=false; should be true. concrete type=%T", srv.sessionHandler)
	}
	if hUp == nil {
		t.Fatal("type-assertion handler is nil")
	}

	// Sanity: concrete should equal the assertion result.
	if hUp != concrete {
		t.Errorf("type-assertion result %p != initial assertion %p", hUp, concrete)
	}
}

// TestUpgrade_EngineMode_FalseWhenSessionHandlerNeverCalled verifies the
// negative case: when SessionHandler() is NEVER called (mode=direct path
// in main.go), srv.sessionHandler stays nil and engineMode=false.
func TestUpgrade_EngineMode_FalseWhenSessionHandlerNeverCalled(t *testing.T) {
	srv := testServer(t)

	// Do NOT call srv.SessionHandler().

	if srv.sessionHandler != nil {
		t.Fatalf("expected srv.sessionHandler nil before SessionHandler() called; got %T", srv.sessionHandler)
	}

	_, engineMode := srv.sessionHandler.(*aimuxHandler)
	if engineMode {
		t.Fatal("expected engineMode=false when sessionHandler is nil")
	}
}

// TestUpgrade_EngineMode_StaysSetAfterSecondCall verifies that calling
// SessionHandler() twice does not reset state — both type assertions resolve
// to a non-nil *aimuxHandler. Tests the "second hot-swap" scenario where the
// daemon has already been through one handoff lifecycle.
func TestUpgrade_EngineMode_StaysSetAfterSecondCall(t *testing.T) {
	srv := testServer(t)

	first := srv.SessionHandler()
	second := srv.SessionHandler()

	if first == nil || second == nil {
		t.Fatal("SessionHandler() returned nil on first or second call")
	}

	// Each call constructs a new aimuxHandler and overwrites s.sessionHandler.
	// That overwrite is acceptable as long as the new value is still a valid
	// *aimuxHandler — engineMode should stay true.
	_, engineMode := srv.sessionHandler.(*aimuxHandler)
	if !engineMode {
		t.Fatal("engineMode=false after second SessionHandler() call")
	}

	// Defensive: log the type so a future regression is loud.
	t.Logf("after two SessionHandler() calls: srv.sessionHandler=%T, engineMode=%t", srv.sessionHandler, engineMode)
}

func TestUpgrade_AutoKeepsRequestedModeInSessionHandlerMode(t *testing.T) {
	srv := testServer(t)
	srv.SessionHandler()

	var capturedMode upgrade.Mode
	var capturedEngineMode bool
	srv.applyUpgrade = func(ctx context.Context, coord *upgrade.Coordinator, mode upgrade.Mode, force bool) (*upgrade.Result, error) {
		capturedMode = mode
		capturedEngineMode = coord.EngineMode
		return &upgrade.Result{
			Method:          "hot_swap",
			PreviousVersion: Version,
			NewVersion:      "local-dev",
			Message:         "Binary updated. Daemon handoff completed successfully.",
		}, nil
	}

	result, err := srv.handleUpgrade(context.Background(), makeRequest("upgrade", map[string]any{
		"action": "apply",
		"source": "local-dev.exe",
		"force":  true,
	}))
	if err != nil {
		t.Fatalf("handleUpgrade: %v", err)
	}
	if capturedMode != upgrade.ModeAuto {
		t.Fatalf("captured mode = %q, want %q", capturedMode, upgrade.ModeAuto)
	}
	if !capturedEngineMode {
		t.Fatal("expected coordinator EngineMode=true in SessionHandler mode")
	}

	payload := parseResult(t, result)
	if payload["status"] != "updated_hot_swap" {
		t.Fatalf("status = %v, want updated_hot_swap; payload=%v", payload["status"], payload)
	}
}

func TestUpgrade_AutoProvidesMuxcoreRestartHelperBeforeEngineRun(t *testing.T) {
	srv := testServer(t)
	handler := srv.SessionHandler()
	eng, err := engine.New(engine.Config{
		Name:           "aimux-test-upgrade-helper",
		SessionHandler: handler,
		Persistent:     true,
		BaseDir:        t.TempDir(),
		SkipSnapshot:   true,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	srv.SetMuxEngine(eng)

	var capturedEngineMode bool
	var capturedHelperAvailable bool
	srv.applyUpgrade = func(ctx context.Context, coord *upgrade.Coordinator, mode upgrade.Mode, force bool) (*upgrade.Result, error) {
		capturedEngineMode = coord.EngineMode
		capturedHelperAvailable = coord.ApplyUpdateAndRestart != nil
		return &upgrade.Result{
			Method:          "hot_swap",
			PreviousVersion: Version,
			NewVersion:      "local-dev",
			Message:         "Binary updated. Daemon handoff completed successfully.",
		}, nil
	}

	result, err := srv.handleUpgrade(context.Background(), makeRequest("upgrade", map[string]any{
		"action": "apply",
		"source": "local-dev.exe",
		"force":  true,
	}))
	if err != nil {
		t.Fatalf("handleUpgrade: %v", err)
	}
	if !capturedEngineMode {
		t.Fatal("expected coordinator EngineMode=true in SessionHandler mode")
	}
	if !capturedHelperAvailable {
		t.Fatal("expected coordinator ApplyUpdateAndRestart helper to be available when mux engine is wired")
	}
	payload := parseResult(t, result)
	if payload["status"] != "updated_hot_swap" {
		t.Fatalf("status = %v, want updated_hot_swap; payload=%v", payload["status"], payload)
	}
}

func TestUpgrade_HotSwapAllowedInSessionHandlerMode(t *testing.T) {
	srv := testServer(t)
	srv.SessionHandler()

	called := false
	var capturedMode upgrade.Mode
	srv.applyUpgrade = func(ctx context.Context, coord *upgrade.Coordinator, mode upgrade.Mode, force bool) (*upgrade.Result, error) {
		called = true
		capturedMode = mode
		if !coord.EngineMode {
			t.Fatal("expected coordinator EngineMode=true in SessionHandler mode")
		}
		return &upgrade.Result{
			Method:          "hot_swap",
			PreviousVersion: Version,
			NewVersion:      "local-dev",
			Message:         "Binary updated. Daemon handoff completed successfully.",
		}, nil
	}

	result, err := srv.handleUpgrade(context.Background(), makeRequest("upgrade", map[string]any{
		"action": "apply",
		"mode":   "hot_swap",
		"source": "local-dev.exe",
		"force":  true,
	}))
	if err != nil {
		t.Fatalf("handleUpgrade: %v", err)
	}
	if !called {
		t.Fatal("expected applyUpgrade to be called for hot_swap mode")
	}
	if capturedMode != upgrade.ModeHotSwap {
		t.Fatalf("captured mode = %q, want %q", capturedMode, upgrade.ModeHotSwap)
	}
	payload := parseResult(t, result)
	if payload["status"] != "updated_hot_swap" {
		t.Fatalf("status = %v, want updated_hot_swap; payload=%v", payload["status"], payload)
	}
}

func TestUpgradePayloadIncludesTopologyDetails(t *testing.T) {
	srv := testServer(t)
	srv.SessionHandler()

	srv.applyUpgrade = func(ctx context.Context, coord *upgrade.Coordinator, mode upgrade.Mode, force bool) (*upgrade.Result, error) {
		return &upgrade.Result{
			Method:          "deferred",
			PreviousVersion: Version,
			NewVersion:      "local-dev",
			HandoffError:    "post-exit install scheduled",
			Message:         "Binary update scheduled. Post-exit helper will stop and restart the daemon.",
			Topology: upgrade.UpdateTopology{
				UpdateMethod:       "deferred",
				RestartTopology:    "post_exit",
				DaemonWasRunning:   true,
				LockAcquired:       true,
				GracefulRestarted:  false,
				FallbackShutdown:   false,
				ReplacementStarted: true,
				ReplacementReady:   false,
				FailurePhase:       "post_exit",
				Warnings:           []string{"waiting for current process exit"},
			},
		}, nil
	}

	result, err := srv.handleUpgrade(context.Background(), makeRequest("upgrade", map[string]any{
		"action": "apply",
		"source": "local-dev.exe",
		"force":  true,
	}))
	if err != nil {
		t.Fatalf("handleUpgrade: %v", err)
	}

	payload := parseResult(t, result)
	if payload["status"] != "updated_deferred" {
		t.Fatalf("status = %v, want updated_deferred; payload=%v", payload["status"], payload)
	}
	if payload["update_method"] != "deferred" {
		t.Fatalf("update_method = %v, want deferred; payload=%v", payload["update_method"], payload)
	}
	topology, ok := payload["update_topology"].(map[string]any)
	if !ok {
		t.Fatalf("update_topology = %#v, want object; payload=%v", payload["update_topology"], payload)
	}
	if topology["restart_topology"] != "post_exit" {
		t.Fatalf("restart_topology = %v, want post_exit; topology=%v", topology["restart_topology"], topology)
	}
	if topology["replacement_started"] != true {
		t.Fatalf("replacement_started = %v, want true; topology=%v", topology["replacement_started"], topology)
	}
	if topology["failure_phase"] != "post_exit" {
		t.Fatalf("failure_phase = %v, want post_exit; topology=%v", topology["failure_phase"], topology)
	}
	warnings, ok := topology["warnings"].([]any)
	if !ok || len(warnings) != 1 || warnings[0] != "waiting for current process exit" {
		t.Fatalf("warnings = %#v, want one topology warning", topology["warnings"])
	}
}
