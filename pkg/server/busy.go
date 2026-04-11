package server

import "time"

// DefaultBusyEstimateMs is used when the caller does not know how long a job
// will run. It matches the default documented in mcp-mux v0.11.0 busy protocol.
const DefaultBusyEstimateMs = 600000 // 10 minutes

// buildBusyPayload constructs the params map for a notifications/x-mux/busy
// notification. Extracted for unit testing — the send path itself requires a
// live MCP server and is covered by integration tests.
func buildBusyPayload(id, task string, estimatedDurationMs int) map[string]any {
	if estimatedDurationMs <= 0 {
		estimatedDurationMs = DefaultBusyEstimateMs
	}
	return map[string]any{
		"id":                  id,
		"startedAt":           time.Now().UTC().Format(time.RFC3339),
		"estimatedDurationMs": estimatedDurationMs,
		"task":                task,
	}
}

// buildIdlePayload constructs the params map for a notifications/x-mux/idle
// notification that clears a previously sent busy declaration.
func buildIdlePayload(id string) map[string]any {
	return map[string]any{
		"id": id,
	}
}

// sendBusy declares to the mcp-mux reaper that aimux is performing background
// work which the MCP request layer cannot observe — e.g. a goroutine running a
// CLI while the originating async call has already returned. While any
// unexpired declaration exists, mcp-mux blocks idle-timeout eviction of this
// upstream. Every sendBusy MUST be paired with a sendIdle carrying the same id.
//
// The id must be stable and unique per concurrent job. The task label is
// logged by mux for observability but never inspected. estimatedDurationMs
// defaults to DefaultBusyEstimateMs when zero or negative.
func (s *Server) sendBusy(id, task string, estimatedDurationMs int) {
	if s.mcp == nil {
		return
	}
	s.mcp.SendNotificationToAllClients(
		"notifications/x-mux/busy",
		buildBusyPayload(id, task, estimatedDurationMs),
	)
}

// sendIdle clears a busy declaration. Safe to call with unknown ids — mcp-mux
// treats them as no-ops. Call exactly once per sendBusy via defer so every
// return path from the background work clears the declaration.
func (s *Server) sendIdle(id string) {
	if s.mcp == nil {
		return
	}
	s.mcp.SendNotificationToAllClients(
		"notifications/x-mux/idle",
		buildIdlePayload(id),
	)
}
