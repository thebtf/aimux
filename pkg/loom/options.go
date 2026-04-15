package loom

import "github.com/thebtf/aimux/pkg/loom/deps"

// WithLogger injects a custom Logger into the LoomEngine.
// If not supplied, NoopLogger is used and all log output is discarded.
func WithLogger(l deps.Logger) Option {
	return func(e *LoomEngine) { e.logger = l }
}

// WithClock injects a custom Clock into the LoomEngine.
// If not supplied, SystemClock (time.Now) is used.
func WithClock(c deps.Clock) Option {
	return func(e *LoomEngine) { e.clock = c }
}

// WithIDGenerator injects a custom IDGenerator into the LoomEngine.
// If not supplied, UUIDGenerator (uuid.NewV7) is used.
func WithIDGenerator(g deps.IDGenerator) Option {
	return func(e *LoomEngine) { e.idGen = g }
}

// WithMeter injects a custom Meter into the LoomEngine for OTel instrumentation.
// If not supplied, NoopMeter is used and all metrics are discarded.
func WithMeter(m deps.Meter) Option {
	return func(e *LoomEngine) { e.meter = m }
}
