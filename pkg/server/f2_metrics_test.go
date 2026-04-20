package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thebtf/mcp-mux/muxcore/control"
)

// serveFakeControl starts a fake control socket at socketPath, handles one
// connection with the provided Response, then closes. Returns a done channel
// that is closed once the single connection has been served.
func serveFakeControl(t *testing.T, socketPath string, resp control.Response) chan struct{} {
	t.Helper()
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("serveFakeControl: listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Drain the incoming request (we don't inspect it).
		dec := json.NewDecoder(conn)
		var req control.Request
		_ = dec.Decode(&req)
		// Write the canned response.
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()
	return done
}

// tempSocket returns a unique Unix socket path short enough for macOS sun_path
// (104-byte limit). t.TempDir() on macOS resolves to /var/folders/... paths that
// routinely exceed 104 bytes once the socket filename is appended, causing
// `bind: invalid argument` during Listen. Use os.TempDir() with a short
// pid + test-name suffix instead; t.Cleanup removes the socket on test exit.
func tempSocket(t *testing.T) string {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	if len(name) > 40 {
		name = name[:40]
	}
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("f2-%d-%s.sock", os.Getpid(), name))
	_ = os.Remove(sock)
	t.Cleanup(func() { _ = os.Remove(sock) })
	return sock
}

// TestQueryF2MetricsAt_AllCounters verifies all three counters are unmarshaled correctly.
func TestQueryF2MetricsAt_AllCounters(t *testing.T) {
	data, _ := json.Marshal(map[string]any{
		"shim_reconnect_refreshed":        uint64(3),
		"shim_reconnect_fallback_spawned": uint64(1),
		"shim_reconnect_gave_up":          uint64(0),
		"other_key":                       "ignored",
	})
	sock := tempSocket(t)
	done := serveFakeControl(t, sock, control.Response{OK: true, Data: json.RawMessage(data)})

	m, err := queryF2MetricsAt(sock)
	<-done

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Refreshed != 3 {
		t.Errorf("Refreshed: got %d, want 3", m.Refreshed)
	}
	if m.FallbackSpawned != 1 {
		t.Errorf("FallbackSpawned: got %d, want 1", m.FallbackSpawned)
	}
	if m.GaveUp != 0 {
		t.Errorf("GaveUp: got %d, want 0", m.GaveUp)
	}
}

// TestQueryF2MetricsAt_MissingKeys verifies that absent keys default to zero.
func TestQueryF2MetricsAt_MissingKeys(t *testing.T) {
	// Response data contains no shim_reconnect_* keys at all.
	data, _ := json.Marshal(map[string]any{"handoff": "ignored"})
	sock := tempSocket(t)
	done := serveFakeControl(t, sock, control.Response{OK: true, Data: json.RawMessage(data)})

	m, err := queryF2MetricsAt(sock)
	<-done

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Refreshed != 0 || m.FallbackSpawned != 0 || m.GaveUp != 0 {
		t.Errorf("expected all-zero metrics, got %+v", m)
	}
}

// TestQueryF2MetricsAt_OKFalse verifies that OK=false returns a non-nil error.
func TestQueryF2MetricsAt_OKFalse(t *testing.T) {
	sock := tempSocket(t)
	done := serveFakeControl(t, sock, control.Response{OK: false, Message: "daemon shutting down"})

	_, err := queryF2MetricsAt(sock)
	<-done

	if err == nil {
		t.Fatal("expected non-nil error for OK=false response")
	}
}

// TestQueryF2MetricsAt_DialFailure verifies that a missing socket returns an error.
func TestQueryF2MetricsAt_DialFailure(t *testing.T) {
	noSock := filepath.Join(t.TempDir(), "nonexistent.ctl.sock")
	_, err := queryF2MetricsAt(noSock)
	if err == nil {
		t.Fatal("expected non-nil error dialing nonexistent socket")
	}
}

// TestQueryF2Metrics_EnvName verifies queryF2Metrics resolves the socket path
// from AIMUX_ENGINE_NAME and successfully reads counters when a fake daemon
// listens at that path.
func TestQueryF2Metrics_EnvName(t *testing.T) {
	data, _ := json.Marshal(map[string]any{
		"shim_reconnect_refreshed":        uint64(7),
		"shim_reconnect_fallback_spawned": uint64(2),
		"shim_reconnect_gave_up":          uint64(1),
	})

	// Compute the expected socket path for a custom engine name. We redirect
	// TempDir so the socket lands under t.TempDir() rather than os.TempDir().
	// queryF2Metrics uses os.TempDir() (baseDir=""), so we must listen there.
	tmpDir := os.TempDir()
	sock := filepath.Join(tmpDir, "testengine-muxd.ctl.sock")
	// Clean up any leftover socket from a previous run.
	_ = os.Remove(sock)
	t.Cleanup(func() { _ = os.Remove(sock) })

	done := serveFakeControl(t, sock, control.Response{OK: true, Data: json.RawMessage(data)})

	t.Setenv("AIMUX_ENGINE_NAME", "testengine")
	m, err := queryF2Metrics()
	<-done

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Refreshed != 7 || m.FallbackSpawned != 2 || m.GaveUp != 1 {
		t.Errorf("unexpected metrics: %+v", m)
	}
}
