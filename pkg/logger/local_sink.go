// Package logger: LocalSink holds the channel-fed drain goroutine and lumberjack writer.
// Extracted from logger.go (T001 — AIMUX-11 Phase 1).
package logger

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

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

// LocalSink holds the central queue (chan entry) and a single drain goroutine that
// writes entries to a lumberjack.Logger (or io.Discard). It is the sole owner of
// the lumberjack file handle — the sole-writer invariant (FR-2, ADR-6).
//
// Usage:
//   - Daemon mode: LocalSink is created via newLocalSink with a lumberjack.Logger.
//   - Tests: pass path="" to get an io.Discard sink.
type LocalSink struct {
	ch      chan entry  // buffered: 1024 in production, small in tests (see newLocalSink)
	writer  io.Writer  // lumberjack.Logger or io.Discard
	closer  io.Closer  // non-nil when writer owns a file
	writeMu sync.Mutex // serializes concurrent goroutine writes within the daemon
	wg      sync.WaitGroup

	maxLineBytes int
	roleTag      string
	daemonRunID  string

	ctx    context.Context
	cancel context.CancelFunc
}

// newLocalSink creates a LocalSink backed by the given writer (typically lumberjack or
// io.Discard). channelBuf controls the entry channel capacity (use 1024 for production).
func newLocalSink(writer io.Writer, closer io.Closer, channelBuf int, maxLineBytes int, roleTag, daemonRunID string) *LocalSink {
	ctx, cancel := context.WithCancel(context.Background())
	s := &LocalSink{
		ch:           make(chan entry, channelBuf),
		writer:       writer,
		closer:       closer,
		maxLineBytes: maxLineBytes,
		roleTag:      roleTag,
		daemonRunID:  daemonRunID,
		ctx:          ctx,
		cancel:       cancel,
	}
	s.wg.Add(1)
	go s.drain()
	return s
}

// newLumberjackSink is a convenience constructor used by NewDaemon. It creates the
// lumberjack.Logger, the directory if needed, and wraps it in a LocalSink.
func newLumberjackSink(path string, opts RotationOpts, roleTag, daemonRunID string) (*LocalSink, error) {
	if path == "" || isNullDevice(path) {
		return newLocalSink(io.Discard, nil, 1024, opts.MaxLineBytes, roleTag, daemonRunID), nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	return newLocalSink(lj, lj, 1024, opts.MaxLineBytes, roleTag, daemonRunID), nil
}

// send enqueues an entry to the drain channel. Non-blocking: drops on full.
func (s *LocalSink) send(e entry) {
	select {
	case s.ch <- e:
	default:
		_, _ = fmt.Fprintf(os.Stderr, "aimux: log channel full, dropping: %s\n", e.message)
	}
}

// drain reads entries from the channel and writes formatted lines to the writer.
func (s *LocalSink) drain() {
	defer s.wg.Done()

	for {
		select {
		case e := <-s.ch:
			s.writeEntry(e)
		case <-s.ctx.Done():
			// Flush remaining entries before returning.
			for {
				select {
				case e := <-s.ch:
					s.writeEntry(e)
				default:
					return
				}
			}
		}
	}
}

// writeEntry formats and writes a single log entry.
// Format: TIMESTAMP [LEVEL] [roleTag-pid-sess] message\n
func (s *LocalSink) writeEntry(e entry) {
	pid := os.Getpid()
	tag := s.roleTag
	if tag == "" {
		tag = "daemon"
	}
	sessTag := s.daemonRunID
	if sessTag == "" {
		sessTag = "dmn-0000"
	}

	line := fmt.Sprintf("%s [%s] [%s-%d-%s] %s\n",
		e.time.Format("2006-01-02T15:04:05.000Z07:00"),
		e.level.String(),
		tag,
		pid,
		sessTag,
		e.message,
	)

	if s.maxLineBytes > 0 && len(line) > s.maxLineBytes {
		orig := len(line)
		keep := s.maxLineBytes - 30
		if keep < 1 {
			keep = 1
		}
		line = line[:keep] + fmt.Sprintf("...[truncated %d bytes]\n", orig)
	}

	s.writeMu.Lock()
	_, _ = fmt.Fprint(s.writer, line)
	s.writeMu.Unlock()
}

// WriteEntryWithRole writes a LogEntry with explicitly provided role, pid, session tag,
// and a pre-sanitized message. Used by LogIngester to write forwarded shim entries with
// verified peer identity (FR-12). The sanitizedMessage replaces the envelope message.
func (s *LocalSink) WriteEntryWithRole(e LogEntry, sanitizedMessage, role string, pid int, sess string) {
	s.WriteEntryWithRoleStr(e, sanitizedMessage, role, fmt.Sprintf("%d", pid), sess)
}

// WriteEntryWithRoleStr is like WriteEntryWithRole but accepts pidStr as a string.
// Used when the pid field is a fallback marker (e.g. "?abc12345") rather than a
// numeric OS peer credential (FR-12: PeerCredsUnavailable path).
func (s *LocalSink) WriteEntryWithRoleStr(e LogEntry, sanitizedMessage, role, pidStr, sess string) {
	line := fmt.Sprintf("%s [%s] [%s-%s-%s] %s\n",
		e.Time.Format("2006-01-02T15:04:05.000Z07:00"),
		e.Level.String(),
		role,
		pidStr,
		sess,
		sanitizedMessage,
	)

	if s.maxLineBytes > 0 && len(line) > s.maxLineBytes {
		orig := len(line)
		keep := s.maxLineBytes - 30
		if keep < 1 {
			keep = 1
		}
		line = line[:keep] + fmt.Sprintf("...[truncated %d bytes]\n", orig)
	}

	s.writeMu.Lock()
	_, _ = fmt.Fprint(s.writer, line)
	s.writeMu.Unlock()
}

// close stops the drain goroutine and waits for it to finish. Closes the underlying
// writer if it is an io.Closer.
func (s *LocalSink) close() error {
	s.cancel()
	s.wg.Wait()
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}
