// Package logger: tests for IPCSink ring buffer + degraded fallback (T018).
package logger

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIPCSink_RingBufferOverflow(t *testing.T) {
	var fbBuf bytes.Buffer
	fb := newStderrFallbackWith(&fbBuf)

	// Sender that blocks forever to keep the ring full.
	blockCh := make(chan struct{})
	defer close(blockCh)
	sender := func(notification []byte) error {
		<-blockCh
		return nil
	}

	sink := NewIPCSink(sender, IPCSinkOpts{
		BufferSize:         3,
		TimeoutMs:          10,
		ReconnectInitialMs: 10000,
		ReconnectMaxMs:     10000,
	}, fb)
	defer sink.Close()

	// Wait until the drain goroutine pulls the first entry.
	for i := 0; i < 5; i++ {
		sink.Send(LogEntry{Level: LevelInfo, Time: time.Now(), Message: "msg-" + string(rune('0'+i))})
	}
	// Give drain goroutine a tick to pull the first entry into its sender.
	time.Sleep(50 * time.Millisecond)

	// Now buffer holds (BufferSize) entries + 1 in flight; further sends should drop.
	for i := 5; i < 20; i++ {
		sink.Send(LogEntry{Level: LevelInfo, Time: time.Now(), Message: "msg-" + string(rune('0'+i))})
	}
	dropped, _, _ := sink.Stats()
	if dropped == 0 {
		t.Fatal("expected dropped > 0 from ring buffer overflow")
	}
}

func TestIPCSink_DegradedFallback(t *testing.T) {
	var fbBuf bytes.Buffer
	fb := newStderrFallbackWith(&fbBuf)

	var failCount atomic.Uint64
	sender := func(notification []byte) error {
		failCount.Add(1)
		return errors.New("simulated failure")
	}

	sink := NewIPCSink(sender, IPCSinkOpts{
		BufferSize:         10,
		TimeoutMs:          50,
		ReconnectInitialMs: 1, // tiny backoff so we don't waste test time
		ReconnectMaxMs:     2,
	}, fb)
	defer sink.Close()

	for i := 0; i < 5; i++ {
		sink.Send(LogEntry{Level: LevelInfo, Time: time.Now(), Message: "fail-test"})
	}

	// Wait until the drain goroutine has tried at least 3 sends and routed to fallback.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, fbUsed := sink.Stats(); fbUsed >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	_, sendFails, fbUsed := sink.Stats()
	if sendFails == 0 {
		t.Errorf("expected sendFailures > 0, got %d", sendFails)
	}
	if fbUsed == 0 {
		t.Errorf("expected fallbackUsed > 0, got %d", fbUsed)
	}
	if state := sink.State(); state != ipcStateDegraded {
		t.Errorf("expected state=degraded(%d), got %d", ipcStateDegraded, state)
	}
	if !strings.Contains(fbBuf.String(), "fail-test") {
		t.Errorf("expected fallback output to contain 'fail-test', got:\n%s", fbBuf.String())
	}
}

func TestIPCSink_RecoversAfterTransientFailure(t *testing.T) {
	var fbBuf bytes.Buffer
	fb := newStderrFallbackWith(&fbBuf)

	var (
		mu        sync.Mutex
		failNext  bool
		callCount int
		received  [][]byte
	)
	sender := func(notification []byte) error {
		mu.Lock()
		callCount++
		fail := failNext
		failNext = false
		if !fail {
			received = append(received, append([]byte{}, notification...))
		}
		mu.Unlock()
		if fail {
			return errors.New("transient")
		}
		return nil
	}

	sink := NewIPCSink(sender, IPCSinkOpts{
		BufferSize:         10,
		TimeoutMs:          50,
		ReconnectInitialMs: 1,
		ReconnectMaxMs:     2,
	}, fb)
	defer sink.Close()

	mu.Lock()
	failNext = true
	mu.Unlock()
	sink.Send(LogEntry{Level: LevelInfo, Time: time.Now(), Message: "first"})

	// Wait for the failed send to be processed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		c := callCount
		mu.Unlock()
		if c > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Subsequent send should succeed and drive state back to ready.
	sink.Send(LogEntry{Level: LevelInfo, Time: time.Now(), Message: "second"})

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sink.State() == ipcStateReady {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if sink.State() != ipcStateReady {
		t.Errorf("expected state=ready after recovery, got %d", sink.State())
	}

	mu.Lock()
	defer mu.Unlock()
	foundSecond := false
	for _, n := range received {
		if strings.Contains(string(n), "second") {
			foundSecond = true
			break
		}
	}
	if !foundSecond {
		t.Errorf("expected 'second' message to be sent successfully; received: %d entries", len(received))
	}
}

func TestStderrFallback_Format(t *testing.T) {
	var buf bytes.Buffer
	fb := newStderrFallbackWith(&buf)

	when := time.Date(2026, 4, 28, 12, 30, 45, 123_456_789, time.UTC)
	fb.Write(LevelInfo, when, "hello world")

	got := buf.String()
	want := "[stderr-fallback] 2026-04-28T12:30:45.123456789Z [INFO] hello world\n"
	if got != want {
		t.Errorf("format mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestStderrFallback_WriteEntry(t *testing.T) {
	var buf bytes.Buffer
	fb := newStderrFallbackWith(&buf)

	entry := LogEntry{
		Level:   LevelError,
		Time:    time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC),
		Message: "oops\nsecond line",
	}
	fb.WriteEntry(entry)

	got := buf.String()
	if !strings.Contains(got, "[ERROR]") {
		t.Errorf("expected [ERROR] tag, got: %q", got)
	}
	if !strings.Contains(got, "oops") {
		t.Errorf("expected message body, got: %q", got)
	}
}
