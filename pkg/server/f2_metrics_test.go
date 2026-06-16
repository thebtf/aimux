package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/mcp-mux/muxcore/control"
	"github.com/thebtf/mcp-mux/muxcore/ipc"
)

// serveFakeControl starts a fake control socket at socketPath, handles one
// connection with the provided Response, then closes. Returns a done channel
// that is closed once the single connection has been served.
func serveFakeControl(t *testing.T, socketPath string, resp control.Response) chan struct{} {
	t.Helper()
	ln, err := ipc.Listen(socketPath)
	if err != nil {
		t.Fatalf("serveFakeControl: listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Decode the incoming request and assert the expected command so that
		// a regression sending the wrong Cmd is caught immediately.
		dec := json.NewDecoder(conn)
		var req control.Request
		if err := dec.Decode(&req); err != nil {
			t.Errorf("serveFakeControl: decode request: %v", err)
			return
		}
		if req.Cmd != "status" {
			t.Errorf("serveFakeControl: unexpected cmd: got %q, want %q", req.Cmd, "status")
			return
		}
		// Write the canned response.
		enc := json.NewEncoder(conn)
		_ = enc.Encode(resp)
	}()
	return done
}

func waitFakeControl(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("serveFakeControl did not receive a control request")
	}
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
	waitFakeControl(t, done)

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
	waitFakeControl(t, done)

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
	waitFakeControl(t, done)

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
	waitFakeControl(t, done)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Refreshed != 7 || m.FallbackSpawned != 2 || m.GaveUp != 1 {
		t.Errorf("unexpected metrics: %+v", m)
	}
}

func TestQueryNativeStatusAt_ExtractsGenerationRestoreAndHandoff(t *testing.T) {
	data, _ := json.Marshal(map[string]any{
		"daemon_generation":               "daemon-gen-2",
		"owner_count":                     1,
		"shim_reconnect_refreshed":        uint64(9),
		"shim_reconnect_fallback_spawned": uint64(4),
		"shim_reconnect_gave_up":          uint64(1),
		"servers": []map[string]any{
			{
				"server_id":                      "owner-1",
				"owner_generation":               "owner-gen-3",
				"restore_source":                 "snapshot",
				"restored_from_owner_generation": "owner-gen-2",
				"active_progress_tokens":         0,
				"busy":                           false,
			},
		},
		"handoff": map[string]any{
			"attempted":                   uint64(3),
			"fallback":                    uint64(2),
			"successor_daemon_generation": "daemon-gen-2",
		},
	})
	sock := tempSocket(t)
	done := serveFakeControl(t, sock, control.Response{OK: true, Data: json.RawMessage(data)})

	status, err := queryNativeStatusAt(sock, "testengine")
	waitFakeControl(t, done)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status["engine_name"] != "testengine" {
		t.Fatalf("engine_name = %v, want testengine; status=%v", status["engine_name"], status)
	}
	if status["daemon_generation"] != "daemon-gen-2" {
		t.Fatalf("daemon_generation = %v, want daemon-gen-2; status=%v", status["daemon_generation"], status)
	}
	if status["owner_count"] != float64(1) {
		t.Fatalf("owner_count = %v, want 1; status=%v", status["owner_count"], status)
	}
	owners, ok := status["owners"].([]map[string]any)
	if !ok || len(owners) != 1 {
		t.Fatalf("owners = %#v, want one normalized owner", status["owners"])
	}
	if owners[0]["owner_generation"] != "owner-gen-3" {
		t.Fatalf("owner_generation = %v, want owner-gen-3", owners[0]["owner_generation"])
	}
	if owners[0]["restore_source"] != "snapshot" {
		t.Fatalf("restore_source = %v, want snapshot", owners[0]["restore_source"])
	}
	handoff, ok := status["handoff"].(map[string]any)
	if !ok {
		t.Fatalf("handoff = %#v, want map", status["handoff"])
	}
	if handoff["fallback"] != float64(2) {
		t.Fatalf("handoff.fallback = %v, want 2", handoff["fallback"])
	}
}
