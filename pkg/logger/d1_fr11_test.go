package logger

import (
	"bytes"
	"sync/atomic"
	"testing"
	"time"
)

// TestFR11_IPCSink_DrainWithin500ms verifies that Close() drains all pending
// entries within 500ms (FR-11: SIGTERM drain test on Windows).
// This simulates the graceful shutdown path where context cancellation triggers
// Close(), and all buffered log entries must be flushed before exit.
func TestFR11_IPCSink_DrainWithin500ms(t *testing.T) {
	const entryCount = 50
	var delivered atomic.Int64

	fb := newStderrFallbackWith(&bytes.Buffer{})
	sink := NewIPCSink(nil, IPCSinkOpts{
		BufferSize:         100,
		TimeoutMs:          50,
		ReconnectInitialMs: 10000,
		ReconnectMaxMs:     10000,
	}, fb)

	// Set a send function that simulates fast delivery (1ms per entry).
	sink.SetSendFunc(func(notification []byte) error {
		time.Sleep(1 * time.Millisecond)
		delivered.Add(1)
		return nil
	})

	// Fill the buffer with entries.
	for i := 0; i < entryCount; i++ {
		sink.Send(LogEntry{
			Level:   LevelInfo,
			Time:    time.Now().UTC(),
			Message: "fr11 drain test",
		})
	}

	// Allow some entries to drain before Close.
	time.Sleep(10 * time.Millisecond)

	// Measure Close() duration — must complete within 500ms.
	start := time.Now()
	sink.Close()
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("Close() took %v; want ≤500ms", elapsed)
	}

	// After Close, all entries should have been either delivered or fallback-routed.
	_, _, fallbackCount := sink.Stats()
	total := delivered.Load() + int64(fallbackCount)
	if total < entryCount {
		t.Errorf("total processed = %d; want ≥%d (delivered=%d, fallback=%d)",
			total, entryCount, delivered.Load(), fallbackCount)
	}
}

// TestFR11_IPCSink_DrainOnSlowSender verifies that Close() still completes
// within 500ms even when the sender is slow — entries route to fallback.
func TestFR11_IPCSink_DrainOnSlowSender(t *testing.T) {
	const entryCount = 20

	fb := newStderrFallbackWith(&bytes.Buffer{})
	sink := NewIPCSink(nil, IPCSinkOpts{
		BufferSize:         30,
		TimeoutMs:          10, // short timeout — entries will route to fallback quickly
		ReconnectInitialMs: 10000,
		ReconnectMaxMs:     10000,
	}, fb)

	sink.SetSendFunc(func(notification []byte) error {
		// Simulate slow sender (100ms per entry).
		time.Sleep(100 * time.Millisecond)
		return nil
	})

	for i := 0; i < entryCount; i++ {
		sink.Send(LogEntry{
			Level:   LevelInfo,
			Time:    time.Now().UTC(),
			Message: "fr11 slow sender test",
		})
	}

	start := time.Now()
	sink.Close()
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("Close() with slow sender took %v; want ≤500ms", elapsed)
	}
}

// TestFR11_IPCSink_EmptyBufferDrain verifies Close() on an empty buffer
// returns near-instantly (no entries to drain).
func TestFR11_IPCSink_EmptyBufferDrain(t *testing.T) {
	fb := newStderrFallbackWith(&bytes.Buffer{})
	sink := NewIPCSink(nil, IPCSinkOpts{
		BufferSize:         10,
		TimeoutMs:          50,
		ReconnectInitialMs: 10000,
		ReconnectMaxMs:     10000,
	}, fb)

	start := time.Now()
	sink.Close()
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("Close() on empty buffer took %v; want ≤50ms", elapsed)
	}
}
