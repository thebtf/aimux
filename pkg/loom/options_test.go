package loom

import (
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/loom/deps"
)

// options_test.go uses the internal package to inspect unexported struct fields
// directly, confirming each Option mutates the correct field.

// TestWithLogger_MutatesField verifies that WithLogger sets the logger field.
func TestWithLogger_MutatesField(t *testing.T) {
	store := newTestStore(t)
	custom := deps.NoopLogger()
	engine := New(store, WithLogger(custom))
	if engine.logger != custom {
		t.Error("WithLogger did not set engine.logger to the provided value")
	}
}

// TestWithClock_MutatesField verifies that WithClock sets the clock field.
func TestWithClock_MutatesField(t *testing.T) {
	store := newTestStore(t)
	frozen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fake := deps.NewFakeClock(frozen)
	engine := New(store, WithClock(fake))
	if engine.clock != fake {
		t.Error("WithClock did not set engine.clock to the provided value")
	}
}

// TestWithIDGenerator_MutatesField verifies that WithIDGenerator sets the idGen field.
func TestWithIDGenerator_MutatesField(t *testing.T) {
	store := newTestStore(t)
	seq := deps.NewSequentialIDGenerator()
	engine := New(store, WithIDGenerator(seq))
	if engine.idGen != seq {
		t.Error("WithIDGenerator did not set engine.idGen to the provided value")
	}
}

// TestWithMeter_MutatesField verifies that WithMeter sets the meter field.
func TestWithMeter_MutatesField(t *testing.T) {
	store := newTestStore(t)
	noop := deps.NoopMeter()
	engine := New(store, WithMeter(noop))
	if engine.meter != noop {
		t.Error("WithMeter did not set engine.meter to the provided value")
	}
}

// TestDefaultDeps_PopulatedOnNew verifies that when no dep options are passed,
// New initialises all dep fields to non-nil defaults.
func TestDefaultDeps_PopulatedOnNew(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	if engine.logger == nil {
		t.Error("engine.logger is nil; expected NoopLogger default")
	}
	if engine.clock == nil {
		t.Error("engine.clock is nil; expected SystemClock default")
	}
	if engine.idGen == nil {
		t.Error("engine.idGen is nil; expected UUIDGenerator default")
	}
	if engine.meter == nil {
		t.Error("engine.meter is nil; expected NoopMeter default")
	}
}
