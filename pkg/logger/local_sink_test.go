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

// TestSanitizeTag — CR-002 T007 S3 HIGH-1 gate.
//
// Validates sanitizeTag rejects characters that could break the [role-pid-sess]
// envelope or spoof a daemon-tagged log line via a crafted CLAUDE_SESSION_ID
// (or any other metadata source). Closes the cross-tenant role spoofing vector
// reported in PRC 2026-04-28 security review.
func TestSanitizeTag(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "anon"},
		{"normal_ascii", "abc12345", "abc12345"},
		{"close_bracket", "]fake", "?fake"},
		{"open_bracket", "[fake", "?fake"},
		{"newline", "abc\nDEF", "abc?DEF"},
		{"carriage_return", "abc\rDEF", "abc?DEF"},
		{"tab", "abc\tDEF", "abc?DEF"},
		{"control_char_low", "abc\x01DEF", "abc?DEF"},
		{"injection_attempt", "] [INFO] [daemon-0-fake", "? ?INFO? ?daemon-0-fake"},
		{"mixed_normal", "shim-12345-abc", "shim-12345-abc"},
		{"unicode_ok", "ёлка12", "ёлка12"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeTag(tc.in)
			if got != tc.want {
				t.Fatalf("sanitizeTag(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestWriteEntryWithRoleStr_AppliesSanitizeTag — integration check that the
// role/pid/sess metadata in the formatted log line passes through sanitizeTag.
func TestWriteEntryWithRoleStr_AppliesSanitizeTag(t *testing.T) {
	var buf bytes.Buffer
	sink := newLocalSink(&buf, nil, 4, 4096, "daemon", "test")
	defer func() { _ = sink.close() }()

	hostile := "] [INFO] [daemon-fake"
	entry := LogEntry{Level: LevelInfo, Time: time.Now(), Message: "payload"}
	sink.WriteEntryWithRoleStr(entry, "payload", "shim", "?abc12345", hostile)

	got := buf.String()
	if got == "" {
		t.Fatal("no output written")
	}
	// Hostile input must not appear verbatim — `]` and `[` must be replaced.
	if bytes.Contains(buf.Bytes(), []byte("] [INFO] [daemon-fake")) {
		t.Fatalf("unsanitised injection survived: %q", got)
	}
	// Sanitised form should contain `?` replacements.
	if !bytes.Contains(buf.Bytes(), []byte("? ?INFO? ?daemon-fake")) {
		t.Fatalf("expected sanitised form '? ?INFO? ?daemon-fake' in output, got %q", got)
	}
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

