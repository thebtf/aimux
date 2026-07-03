package logger

import (
	"bytes"
	"sync/atomic"
	"testing"
	"time"
)

// TestFR9_IPCSink_NonDefaultBufferSize exercises IPCSink with non-default buffer sizes.
// This is FR-9 from AIMUX-11: table-driven test for non-default BufferSize/Timeout.
func TestFR9_IPCSink_NonDefaultBufferSize(t *testing.T) {
	tests := []struct {
		name       string
		bufferSize int
		sendCount  int
	}{
		{"tiny_buffer_1", 1, 5},
		{"small_buffer_3", 3, 3},
		{"medium_buffer_50", 50, 10},
		{"large_buffer_1000", 1000, 100},
		{"overflow_buffer_2", 2, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var delivered atomic.Int64
			fb := newStderrFallbackWith(&bytes.Buffer{})
			sink := NewIPCSink(nil, IPCSinkOpts{
				BufferSize:         tt.bufferSize,
				TimeoutMs:          50,
				ReconnectInitialMs: 10000,
				ReconnectMaxMs:     10000,
			}, fb)

			// Set a send function that counts deliveries.
			sink.SetSendFunc(func(notification []byte) error {
				delivered.Add(1)
				return nil
			})

			// Send entries.
			for i := 0; i < tt.sendCount; i++ {
				sink.Send(LogEntry{
					Level:   LevelInfo,
					Time:    time.Now().UTC(),
					Message: "fr9 test",
				})
			}

			// Verify the ring buffer was created with correct capacity.
			bufCap := cap(sink.ringBuf)
			if bufCap != tt.bufferSize {
				t.Errorf("ringBuf capacity = %d, want %d", bufCap, tt.bufferSize)
			}

			sink.Close()
		})
	}
}

// TestFR9_IPCSink_NonDefaultTimeout exercises IPCSink with non-default timeout values.
func TestFR9_IPCSink_NonDefaultTimeout(t *testing.T) {
	tests := []struct {
		name      string
		timeoutMs int
	}{
		{"timeout_10ms", 10},
		{"timeout_50ms", 50},
		{"timeout_200ms", 200},
		{"timeout_500ms", 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var delivered atomic.Int64
			fb := newStderrFallbackWith(&bytes.Buffer{})
			sink := NewIPCSink(nil, IPCSinkOpts{
				BufferSize:         10,
				TimeoutMs:          tt.timeoutMs,
				ReconnectInitialMs: 10000,
				ReconnectMaxMs:     10000,
			}, fb)

			sink.SetSendFunc(func(notification []byte) error {
				delivered.Add(1)
				return nil
			})

			// The stored opts should reflect the configured timeout.
			if sink.opts.TimeoutMs != tt.timeoutMs {
				t.Errorf("opts.TimeoutMs = %d, want %d", sink.opts.TimeoutMs, tt.timeoutMs)
			}

			// Send one entry and verify it's delivered.
			sink.Send(LogEntry{
				Level:   LevelInfo,
				Time:    time.Now().UTC(),
				Message: "fr9 timeout test",
			})

			// Allow drain loop to dequeue and deliver the entry.
			time.Sleep(50 * time.Millisecond)
			sink.Close()
			if delivered.Load() == 0 {
				t.Error("expected at least one entry delivered")
			}
		})
	}
}

// TestFR9_Config_DefaultsApplied verifies that zero-value config fields get
// correct defaults (BufferSize=100, TimeoutMs=100).
func TestFR9_Config_DefaultsApplied(t *testing.T) {
	fb := newStderrFallbackWith(&bytes.Buffer{})

	// Pass zero-value opts — constructor should fill defaults.
	sink := NewIPCSink(nil, IPCSinkOpts{}, fb)
	defer sink.Close()

	if sink.opts.BufferSize != 100 {
		t.Errorf("default BufferSize = %d, want 100", sink.opts.BufferSize)
	}
	if sink.opts.TimeoutMs != 100 {
		t.Errorf("default TimeoutMs = %d, want 100", sink.opts.TimeoutMs)
	}
	if sink.opts.ReconnectInitialMs != 1000 {
		t.Errorf("default ReconnectInitialMs = %d, want 1000", sink.opts.ReconnectInitialMs)
	}
	if sink.opts.ReconnectMaxMs != 5000 {
		t.Errorf("default ReconnectMaxMs = %d, want 5000", sink.opts.ReconnectMaxMs)
	}
}
