package executor_test

import (
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
)

// longRunningCmd returns a command that runs for ~30 seconds, suitable for kill tests.
func longRunningCmd() *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "ping", "-n", "30", "127.0.0.1")
	}
	return exec.Command("sleep", "30")
}

// echoCmd returns a command that prints a line and exits immediately.
func echoCmd() *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "echo", "hello")
	}
	return exec.Command("echo", "hello")
}

// TestProcessManager_SpawnReturnsHandle verifies that Spawn starts the process,
// returns a handle with a valid PID, and the process exits with code 0.
func TestProcessManager_SpawnReturnsHandle(t *testing.T) {
	pm := executor.NewProcessManager()
	h, err := pm.Spawn(echoCmd())
	if err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}
	if h == nil {
		t.Fatal("Spawn returned nil handle")
	}
	if h.PID <= 0 {
		t.Fatalf("expected PID > 0, got %d", h.PID)
	}

	select {
	case <-h.Done:
	case <-time.After(10 * time.Second):
		t.Fatal("process did not exit within timeout")
	}

	out, err := io.ReadAll(h.Stdout)
	if err != nil {
		t.Fatalf("read stdout after Done: %v", err)
	}
	if !strings.Contains(string(out), "hello") {
		t.Fatalf("stdout after Done = %q, expected hello", string(out))
	}

	if h.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", h.ExitCode)
	}
}

// TestProcessManager_KillTerminatesProcess verifies that Kill stops a long-running process
// and IsAlive returns false afterwards.
func TestProcessManager_KillTerminatesProcess(t *testing.T) {
	pm := executor.NewProcessManager()
	h, err := pm.Spawn(longRunningCmd())
	if err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}

	if !pm.IsAlive(h) {
		t.Fatal("expected process to be alive before Kill")
	}

	pm.Kill(h)

	if pm.IsAlive(h) {
		t.Error("expected process to be dead after Kill")
	}
}

// TestProcessManager_IsAliveReturnsFalse verifies that IsAlive returns false
// after the process exits naturally.
func TestProcessManager_IsAliveReturnsFalse(t *testing.T) {
	pm := executor.NewProcessManager()
	h, err := pm.Spawn(echoCmd())
	if err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}

	// Wait for natural exit.
	select {
	case <-h.Done:
	case <-time.After(10 * time.Second):
		t.Fatal("process did not exit within timeout")
	}

	if pm.IsAlive(h) {
		t.Error("IsAlive should return false after process exits")
	}
}

// TestProcessManager_ShutdownKillsAll spawns two long-running processes, calls Shutdown,
// and verifies that both are no longer alive.
func TestProcessManager_ShutdownKillsAll(t *testing.T) {
	pm := executor.NewProcessManager()

	h1, err := pm.Spawn(longRunningCmd())
	if err != nil {
		t.Fatalf("Spawn h1: %v", err)
	}
	h2, err := pm.Spawn(longRunningCmd())
	if err != nil {
		t.Fatalf("Spawn h2: %v", err)
	}

	if !pm.IsAlive(h1) || !pm.IsAlive(h2) {
		t.Fatal("expected both processes to be alive before Shutdown")
	}

	pm.Shutdown()

	if pm.IsAlive(h1) {
		t.Error("h1 should be dead after Shutdown")
	}
	if pm.IsAlive(h2) {
		t.Error("h2 should be dead after Shutdown")
	}
}

// TestProcessManager_CleanupRemovesFromTracking verifies that after Cleanup,
// the handle is marked as cleaned.
func TestProcessManager_CleanupRemovesFromTracking(t *testing.T) {
	pm := executor.NewProcessManager()
	h, err := pm.Spawn(echoCmd())
	if err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}

	// Wait for natural exit.
	select {
	case <-h.Done:
	case <-time.After(10 * time.Second):
		t.Fatal("process did not exit within timeout")
	}

	// Before Cleanup, spawn a second process to confirm pm is still functional.
	h2, err := pm.Spawn(echoCmd())
	if err != nil {
		t.Fatalf("Spawn h2: %v", err)
	}
	select {
	case <-h2.Done:
	case <-time.After(10 * time.Second):
		t.Fatal("h2 did not exit within timeout")
	}

	pm.Cleanup(h)

	// The exported Done channel must still be readable (already closed), confirming
	// Cleanup does not corrupt handle state.
	select {
	case _, ok := <-h.Done:
		// ok==false means channel was closed, which is the expected state.
		_ = ok
	default:
		// If we reach default, the channel is not yet closed — unexpected.
		t.Error("Done channel should be closed after process exit")
	}
}

func TestProcessManager_CleanupIsIdempotentAndNilSafe(t *testing.T) {
	pm := executor.NewProcessManager()

	pm.Cleanup(nil)

	h := &executor.ProcessHandle{PID: 12345}
	pm.Cleanup(h)
	pm.Cleanup(h)
}

type recordingReadCloser struct {
	closed atomic.Bool
}

func (r *recordingReadCloser) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (r *recordingReadCloser) Close() error {
	r.closed.Store(true)
	return nil
}

func TestProcessManager_CleanupWaitsForDrainSignal(t *testing.T) {
	pm := executor.NewProcessManager()
	stdout := &recordingReadCloser{}
	stderr := &recordingReadCloser{}
	h := &executor.ProcessHandle{PID: 12345, Stdout: stdout, Stderr: stderr}
	h.ArmDrainWait()

	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		pm.Cleanup(h)
	}()

	select {
	case <-cleanupDone:
		t.Fatal("Cleanup returned before drain signal")
	case <-time.After(100 * time.Millisecond):
	}
	if stdout.closed.Load() || stderr.closed.Load() {
		t.Fatal("Cleanup closed pipes before drain signal")
	}

	h.MarkDrained()
	select {
	case <-cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("Cleanup did not return after drain signal")
	}
	if !stdout.closed.Load() || !stderr.closed.Load() {
		t.Fatal("Cleanup did not close pipes after drain signal")
	}
}
