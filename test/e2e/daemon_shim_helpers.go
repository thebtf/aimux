package e2e

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/mcp-mux/muxcore/control"
	"github.com/thebtf/mcp-mux/muxcore/registry"
)

// newMuxcoreIsolatedTemp returns a per-test temp root that keeps muxcore Unix
// socket paths under platform sun_path limits. macOS's os.TempDir() resolves to
// long /var/folders/... paths, so Unix tests need the flat /tmp base.
func newMuxcoreIsolatedTemp(t *testing.T, pattern string) string {
	t.Helper()

	tmpRoot := os.TempDir()
	if runtime.GOOS != "windows" {
		tmpRoot = "/tmp"
	}
	isolatedTmp, err := os.MkdirTemp(tmpRoot, pattern)
	if err != nil {
		t.Fatalf("create isolated tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(isolatedTmp) })
	return isolatedTmp
}

// startDaemonAndShim launches a daemon process via `aimux --muxcore-daemon` and
// a shim client process that bridges stdio↔IPC to it. Returns the shim cmd,
// its stdin write-end, and a bufio.Reader on its stdout — matching the legacy
// signature of startServer/startServerWithTestCLI so individual tests do not
// need to change.
//
// Rationale: AIMUX-6 removed the AIMUX_NO_ENGINE=1 stdio-direct bypass, so
// e2e tests can no longer run aimux as a single-process stdio MCP server.
// Engine mode requires the daemon+shim pair to be spawned separately; the
// shim inherits env from its parent (this test) and forwards PATH so the
// daemon finds testcli binaries when it auto-spawns sub-processes.
//
// Each test gets a unique AIMUX_ENGINE_NAME so parallel tests have distinct
// display labels; muxcore derives the transport namespace and publishes the
// actual control path through its registry descriptor.
//
// Known constraint: muxcore/owner/resilient_client.go exits the shim on
// stdin EOF detection even for persistent MCP sessions (engram mcp-mux#153).
// Tests MUST NOT close the shim's stdin until they are done reading all
// expected responses. t.Cleanup closes stdin last.
func startDaemonAndShim(t *testing.T, aimuxBin, testcliDir, configDir string) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()

	// Engine name is a display label that muxcore includes while deriving its
	// internal namespace. Keep the label VERY short: the final control socket is
	// <tmp>/<engineName>-<hash>-muxd.ctl.sock, so every extra character here eats
	// into Unix-domain socket limits (Linux ~108 bytes, macOS ~104 bytes).
	var randSuffix [4]byte
	if _, err := rand.Read(randSuffix[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	engineName := "ae-" + hex.EncodeToString(randSuffix[:])
	t.Logf("startDaemonAndShim: engine=%s test=%s", engineName, t.Name())

	var pathEnv string
	if testcliDir != "" {
		pathEnv = testcliDir + string(os.PathListSeparator) + os.Getenv("PATH")
	} else {
		pathEnv = os.Getenv("PATH")
	}

	// Isolate muxcore's own IPC sockets from any production aimux daemon on the
	// same machine. muxcore derives socket paths under os.TempDir(); overriding
	// TMPDIR/TEMP/TMP per test points them into a test-scoped tempdir, so the
	// fresh daemon never collides with a user's long-running aimux server.
	//
	// Unix-domain socket limits are short and platform-specific (Linux ~108B,
	// macOS ~104B). macOS's default os.TempDir() is the long /var/folders/.../T
	// path, so even a short child dir can still overflow once muxcore appends the
	// engine name + hash + "-muxd.ctl.sock" suffix. Use /tmp as the flat Unix base
	// and keep the engine name short. On Windows, keep os.TempDir().
	isolatedTmp := newMuxcoreIsolatedTemp(t, "ae")
	tempEnvName := strings.Join([]string{"TE", "MP"}, "")
	baseEnv := append(os.Environ(),
		"AIMUX_CONFIG_DIR="+configDir,
		"AIMUX_ENGINE_NAME="+engineName,
		"AIMUX_WARMUP=false",
		// Per-test daemons must not contend on the shared testdata
		// sessions.db. memory store skips SQLite entirely (feature added
		// in v4.5.0 for exactly this use case).
		"AIMUX_SESSION_STORE=memory",
		"PATH="+pathEnv,
		"TMPDIR="+isolatedTmp,
		tempEnvName+"="+isolatedTmp,
		"TMP="+isolatedTmp,
	)

	// --- Spawn daemon ---
	var ctlSock string
	daemonCmd := exec.Command(aimuxBin, "--muxcore-daemon")
	daemonCmd.Env = baseEnv
	daemonCmd.Stderr = os.Stderr
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	t.Cleanup(func() {
		cleanupDaemon(t, ctlSock, daemonCmd, "startDaemonAndShim")
	})

	// --- Wait for daemon readiness via muxcore registry descriptor ---
	// Readiness timeout is generous (60s) because the test suite may spawn
	// many daemons in rapid succession; a cold daemon on a loaded machine can
	// take several seconds to publish and verify its descriptor.
	rec := waitForHealthyRegistryDescriptor(t, isolatedTmp, engineName, 60*time.Second)
	ctlSock = rec.Descriptor.DaemonControlPath

	// --- Spawn shim with os.Pipe for stdin/stdout ---
	//
	// Using os.Pipe (vs cmd.StdinPipe/StdoutPipe) gives us explicit control
	// over when the parent closes its ends — required by the shim's EOF
	// detection (mcp-mux#153): we must keep the stdin write-end open for the
	// entire test lifetime, not just until fmt.Fprint returns.
	shimStdinR, shimStdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("shim stdin pipe: %v", err)
	}
	shimStdoutR, shimStdoutW, err := os.Pipe()
	if err != nil {
		shimStdinR.Close()
		shimStdinW.Close()
		t.Fatalf("shim stdout pipe: %v", err)
	}

	shimCmd := exec.Command(aimuxBin) // no --muxcore-daemon = shim mode
	shimCmd.Env = baseEnv
	shimCmd.Stdin = shimStdinR
	shimCmd.Stdout = shimStdoutW
	shimCmd.Stderr = os.Stderr

	if err := shimCmd.Start(); err != nil {
		shimStdinR.Close()
		shimStdinW.Close()
		shimStdoutR.Close()
		shimStdoutW.Close()
		t.Fatalf("start shim: %v", err)
	}
	// Parent closes the ends it handed to the child.
	shimStdinR.Close()
	shimStdoutW.Close()

	t.Cleanup(func() {
		// Close stdin write-end — shim's muxcore resilient client exits
		// on its stdin EOF (mcp-mux#153). Give it 2s, then force-kill.
		shimStdinW.Close()
		if shimCmd.Process != nil {
			done := make(chan struct{})
			go func() {
				_ = shimCmd.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = shimCmd.Process.Kill()
				select {
				case <-done:
				case <-time.After(1 * time.Second):
					t.Logf("startDaemonAndShim cleanup: shim Wait() did not return within 1s after Kill")
				}
			}
		}
		shimStdoutR.Close()
	})

	return shimCmd, shimStdinW, bufio.NewReader(shimStdoutR)
}

// waitForCtlSocket polls the engine's control socket until it accepts a
// connection or the timeout expires. Used by startDaemonAndShim to confirm
// the daemon is ready before spawning the shim client.
func waitForCtlSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := control.SendWithTimeout(path, control.Request{Cmd: "ping"}, 500*time.Millisecond)
		if err == nil && resp != nil && resp.OK {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("control socket did not become ready within %v: %s", timeout, path)
}

func waitForRegistryDescriptor(t *testing.T, baseDir, engineName string, timeout time.Duration) registry.Record {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		records, err := registry.ListDescriptors(baseDir)
		if err != nil {
			lastErr = err
		} else if rec, err := registry.ResolveEngine(records, engineName); err == nil {
			return rec
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("registry descriptor for %q not found within %v: %v", engineName, timeout, lastErr)
	return registry.Record{}
}

func waitForHealthyRegistryDescriptor(t *testing.T, baseDir, engineName string, timeout time.Duration) registry.Record {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		rec := waitForRegistryDescriptorSoft(baseDir, engineName)
		if rec.err != nil {
			lastErr = rec.err
		} else {
			verified := registry.VerifyDescriptor(rec.record)
			if verified.State == registry.StateHealthy && verified.Reachable {
				return rec.record
			}
			lastErr = fmt.Errorf("descriptor not healthy: state=%q reachable=%v reason=%q path=%s",
				verified.State, verified.Reachable, verified.Reason, rec.record.Descriptor.DaemonControlPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("healthy registry descriptor for %q not found within %v: %v", engineName, timeout, lastErr)
	return registry.Record{}
}

type registryDescriptorResult struct {
	record registry.Record
	err    error
}

func waitForRegistryDescriptorSoft(baseDir, engineName string) registryDescriptorResult {
	records, err := registry.ListDescriptors(baseDir)
	if err != nil {
		return registryDescriptorResult{err: err}
	}
	rec, err := registry.ResolveEngine(records, engineName)
	if err != nil {
		return registryDescriptorResult{err: err}
	}
	return registryDescriptorResult{record: rec}
}

func cleanupDaemon(t *testing.T, ctlSock string, daemonCmd *exec.Cmd, prefix string) {
	t.Helper()
	if daemonCmd == nil || daemonCmd.Process == nil {
		return
	}

	if err := shutdownDaemonViaControl(ctlSock, 1500*time.Millisecond, 1500); err != nil {
		t.Logf("%s cleanup: control shutdown failed, falling back to Kill: %v", prefix, err)
	}

	done := make(chan struct{})
	go func() {
		_ = daemonCmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(2 * time.Second):
	}

	_ = daemonCmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Logf("%s cleanup: daemon Wait() did not return within 2s after Kill", prefix)
	}
}

func shutdownDaemonViaControl(ctlSock string, timeout time.Duration, drainTimeoutMs int) error {
	if ctlSock == "" {
		return fmt.Errorf("empty control socket path")
	}
	if timeout <= 0 {
		return fmt.Errorf("non-positive timeout")
	}

	resp, err := control.SendWithTimeout(ctlSock, control.Request{
		Cmd:            "shutdown",
		DrainTimeoutMs: drainTimeoutMs,
	}, timeout)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("empty control response")
	}
	if !resp.OK {
		if resp.Message != "" {
			return fmt.Errorf("%s", resp.Message)
		}
		return fmt.Errorf("shutdown rejected")
	}
	return nil
}

func startDaemonAndShimWithEnv(t *testing.T, aimuxBin, testcliDir, configDir string, extraEnv []string) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()

	var randSuffix [4]byte
	if _, err := rand.Read(randSuffix[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	engineName := "ae-" + hex.EncodeToString(randSuffix[:])
	t.Logf("startDaemonAndShimWithEnv: engine=%s test=%s", engineName, t.Name())

	var pathEnv string
	if testcliDir != "" {
		pathEnv = testcliDir + string(os.PathListSeparator) + os.Getenv("PATH")
	} else {
		pathEnv = os.Getenv("PATH")
	}

	isolatedTmp := newMuxcoreIsolatedTemp(t, "ae")
	tempEnvName := strings.Join([]string{"TE", "MP"}, "")
	baseEnv := append(os.Environ(),
		"AIMUX_CONFIG_DIR="+configDir,
		"AIMUX_ENGINE_NAME="+engineName,
		"AIMUX_WARMUP=false",
		"AIMUX_SESSION_STORE=memory",
		"PATH="+pathEnv,
		"TMPDIR="+isolatedTmp,
		tempEnvName+"="+isolatedTmp,
		"TMP="+isolatedTmp,
	)
	baseEnv = append(baseEnv, extraEnv...)

	var ctlSock string
	daemonCmd := exec.Command(aimuxBin, "--muxcore-daemon")
	daemonCmd.Env = baseEnv
	daemonCmd.Stderr = os.Stderr
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	t.Cleanup(func() {
		cleanupDaemon(t, ctlSock, daemonCmd, "startDaemonAndShimWithEnv")
	})
	rec := waitForHealthyRegistryDescriptor(t, isolatedTmp, engineName, 60*time.Second)
	ctlSock = rec.Descriptor.DaemonControlPath

	shimStdinR, shimStdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("shim stdin pipe: %v", err)
	}
	shimStdoutR, shimStdoutW, err := os.Pipe()
	if err != nil {
		shimStdinR.Close()
		shimStdinW.Close()
		t.Fatalf("shim stdout pipe: %v", err)
	}

	shimCmd := exec.Command(aimuxBin)
	shimCmd.Env = baseEnv
	shimCmd.Stdin = shimStdinR
	shimCmd.Stdout = shimStdoutW
	shimCmd.Stderr = os.Stderr

	if err := shimCmd.Start(); err != nil {
		shimStdinR.Close()
		shimStdinW.Close()
		shimStdoutR.Close()
		shimStdoutW.Close()
		t.Fatalf("start shim: %v", err)
	}
	shimStdinR.Close()
	shimStdoutW.Close()

	t.Cleanup(func() {
		shimStdinW.Close()
		if shimCmd.Process != nil {
			done := make(chan struct{})
			go func() {
				_ = shimCmd.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = shimCmd.Process.Kill()
				select {
				case <-done:
				case <-time.After(1 * time.Second):
					t.Logf("startDaemonAndShimWithEnv cleanup: shim Wait() did not return within 1s after Kill")
				}
			}
		}
		shimStdoutR.Close()
	})

	return shimCmd, shimStdinW, bufio.NewReader(shimStdoutR)
}
