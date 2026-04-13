package pipeline

import "github.com/thebtf/aimux/pkg/types"

// RouteResult holds the channels that events are routed to.
type RouteResult struct {
	Progress <-chan types.Event
	Content  <-chan types.Event
	Complete <-chan types.Event
	Errors   <-chan types.Event
}

// RouteEvents splits an event stream into typed channels.
func RouteEvents(in <-chan types.Event) RouteResult {
	progress := make(chan types.Event, 32)
	content := make(chan types.Event, 64)
	complete := make(chan types.Event, 1)
	errors := make(chan types.Event, 8)

	go func() {
		defer close(progress)
		defer close(content)
		defer close(complete)
		defer close(errors)

		for evt := range in {
			switch evt.Type {
			case types.EventTypeProgress:
				select {
				case progress <- evt:
				default: // drop if consumer is not reading progress
				}
			case types.EventTypeContent:
				select {
				case content <- evt:
				default: // drop if consumer is not reading content
				}
			case types.EventTypeComplete:
				select {
				case complete <- evt:
				default: // drop if consumer is not reading complete
				}
			case types.EventTypeError:
				select {
				case errors <- evt:
				default: // drop if consumer is not reading errors
				}
			}
		}
	}()

	return RouteResult{
		Progress: progress,
		Content:  content,
		Complete: complete,
		Errors:   errors,
	}
}
