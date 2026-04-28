// Package logger provides an async file logger with channel-based writes
// and lumberjack-based log rotation.
// Inspired by ccg-workflow's async logger pattern (ADR-014 Decision 16).
package logger

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	lumberjack "gopkg.in/natefinch/lumberjack.v2"
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

type entry struct {
	level   Level
	message string
	time    time.Time
}

// Logger is an async file logger that writes via a buffered channel.
type Logger struct {
	level        Level
	ch           chan entry
	writer       io.Writer  // destination: lumberjack.Logger or io.Discard
	closer       io.Closer  // non-nil when writer owns a file (lumberjack); nil for Discard
	maxLineBytes int
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.RWMutex // protects level changes
	writeMu      sync.Mutex   // serializes writes from drain goroutine and sync callers
}

// isNullDevice returns true if path refers to the OS null device.
// Accepts /dev/null (Unix convention) and NUL (Windows) cross-platform.
func isNullDevice(path string) bool {
	if path == "/dev/null" {
		return true
	}
	if runtime.GOOS == "windows" && (path == "NUL" || path == "nul") {
		return true
	}
	return false
}

// New creates a logger writing to the specified file path with optional rotation.
// If path is empty or refers to the null device (/dev/null, NUL), log output
// is discarded. This allows cross-platform test configs that use /dev/null.
// Channel buffer size controls backpressure (default 1024).
//
// Pass RotationOpts{} (zero value) to match the old no-rotation behavior for tests.
func New(path string, level Level, opts RotationOpts) (*Logger, error) {
	ctx, cancel := context.WithCancel(context.Background())

	l := &Logger{
		level:        level,
		ch:           make(chan entry, 1024),
		ctx:          ctx,
		cancel:       cancel,
		maxLineBytes: opts.MaxLineBytes,
	}

	if path == "" || isNullDevice(path) {
		// Discard all log output — used in tests and when no log file is configured.
		l.writer = io.Discard
	} else {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			cancel()
			return nil, fmt.Errorf("create log directory %s: %w", dir, err)
		}

		lj := &lumberjack.Logger{
			Filename:   path,
			MaxSize:    opts.MaxSizeMB,
			MaxBackups: opts.MaxBackups,
			MaxAge:     opts.MaxAgeDays,
			Compress:   opts.Compress,
			LocalTime:  true,
		}
		l.writer = lj
		l.closer = lj
	}

	l.wg.Add(1)
	go l.drain()

	return l, nil
}

// SetLevel changes the log level at runtime.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	l.level = level
	l.mu.Unlock()
}

// Level returns the current log level.
func (l *Logger) GetLevel() Level {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.level
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

// Close flushes pending entries and closes the underlying writer.
func (l *Logger) Close() error {
	l.cancel()
	l.wg.Wait()
	if l.closer != nil {
		return l.closer.Close()
	}
	return nil
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
	w.l.writeEntry(entry{
		level:   LevelInfo,
		message: msg,
		time:    time.Now(),
	})
	return len(p), nil
}

func (l *Logger) log(level Level, format string, args ...any) {
	l.mu.RLock()
	currentLevel := l.level
	l.mu.RUnlock()

	if level < currentLevel {
		return
	}

	e := entry{
		level:   level,
		message: fmt.Sprintf(format, args...),
		time:    time.Now(),
	}

	select {
	case l.ch <- e:
	default:
		// Channel full — drop message to avoid blocking.
		// This should be rare with buffer size 1024.
		_, _ = fmt.Fprintf(os.Stderr, "aimux: log channel full, dropping: %s\n", e.message)
	}
}

// drain reads entries from the channel and writes to file.
func (l *Logger) drain() {
	defer l.wg.Done()

	for {
		select {
		case e := <-l.ch:
			l.writeEntry(e)
		case <-l.ctx.Done():
			// Flush remaining entries
			for {
				select {
				case e := <-l.ch:
					l.writeEntry(e)
				default:
					return
				}
			}
		}
	}
}

func (l *Logger) writeEntry(e entry) {
	line := fmt.Sprintf("%s [%s] %s\n",
		e.time.Format("2006-01-02T15:04:05.000Z07:00"),
		e.level.String(),
		e.message,
	)

	if l.maxLineBytes > 0 && len(line) > l.maxLineBytes {
		orig := len(line)
		// Truncate to leave room for the marker; marker is at most ~30 bytes.
		keep := l.maxLineBytes - 30
		if keep < 1 {
			keep = 1
		}
		line = line[:keep] + fmt.Sprintf("...[truncated %d bytes]\n", orig)
	}

	l.writeMu.Lock()
	_, _ = fmt.Fprint(l.writer, line)
	l.writeMu.Unlock()
}
