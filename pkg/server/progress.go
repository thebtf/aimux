package server

import (
	"context"
	"time"

	"github.com/thebtf/aimux/pkg/parser"
	"github.com/thebtf/aimux/pkg/types"
)

// liveProgress returns an OnOutput callback that sends notifications/progress
// to all MCP clients in real time. Works for both sync and async tool calls.
// For async calls, wrap with jobProgress() to also persist to job store.
func (s *Server) liveProgress(toolName string) func(cli, line string) {
	return func(cli, line string) {
		if s.mcp == nil {
			return
		}
		s.mcp.SendNotificationToAllClients("notifications/progress", map[string]any{
			"progressToken": toolName,
			"message":       line,
		})
	}
}

// jobProgress returns an OnOutput callback that both sends live notifications
// AND persists progress to the job store. Use for async tool calls.
func (s *Server) jobProgress(jobID string) func(cli, line string) {
	return func(cli, line string) {
		s.sendJobProgress(jobID, line)
	}
}

// jobProgressFormatted returns an OnOutput callback that normalizes output
// (strips JSONL framing) before persisting and notifying. Use for async
// tool calls with CLIs that emit structured output (codex jsonl, etc.).
func (s *Server) jobProgressFormatted(jobID, outputFormat string) func(resolvedCLI, line string) {
	return func(resolvedCLI, line string) {
		format := outputFormat
		if profile, err := s.registry.Get(resolvedCLI); err == nil {
			format = profile.OutputFormat
		}
		s.progressSink(jobID, format)(line)
	}
}

// sendJobProgress appends a progress line to the job store and pushes a
// notifications/progress MCP notification so clients receive real-time output.
// This single method is the canonical path for both exec and agent handlers —
// centralising the format prevents silent divergence between the two.
func (s *Server) sendJobProgress(jobID, line string) {
	if !s.appendLoomProgressIfTask(jobID, line) {
		s.jobs.AppendProgress(jobID, line)
	}
	if s.mcp != nil {
		s.mcp.SendNotificationToAllClients("notifications/progress", map[string]any{
			"progressToken": jobID,
			"message":       line,
		})
	}
}

// ProgressBridge connects internal Go channels to MCP progress notifications.
// Constitution P6: Push, not poll. Job progress delivered via channels.
type ProgressBridge struct {
	interval time.Duration
}

// NewProgressBridge creates a bridge with the given notification interval.
func NewProgressBridge(intervalSeconds int) *ProgressBridge {
	if intervalSeconds <= 0 {
		intervalSeconds = 15
	}
	return &ProgressBridge{
		interval: time.Duration(intervalSeconds) * time.Second,
	}
}

func normalizeProgressLine(outputFormat, line string) string {
	parsed, _ := parser.ParseContent(line, outputFormat)
	if outputFormat == "json" || outputFormat == "jsonl" {
		if parsed == "" || parsed == line {
			return ""
		}
	}
	return parsed
}

func agentBusyEstimateMs(timeoutSeconds, maxTurns int) int {
	effectiveTurns := maxTurns
	if effectiveTurns <= 0 {
		effectiveTurns = 1
	}
	return effectiveTurns * timeoutSeconds * 1000
}

func (s *Server) progressSink(jobID, outputFormat string) func(string) {
	return func(line string) {
		normalized := normalizeProgressLine(outputFormat, line)
		if normalized == "" {
			return
		}
		if !s.appendLoomProgressIfTask(jobID, normalized) {
			s.jobs.AppendProgress(jobID, normalized)
		}
		if s.mcp != nil {
			s.mcp.SendNotificationToAllClients("notifications/progress", map[string]any{
				"progressToken": jobID,
				"message":       normalized,
			})
		}
	}
}

// Forward reads events from a channel and sends MCP progress notifications.
// Runs until the channel is closed or context is cancelled.
func (b *ProgressBridge) Forward(ctx context.Context, events <-chan types.Event, onProgress func(string)) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case evt, ok := <-events:
			if !ok {
				return // channel closed
			}
			switch evt.Type {
			case types.EventTypeProgress:
				onProgress(evt.Content)
			case types.EventTypeContent:
				// content events are not forwarded as progress
			case types.EventTypeComplete:
				return
			case types.EventTypeError:
				return
			}

		case <-ticker.C:
			// Send a distinct keepalive message — never replay stale content.
			onProgress("...still working")
		}
	}
}
