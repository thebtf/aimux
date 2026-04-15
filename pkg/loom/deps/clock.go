package deps

import (
	"sync"
	"time"
)

// Clock abstracts wall-clock time so callers can inject a fake
// implementation in tests to produce deterministic timestamps and
// controllable sleeps.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

// systemClock is the production Clock backed by time.Now.
type systemClock struct{}

// SystemClock returns a Clock that delegates to time.Now.
func SystemClock() Clock { return systemClock{} }

func (systemClock) Now() time.Time             { return time.Now() }
func (systemClock) Sleep(d time.Duration) { time.Sleep(d) }

// FakeClock is a deterministic Clock for use in tests.
// Its frozen time is advanced explicitly via Advance.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock returns a FakeClock frozen at t.
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{now: t} }

// Now returns the frozen time. Concurrent calls are safe.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the frozen time forward by d. Concurrent calls are safe.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// Sleep advances the frozen time by d, satisfying the Clock interface.
// Unlike real sleep it returns immediately — callers get deterministic time control.
func (f *FakeClock) Sleep(d time.Duration) { f.Advance(d) }
