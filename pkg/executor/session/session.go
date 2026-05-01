// Package session provides shared persistent-session machinery for executor implementations.
//
// BaseSession generalises the pipeSession pattern from pkg/executor/pipe:
// a lifetime reader goroutine owns stdout exclusively and forwards chunks via readCh;
// Send() consumes chunks with an inactivity timer to detect response boundaries;
// Close() shuts everything down cleanly and exactly once.
//
// Transport-specific executors (conpty, pty, pipe) create their stdin/stdout pair,
// spawn the child process, and wrap the handles in a BaseSession via New().
package session

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

// readerBufferSize is the per-line scanner buffer cap (1 MB). Default
// bufio.Scanner ceiling is 64 KB; LLM-CLI tools may emit longer JSON blobs
// (reasoning chains, tool-result payloads) which would otherwise terminate
// the session с bufio.ErrTooLong (PR #134 review — gemini medium).
const readerBufferSize = 1 << 20

// readChunk is a single read result from the lifetime reader goroutine.
// When completionMatched is true the chunk's data is the sentinel-matched line
// itself — Send() flushes it into the response buffer BEFORE returning, so the
// caller sees the matched line as the final newline-terminated row.
type readChunk struct {
	data              []byte
	err               error
	completionMatched bool
}

// BaseSession provides common persistent-session machinery:
//   - A lifetime reader goroutine that owns stdout exclusively.
//   - A readCh channel that carries chunks from the reader to Send().
//   - Inactivity-based response boundary detection in Send().
//   - Serialised Send() calls via mu.
//   - Idempotent Close() via closeOnce.
//
// Embedding or wrapping executors supply stdin/stdout via New() and optionally
// a *executor.ProcessHandle for lifecycle tracking.  When handle is nil, Alive()
// always returns false and PID() returns 0.
type BaseSession struct {
	id     string
	stdin  io.WriteCloser
	stdout io.ReadCloser
	handle *executor.ProcessHandle
	pm     *executor.ProcessManager // manager that owns the handle; may be nil

	// inactivityTimeout is the duration of silence that signals end-of-response.
	inactivityTimeout time.Duration

	// completionPattern is the compiled regex for line-anchored sentinel detection
	// (FR-3, Q-CLAR-1). Each newline-terminated line from stdout is matched against
	// this pattern; on match, Send() is signalled complete without waiting for the
	// inactivity timeout. Nil when no pattern was provided (idle-timeout-only fallback).
	completionPattern *regexp.Regexp

	// readCh carries chunks from the lifetime reader to Send().
	// Buffered to absorb bursts without blocking the reader.
	readCh chan readChunk

	// readerDone is closed when the lifetime reader goroutine exits.
	readerDone chan struct{}

	// stopCh is closed by Close() to unblock the reader goroutine when readCh is full.
	stopCh chan struct{}

	// closeOnce ensures cleanup runs exactly once even under concurrent Close calls.
	closeOnce sync.Once

	// mu serialises concurrent Send() calls.
	mu sync.Mutex
}

// New creates a BaseSession wrapping the given stdin/stdout pair and starts the
// lifetime reader goroutine.
//
// Use New for request/response sessions (Send/Stream calls). For interactive
// sessions that need raw Stdout() access use NewInteractiveSession instead —
// New's background goroutine would race with any external reader on the same
// ReadCloser.
//
// Parameters:
//   - id: session identifier; if empty, a random UUID is generated.
//   - stdin: writable end of the process's stdin pipe.
//   - stdout: readable end of the process's stdout pipe.
//   - inactivityTimeout: duration of silence that marks end-of-response in Send().
//   - handle: optional process handle for Alive()/PID()/Kill(); pass nil if not applicable.
//   - pm: optional ProcessManager that owns handle; used for Kill/Cleanup on Close(). Pass nil if none.
//   - completionPattern: optional line-anchored regex for sentinel-based completion detection
//     (FR-3, Q-CLAR-1). The reader goroutine matches each newline-terminated stdout line
//     against this pattern; on match, Send() returns immediately without waiting for the
//     inactivity timeout. Empty string disables sentinel detection — existing idle-timeout
//     behavior is preserved byte-identically. The pattern is compiled once at construction
//     time; invalid patterns are silently ignored (nil regex, idle-timeout fallback).
func New(
	id string,
	stdin io.WriteCloser,
	stdout io.ReadCloser,
	inactivityTimeout time.Duration,
	handle *executor.ProcessHandle,
	pm *executor.ProcessManager,
	completionPattern string,
) *BaseSession {
	if id == "" {
		id = uuid.NewString()
	}

	var compiled *regexp.Regexp
	if completionPattern != "" {
		c, err := regexp.Compile(completionPattern)
		if err != nil {
			// Surface the invalid pattern instead of silently falling back
			// (PR #134 review — gemini medium). Operators relying on sentinel
			// detection see WHY their pattern is not firing instead of guessing.
			log.Printf("session.New: invalid completion pattern %q: %v "+
				"(falling back to idle-timeout-only behavior)", completionPattern, err)
		} else {
			compiled = c
		}
	}

	s := &BaseSession{
		id:                id,
		stdin:             stdin,
		stdout:            stdout,
		handle:            handle,
		pm:                pm,
		inactivityTimeout: inactivityTimeout,
		completionPattern: compiled,
		readCh:            make(chan readChunk, 32),
		readerDone:        make(chan struct{}),
		stopCh:            make(chan struct{}),
	}

	// Start the single lifetime reader goroutine.
	// It owns stdout exclusively and forwards all data to readCh.
	// It reads stdout line-by-line via bufio.Scanner; each complete line is checked
	// against the compiled completionPattern (if set). On pattern match, completeCh
	// is signalled so Send() can return without waiting for the inactivity timeout.
	// The goroutine exits when stdout returns any error (EOF, closed pipe, etc.).
	go func() {
		defer close(s.readerDone)
		scanner := bufio.NewScanner(s.stdout)
		// Raise default 64 KB token cap к 1 MB — LLM-CLI tools may emit long
		// JSON blobs as a single line; 64 KB triggers bufio.ErrTooLong and
		// terminates the session prematurely (PR #134 review — gemini medium).
		scanner.Buffer(make([]byte, 0, 64*1024), readerBufferSize)
		for scanner.Scan() {
			line := scanner.Text()
			// Line-anchored sentinel detection (FR-3, Q-CLAR-1):
			// match the complete line against the compiled pattern BEFORE sending,
			// so completion-matched chunks atomically carry both the data and the
			// completion flag — eliminates the race between data delivery and the
			// completion signal previously dispatched on a separate channel.
			matched := s.completionPattern != nil && s.completionPattern.MatchString(line)
			cp := []byte(line + "\n")
			select {
			case s.readCh <- readChunk{data: cp, completionMatched: matched}:
			case <-s.stopCh:
				return
			}
		}
		// Scanner exited: EOF or read error — signal Send() that stdout is closed.
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case s.readCh <- readChunk{err: err}:
		case <-s.stopCh:
		}
	}()

	return s
}

// NewInteractiveSession creates a BaseSession for bidirectional interactive
// (TUI) use. Unlike New, it does NOT start the lifetime reader goroutine, so
// Stdout() may be read exclusively by the caller (e.g. runInteractiveSession).
//
// Send and Stream MUST NOT be called on a session created with
// NewInteractiveSession — there is no background reader to service readCh.
// Use the raw Stdin()/Stdout() accessors instead.
//
// Parameters are identical to New except completionPattern (not applicable to
// interactive sessions).
func NewInteractiveSession(
	id string,
	stdin io.WriteCloser,
	stdout io.ReadCloser,
	inactivityTimeout time.Duration,
	handle *executor.ProcessHandle,
	pm *executor.ProcessManager,
) *BaseSession {
	if id == "" {
		id = uuid.NewString()
	}
	readerDone := make(chan struct{})
	// No background reader goroutine is started; close readerDone immediately
	// so Close() does not block waiting for a goroutine that never ran.
	close(readerDone)
	return &BaseSession{
		id:                id,
		stdin:             stdin,
		stdout:            stdout,
		handle:            handle,
		pm:                pm,
		inactivityTimeout: inactivityTimeout,
		readCh:            make(chan readChunk, 32),
		readerDone:        readerDone,
		stopCh:            make(chan struct{}),
	}
}

// Compile-time assertion: BaseSession must satisfy types.SessionPipes.
var _ types.SessionPipes = (*BaseSession)(nil)

// ID returns the session identifier.
func (s *BaseSession) ID() string { return s.id }

// Stdin returns a writer for raw input bytes to the underlying process.
//
// WARNING: Using Stdin/Stdout concurrently with Send/Stream causes undefined
// behaviour — the interactive loop becomes the exclusive owner of the I/O
// handles. Do not mix the two access patterns on the same session.
func (s *BaseSession) Stdin() io.Writer { return s.stdin }

// Stdout returns a reader for raw output bytes from the underlying process.
//
// The reader is shared: if the lifetime reader goroutine (started in New) is
// still running it owns the underlying ReadCloser exclusively via bufio.Scanner.
// Callers that need the raw reader MUST bypass Send/Stream entirely and manage
// reads themselves. The interactive loop in tools/launcher/interactive.go is
// the sole intended consumer.
func (s *BaseSession) Stdout() io.Reader { return s.stdout }

// Send writes prompt to stdin and reads the response via the lifetime reader.
//
// Inactivity detection: the timer is reset on each incoming chunk. When no new
// data arrives for inactivityTimeout the response is treated as complete and
// Send returns with Partial=true. The lifetime reader goroutine is NOT stopped —
// it persists for future Send() calls.
func (s *BaseSession) Send(ctx context.Context, prompt string) (*types.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	start := time.Now()

	_, err := fmt.Fprintln(s.stdin, prompt)
	if err != nil {
		return nil, types.NewExecutorError("failed to write to stdin", err, "")
	}

	var buf bytes.Buffer
	timer := time.NewTimer(s.inactivityTimeout)
	defer timer.Stop()

	partial := false
	for {
		select {
		case c := <-s.readCh:
			if len(c.data) > 0 {
				buf.Write(c.data)
				// Reset inactivity timer — more data may still be coming.
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(s.inactivityTimeout)
			}
			if c.completionMatched {
				// Sentinel line matched completionPattern — line already accumulated
				// into buf above. Response is complete; reader stays running.
				goto done
			}
			if c.err != nil {
				// stdout closed (process exited or Close was called) — done.
				goto done
			}

		case <-timer.C:
			// No new data for inactivityTimeout — treat as end of response.
			// The lifetime reader goroutine stays running for subsequent Send()s.
			partial = true
			goto done

		case <-ctx.Done():
			partial = true
			goto done
		}
	}

done:
	return &types.Result{
		Content:    buf.String(),
		Partial:    partial,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// Stream sends a prompt and returns an event channel for streaming output.
// Internally calls Send() and delivers the result as a single content event
// followed by a complete event. The lifetime reader goroutine is shared.
func (s *BaseSession) Stream(ctx context.Context, prompt string) (<-chan types.Event, error) {
	ch := make(chan types.Event, 64)

	go func() {
		defer close(ch)
		result, err := s.Send(ctx, prompt)
		if err != nil {
			ch <- types.Event{Type: types.EventTypeError, Error: err}
			return
		}
		ch <- types.Event{Type: types.EventTypeContent, Content: result.Content}
		ch <- types.Event{Type: types.EventTypeComplete}
	}()

	return ch, nil
}

// Close terminates the session. It closes stdout first (causing the lifetime
// reader goroutine to exit via EOF), waits for the reader to exit, then closes
// stdin and kills/cleans the process handle if one was provided.
//
// Close is idempotent — subsequent calls are no-ops.
func (s *BaseSession) Close() error {
	s.closeOnce.Do(func() {
		// Signal the reader goroutine to stop even if readCh is full.
		close(s.stopCh)
		// Close stdout so the lifetime reader unblocks from Read() and exits.
		_ = s.stdout.Close()
		// Wait for the reader to exit before killing — avoids a race on the handle.
		<-s.readerDone
		_ = s.stdin.Close()
		if s.pm != nil && s.handle != nil {
			s.pm.Kill(s.handle)
			s.pm.Cleanup(s.handle)
		}
	})
	return nil
}

// Alive returns true if the underlying process handle reports the process as
// still running. Returns false when no handle was provided.
func (s *BaseSession) Alive() bool {
	if s.pm == nil || s.handle == nil {
		return false
	}
	return s.pm.IsAlive(s.handle)
}

// PID returns the OS process ID. Returns 0 when no handle was provided.
func (s *BaseSession) PID() int {
	if s.handle != nil {
		return s.handle.PID
	}
	return 0
}
