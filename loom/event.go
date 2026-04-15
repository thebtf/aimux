package loom

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thebtf/aimux/loom/deps"
)

// EventType identifies a task lifecycle event.
type EventType string

const (
	EventTaskCreated     EventType = "task.created"
	EventTaskDispatched  EventType = "task.dispatched"
	EventTaskRunning     EventType = "task.running"
	EventTaskCompleted   EventType = "task.completed"
	EventTaskFailed      EventType = "task.failed"
	EventTaskFailedCrash EventType = "task.failed_crash"
	EventTaskRetrying    EventType = "task.retrying"
	EventTaskCancelled   EventType = "task.cancelled"
)

// TaskEvent carries task lifecycle data to subscribers.
// All fields are required — subscribers can filter on ProjectID for multi-tenant
// fanout and correlate with RequestID for distributed tracing.
type TaskEvent struct {
	Type      EventType  `json:"type"`
	TaskID    string     `json:"task_id"`
	ProjectID string     `json:"project_id"`
	RequestID string     `json:"request_id,omitempty"`
	Status    TaskStatus `json:"status"`
	Timestamp time.Time  `json:"timestamp"`
}

// subscription holds a registered callback and its unique ID.
type subscription struct {
	id      uint64
	handler func(TaskEvent)
}

// EventBus is a synchronous fan-out event broadcaster with callback subscribers.
// Subscribers are invoked in registration order, synchronously from the emitter's
// goroutine. Panics in a subscriber are recovered and logged; they do NOT affect
// other subscribers or the engine. Slow subscribers block the engine — subscribers
// MUST return quickly and offload heavy work to their own goroutine.
type EventBus struct {
	mu       sync.RWMutex
	subs     map[uint64]*subscription
	order    []uint64 // insertion order for deterministic fanout
	nextID   uint64   // accessed via atomic
	logger   deps.Logger
}

// NewEventBus creates a new EventBus with an optional logger for panic recovery.
// If logger is nil, a NoopLogger is used.
func NewEventBus(logger deps.Logger) *EventBus {
	if logger == nil {
		logger = deps.NoopLogger()
	}
	return &EventBus{
		subs:   make(map[uint64]*subscription),
		logger: logger,
	}
}

// Subscribe registers a callback and returns an unsubscribe function.
// Calling the returned unsubscribe multiple times is safe (idempotent).
func (b *EventBus) Subscribe(handler func(TaskEvent)) func() {
	id := atomic.AddUint64(&b.nextID, 1)
	sub := &subscription{id: id, handler: handler}

	b.mu.Lock()
	b.subs[id] = sub
	b.order = append(b.order, id)
	b.mu.Unlock()

	return func() { b.unsubscribe(id) }
}

// unsubscribe removes a subscription by ID. Idempotent.
func (b *EventBus) unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.subs[id]; !ok {
		return // already removed
	}
	delete(b.subs, id)

	// Remove from order slice (preserving relative order of remaining entries).
	newOrder := make([]uint64, 0, len(b.order))
	for _, oid := range b.order {
		if oid != id {
			newOrder = append(newOrder, oid)
		}
	}
	b.order = newOrder
}

// Emit delivers the event to all subscribers synchronously in registration order.
// Panics in a subscriber are recovered and logged — they do NOT propagate to
// other subscribers or back to the caller.
func (b *EventBus) Emit(e TaskEvent) {
	// Snapshot handlers under read lock to avoid holding the lock during callbacks.
	b.mu.RLock()
	handlers := make([]*subscription, 0, len(b.order))
	for _, id := range b.order {
		if sub, ok := b.subs[id]; ok {
			handlers = append(handlers, sub)
		}
	}
	b.mu.RUnlock()

	bg := context.Background()
	for _, sub := range handlers {
		func(s *subscription) {
			defer func() {
				if r := recover(); r != nil {
					b.logger.ErrorContext(bg, "event bus subscriber panic",
						"task_id", e.TaskID,
						"event_type", string(e.Type),
						"panic", fmt.Sprintf("%v", r),
					)
				}
			}()
			s.handler(e)
		}(sub)
	}
}
