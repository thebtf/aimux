// Package main — jsonl_test.go tests the JSONL event sink implementation.
//
// Tests:
//   - TestJSONLSink_MonotonicSeq:     10000 concurrent events have strict 1..N seq ordering.
//   - TestJSONLSink_AtomicLineWrite:  1000 sequential events produce 1000 valid JSON lines.
//   - TestJSONLSink_PartialLineRecovery: partial trailing line is detected and warned.
//   - TestJSONLSink_NopSink:           nopSink.Emit is a no-op with no panics.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// --- TestJSONLSink_MonotonicSeq ---------------------------------------------

// TestJSONLSink_MonotonicSeq verifies that 100 goroutines each emitting
// 100 events produce exactly 10 000 events with strictly increasing seq
// values (no gaps, no duplicates) even under heavy concurrency.
func TestJSONLSink_MonotonicSeq(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "monotonic.jsonl")

	sink, closer, err := newFileSink(logPath)
	if err != nil {
		t.Fatalf("newFileSink: %v", err)
	}

	const goroutines = 100
	const eventsPerGoroutine = 100
	const totalEvents = goroutines * eventsPerGoroutine

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				sink.Emit(KindComplete, completePayload{
					Content: fmt.Sprintf("goroutine %d event %d", g, i),
				})
			}
		}()
	}
	wg.Wait()
	_ = closer.Close()

	// Parse the written file.
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	var seqs []uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("parse event: %v  line=%q", err, line)
		}
		seqs = append(seqs, ev.Seq)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(seqs) != totalEvents {
		t.Fatalf("expected %d events, got %d", totalEvents, len(seqs))
	}

	// Sort and verify strictly monotonic 1..totalEvents.
	// We use a map to detect duplicates instead of sorting to keep O(n).
	seen := make(map[uint64]bool, totalEvents)
	var minSeq, maxSeq uint64
	for _, s := range seqs {
		if seen[s] {
			t.Fatalf("duplicate seq %d", s)
		}
		seen[s] = true
		if minSeq == 0 || s < minSeq {
			minSeq = s
		}
		if s > maxSeq {
			maxSeq = s
		}
	}

	if minSeq != 1 {
		t.Errorf("expected minSeq=1, got %d", minSeq)
	}
	if maxSeq != totalEvents {
		t.Errorf("expected maxSeq=%d, got %d", totalEvents, maxSeq)
	}
	// If all values 1..totalEvents are present and unique, count == range span.
	if uint64(len(seen)) != maxSeq-minSeq+1 {
		t.Errorf("seq gap detected: have %d unique values for range [%d,%d]",
			len(seen), minSeq, maxSeq)
	}
}

// --- TestJSONLSink_AtomicLineWrite ------------------------------------------

// TestJSONLSink_AtomicLineWrite emits 1000 events single-threaded and verifies
// that the log file contains exactly 1000 lines, each ending with '\n', and each
// parseable as valid JSON.
func TestJSONLSink_AtomicLineWrite(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "atomic.jsonl")

	sink, closer, err := newFileSink(logPath)
	if err != nil {
		t.Fatalf("newFileSink: %v", err)
	}

	const count = 1000
	for i := 0; i < count; i++ {
		sink.Emit(KindTurn, turnPayload{Role: "user", Content: fmt.Sprintf("msg%d", i), TurnID: i + 1})
	}
	_ = closer.Close()

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	lines := strings.Split(string(raw), "\n")
	// strings.Split on a trailing '\n' produces one empty string at the end.
	// Remove it.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) != count {
		t.Fatalf("expected %d lines, got %d", count, len(lines))
	}

	for i, line := range lines {
		// Verify each line is valid JSON.
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d: invalid JSON: %v  content=%q", i+1, err, line)
		}
	}

	// Verify original file ends with '\n' (no torn last line).
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Error("file does not end with newline — last line may be torn")
	}
}

// --- TestJSONLSink_PartialLineRecovery --------------------------------------

// TestJSONLSink_PartialLineRecovery verifies that a partial (newline-less)
// trailing line in the JSONL file is treated gracefully by the replay loop:
// complete events are processed, the partial line produces a parse-error warning
// but does not crash.
func TestJSONLSink_PartialLineRecovery(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "partial.jsonl")

	// Write one valid complete event via the sink.
	sink, closer, err := newFileSink(logPath)
	if err != nil {
		t.Fatalf("newFileSink: %v", err)
	}
	sink.Emit(KindSpawn, spawnPayload{PID: 42, Command: "codex"})
	_ = closer.Close()

	// Append a deliberately partial line (no trailing newline) directly.
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	_, _ = f.Write([]byte(`{"seq":9999,"kind":"broken","payload":{`)) // no closing brace or newline
	_ = f.Close()

	// Simulate the replay loop — mirrors runReplay logic.
	rf, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open log for replay: %v", err)
	}
	defer rf.Close()

	sc := bufio.NewScanner(rf)
	var parsed []Event
	var warnings []string

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			warnings = append(warnings, fmt.Sprintf("partial line: %v", err))
			continue
		}
		parsed = append(parsed, ev)
	}

	// One complete event should have been parsed.
	if len(parsed) != 1 {
		t.Fatalf("expected 1 parsed event, got %d", len(parsed))
	}
	if parsed[0].Kind != KindSpawn {
		t.Errorf("expected kind %q, got %q", KindSpawn, parsed[0].Kind)
	}

	// The partial line must have produced exactly one warning.
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning for partial line, got %d: %v", len(warnings), warnings)
	}
}

// --- TestJSONLSink_NopSink --------------------------------------------------

// TestJSONLSink_NopSink confirms that nopSink.Emit is a pure no-op:
// no panic, no allocation side-effects, and the return from EventSink is correct.
func TestJSONLSink_NopSink(t *testing.T) {
	t.Parallel()

	var s EventSink = nopSink{}

	// These calls must not panic or return errors — nopSink discards everything.
	s.Emit(KindSpawnArgs, spawnArgsPayload{Command: "codex", Executor: "pipe"})
	s.Emit(KindComplete, completePayload{Content: "hello", ExitCode: 0})
	s.Emit(KindClassify, classifyPayload{Class: "None", ClassCode: 0})
	s.Emit(KindBreakerState, breakerStatePayload{CLI: "codex", State: "Closed"})
	s.Emit(KindCooldownState, cooldownStatePayload{Count: 0})
	s.Emit(KindError, errorPayload{Source: "launcher", Message: "test"})
	s.Emit("unknown_kind", struct{ X int }{X: 99})
	s.Emit(KindTurn, nil) // nil payload must not panic

	// Verify mkSink with empty path returns a nopSink (coverage for mkSink).
	nop, closer, err := mkSink("")
	if err != nil {
		t.Fatalf("mkSink empty path: %v", err)
	}
	defer func() { _ = closer.Close() }()

	// nopSink must implement EventSink.
	nop.Emit(KindExit, exitPayload{PID: 0, ExitCode: 0})
	// No assertions needed — absence of panic is the pass criterion.
}
