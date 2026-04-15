package deps_test

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/loom/deps"
)

// TestNoopMeter_DoesNotPanic verifies that every instrument factory on the
// noop Meter can be called without panicking.
func TestNoopMeter_DoesNotPanic(t *testing.T) {
	m := deps.NoopMeter()
	_, err := m.Float64Histogram("test.histogram")
	if err != nil {
		t.Fatalf("Float64Histogram returned error: %v", err)
	}
	_, err = m.Int64Counter("test.counter")
	if err != nil {
		t.Fatalf("Int64Counter returned error: %v", err)
	}
	_, err = m.Int64UpDownCounter("test.updown")
	if err != nil {
		t.Fatalf("Int64UpDownCounter returned error: %v", err)
	}
}

// TestNoopMeter_InstrumentsDoNotPanic verifies that the returned noop
// instruments can be used to record values without panicking.
func TestNoopMeter_InstrumentsDoNotPanic(t *testing.T) {
	m := deps.NoopMeter()
	ctx := context.Background()

	hist, err := m.Float64Histogram("loom.task.duration")
	if err != nil {
		t.Fatalf("Float64Histogram error: %v", err)
	}
	hist.Record(ctx, 1.5)

	counter, err := m.Int64Counter("loom.task.submitted")
	if err != nil {
		t.Fatalf("Int64Counter error: %v", err)
	}
	counter.Add(ctx, 1)

	updown, err := m.Int64UpDownCounter("loom.task.running")
	if err != nil {
		t.Fatalf("Int64UpDownCounter error: %v", err)
	}
	updown.Add(ctx, 1)
	updown.Add(ctx, -1)
}

// TestNoopMeter_SatisfiesInterface verifies that NoopMeter() is assignable
// to the deps.Meter interface at compile time and runtime.
func TestNoopMeter_SatisfiesInterface(t *testing.T) {
	var m deps.Meter = deps.NoopMeter()
	if m == nil {
		t.Fatal("NoopMeter returned nil")
	}
}
