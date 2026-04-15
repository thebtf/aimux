package loom

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom/deps"
)

// TestEventBus_OrderedDelivery verifies that 4 events emitted in order are received
// in the same order by the subscriber, with correct field values.
func TestEventBus_OrderedDelivery(t *testing.T) {
	bus := NewEventBus(deps.NoopLogger())

	var received []TaskEvent
	var mu sync.Mutex

	unsub := bus.Subscribe(func(e TaskEvent) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})
	defer unsub()

	events := []TaskEvent{
		{Type: EventTaskCreated, TaskID: "t1", ProjectID: "proj-A", RequestID: "req-1", Status: TaskStatusPending, Timestamp: time.Now()},
		{Type: EventTaskDispatched, TaskID: "t1", ProjectID: "proj-A", RequestID: "req-1", Status: TaskStatusDispatched, Timestamp: time.Now()},
		{Type: EventTaskRunning, TaskID: "t1", ProjectID: "proj-A", RequestID: "req-1", Status: TaskStatusRunning, Timestamp: time.Now()},
		{Type: EventTaskCompleted, TaskID: "t1", ProjectID: "proj-A", RequestID: "req-1", Status: TaskStatusCompleted, Timestamp: time.Now()},
	}

	for _, e := range events {
		bus.Emit(e)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 4 {
		t.Fatalf("expected 4 events, got %d", len(received))
	}

	expectedTypes := []EventType{EventTaskCreated, EventTaskDispatched, EventTaskRunning, EventTaskCompleted}
	for i, e := range received {
		if e.Type != expectedTypes[i] {
			t.Errorf("event[%d]: type = %q, want %q", i, e.Type, expectedTypes[i])
		}
		if e.TaskID != "t1" {
			t.Errorf("event[%d]: task_id = %q, want \"t1\"", i, e.TaskID)
		}
		if e.ProjectID != "proj-A" {
			t.Errorf("event[%d]: project_id = %q, want \"proj-A\"", i, e.ProjectID)
		}
		if e.RequestID != "req-1" {
			t.Errorf("event[%d]: request_id = %q, want \"req-1\"", i, e.RequestID)
		}
	}
}

// TestEventBus_Unsubscribe verifies that after calling the unsubscribe function,
// the handler no longer receives events.
func TestEventBus_Unsubscribe(t *testing.T) {
	bus := NewEventBus(deps.NoopLogger())

	var count atomic.Int32
	unsub := bus.Subscribe(func(_ TaskEvent) {
		count.Add(1)
	})

	// Emit before unsubscribe — should be received.
	bus.Emit(TaskEvent{Type: EventTaskCreated, TaskID: "t1"})
	if count.Load() != 1 {
		t.Fatalf("expected 1 event before unsubscribe, got %d", count.Load())
	}

	// Unsubscribe.
	unsub()

	// Emit after unsubscribe — should NOT be received.
	bus.Emit(TaskEvent{Type: EventTaskCompleted, TaskID: "t1"})
	if count.Load() != 1 {
		t.Errorf("expected count to remain 1 after unsubscribe, got %d", count.Load())
	}

	// Calling unsubscribe again is idempotent — must not panic.
	unsub()
	unsub()
}

// TestEventBus_PanicRecovery verifies that a panicking subscriber does NOT prevent
// subsequent subscribers from receiving the same event.
func TestEventBus_PanicRecovery(t *testing.T) {
	bus := NewEventBus(deps.NoopLogger())

	panicCalls := atomic.Int32{}
	safeCalls := atomic.Int32{}

	// First subscriber panics on every call.
	bus.Subscribe(func(e TaskEvent) {
		panicCalls.Add(1)
		panic("intentional test panic")
	})

	// Second subscriber must still receive the event.
	bus.Subscribe(func(e TaskEvent) {
		safeCalls.Add(1)
	})

	bus.Emit(TaskEvent{Type: EventTaskCreated, TaskID: "t2"})

	if panicCalls.Load() != 1 {
		t.Errorf("panic subscriber called %d times, want 1", panicCalls.Load())
	}
	if safeCalls.Load() != 1 {
		t.Errorf("safe subscriber called %d times, want 1 (panic isolation failed)", safeCalls.Load())
	}
}

// TestEventBus_ConcurrentSubscribeEmit verifies that concurrent Subscribe and Emit
// calls do not produce a data race. This test is effective under -race.
func TestEventBus_ConcurrentSubscribeEmit(t *testing.T) {
	bus := NewEventBus(deps.NoopLogger())

	const workers = 10
	var wg sync.WaitGroup

	// Start emitters in goroutines.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Emit(TaskEvent{Type: EventTaskCreated, TaskID: "concurrent-t"})
		}()
	}

	// Start subscribers concurrently.
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unsub := bus.Subscribe(func(_ TaskEvent) {})
			// Immediately unsubscribe to exercise concurrent removal path.
			unsub()
		}()
	}

	wg.Wait()
}

// TestEventBus_UnsubscribeIdempotent verifies calling unsubscribe multiple times
// does not panic or cause incorrect behaviour.
func TestEventBus_UnsubscribeIdempotent(t *testing.T) {
	bus := NewEventBus(deps.NoopLogger())

	var count atomic.Int32
	unsub := bus.Subscribe(func(_ TaskEvent) {
		count.Add(1)
	})

	bus.Emit(TaskEvent{Type: EventTaskCreated, TaskID: "t3"})

	// Call unsubscribe many times.
	for i := 0; i < 5; i++ {
		unsub()
	}

	bus.Emit(TaskEvent{Type: EventTaskCompleted, TaskID: "t3"})

	if count.Load() != 1 {
		t.Errorf("count = %d, want 1 (second event should be dropped)", count.Load())
	}
}

// TestEventBus_MultipleSubscribers verifies that multiple independent subscribers
// all receive the same event.
func TestEventBus_MultipleSubscribers(t *testing.T) {
	bus := NewEventBus(deps.NoopLogger())

	const n = 5
	counts := make([]atomic.Int32, n)

	unsubs := make([]func(), n)
	for i := 0; i < n; i++ {
		idx := i
		unsubs[i] = bus.Subscribe(func(_ TaskEvent) {
			counts[idx].Add(1)
		})
	}
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()

	bus.Emit(TaskEvent{Type: EventTaskRunning, TaskID: "t4"})

	for i := 0; i < n; i++ {
		if counts[i].Load() != 1 {
			t.Errorf("subscriber[%d]: count = %d, want 1", i, counts[i].Load())
		}
	}
}
