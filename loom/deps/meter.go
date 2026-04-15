package deps

import (
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// Meter exposes the OTel metric methods that LoomEngine emits through.
// It is a narrowed subset of otelmetric.Meter to avoid coupling to the full
// OTel SDK interface (which embeds embedded.Meter and may grow in minor releases).
// A real otelmetric.Meter satisfies this interface because the method signatures
// are identical.
type Meter interface {
	Float64Histogram(name string, opts ...otelmetric.Float64HistogramOption) (otelmetric.Float64Histogram, error)
	Int64Counter(name string, opts ...otelmetric.Int64CounterOption) (otelmetric.Int64Counter, error)
	Int64UpDownCounter(name string, opts ...otelmetric.Int64UpDownCounterOption) (otelmetric.Int64UpDownCounter, error)
}

// noopMeter wraps noop.Meter to satisfy deps.Meter without exposing the full
// OTel interface. All instrument factory calls return noop instruments.
type noopMeter struct {
	m noop.Meter
}

// NoopMeter returns a Meter where every instrument factory returns a noop
// instrument and nil error. This is the default when no meter is injected.
func NoopMeter() Meter {
	return &noopMeter{m: noop.Meter{}}
}

func (n *noopMeter) Float64Histogram(name string, opts ...otelmetric.Float64HistogramOption) (otelmetric.Float64Histogram, error) {
	return n.m.Float64Histogram(name, opts...)
}

func (n *noopMeter) Int64Counter(name string, opts ...otelmetric.Int64CounterOption) (otelmetric.Int64Counter, error) {
	return n.m.Int64Counter(name, opts...)
}

func (n *noopMeter) Int64UpDownCounter(name string, opts ...otelmetric.Int64UpDownCounterOption) (otelmetric.Int64UpDownCounter, error) {
	return n.m.Int64UpDownCounter(name, opts...)
}
