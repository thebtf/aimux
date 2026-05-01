//go:build windows

// Package main — raw_spawn_test.go tests the L2 raw subprocess capture path.
//
// Tests:
//   - TestRawSpawn_TeeReaderCapturesBytesPreStrip: subprocess output is captured
//     as both raw (bytes_hex) and stripped (line) events side-by-side.
//   - TestRawSpawn_LineSplit:  a single line of output produces at least one
//     raw event and one line event.
//
// Both tests use cmd.exe (always available on Windows) for a deterministic
// subprocess without any external CLI binary dependency.
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

// echoSpawnArgs returns a SpawnArgs that runs "cmd.exe /c echo <text>".
// cmd.exe is always available on Windows and exits 0 with predictable output.
func echoSpawnArgs(text string) types.SpawnArgs {
	return types.SpawnArgs{
		Command:        "cmd.exe",
		Args:           []string{"/c", "echo", text},
		TimeoutSeconds: 10,
	}
}

// --- TestRawSpawn_TeeReaderCapturesBytesPreStrip ----------------------------

// TestRawSpawn_TeeReaderCapturesBytesPreStrip verifies that runRawCLI inserts
// a TeeReader so that the raw stdout event's bytes_hex contains the bytes of
// the subprocess output.
//
// "echo test" from cmd.exe produces bytes containing 74657374 ("test" in hex).
func TestRawSpawn_TeeReaderCapturesBytesPreStrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	sink := &captureSink{}
	ctx := context.Background()

	exitCode, err := runRawCLI(ctx, ctx, echoSpawnArgs("test"), sink)
	if err != nil {
		t.Fatalf("runRawCLI: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	// Find raw and line stdout events.
	var rawPayload *stdoutPayload
	var linePayload *stdoutPayload

	for i := range sink.events {
		ev := &sink.events[i]
		if ev.Kind != KindStdout {
			continue
		}
		var p stdoutPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("unmarshal stdout payload: %v", err)
		}
		switch p.Stream {
		case "raw":
			cp := p
			rawPayload = &cp
		case "line":
			cp := p
			linePayload = &cp
		}
	}

	if rawPayload == nil {
		t.Fatal("no stdout{stream:raw} event — TeeReader not capturing raw bytes")
	}
	if linePayload == nil {
		t.Fatal("no stdout{stream:line} event emitted")
	}

	// Raw bytes must contain hex encoding of "test" = 74657374.
	if !strings.Contains(rawPayload.BytesHex, "74657374") {
		t.Errorf("bytes_hex %q does not contain hex(\"test\")=74657374", rawPayload.BytesHex)
	}

	// Line event must contain "test" text.
	if !strings.Contains(linePayload.Content, "test") {
		t.Errorf("line content %q does not contain \"test\"", linePayload.Content)
	}
}

// --- TestRawSpawn_LineSplit --------------------------------------------------

// TestRawSpawn_LineSplit verifies that for a single line of stdout output,
// runRawCLI emits both a raw event and a line event — the TeeReader must
// split the stream into two parallel paths.
func TestRawSpawn_LineSplit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	sink := &captureSink{}
	ctx := context.Background()

	exitCode, err := runRawCLI(ctx, ctx, echoSpawnArgs("hello"), sink)
	if err != nil {
		t.Fatalf("runRawCLI: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	rawCount := 0
	lineCount := 0
	for _, ev := range sink.events {
		if ev.Kind != KindStdout {
			continue
		}
		var p stdoutPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("unmarshal stdout: %v", err)
		}
		switch p.Stream {
		case "raw":
			rawCount++
		case "line":
			lineCount++
		}
	}

	if rawCount == 0 {
		t.Error("no raw stdout events — TeeReader not splitting stream")
	}
	if lineCount == 0 {
		t.Error("no line stdout events emitted")
	}
}
