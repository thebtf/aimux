package server

import (
	"context"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/upgrade"
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

	// Lint: silence unused if checks above are removed.
	_ = strings.TrimSpace("")
}

func TestUpgrade_AutoUsesDeferredInSessionHandlerMode(t *testing.T) {
	srv := testServer(t)
	srv.SessionHandler()

	var capturedMode upgrade.Mode
	srv.applyUpgrade = func(ctx context.Context, coord *upgrade.Coordinator, mode upgrade.Mode, force bool) (*upgrade.Result, error) {
		capturedMode = mode
		return &upgrade.Result{
			Method:          "deferred",
			PreviousVersion: Version,
			NewVersion:      "local-dev",
			Message:         "Binary updated. Daemon will restart when all CC sessions disconnect.",
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
	if capturedMode != upgrade.ModeDeferred {
		t.Fatalf("captured mode = %q, want %q", capturedMode, upgrade.ModeDeferred)
	}

	payload := parseResult(t, result)
	if payload["status"] != "updated_deferred" {
		t.Fatalf("status = %v, want updated_deferred; payload=%v", payload["status"], payload)
	}
	if got := payload["handoff_error"]; !strings.Contains(got.(string), "SessionHandler mode") {
		t.Fatalf("handoff_error = %v, want SessionHandler mode reason", got)
	}
}

func TestUpgrade_HotSwapRejectedInSessionHandlerMode(t *testing.T) {
	srv := testServer(t)
	srv.SessionHandler()

	called := false
	srv.applyUpgrade = func(ctx context.Context, coord *upgrade.Coordinator, mode upgrade.Mode, force bool) (*upgrade.Result, error) {
		called = true
		return nil, nil
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
	if called {
		t.Fatal("applyUpgrade was called for unsupported hot_swap mode")
	}

	payload := parseResult(t, result)
	if got := payload["text"]; !strings.Contains(got.(string), "hot-swap unsupported") {
		t.Fatalf("error payload = %v, want hot-swap unsupported", got)
	}
}
