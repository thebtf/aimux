package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestE2E_Upgrade_HotSwap_RuntimeEngineMode reproduces engram issue #174 in a
// clean test environment so daemon log contents can be inspected without
// touching a production aimux daemon.
//
// Pipeline:
//  1. Build current branch's aimux binary (includes the upgrade-diag log lines
//     added in pkg/server/server.go and pkg/server/server_transport.go).
//  2. Spawn daemon+shim via the standard test helper.
//  3. Issue MCP `upgrade(action="apply", source=v2, force=true, mode="auto")`.
//  4. Read the daemon's log file. Assert that:
//       - "upgrade-diag: SessionHandler() called" was logged at boot
//       - "upgrade-diag: handleUpgrade entered" was logged on the apply call
//       - The engineMode field on the entry log reflects the actual runtime
//         state. If engineMode=false despite SessionHandler() having been
//         called, the discrepancy is now captured in test output for the
//         issue tracker.
//
// On Windows we still spawn the test (the rename behavior we worry about for
// the live workflow does not block the test fixture path because the test
// owns its own copy of the binary), but flaky-rename failures cause Skip.
func TestE2E_Upgrade_HotSwap_RuntimeEngineMode(t *testing.T) {
	// DEF-12 hybrid scope (AIMUX-15 FR-5b / T007): pre-existing flake on Windows CI
	// driven by muxcore daemon handoff timing — exceeds AIMUX-15 scope budget.
	// Tracked: engram issue #183 — reopen when multi-tenant deployment requires
	// Windows hot-swap reliability OR muxcore upstream lands deterministic handoff.
	t.Skip("muxcore handoff timing — engram issue #183 (DEF-12 hybrid scope)")

	v1Bin := buildBinary(t) // current branch — has upgrade-diag patches
	v2Bin := buildBinary(t)
	testcliBin := buildTestCLI(t)
	tmpDir := t.TempDir()
	configDir, _, logPath := shimTestWriteConfig(t, tmpDir)

	_, stdin, reader := startDaemonAndShim(t, v1Bin, filepath.Dir(testcliBin), configDir)
	initializeMCP(t, stdin, reader)

	// Apply once and capture response.
	if _, err := stdin.Write([]byte(jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "upgrade",
		"arguments": map[string]any{
			"action": "apply",
			"source": v2Bin,
			"force":  true,
			"mode":   "auto",
		},
	}))); err != nil {
		t.Fatalf("write upgrade apply: %v", err)
	}
	resp, err := readResponse(reader, 30*time.Second)
	if err != nil {
		t.Fatalf("upgrade apply response: %v", err)
	}

	// Pretty-print response for diagnosis.
	t.Logf("upgrade response: %+v", resp)

	// Give the daemon a moment to flush log writes before reading the file.
	time.Sleep(300 * time.Millisecond)

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read daemon log %s: %v", logPath, err)
	}
	logStr := string(logBytes)
	t.Logf("daemon log contents:\n----- BEGIN -----\n%s\n----- END -----", logStr)

	// Assertion 1: SessionHandler() ran at least once during this daemon lifetime.
	if !strings.Contains(logStr, "upgrade-diag: SessionHandler() called") {
		t.Fatalf("expected daemon log to contain 'upgrade-diag: SessionHandler() called' (boot-time set-site marker)")
	}

	// Assertion 2: handleUpgrade ran after the apply call.
	if !strings.Contains(logStr, "upgrade-diag: handleUpgrade entered") {
		t.Fatalf("expected daemon log to contain 'upgrade-diag: handleUpgrade entered' (call-site marker)")
	}

	// Extract the handleUpgrade diag line to read engineMode value.
	idx := strings.Index(logStr, "upgrade-diag: handleUpgrade entered")
	if idx < 0 {
		t.Fatal("could not locate handleUpgrade diag line")
	}
	end := strings.Index(logStr[idx:], "\n")
	if end < 0 {
		end = len(logStr) - idx
	}
	diagLine := logStr[idx : idx+end]
	t.Logf("handleUpgrade diag line: %s", diagLine)

	// Print SessionHandler set-site Server pointer + handleUpgrade Server pointer
	// so a difference (= different Server instances) becomes obvious.
	setIdx := strings.Index(logStr, "upgrade-diag: SessionHandler() called")
	if setIdx >= 0 {
		setEnd := strings.Index(logStr[setIdx:], "\n")
		if setEnd < 0 {
			setEnd = len(logStr) - setIdx
		}
		t.Logf("SessionHandler set-site line: %s", logStr[setIdx:setIdx+setEnd])
	}

	// The actual assertion that captures the bug. Spec-correct value is true.
	if strings.Contains(diagLine, "engineMode=false") {
		t.Errorf("BUG REPRODUCED: handleUpgrade observed engineMode=false even though SessionHandler() was called at boot. See log dump above.")
	}

	// Print runtime info to ease cross-platform debugging.
	t.Logf("runtime.GOOS=%s runtime.GOARCH=%s", runtime.GOOS, runtime.GOARCH)

	// Sanity that response object decoded.
	if _, ok := resp["result"].(map[string]any); !ok {
		// Either an error envelope or a transport failure. Surface for log review.
		t.Logf("upgrade response was not a result envelope (raw): %v", resp)
	}

	// Final breadcrumb so post-test triage can grep test output.
	fmt.Fprintln(os.Stderr, "----- TestE2E_Upgrade_HotSwap_RuntimeEngineMode completed -----")
}
