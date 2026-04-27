package server

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/thebtf/mcp-mux/muxcore/control"
	"github.com/thebtf/mcp-mux/muxcore/serverid"
)

// F2Metrics holds the three F2 shim-reconnect counters surfaced by
// muxcore v0.21.1's Daemon.HandleStatus.
//
// TODO(muxcore/engine-daemon-accessor): This uses the control socket in
// ALL modes, which is correct for client/shim mode but is a self-loopback
// in daemon mode — NDJSON marshal + Unix-socket hop to our own process
// just to read an in-memory counter. Accepted as TEMPORARY because
// engine.MuxEngine currently has no public accessor for its *daemon.Daemon
// (or a narrow `Status() map[string]any`) — tracked as engram mcp-mux#146.
// When that lands (muxcore v0.22.0+), switch to mode-aware branching:
//
//	if eng.IsDaemon() {
//	    stats = eng.Status()          // in-process, no IO
//	} else {
//	    stats = queryF2MetricsAt(...) // cross-process, socket hop required
//	}
//
// Docstring below reflects the current (temporary) all-modes-socket path.
type F2Metrics struct {
	Refreshed       uint64 `json:"shim_reconnect_refreshed"`
	FallbackSpawned uint64 `json:"shim_reconnect_fallback_spawned"`
	GaveUp          uint64 `json:"shim_reconnect_gave_up"`
}

// queryF2Metrics contacts the aimux daemon control socket and extracts
// the three F2 shim-reconnect counters. Returns zero values and a non-nil
// error if the socket cannot be reached or the response is malformed.
func queryF2Metrics() (F2Metrics, error) {
	name := ResolveEngineName()
	return queryF2MetricsAt(serverid.DaemonControlPath("", name))
}

// queryF2MetricsAt contacts the control socket at socketPath and extracts
// the three F2 shim-reconnect counters. Separated from queryF2Metrics so
// unit tests can inject a local socket path without env-var trickery.
//
// A 5-second timeout guards the blocking control.Send call via muxcore's
// native control.SendWithTimeout helper — the health endpoint never hangs
// indefinitely even if the daemon is deadlocked or the socket unresponsive.
func queryF2MetricsAt(socketPath string) (F2Metrics, error) {
	resp, err := control.SendWithTimeout(socketPath, control.Request{Cmd: "status"}, 5*time.Second)
	if err != nil {
		return F2Metrics{}, err
	}
	if resp == nil {
		return F2Metrics{}, fmt.Errorf("control: nil response")
	}
	if !resp.OK {
		return F2Metrics{}, fmt.Errorf("control: %s", resp.Message)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return F2Metrics{}, err
	}
	var m F2Metrics
	// Absent keys are left as zero (graceful degradation — older daemon builds
	// or startup state may not yet expose these counters). A key that IS present
	// but contains malformed JSON is a contract violation; propagate the error
	// so callers detect format incompatibility rather than silently reading zeros.
	if v, ok := raw["shim_reconnect_refreshed"]; ok && len(v) > 0 {
		if err := json.Unmarshal(v, &m.Refreshed); err != nil {
			return F2Metrics{}, fmt.Errorf("invalid shim_reconnect_refreshed: %w", err)
		}
	}
	if v, ok := raw["shim_reconnect_fallback_spawned"]; ok && len(v) > 0 {
		if err := json.Unmarshal(v, &m.FallbackSpawned); err != nil {
			return F2Metrics{}, fmt.Errorf("invalid shim_reconnect_fallback_spawned: %w", err)
		}
	}
	if v, ok := raw["shim_reconnect_gave_up"]; ok && len(v) > 0 {
		if err := json.Unmarshal(v, &m.GaveUp); err != nil {
			return F2Metrics{}, fmt.Errorf("invalid shim_reconnect_gave_up: %w", err)
		}
	}
	return m, nil
}
