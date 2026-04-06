package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"time"
)

// --- JSONL Output ---

// emitJSONL writes a JSON object as a single line to the writer.
func emitJSONL(w io.Writer, obj any) {
	data, _ := json.Marshal(obj)
	fmt.Fprintf(w, "%s\n", data)
}

// emitBufferedJSON writes a single pretty-printed JSON object to the writer.
// Used for gemini --output-format json (buffered mode).
func emitBufferedJSON(w io.Writer, obj any) {
	data, _ := json.MarshalIndent(obj, "", "  ")
	fmt.Fprintf(w, "%s\n", data)
}

// --- Stderr Output ---

// toStderr writes a formatted message to stderr (diagnostics, not data).
func toStderr(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// --- Stdin Handling ---

// readStdinToEOF reads all of stdin until EOF. Returns the content.
// Blocks until EOF is received (matching codex/claude behavior).
func readStdinToEOF() string {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return ""
	}
	return string(data)
}

// isStdinTerminal checks if stdin is a terminal (TTY).
func isStdinTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// isStdoutTerminal checks if stdout is a terminal (TTY).
func isStdoutTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// --- Timing ---

// simulateLatency adds a small random delay to simulate API latency.
// min and max are in milliseconds.
func simulateLatency(minMs, maxMs int) {
	if minMs <= 0 || maxMs <= minMs {
		return
	}
	delay := minMs + rand.IntN(maxMs-minMs)
	time.Sleep(time.Duration(delay) * time.Millisecond)
}

// --- UUID ---

// pseudoUUID generates a pseudo-random UUID-like string for test purposes.
func pseudoUUID() string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		rand.Uint32(),
		rand.Uint32()&0xffff,
		(rand.Uint32()&0x0fff)|0x4000,
		(rand.Uint32()&0x3fff)|0x8000,
		rand.Uint64()&0xffffffffffff,
	)
}

// --- Timestamp ---

// isoTimestamp returns the current time in ISO 8601 format.
func isoTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
