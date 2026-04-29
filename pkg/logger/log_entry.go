// Package logger: LogEntry is the wire format for log entries forwarded from
// shim processes to the daemon via JSON-RPC notification (T002 — AIMUX-11 Phase 1).
package logger

import (
	"encoding/json"
	"fmt"
	"time"
)

// LogEntry is the JSON-serialisable log entry exchanged over the IPC transport.
// Shim processes marshal LogEntry into the params of the
// "notifications/aimux/log_forward" JSON-RPC notification; the daemon unmarshals
// it in LogIngester.Receive.
//
// Note: pid and role are NOT included in the envelope — they are derived from OS
// peer credentials on the daemon side for security (FR-12, ADR-4).
type LogEntry struct {
	Level   Level     `json:"level"`
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// logEntryJSON is the wire representation used for JSON marshal/unmarshal.
// Level is serialised as its string name; Time is RFC3339Nano.
type logEntryJSON struct {
	Level   string `json:"level"`
	Time    string `json:"time"`
	Message string `json:"message"`
}

// MarshalJSON implements json.Marshaler for LogEntry.
// Level is written as its string name ("DEBUG", "INFO", "WARN", "ERROR").
// Time is written in RFC3339Nano format.
func (e LogEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(logEntryJSON{
		Level:   e.Level.String(),
		Time:    e.Time.UTC().Format(time.RFC3339Nano),
		Message: e.Message,
	})
}

// UnmarshalJSON implements json.Unmarshaler for LogEntry.
func (e *LogEntry) UnmarshalJSON(data []byte) error {
	var wire logEntryJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("LogEntry: %w", err)
	}

	e.Level = ParseLevel(wire.Level)

	t, err := time.Parse(time.RFC3339Nano, wire.Time)
	if err != nil {
		return fmt.Errorf("LogEntry: time field: %w", err)
	}
	e.Time = t
	e.Message = wire.Message
	return nil
}
