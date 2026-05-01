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

// spawnArgsPayload records the resolved SpawnArgs before a CLI dispatch (kind="spawn_args").
type spawnArgsPayload struct {
	Command  string   `json:"command"`           // fully resolved binary path
	Args     []string `json:"args,omitempty"`    // command-line arguments
	CWD      string   `json:"cwd,omitempty"`     // working directory for the spawned process
	Model    string   `json:"model,omitempty"`   // resolved model name, if known
	Executor string   `json:"executor"`          // backend identifier (pipe/conpty/pty)
}

// completePayload records the full Response returned by the inner executor (kind="complete").
type completePayload struct {
	Content    string           `json:"content"`
	ExitCode   int              `json:"exit_code"`
	TokensUsed types.TokenCount `json:"tokens_used"`
	DurationMs int64            `json:"duration_ms"`
	Error      string           `json:"error,omitempty"` // set when Send returned a non-nil error
}

// classifyPayload records the ErrorClass determined by ClassifyError (kind="classify").
type classifyPayload struct {
	Class     string `json:"class"`      // None/Quota/ModelUnavailable/Transient/Fatal/Unknown
	ClassCode int    `json:"class_code"` // integer value for programmatic filtering
}

// breakerStatePayload records a CircuitBreaker snapshot (kind="breaker_state").
type breakerStatePayload struct {
	CLI      string `json:"cli"`      // name of the CLI whose breaker is reported
	State    string `json:"state"`    // Closed/Open/HalfOpen
	Failures int    `json:"failures"` // current consecutive failure count
}

// cooldownStatePayload records all active cooldown entries (kind="cooldown_state").
type cooldownStatePayload struct {
	Entries []types.CooldownEntry `json:"entries"` // currently active (non-expired) entries
	Count   int                   `json:"count"`   // convenience field for log scanning
}

// errorPayload records a launcher-level or signal-triggered error (kind="error").
type errorPayload struct {
	Source  string `json:"source"`           // "launcher", "executor", etc.
	Message string `json:"message,omitempty"`
	Signal  string `json:"signal,omitempty"` // signal name when error was caused by a signal
}

// turnPayload records one REPL session turn (kind="turn").
type turnPayload struct {
	Role    string `json:"role"`    // "user" or "agent"
	Content string `json:"content"`
	TurnID  int    `json:"turn_id"` // 1-based monotonic counter within the session
}

// chunkPayload records one streaming chunk from SendStream (kind="chunk").
type chunkPayload struct {
	Content string `json:"content"`
	Done    bool   `json:"done"`   // true for the final chunk
	Stream  string `json:"stream"` // "api_delta", "api_complete", or "cli_line"
}

// stdoutPayload records one line of subprocess stdout (kind="stdout").
// stream="raw" sets bytes_hex; stream="line" sets content (ANSI-stripped).
type stdoutPayload struct {
	Stream   string `json:"stream"`
	Content  string `json:"content,omitempty"`
	BytesHex string `json:"bytes_hex,omitempty"`
}

// stderrPayload records one line of subprocess stderr (kind="stderr").
// stream="raw" sets bytes_hex; stream="line" sets content (ANSI-stripped).
type stderrPayload struct {
	Stream   string `json:"stream"`
	Content  string `json:"content,omitempty"`
	BytesHex string `json:"bytes_hex,omitempty"`
}

// spawnPayload records the OS-level process spawn event (kind="spawn").
type spawnPayload struct {
	PID     int    `json:"pid"`     // OS process identifier of the spawned subprocess
	Command string `json:"command"` // resolved binary path that was executed
}

// exitPayload records the subprocess exit event (kind="exit").
type exitPayload struct {
	PID      int `json:"pid"`       // OS process identifier that exited
	ExitCode int `json:"exit_code"` // process exit status
}

// KindHeartbeat is emitted by the --diag realtime path when the process
// produces no output for a configured idle interval (default 5 s).
const KindHeartbeat = "heartbeat"

// heartbeatPayload records a single idle-heartbeat event in diag mode (kind="heartbeat").
type heartbeatPayload struct {
	IdleSeconds  float64 `json:"idle_seconds"`          // seconds since last output line
	TotalElapsed float64 `json:"total_elapsed_seconds"` // seconds since Run call started
}
