package e2e

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/mcp-mux/muxcore/control"
)

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
// Each test gets a unique AIMUX_ENGINE_NAME derived from t.TempDir() so
// parallel tests never collide on the control or IPC socket paths.
//
// Known constraint: muxcore/owner/resilient_client.go exits the shim on
// stdin EOF detection even for persistent MCP sessions (engram mcp-mux#153).
// Tests MUST NOT close the shim's stdin until they are done reading all
// expected responses. t.Cleanup closes stdin last.
func startDaemonAndShim(t *testing.T, aimuxBin, testcliDir, configDir string) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()

	// Engine name MUST be unique per test to prevent control-socket collisions.
	// Unix-socket paths have an effective ~107-char ceiling on both Linux
	// (sockaddr_un.sun_path[108]) and Windows (AF_UNIX path limit), so we
	// keep the engine name minimal: "aimux-e2e-" + 8 hex chars = 18 bytes.
	// The random suffix alone gives 2^32 uniqueness — more than enough for
	// the suite. We log the mapping so failing tests can be correlated.
	var randSuffix [4]byte
	if _, err := rand.Read(randSuffix[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	engineName := "aimux-e2e-" + hex.EncodeToString(randSuffix[:])
	t.Logf("startDaemonAndShim: engine=%s test=%s", engineName, t.Name())

	var pathEnv string
	if testcliDir != "" {
		pathEnv = testcliDir + string(os.PathListSeparator) + os.Getenv("PATH")
	} else {
		pathEnv = os.Getenv("PATH")
	}

	// Isolate muxcore's own IPC sockets (mcp-mux-${server_id}.sock) from any
	// production aimux daemon on the same machine. muxcore derives its socket
	// paths from os.TempDir(); overriding TMPDIR/TEMP/TMP per test points them
	// into a test-scoped tempdir, so the fresh daemon never collides with a
	// user's long-running aimux server.
	//
	// t.TempDir() produces deeply-nested paths (TestName1234567890/001/…)
	// that overflow the Unix-socket path limit (~108 chars on Linux/Windows)
	// once the engine name + "-muxd.ctl.sock" suffix is appended. Use a
	// short-named sibling directory under os.TempDir() instead.
	shortTmp, tmpErr := os.MkdirTemp(os.TempDir(), "ae")
	if tmpErr != nil {
		t.Fatalf("create isolated tmp: %v", tmpErr)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortTmp) })
	isolatedTmp := shortTmp
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
	//
	// The control socket is created by the engine at
	// as `<dir>/<engineName>-muxd.ctl.sock`. Once the daemon is listening
	// there, it is ready to accept IPC connections from shims. Dial with
	// retries until the socket accepts a connection or the timeout expires.
	ctlSock := filepath.Join(isolatedTmp, engineName+"-muxd.ctl.sock")
	daemonCmd := exec.Command(aimuxBin, "--muxcore-daemon")
	daemonCmd.Env = baseEnv
	daemonCmd.Stderr = os.Stderr
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	t.Cleanup(func() {
		cleanupDaemon(t, ctlSock, daemonCmd, "startDaemonAndShim")
	})

	// --- Wait for daemon readiness via control socket dial ---
	// Readiness timeout is generous (60s) because the test suite may spawn
	// many daemons in rapid succession; a cold daemon on a loaded machine
	// can take several seconds to create its control socket.
	if err := waitForCtlSocket(ctlSock, 60*time.Second); err != nil {
		t.Fatalf("daemon readiness: %v (name=%s)", err, engineName)
	}

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
		conn, err := net.Dial("unix", path)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("control socket did not become ready within %v: %s", timeout, path)
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
	if _, err := os.Stat(ctlSock); err != nil {
		return err
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
	engineName := "aimux-e2e-" + hex.EncodeToString(randSuffix[:])
	t.Logf("startDaemonAndShimWithEnv: engine=%s test=%s", engineName, t.Name())

	var pathEnv string
	if testcliDir != "" {
		pathEnv = testcliDir + string(os.PathListSeparator) + os.Getenv("PATH")
	} else {
		pathEnv = os.Getenv("PATH")
	}

	shortTmp, tmpErr := os.MkdirTemp(os.TempDir(), "ae")
	if tmpErr != nil {
		t.Fatalf("create isolated tmp: %v", tmpErr)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortTmp) })
	isolatedTmp := shortTmp
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

	ctlSock := filepath.Join(isolatedTmp, engineName+"-muxd.ctl.sock")
	daemonCmd := exec.Command(aimuxBin, "--muxcore-daemon")
	daemonCmd.Env = baseEnv
	daemonCmd.Stderr = os.Stderr
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	t.Cleanup(func() {
		cleanupDaemon(t, ctlSock, daemonCmd, "startDaemonAndShimWithEnv")
	})
	if err := waitForCtlSocket(ctlSock, 60*time.Second); err != nil {
		t.Fatalf("daemon readiness: %v (name=%s)", err, engineName)
	}

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
