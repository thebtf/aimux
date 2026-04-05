// Package logger provides an async file logger with channel-based writes.
// Inspired by ccg-workflow's async logger pattern (ADR-014 Decision 16).
package logger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
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

type entry struct {
	level   Level
	message string
	time    time.Time
}

// Logger is an async file logger that writes via a buffered channel.
type Logger struct {
	level   Level
	ch      chan entry
	file    *os.File
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.RWMutex // protects level changes
}

// New creates a logger writing to the specified file path.
// Channel buffer size controls backpressure (default 1024).
func New(path string, level Level) (*Logger, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log directory %s: %w", dir, err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file %s: %w", path, err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	l := &Logger{
		level:  level,
		ch:     make(chan entry, 1024),
		file:   f,
		ctx:    ctx,
		cancel: cancel,
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

// Close flushes pending entries and closes the file.
func (l *Logger) Close() error {
	l.cancel()
	l.wg.Wait()
	return l.file.Close()
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
	_, _ = l.file.WriteString(line)
}
