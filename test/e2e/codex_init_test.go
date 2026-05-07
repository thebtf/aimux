// Package e2e: integration test for codex AppServerProcess initialize handshake
// against a real codex binary.
//
// This test is the gate that would have caught the v5.10.0 bug (missing clientInfo
// field). It is skipped when codex is not on PATH, matching the pattern used by
// the rest of the codex executor tests in this package.
package e2e

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/codex"
	"github.com/thebtf/aimux/pkg/executor/runtime"
)

// TestE2E_CodexInitialize_RealBinary spawns a real codex app-server, performs
// the initialize handshake, and asserts it succeeds without error.
//
// This is the regression gate for AIMUX-bug: "initialize RPC: Invalid request:
// missing field `clientInfo`". The test is skipped when codex is not on PATH.
func TestE2E_CodexInitialize_RealBinary(t *testing.T) {
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex binary not on PATH — skipping real initialize integration test")
	}

	// Use os.MkdirTemp instead of t.TempDir() to avoid a Windows-specific
	// cleanup failure: codex may hold a directory handle open briefly after
	// Shutdown returns, causing TempDir's deferred RemoveAll to fail on Windows.
	workDir, err := os.MkdirTemp("", "codex-init-test-*")
	if err != nil {
		t.Fatalf("create work dir: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort cleanup — ignore errors (Windows process handle delay).
		_ = os.RemoveAll(workDir)
	})

	profile := runtime.CLIRuntimeProfile{
		WorkDir: workDir,
	}

	p := codex.NewAppServerProcess(codexPath, profile)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("AppServerProcess.Start (real codex binary): %v\n"+
			"If this is an auth error, run 'codex auth login' first.\n"+
			"This test catches missing required fields in the initialize RPC.", err)
	}

	// Verify process is Ready after a successful handshake.
	if got := p.State(); got != codex.AppServerStateReady {
		t.Errorf("expected state Ready after Start, got %s", got)
	}

	// Clean shutdown.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := p.Shutdown(shutdownCtx); err != nil {
		t.Logf("Shutdown warning (non-fatal): %v", err)
	}
}
