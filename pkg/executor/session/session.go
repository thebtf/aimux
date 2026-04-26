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
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

// readChunk is a single read result from the lifetime reader goroutine.
type readChunk struct {
	data []byte
	err  error
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

	// readCh carries chunks from the lifetime reader to Send().
	// Buffered to absorb bursts without blocking the reader.
	readCh chan readChunk

	// readerDone is closed when the lifetime reader goroutine exits.
	readerDone chan struct{}

	// closeOnce ensures cleanup runs exactly once even under concurrent Close calls.
	closeOnce sync.Once

	// mu serialises concurrent Send() calls.
	mu sync.Mutex
}

// New creates a BaseSession wrapping the given stdin/stdout pair and starts the
// lifetime reader goroutine.
//
// Parameters:
//   - id: session identifier; if empty, a random UUID is generated.
//   - stdin: writable end of the process's stdin pipe.
//   - stdout: readable end of the process's stdout pipe.
//   - inactivityTimeout: duration of silence that marks end-of-response in Send().
//   - handle: optional process handle for Alive()/PID()/Kill(); pass nil if not applicable.
//   - pm: optional ProcessManager that owns handle; used for Kill/Cleanup on Close(). Pass nil if none.
func New(
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

	s := &BaseSession{
		id:                id,
		stdin:             stdin,
		stdout:            stdout,
		handle:            handle,
		pm:                pm,
		inactivityTimeout: inactivityTimeout,
		readCh:            make(chan readChunk, 32),
		readerDone:        make(chan struct{}),
	}

	// Start the single lifetime reader goroutine.
	// It owns stdout exclusively and forwards all data to readCh.
	// It exits when stdout returns any error (EOF, closed pipe, etc.).
	go func() {
		defer close(s.readerDone)
		tmp := make([]byte, 4096)
		for {
			n, readErr := s.stdout.Read(tmp)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, tmp[:n])
				s.readCh <- readChunk{data: cp}
			}
			if readErr != nil {
				s.readCh <- readChunk{err: readErr}
				return
			}
		}
	}()

	return s
}

// ID returns the session identifier.
func (s *BaseSession) ID() string { return s.id }

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
		// Close stdout first so the lifetime reader unblocks and exits.
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
