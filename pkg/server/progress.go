package server

import (
	"context"
	"time"

	"github.com/thebtf/aimux/pkg/parser"
	"github.com/thebtf/aimux/pkg/types"
)

// sendJobProgress appends a progress line to the job store and pushes a
// notifications/progress MCP notification so clients receive real-time output.
// This single method is the canonical path for both exec and agent handlers —
// centralising the format prevents silent divergence between the two.
func (s *Server) sendJobProgress(jobID, line string) {
	s.jobs.AppendProgress(jobID, line)
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
		s.jobs.AppendProgress(jobID, normalized)
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
