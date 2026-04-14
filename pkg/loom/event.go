package loom

import "log"

// EventType identifies a task lifecycle event.
type EventType string

const (
	EventTaskCreated    EventType = "task.created"
	EventTaskDispatched EventType = "task.dispatched"
	EventTaskProgress   EventType = "task.progress"
	EventTaskGatePass   EventType = "task.gate.pass"
	EventTaskGateFail   EventType = "task.gate.fail"
	EventTaskCompleted  EventType = "task.completed"
	EventTaskFailed     EventType = "task.failed"
)

// Event carries task lifecycle data to subscribers.
type Event struct {
	Type   EventType
	TaskID string
	Data   map[string]any
}

// EventBus is a simple fan-out event broadcaster.
// Initial implementation logs events and delivers to buffered subscriber channels.
type EventBus struct {
	subs []chan Event
}

// NewEventBus creates a new EventBus.
func NewEventBus() *EventBus {
	return &EventBus{}
}

// Subscribe registers a new subscriber and returns a buffered channel for receiving events.
func (b *EventBus) Subscribe() chan Event {
	ch := make(chan Event, 64)
	b.subs = append(b.subs, ch)
	return ch
}

// Emit logs the event and delivers it to all subscribers.
// If a subscriber's channel is full, the event is dropped (non-blocking delivery).
func (b *EventBus) Emit(e Event) {
	log.Printf("[loom] %s task=%s", e.Type, e.TaskID)
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
			// Drop event if subscriber is slow — don't block engine.
		}
	}
}
