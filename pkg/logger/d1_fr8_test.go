package logger

import (
	"bytes"
	"sync/atomic"
	"testing"
	"time"
)

// TestFR8_IPCSink_HandoffContinuity verifies that log entries are not lost
// during a hot-swap handoff — when SetSendFunc replaces the transport callback.
// FR-8 from AIMUX-11: hot-swap log handoff continuity test.
//
// Scenario: entries sent before, during, and after SetSendFunc replacement
// must all be delivered or fallback-routed — zero silent drops.
func TestFR8_IPCSink_HandoffContinuity(t *testing.T) {
	var oldDelivered, newDelivered atomic.Int64

	fb := newStderrFallbackWith(&bytes.Buffer{})
	sink := NewIPCSink(nil, IPCSinkOpts{
		BufferSize:         50,
		TimeoutMs:          50,
		ReconnectInitialMs: 10000,
		ReconnectMaxMs:     10000,
	}, fb)

	// Phase 1: old send function
	sink.SetSendFunc(func(notification []byte) error {
		oldDelivered.Add(1)
		return nil
	})

	for i := 0; i < 10; i++ {
		sink.Send(LogEntry{Level: LevelInfo, Time: time.Now().UTC(), Message: "phase1"})
	}
	time.Sleep(50 * time.Millisecond) // let drain loop deliver

	// Phase 2: hot-swap — replace send function
	sink.SetSendFunc(func(notification []byte) error {
		newDelivered.Add(1)
		return nil
	})

	for i := 0; i < 10; i++ {
		sink.Send(LogEntry{Level: LevelInfo, Time: time.Now().UTC(), Message: "phase2"})
	}
	time.Sleep(50 * time.Millisecond) // let drain loop deliver

	sink.Close()

	// Verify: all 20 entries were processed (delivered via old or new or fallback).
	_, _, fallbackCount := sink.Stats()
	total := oldDelivered.Load() + newDelivered.Load() + int64(fallbackCount)
	if total < 20 {
		t.Errorf("total processed = %d; want ≥20 (old=%d, new=%d, fallback=%d)",
			total, oldDelivered.Load(), newDelivered.Load(), fallbackCount)
	}

	// Phase 2 entries should go through new sender.
	if newDelivered.Load() == 0 {
		t.Error("expected new sender to deliver at least some entries after handoff")
	}
}

// TestFR8_IPCSink_HandoffToNil verifies that replacing send with nil routes
// all subsequent entries to fallback (simulating a disconnected daemon).
func TestFR8_IPCSink_HandoffToNil(t *testing.T) {
	var delivered atomic.Int64
	var fbBuf bytes.Buffer
	fb := newStderrFallbackWith(&fbBuf)

	sink := NewIPCSink(nil, IPCSinkOpts{
		BufferSize:         20,
		TimeoutMs:          50,
		ReconnectInitialMs: 10000,
		ReconnectMaxMs:     10000,
	}, fb)

	// Set a working sender first.
	sink.SetSendFunc(func(notification []byte) error {
		delivered.Add(1)
		return nil
	})

	sink.Send(LogEntry{Level: LevelInfo, Time: time.Now().UTC(), Message: "connected"})
	time.Sleep(30 * time.Millisecond)

	// Simulate disconnect — set send to nil.
	sink.SetSendFunc(nil)

	for i := 0; i < 5; i++ {
		sink.Send(LogEntry{Level: LevelInfo, Time: time.Now().UTC(), Message: "disconnected"})
	}
	time.Sleep(30 * time.Millisecond)

	sink.Close()

	// Disconnected entries should route to fallback.
	_, _, fallbackCount := sink.Stats()
	if fallbackCount == 0 {
		t.Error("expected fallback entries after setting send to nil")
	}
}
