// Package logger: tests for LocalSink — central queue + lumberjack writer.
// Adds CR-002 T004 + T005 gates (FR-6 channelBuf=4096, NFR-8 DrainSaturated wire).
package logger

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

// TestLocalSink_BufferCap_Matches_FR6 — CR-002 T004 P1 spec compliance gate.
//
// Validates that newLumberjackSink (production constructor) uses channelBuf=4096
// per FR-6 hard lower bound. Pre-CR-002 used 1024 — burst-emit under 5-6
// concurrent CC sessions could overflow.
func TestLocalSink_BufferCap_Matches_FR6(t *testing.T) {
	// Use the io.Discard fast-path (path="" routes through that branch).
	sink, err := newLumberjackSink("", RotationOpts{}, "daemon", "test")
	if err != nil {
		t.Fatalf("newLumberjackSink failed: %v", err)
	}
	defer func() { _ = sink.close() }()

	got := cap(sink.ch)
	want := 4096
	if got != want {
		t.Fatalf("channelBuf mismatch: got %d, want %d (FR-6 hard lower bound)", got, want)
	}
}

// TestLocalSink_DrainSaturated_Increments — CR-002 T005 P1 NFR-8 gate.
//
// Validates that DrainSaturated counter increments on send() overflow drop.
// Pre-CR-002 the counter was declared, exposed via sessions(action=health),
// but never incremented in any code path — a dead metric.
func TestLocalSink_DrainSaturated_Increments(t *testing.T) {
	// Build a LocalSink with tiny buffer (cap=2) so we can saturate it without
	// the drain goroutine pulling entries.
	sink := newLocalSink(&blockingWriter{}, nil, 2, 4096, "daemon", "test")
	defer func() { _ = sink.close() }()

	// drain goroutine has already started — pause it by ensuring the writer blocks.
	// The writer is mu-locked synchronously by writeEntry; first entry enters
	// drain goroutine and blocks on the writer. Second/third entries fill the channel.
	sink.send(entry{level: LevelInfo, message: "1", time: time.Now()})
	time.Sleep(20 * time.Millisecond) // drain goroutine pulls #1, blocks on writer
	sink.send(entry{level: LevelInfo, message: "2", time: time.Now()}) // fills slot 1
	sink.send(entry{level: LevelInfo, message: "3", time: time.Now()}) // fills slot 2

	if got := sink.DrainSaturated(); got != 0 {
		t.Fatalf("baseline DrainSaturated should be 0, got %d", got)
	}

	// Burst — channel + writer slot are all occupied; subsequent sends drop.
	for i := 0; i < 10; i++ {
		sink.send(entry{level: LevelInfo, message: "drop", time: time.Now()})
	}

	got := sink.DrainSaturated()
	if got == 0 {
		t.Fatalf("expected DrainSaturated > 0 after burst, got %d", got)
	}
	if got > 10 {
		t.Fatalf("expected DrainSaturated <= 10, got %d", got)
	}
	t.Logf("OK: DrainSaturated=%d after 10-entry burst with full channel", got)
}

// blockingWriter blocks on Write until released. Used to keep drain goroutine
// busy so the channel can be observably saturated.
type blockingWriter struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Simulate slow file write — keeps drain goroutine occupied long enough
	// for follow-up sends to saturate the channel.
	time.Sleep(500 * time.Millisecond)
	return b.Buffer.Write(p)
}

