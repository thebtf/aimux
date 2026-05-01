// Package main — jsonl.go implements the JSONL event sink for the launcher debug tool.
//
// The EventSink interface abstracts NDJSON output so that all higher-level
// components (debug_executor, raw_spawn, repl) emit structured events without
// knowing whether output goes to a file or is silently discarded.
//
// Event layout:
//
//	{"seq":N,"ts":"...","kind":"<kind>","payload":{...}}
//
// Every Emit call increments the monotonic sequence counter atomically, so
// concurrent goroutines always produce strictly ordered seq values even when
// timestamps collide on fast hardware.
package main

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

// ---- Event kinds -------------------------------------------------------

// Kind constants for the "kind" field in each JSONL event.
const (
	KindSpawnArgs     = "spawn_args"
	KindSpawn         = "spawn"
	KindStdout        = "stdout"
	KindStderr        = "stderr"
	KindChunk         = "chunk"
	KindExit          = "exit"
	KindComplete      = "complete"
	KindClassify      = "classify"
	KindBreakerState  = "breaker_state"
	KindCooldownState = "cooldown_state"
	KindTurn          = "turn"
	KindError         = "error"
	// KindHTTPRequest and KindHTTPResponse are defined but not currently emitted.
	// Payload structs and Path B rationale are in backend.go (T014).
	KindHTTPRequest  = "http_request"
	KindHTTPResponse = "http_response"
)

// ---- Event envelope ----------------------------------------------------

// Event is one line in the JSONL replay log.
type Event struct {
	// Seq is a strictly monotonic counter that orders events within a session.
	// Monotonicity is guaranteed even when Ts has sub-nanosecond collisions.
	Seq uint64 `json:"seq"`

	// Ts is the wall-clock time of the Emit call, RFC3339Nano.
	Ts time.Time `json:"ts"`

	// Kind identifies the event type; see Kind* constants.
	Kind string `json:"kind"`

	// Payload is the kind-specific data encoded as a JSON object.
	Payload json.RawMessage `json:"payload"`
}

// ---- EventSink interface -----------------------------------------------

// EventSink receives structured debug events from the launcher components.
// Implementations are either the file-backed jsonlSink or the silent nopSink.
type EventSink interface {
	// Emit encodes payload as JSON and writes one NDJSON line.
	// Implementations MUST be safe for concurrent use.
	Emit(kind string, payload any)
}

// ---- jsonlSink (file-backed) -------------------------------------------

// jsonlSink writes one NDJSON event per Emit call to an underlying writer.
// Each write is protected by a mutex so goroutines cannot interleave lines.
// An atomic counter provides seq values that are unique and monotonically
// increasing independent of wall-clock precision.
type jsonlSink struct {
	mu  sync.Mutex
	w   io.Writer
	seq atomic.Uint64
}

// newFileSink opens or creates the file at path for append-only writing and
// returns a jsonlSink that writes to it.  The caller is responsible for
// closing the returned closer (typically via defer closer.Close()).
func newFileSink(path string) (*jsonlSink, io.Closer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	return &jsonlSink{w: f}, f, nil
}

// Emit serialises kind+payload into one JSON line and appends it to the
// underlying writer.  seq is incremented atomically before encoding so the
// value is unique even under concurrent calls.  fsync is called after every
// successful write per spec edge-case "log corruption on SIGKILL".
func (s *jsonlSink) Emit(kind string, payload any) {
	seq := s.seq.Add(1) // atomic: 1-based, monotonic

	raw, err := json.Marshal(payload)
	if err != nil {
		raw = json.RawMessage(`{"error":"payload marshal failed"}`)
	}

	ev := Event{
		Seq:     seq,
		Ts:      time.Now(),
		Kind:    kind,
		Payload: raw,
	}

	line, err := json.Marshal(ev)
	if err != nil {
		return // cannot emit a broken envelope — drop silently
	}
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	_, _ = s.w.Write(line)

	// fsync after every write so the log survives SIGKILL.
	// Only meaningful for *os.File; other writers (e.g. bytes.Buffer in tests)
	// simply ignore the type assertion and skip the sync.
	if f, ok := s.w.(*os.File); ok {
		_ = f.Sync()
	}
}

// ---- nopSink (discard) -------------------------------------------------

// nopSink is returned when no --log flag is given.  All Emit calls are
// no-ops; there is no allocation and no locking overhead.
type nopSink struct{}

// Emit discards the event.
func (nopSink) Emit(_ string, _ any) {}

// ---- Sink constructor --------------------------------------------------

// nopCloser is a no-op io.Closer returned when no log file is opened.
type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// mkSink returns a file-backed sink and its closer when logPath is non-empty,
// or a nopSink (with a no-op closer) otherwise.
func mkSink(logPath string) (EventSink, io.Closer, error) {
	if logPath == "" {
		return nopSink{}, nopCloser{}, nil
	}
	sink, closer, err := newFileSink(logPath)
	if err != nil {
		return nil, nil, err
	}
	return sink, closer, nil
}

// ---- Typed payload structs (T004) --------------------------------------

// spawnArgsPayload records the resolved SpawnArgs before a CLI dispatch.
// Emitted as kind="spawn_args" immediately before the inner executor Send call.
type spawnArgsPayload struct {
	// Command is the fully resolved binary path (e.g., /usr/bin/codex).
	Command string `json:"command"`
	// Args are the command-line arguments passed to the binary.
	Args []string `json:"args,omitempty"`
	// CWD is the working directory for the spawned process.
	CWD string `json:"cwd,omitempty"`
	// Model is the resolved model name, if known.
	Model string `json:"model,omitempty"`
	// Executor identifies the backend (pipe/conpty/pty) being used.
	Executor string `json:"executor"`
}

// completePayload records the full Response returned by the inner executor.
// Emitted as kind="complete" after Send/SendStream returns.
type completePayload struct {
	// Content is the response text from the executor.
	Content string `json:"content"`
	// ExitCode is the CLI exit code (0 for API executors).
	ExitCode int `json:"exit_code"`
	// TokensUsed captures input/output token counts for API executors.
	TokensUsed types.TokenCount `json:"tokens_used"`
	// DurationMs is the round-trip time in milliseconds.
	DurationMs int64 `json:"duration_ms"`
	// Error contains the error string when Send returned a non-nil error.
	Error string `json:"error,omitempty"`
}

// classifyPayload records the ErrorClass determined by ClassifyError.
// Emitted as kind="classify" after each Send/SendStream completes.
type classifyPayload struct {
	// Class is the string name of the ErrorClass (None/Quota/ModelUnavailable/Transient/Fatal/Unknown).
	Class string `json:"class"`
	// ClassCode is the integer value of the ErrorClass for programmatic filtering.
	ClassCode int `json:"class_code"`
}

// breakerStatePayload records a snapshot of the CircuitBreaker state.
// Emitted as kind="breaker_state" when a BreakerRegistry is available.
type breakerStatePayload struct {
	// CLI is the name of the CLI whose breaker is reported.
	CLI string `json:"cli"`
	// State is the string name of the breaker state (Closed/Open/HalfOpen).
	State string `json:"state"`
	// Failures is the current consecutive failure count.
	Failures int `json:"failures"`
}

// cooldownStatePayload records all active cooldown entries at a point in time.
// Emitted as kind="cooldown_state" when a ModelCooldownTracker is available.
type cooldownStatePayload struct {
	// Entries is the list of currently active (non-expired) cooldown entries.
	Entries []types.CooldownEntry `json:"entries"`
	// Count is the number of active entries (convenience field for log scanning).
	Count int `json:"count"`
}

// errorPayload records a launcher-level or signal-triggered error.
// Emitted as kind="error" on Ctrl+C, SIGTERM, or internal launcher failures.
type errorPayload struct {
	// Source identifies who emitted the error ("launcher", "executor", etc.).
	Source string `json:"source"`
	// Message is the human-readable error description.
	Message string `json:"message,omitempty"`
	// Signal contains the signal name when the error was caused by a signal (e.g., "interrupt").
	Signal string `json:"signal,omitempty"`
}

// turnPayload records one turn in an interactive REPL session.
// Emitted as kind="turn" for both user input and agent responses.
type turnPayload struct {
	// Role is "user" or "agent".
	Role string `json:"role"`
	// Content is the text of the turn.
	Content string `json:"content"`
	// TurnID is the 1-based monotonic turn counter within the session.
	TurnID int `json:"turn_id"`
}

// chunkPayload records one streaming chunk from SendStream.
// Emitted as kind="chunk" for each incremental fragment.
type chunkPayload struct {
	// Content is the text fragment delivered by this chunk.
	Content string `json:"content"`
	// Done is true for the final chunk (Content may be empty).
	Done bool `json:"done"`
	// Stream discriminates the source: "api_delta", "api_complete", or "cli_line".
	Stream string `json:"stream"`
}

// stdoutPayload records one line of subprocess stdout output.
// Emitted as kind="stdout" by the L2 raw spawn path.
type stdoutPayload struct {
	// Stream is "raw" for pre-StripANSI bytes (bytes_hex set) or "line" for
	// the ANSI-stripped UTF-8 text (content set).
	Stream string `json:"stream"`
	// Content is the ANSI-stripped line text (stream="line").
	Content string `json:"content,omitempty"`
	// BytesHex is the hex-encoded raw bytes (stream="raw").
	BytesHex string `json:"bytes_hex,omitempty"`
}

// stderrPayload records one line of subprocess stderr output.
// Emitted as kind="stderr" by the L2 raw spawn path.
type stderrPayload struct {
	// Stream is "raw" for hex-encoded bytes or "line" for ANSI-stripped text.
	Stream string `json:"stream"`
	// Content is the ANSI-stripped line text (stream="line").
	Content string `json:"content,omitempty"`
	// BytesHex is the hex-encoded raw bytes (stream="raw").
	BytesHex string `json:"bytes_hex,omitempty"`
}

// spawnPayload records the OS-level process spawn event.
// Emitted as kind="spawn" when the subprocess is created.
type spawnPayload struct {
	// PID is the OS process identifier of the spawned subprocess.
	PID int `json:"pid"`
	// Command is the resolved binary path that was executed.
	Command string `json:"command"`
}

// exitPayload records the subprocess exit event.
// Emitted as kind="exit" when the subprocess terminates.
type exitPayload struct {
	// PID is the OS process identifier that exited.
	PID int `json:"pid"`
	// ExitCode is the process exit status.
	ExitCode int `json:"exit_code"`
}

