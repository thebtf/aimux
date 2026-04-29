// Package logger provides an async file logger with channel-based writes
// and lumberjack-based log rotation.
// Inspired by ccg-workflow's async logger pattern (ADR-014 Decision 16).
package logger

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Level represents log severity.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String returns the level name.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ParseLevel converts a string to Level.
func ParseLevel(s string) Level {
	switch s {
	case "debug", "DEBUG":
		return LevelDebug
	case "info", "INFO":
		return LevelInfo
	case "warn", "WARN", "warning", "WARNING":
		return LevelWarn
	case "error", "ERROR":
		return LevelError
	default:
		return LevelInfo
	}
}

// RotationOpts configures log file rotation. Zero values disable rotation
// for that dimension (matches lumberjack semantics).
type RotationOpts struct {
	// MaxSizeMB is the maximum size in megabytes before rotation.
	// Zero means lumberjack's default (100 MB); pass -1 is not meaningful —
	// use zero or a positive value. Lumberjack rotates when the file reaches this size.
	MaxSizeMB int
	// MaxBackups is the maximum number of old log files to retain.
	// Zero means retain all old files.
	MaxBackups int
	// MaxAgeDays is the maximum number of days to retain old log files.
	// Zero means no age-based deletion.
	MaxAgeDays int
	// Compress enables gzip compression of rotated backups.
	Compress bool
	// MaxLineBytes caps individual log lines. Zero means no cap.
	// When a formatted line exceeds this limit it is truncated to
	// (MaxLineBytes - 30) bytes and the marker "...[truncated NNN bytes]\n"
	// is appended, keeping the total at or below MaxLineBytes.
	MaxLineBytes int
}

// entry is the internal queue element passed from producers to the drain goroutine.
type entry struct {
	level   Level
	message string
	time    time.Time
}

// Logger is an async file logger that writes via a buffered channel.
// In daemon mode it owns a LocalSink (sole file writer — ADR-6).
// In shim mode it owns an IPCSink that forwards entries to the daemon via IPC,
// plus a StderrFallback for bootstrap and degradation.
type Logger struct {
	level    Level
	sink     *LocalSink      // daemon mode: sole file writer
	ipcSink  *IPCSink        // shim mode: notification forwarder
	fallback *StderrFallback // shim mode: bootstrap + degradation
	mu       sync.RWMutex    // protects level changes
}

// newRunID generates a short random identifier for the daemon run (4 hex chars).
// Used in the log line tag: [daemon-<pid>-dmn-<runID>].
func newRunID() string {
	id := uuid.New()
	// Take the first 4 hex chars of the UUID string (after removing dashes).
	raw := id.String()
	// UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx — first 8 hex chars before first dash.
	if len(raw) >= 8 {
		return raw[:8]
	}
	return "00000000"
}

// NewDaemon creates a daemon-mode Logger that writes directly to a log file via
// lumberjack. This is the ONLY mode that opens the log file — the sole-writer
// invariant (ADR-6). Shims must use NewShim instead.
//
// Pass RotationOpts{} (zero value) for no-rotation behavior (useful in tests).
func NewDaemon(path string, level Level, opts RotationOpts) (*Logger, error) {
	runID := newRunID()
	sink, err := newLumberjackSink(path, opts, "daemon", runID)
	if err != nil {
		return nil, err
	}
	return &Logger{
		level: level,
		sink:  sink,
	}, nil
}

// NewShim creates a shim-mode Logger that forwards entries via IPCSink and
// falls back to StderrFallback on transport failure or pre-IPC bootstrap.
//
// The shim never opens the log file (FR-2 sole-writer invariant). All output
// either reaches the daemon (via IPC notification) or stderr (via fallback).
//
// ipc and fallback must be non-nil. Constructor panics otherwise.
func NewShim(level Level, ipc *IPCSink, fallback *StderrFallback) *Logger {
	if ipc == nil {
		panic("logger.NewShim: ipc must not be nil")
	}
	if fallback == nil {
		panic("logger.NewShim: fallback must not be nil")
	}
	return &Logger{
		level:    level,
		ipcSink:  ipc,
		fallback: fallback,
	}
}

// New creates a logger writing to the specified file path with optional rotation.
// Deprecated: prefer NewDaemon or NewShim. Retained for test and legacy compatibility.
func New(path string, level Level, opts RotationOpts) (*Logger, error) {
	return NewDaemon(path, level, opts)
}

// SetLevel changes the log level at runtime.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	l.level = level
	l.mu.Unlock()
}

// GetLevel returns the current log level.
func (l *Logger) GetLevel() Level {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.level
}

// LocalSink returns the daemon-side LocalSink for LogIngester wiring.
// Returns nil in shim mode (IPCSink is used instead).
func (l *Logger) LocalSink() *LocalSink {
	return l.sink
}

// Debug logs at debug level.
func (l *Logger) Debug(format string, args ...any) {
	l.log(LevelDebug, format, args...)
}

// Info logs at info level.
func (l *Logger) Info(format string, args ...any) {
	l.log(LevelInfo, format, args...)
}

// Warn logs at warn level.
func (l *Logger) Warn(format string, args ...any) {
	l.log(LevelWarn, format, args...)
}

// Error logs at error level.
func (l *Logger) Error(format string, args ...any) {
	l.log(LevelError, format, args...)
}

// Close flushes pending entries and closes the underlying writer/forwarder.
func (l *Logger) Close() error {
	if l.sink != nil {
		return l.sink.close()
	}
	if l.ipcSink != nil {
		return l.ipcSink.Close()
	}
	return nil
}

// DrainWithDeadline stops the drain goroutine, then reads remaining pending channel
// entries until empty or deadline exceeded. Returns (drained, lost) counts.
// Used for graceful shutdown (FR-11, T026, T027).
//
// Calling DrainWithDeadline implicitly closes the sink — subsequent log calls are
// silently discarded. Call only once, during process shutdown.
func (l *Logger) DrainWithDeadline(d time.Duration) (drained, lost int) {
	if l.sink == nil {
		return 0, 0
	}
	// Stop the drain goroutine so we have exclusive access to the channel.
	l.sink.cancel()
	l.sink.wg.Wait()

	deadline := time.Now().Add(d)
	ch := l.sink.ch
	for {
		if time.Now().After(deadline) {
			// Count remaining entries as lost.
			for {
				select {
				case <-ch:
					lost++
				default:
					if lost > 0 {
						_, _ = fmt.Fprintf(os.Stderr, "aimux: DrainWithDeadline: lost %d entries\n", lost)
					}
					return drained, lost
				}
			}
		}
		select {
		case e := <-ch:
			l.sink.writeEntry(e)
			drained++
		default:
			return drained, lost
		}
	}
}

// StdLogger returns a *log.Logger that routes output through this Logger at
// INFO level. Useful for passing to libraries that accept *log.Logger (e.g.
// muxcore engine.Config.Logger). The returned logger has no prefix and no
// flags — timestamps and level tags are added by this Logger's writeEntry.
func (l *Logger) StdLogger() *log.Logger {
	return log.New(&loggerWriter{l: l}, "", 0)
}

// loggerWriter adapts Logger to io.Writer for use with log.New.
// Each Write call is treated as a single INFO log entry.
type loggerWriter struct {
	l *Logger
}

func (w *loggerWriter) Write(p []byte) (int, error) {
	msg := string(p)
	// Trim trailing newline added by log.Logger.Output
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	// Write synchronously, bypassing the async channel. muxcore daemon logs
	// (handoff, snapshot, control) must hit disk immediately — the process may
	// be terminated before the async channel drains.
	if w.l.sink != nil {
		w.l.sink.writeEntry(entry{
			level:   LevelInfo,
			message: msg,
			time:    time.Now(),
		})
	}
	return len(p), nil
}

func (l *Logger) log(level Level, format string, args ...any) {
	l.mu.RLock()
	currentLevel := l.level
	l.mu.RUnlock()

	if level < currentLevel {
		return
	}

	now := time.Now()
	msg := fmt.Sprintf(format, args...)

	// Daemon mode: in-process channel + lumberjack writer.
	if l.sink != nil {
		l.sink.send(entry{
			level:   level,
			message: msg,
			time:    now,
		})
		return
	}

	// Shim mode: forward via IPCSink. IPCSink internally routes to fallback
	// when transport is unavailable (FR-3 + FR-4 + FR-7).
	if l.ipcSink != nil {
		l.ipcSink.Send(LogEntry{
			Level:   level,
			Time:    now,
			Message: msg,
		})
		return
	}
}

// nilWriter is an io.Writer that discards all output.
// Used as the writer for shim-mode Logger until IPCSink is wired in Phase 3.
type nilWriter struct{}

func (nilWriter) Write(p []byte) (int, error) { return len(p), nil }
