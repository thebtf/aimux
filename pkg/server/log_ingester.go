// Package server: LogIngester receives forwarded log entries from shim processes
// via the "notifications/aimux/log_forward" JSON-RPC notification channel.
// It verifies peer identity, sanitizes messages, and writes to the daemon's LocalSink.
// (T006 — AIMUX-11 Phase 1)
package server

import (
	"fmt"
	"sync/atomic"
	"unicode/utf8"

	"github.com/thebtf/aimux/pkg/logger"
)

// LogIngester receives log-forward envelopes from authenticated shim peers and
// pushes them through the daemon's LocalSink.
//
// Counters (observable via status tool):
//   - PidMismatch: envelope pid did not match OS peer creds (dropped).
//   - PeerCredsUnavailable: OS peer creds query failed (envelope accepted with warning).
//   - EnvelopeMalformed: message exceeded MaxLineBytes or contained invalid data.
//   - DrainSaturated: LocalSink channel was full at time of push (entry dropped by sink).
type LogIngester struct {
	sink         *logger.LocalSink
	maxLineBytes int

	PidMismatch          atomic.Uint64
	PeerCredsUnavailable atomic.Uint64
	EnvelopeMalformed    atomic.Uint64
	DrainSaturated       atomic.Uint64
}

// NewLogIngester creates a LogIngester that writes to the given LocalSink.
// maxLineBytes is the per-line cap from ServerConfig.LogMaxLineBytes; pass 0 to disable.
func NewLogIngester(sink *logger.LocalSink, maxLineBytes int) *LogIngester {
	return &LogIngester{
		sink:         sink,
		maxLineBytes: maxLineBytes,
	}
}

// Receive processes a forwarded log entry from a shim peer.
//
//   - envelope: decoded LogEntry from the JSON-RPC params.
//   - peerPid: process ID from OS peer credentials (0 if unavailable).
//   - sess: session tag derived from CLAUDE_SESSION_ID / project ID / "anon".
//
// The envelope's message is sanitized (FR-13) before writing. The role tag is
// always "shim" — the daemon does not trust the envelope to carry a role claim.
func (i *LogIngester) Receive(envelope logger.LogEntry, peerPid int, sess string) error {
	// Validate message byte length BEFORE sanitization to catch oversized envelopes.
	if i.maxLineBytes > 0 && len(envelope.Message) > i.maxLineBytes {
		i.EnvelopeMalformed.Add(1)
		return fmt.Errorf("log_ingester: message length %d exceeds max %d bytes", len(envelope.Message), i.maxLineBytes)
	}

	sanitized := sanitizeMessage(envelope.Message)

	if sess == "" {
		sess = "anon"
	}

	// Write via WriteEntryWithRole so the daemon-side formatter uses the verified
	// peer identity (role="shim", pid from OS creds) — not envelope claims (FR-12).
	i.sink.WriteEntryWithRole(envelope, sanitized, "shim", peerPid, sess)
	return nil
}

// ReceiveNotification processes a forwarded log entry arriving via the
// HandleNotification path where OS peer credentials are not available.
// pidMarker is a pre-built string tag (e.g. "?abc12345") used as the pid field
// in the output line per FR-12 (PeerCredsUnavailable fallback).
func (i *LogIngester) ReceiveNotification(envelope logger.LogEntry, pidMarker, sess string) error {
	if i.maxLineBytes > 0 && len(envelope.Message) > i.maxLineBytes {
		i.EnvelopeMalformed.Add(1)
		return fmt.Errorf("log_ingester: message length %d exceeds max %d bytes", len(envelope.Message), i.maxLineBytes)
	}

	sanitized := sanitizeMessage(envelope.Message)

	if sess == "" {
		sess = "anon"
	}

	i.sink.WriteEntryWithRoleStr(envelope, sanitized, "shim", pidMarker, sess)
	return nil
}

// sanitizeMessage replaces control characters in the message with safe representations
// per FR-13: \n → \\n, \r → \\r, 0x00-0x08 / 0x0B-0x0C / 0x0E-0x1F → \\xNN.
// Tabs (\t, 0x09) are preserved.
func sanitizeMessage(msg string) string {
	if len(msg) == 0 {
		return msg
	}

	// Fast path: check if any sanitization is needed.
	needsSanitize := false
	for i := 0; i < len(msg); i++ {
		c := msg[i]
		if c < 0x20 && c != '\t' {
			needsSanitize = true
			break
		}
	}
	if !needsSanitize {
		return msg
	}

	out := make([]byte, 0, len(msg)+16)
	for i := 0; i < len(msg); {
		r, size := utf8.DecodeRuneInString(msg[i:])
		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8 byte — emit as \xNN.
			out = append(out, fmt.Sprintf("\\x%02X", msg[i])...)
			i++
			continue
		}

		b := msg[i]
		switch {
		case b == '\n':
			out = append(out, '\\', 'n')
		case b == '\r':
			out = append(out, '\\', 'r')
		case b == '\t':
			out = append(out, '\t')
		case b < 0x20:
			out = append(out, fmt.Sprintf("\\x%02X", b)...)
		default:
			out = append(out, msg[i:i+size]...)
		}
		i += size
	}
	return string(out)
}
