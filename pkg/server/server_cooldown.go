package server

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// handleCooldown routes sessions(action=cooldown_*) requests to the cooldown
// sub-handlers. Returns (nil, nil) when the action is not a cooldown action so
// that the caller can fall through to the default error branch.
//
// Supported actions:
//
//	cooldown_list  — list all currently-active (non-expired) cooldown entries
//	cooldown_flush — remove a specific (cli, model) cooldown entry immediately
//	cooldown_set   — override the cooldown duration for a (cli, model) pair
func (s *Server) handleCooldown(_ context.Context, request mcp.CallToolRequest, action string) (*mcp.CallToolResult, error) {
	switch action {
	case "cooldown_list":
		entries := s.cooldownTracker.List()
		type entryView struct {
			CLI           string    `json:"cli"`
			Model         string    `json:"model"`
			ExpiresAt     time.Time `json:"expires_at"`
			SecondsLeft   int64     `json:"seconds_left"`
			TriggerStderr string    `json:"trigger_stderr,omitempty"`
		}
		views := make([]entryView, 0, len(entries))
		now := time.Now()
		for _, e := range entries {
			secsLeft := int64(e.ExpiresAt.Sub(now).Seconds())
			if secsLeft < 0 {
				secsLeft = 0
			}
			views = append(views, entryView{
				CLI:           e.CLI,
				Model:         e.Model,
				ExpiresAt:     e.ExpiresAt,
				SecondsLeft:   secsLeft,
				TriggerStderr: e.TriggerStderr,
			})
		}
		return marshalToolResult(map[string]any{
			"count":   len(views),
			"entries": views,
		})

	case "cooldown_flush":
		cli := request.GetString("cli", "")
		model := request.GetString("model", "")
		if cli == "" || model == "" {
			return mcp.NewToolResultError("cli and model are required for cooldown_flush"), nil
		}
		if err := s.cooldownTracker.Flush(cli, model); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return marshalToolResult(map[string]any{
			"flushed": true,
			"cli":     cli,
			"model":   model,
		})

	case "cooldown_set":
		cli := request.GetString("cli", "")
		model := request.GetString("model", "")
		seconds := int(request.GetFloat("seconds", 0))
		if cli == "" || model == "" {
			return mcp.NewToolResultError("cli and model are required for cooldown_set"), nil
		}
		if seconds <= 0 {
			return mcp.NewToolResultError("seconds must be a positive integer for cooldown_set"), nil
		}
		s.cooldownTracker.SetDuration(cli, model, time.Duration(seconds)*time.Second)
		return marshalToolResult(map[string]any{
			"set":     true,
			"cli":     cli,
			"model":   model,
			"seconds": seconds,
		})

	default:
		return nil, nil // not a cooldown action; caller should handle
	}
}
