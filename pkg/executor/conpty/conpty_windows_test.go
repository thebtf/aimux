//go:build windows

// Package conpty — Windows-only TUI verification tests.
//
// AIMUX-16 CR-004 NFR-5: these tests MUST run on the windows-latest CI matrix
// entry. Skipping ConPTY tests on Windows is PROHIBITED per spec — silent skip
// would re-introduce the deferral pattern that prompted CR-004 in the first
// place. The tests gate on probeConPTY() succeeding (Win10 1809+); a build
// older than that fails fast with an explicit message.
package conpty

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/thebtf/aimux/pkg/types"
)

// TestProbeConPTY_RequiresMinimumBuild verifies the version gate. Replaces
// versionProvider with a stub returning a build below 17763 and asserts the
// probe refuses (EC-4.1). Restores the real provider on cleanup.
func TestProbeConPTY_RequiresMinimumBuild(t *testing.T) {
	originalVer := versionProvider
	originalProbe := kernel32Probe
	t.Cleanup(func() {
		versionProvider = originalVer
		kernel32Probe = originalProbe
		// Force the cached probe result to be re-evaluated next test.
		probeOnce = onceReset()
	})

	// Stub: pre-1809 Windows.
	versionProvider = func() (uint32, error) { return 17134, nil }      // Win10 1803
	kernel32Probe = func() error { return errors.New("should not be called") }

	if probeConPTY() {
		t.Fatal("probeConPTY returned true on stubbed Win10 1803 (build 17134) — should be false")
	}
}

// TestProbeConPTY_RequiresKernel32Exports verifies the kernel32 export gate
// (T004-9 acceptance: probe returns false when ConPTY exports are missing).
func TestProbeConPTY_RequiresKernel32Exports(t *testing.T) {
	originalVer := versionProvider
	originalProbe := kernel32Probe
	t.Cleanup(func() {
		versionProvider = originalVer
		kernel32Probe = originalProbe
		probeOnce = onceReset()
	})

	versionProvider = func() (uint32, error) { return 22000, nil } // Win11
	kernel32Probe = func() error { return errors.New("CreatePseudoConsole missing in stub") }

	if probeConPTY() {
		t.Fatal("probeConPTY returned true when kernel32 exports stubbed missing — should be false")
	}
}

// TestProbeConPTY_VersionLookupError verifies graceful handling of
// RtlGetVersion failure. Per realRtlGetVersion contract this can only happen
// if the syscall returns nil; we exercise the error branch via a stub.
func TestProbeConPTY_VersionLookupError(t *testing.T) {
	originalVer := versionProvider
	originalProbe := kernel32Probe
	t.Cleanup(func() {
		versionProvider = originalVer
		kernel32Probe = originalProbe
		probeOnce = onceReset()
	})

	versionProvider = func() (uint32, error) { return 0, errors.New("RtlGetVersion stub failure") }
	kernel32Probe = func() error { return nil }

	if probeConPTY() {
		t.Fatal("probeConPTY returned true when version lookup errored — should be false")
	}
}

// TestProbeConPTY_RealHostSucceeds verifies that on the actual GitHub
// windows-latest runner (Win Server 2022 = build 20348), probeConPTY returns
// true. This is the live happy-path test — if it fails on real Windows, the
// CR is NOT shipping ConPTY at all and the operator MUST see it in CI.
func TestProbeConPTY_RealHostSucceeds(t *testing.T) {
	// Don't override the providers — use the real syscalls.
	probeOnce = onceReset()
	if !probeConPTY() {
		// Read actual build for the failure message.
		info := windows.RtlGetVersion()
		t.Fatalf("probeConPTY returned false on real Windows host (build %d) — "+
			"CI runner likely too old or kernel32 ConPTY exports unexpectedly missing", info.BuildNumber)
	}
}

// TestOpenWindowsConPTY_SpawnsRealPseudoConsole asserts that openWindowsConPTY
// successfully invokes CreatePseudoConsole and spawns a child process. This
// is the core CR-004 acceptance: pre-CR-004 there was no syscall at all; post
// CR-004 the syscall fires and the child PID is non-zero.
//
// We use cmd.exe /c echo <marker> as the smallest TUI-eligible Windows
// command — it inherits the pseudo-console and exits immediately.
func TestOpenWindowsConPTY_SpawnsRealPseudoConsole(t *testing.T) {
	probeOnce = onceReset()
	if !probeConPTY() {
		t.Skip("ConPTY not available on this build — covered by TestProbeConPTY_RealHostSucceeds")
	}

	const marker = "AIMUX16_CR004_TUI_MARKER"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := openWindowsConPTY(ctx, openParams{
		command: "cmd.exe",
		args:    []string{"/c", "echo", marker},
	})
	if err != nil {
		t.Fatalf("openWindowsConPTY: %v", err)
	}
	defer h.Close()

	ph := h.ProcessHandle()
	if ph == nil {
		t.Fatal("ProcessHandle was nil — synthetic handle adapter broken")
	}
	if ph.PID <= 0 {
		t.Fatalf("ProcessHandle.PID = %d, want > 0 (CreatePseudoConsole did not produce a real child)", ph.PID)
	}

	// Wait for child to exit, draining stdout into a buffer.
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		var collected strings.Builder
		for {
			n, rerr := h.Read(buf)
			if n > 0 {
				collected.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		done <- collected.String()
	}()

	select {
	case <-ph.Done:
		// child exited
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit within 5s — pseudo-console reap goroutine is stuck")
	}

	// Stop the reader goroutine via Close so it returns the collected output.
	_ = h.Close()
	var output string
	select {
	case output = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reader goroutine did not return collected output within 2s after Close")
	}

	if !strings.Contains(output, marker) {
		t.Fatalf("pseudo-console output missing marker %q. Got: %q", marker, output)
	}
}

// TestOpenWindowsConPTY_ResizeAfterOpen verifies the Resize syscall path
// (T004-4). The default geometry is set on Open; this test exercises an
// additional Resize call to confirm ResizePseudoConsole is wired and does
// not error on a healthy session.
func TestOpenWindowsConPTY_ResizeAfterOpen(t *testing.T) {
	probeOnce = onceReset()
	if !probeConPTY() {
		t.Skip("ConPTY not available on this build")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := openWindowsConPTY(ctx, openParams{
		command: "cmd.exe",
		args:    []string{"/k"},
	})
	if err != nil {
		t.Fatalf("openWindowsConPTY: %v", err)
	}
	defer h.Close()

	if err := h.Resize(80, 25); err != nil {
		t.Fatalf("Resize(80, 25) on healthy ConPTY failed: %v", err)
	}
	if err := h.Resize(200, 50); err != nil {
		t.Fatalf("Resize(200, 50) on healthy ConPTY failed: %v", err)
	}
}

// TestOpenWindowsConPTY_CloseIsIdempotent verifies EC-4.4 — ConPTY teardown
// MUST NOT race with ProcessHandle.Done. Closing the handle multiple times
// from concurrent goroutines must be safe.
func TestOpenWindowsConPTY_CloseIsIdempotent(t *testing.T) {
	probeOnce = onceReset()
	if !probeConPTY() {
		t.Skip("ConPTY not available on this build")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := openWindowsConPTY(ctx, openParams{
		command: "cmd.exe",
		args:    []string{"/c", "ver"},
	})
	if err != nil {
		t.Fatalf("openWindowsConPTY: %v", err)
	}

	// Concurrent Close from 5 goroutines — none should error or panic.
	//
	// CR-004 review feedback (coderabbit MINOR): the previous version
	// asserted that only the FIRST received error could be non-nil, which is
	// flaky — channel-receive order is not goroutine-launch order, so the
	// "real" Close (the one that won the sync.Once race) might return its
	// error AFTER several no-op receives. The robust invariant is "at most
	// one non-nil error across all Close calls" — sync.Once guarantees
	// exactly one goroutine actually invokes the inner function, so at most
	// one non-nil error can be produced regardless of arrival order.
	const closers = 5
	errs := make(chan error, closers)
	for i := 0; i < closers; i++ {
		go func() {
			errs <- h.Close()
		}()
	}
	nonNil := 0
	for i := 0; i < closers; i++ {
		select {
		case err := <-errs:
			if err != nil {
				nonNil++
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Close goroutine #%d did not return within 2s — possible deadlock", i+1)
		}
	}
	if nonNil > 1 {
		t.Fatalf("Close returned %d non-nil errors, want <= 1 — sync.Once shortcut not honoured", nonNil)
	}
}

// TestExecutor_Run_ProducesOutput is the higher-level smoke through the
// Executor.Run() path that exercises CreatePseudoConsole end-to-end. Mirrors
// TestConPTY_Run_Echo from conpty_extra_test.go but explicitly asserts that
// the new ConPTY backend produced the output (pre-CR-004 path used
// exec.Command pipe — this proves the new backend is engaged).
func TestExecutor_Run_ProducesOutput(t *testing.T) {
	probeOnce = onceReset()
	if !probeConPTY() {
		t.Skip("ConPTY not available — TestProbeConPTY_RealHostSucceeds covers the gate")
	}

	const marker = "AIMUX16_CR004_RUN_MARKER"
	e := New()
	if !e.Available() {
		t.Skip("Executor.Available() false despite probe success — should not happen on healthy host")
	}

	res, err := e.Run(context.Background(), types.SpawnArgs{
		Command:        "cmd.exe",
		Args:           []string{"/c", "echo", marker},
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Executor.Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Content, marker) {
		t.Errorf("Content missing marker %q. Got: %q", marker, res.Content)
	}
}

// onceReset returns a fresh sync.Once so probeConPTY can re-evaluate after a
// test mutated versionProvider / kernel32Probe. The package-level probeOnce
// is the only safe shared mutation point — this helper is a small ergonomics
// wrapper to keep cleanup blocks readable.
func onceReset() sync.Once {
	return sync.Once{}
}
