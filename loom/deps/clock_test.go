package deps_test

import (
	"testing"
	"time"

	"github.com/thebtf/aimux/loom/deps"
)

// TestSystemClock_Monotonic verifies that two successive calls to SystemClock.Now
// never go backwards (monotonic property of wall-clock time).
func TestSystemClock_Monotonic(t *testing.T) {
	c := deps.SystemClock()
	t1 := c.Now()
	t2 := c.Now()
	if t2.Before(t1) {
		t.Errorf("SystemClock is not monotonic: %v > %v", t1, t2)
	}
}

// TestFakeClock_Deterministic verifies that FakeClock.Now returns the frozen time
// passed to NewFakeClock unchanged.
func TestFakeClock_Deterministic(t *testing.T) {
	frozen := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	fc := deps.NewFakeClock(frozen)
	if got := fc.Now(); !got.Equal(frozen) {
		t.Errorf("FakeClock.Now = %v; want %v", got, frozen)
	}
}

// TestFakeClock_Advance verifies that Advance mutates the frozen time by the given duration.
func TestFakeClock_Advance(t *testing.T) {
	frozen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fc := deps.NewFakeClock(frozen)
	fc.Advance(24 * time.Hour)
	want := frozen.Add(24 * time.Hour)
	if got := fc.Now(); !got.Equal(want) {
		t.Errorf("after Advance FakeClock.Now = %v; want %v", got, want)
	}
}
