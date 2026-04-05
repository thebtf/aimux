package server

import (
	"context"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

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

// Forward reads events from a channel and sends MCP progress notifications.
// Runs until the channel is closed or context is cancelled.
func (b *ProgressBridge) Forward(ctx context.Context, events <-chan types.Event, onProgress func(string)) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	lastContent := ""

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
				lastContent = evt.Content
				onProgress(evt.Content)
			case types.EventTypeContent:
				lastContent = evt.Content
			case types.EventTypeComplete:
				return
			case types.EventTypeError:
				return
			}

		case <-ticker.C:
			// Send keepalive progress notification even if no new events
			if lastContent != "" {
				onProgress(lastContent)
			} else {
				onProgress("working...")
			}
		}
	}
}
