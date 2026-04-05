package pipeline

import "github.com/thebtf/aimux/pkg/types"

// AllowedEventTypes defines which event types pass through the filter.
// By default: agent_message and turn.completed (skip heavy events like
// tool calls, file writes, etc.)
var AllowedEventTypes = map[types.EventType]bool{
	types.EventTypeContent:  true,
	types.EventTypeProgress: true,
	types.EventTypeComplete: true,
	types.EventTypeError:    true,
}

// FilterEvents removes unwanted events from a channel.
func FilterEvents(in <-chan types.Event) <-chan types.Event {
	out := make(chan types.Event, cap(in))

	go func() {
		defer close(out)
		for evt := range in {
			if AllowedEventTypes[evt.Type] {
				out <- evt
			}
		}
	}()

	return out
}
