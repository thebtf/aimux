package loom

import "github.com/thebtf/aimux/pkg/loom/deps"

// WithLogger injects a custom Logger into the LoomEngine.
// If not supplied, NoopLogger is used and all log output is discarded.
// A nil argument is ignored so the safe default is never overwritten.
func WithLogger(l deps.Logger) Option {
	return func(e *LoomEngine) {
		if l != nil {
			e.logger = l
		}
	}
}

// WithClock injects a custom Clock into the LoomEngine.
// If not supplied, SystemClock (time.Now) is used.
// A nil argument is ignored so the safe default is never overwritten.
func WithClock(c deps.Clock) Option {
	return func(e *LoomEngine) {
		if c != nil {
			e.clock = c
		}
	}
}

// WithIDGenerator injects a custom IDGenerator into the LoomEngine.
// If not supplied, UUIDGenerator (uuid.NewV7) is used.
// A nil argument is ignored so the safe default is never overwritten.
func WithIDGenerator(g deps.IDGenerator) Option {
	return func(e *LoomEngine) {
		if g != nil {
			e.idGen = g
		}
	}
}

// WithMeter injects a custom Meter into the LoomEngine for OTel instrumentation.
// If not supplied, NoopMeter is used and all metrics are discarded.
// A nil argument is ignored so the safe default is never overwritten.
func WithMeter(m deps.Meter) Option {
	return func(e *LoomEngine) {
		if m != nil {
			e.meter = m
		}
	}
}
