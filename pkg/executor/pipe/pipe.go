// Package pipe implements the fallback pipe executor.
// Uses stdin/stdout pipes with optional --json flag injection.
// This is the simplest executor — works everywhere but output is buffered.
package pipe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

const (
	// defaultInactivitySeconds is the fallback when SpawnArgs.InactivitySeconds is 0.
	defaultInactivitySeconds = 5
	// stderrCapBytes is the maximum stderr captured per process (64 KiB).
	stderrCapBytes = 64 * 1024
)

// Executor spawns CLI processes via stdin/stdout pipes.
type Executor struct{}

// New creates a pipe executor.
func New() *Executor {
	return &Executor{}
}

// Name returns the executor name.
func (e *Executor) Name() string { return "pipe" }

// Available always returns true — pipe works on all platforms.
func (e *Executor) Available() bool { return true }

// Run executes a single prompt and returns the result.
// Uses ProcessManager for lifecycle and IOManager for streaming I/O.
func (e *Executor) Run(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
	start := time.Now()

	cmd := exec.Command(args.Command, args.Args...)
	cmd.Dir = args.CWD
	switch {
	case len(args.EnvList) > 0:
		cmd.Env = args.EnvList
	case len(args.Env) > 0:
		cmd.Env = mergeEnv(args.Env)
	}
	if args.Stdin != "" {
		cmd.Stdin = strings.NewReader(args.Stdin)
	}

	// Control plane: spawn process via shared manager (tracked for server shutdown)
	handle, err := executor.SharedPM.Spawn(cmd)
	if err != nil {
		return nil, types.NewExecutorError(
			fmt.Sprintf("failed to start %s", args.Command), err, "")
	}
	defer executor.SharedPM.Cleanup(handle)

	// T003: drain stderr into a capped buffer in the background.
	// Without draining, a process that writes >64KB to stderr will block forever
	// because the OS pipe buffer fills up.
	stderrBuf := &cappedBuffer{cap: stderrCapBytes}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		io.Copy(stderrBuf, handle.Stderr) //nolint:errcheck // drain only; errors are non-fatal
	}()

	// Data plane: stream I/O with optional live progress callback
	iom := executor.NewIOManager(handle.Stdout, args.CompletionPattern, args.OnOutput)
	iom.StreamLines()

	// Build optional timeout channel
	var timerC <-chan time.Time
	if args.TimeoutSeconds > 0 {
		timer := time.NewTimer(time.Duration(args.TimeoutSeconds) * time.Second)
		defer timer.Stop()
		timerC = timer.C
	}

	// 4-way select: process exit | pattern | timeout | cancel
	select {
	case waitErr := <-handle.Done:
		iom.Drain(1 * time.Second)
		<-stderrDone
		content := iom.Collect()
		exitCode := 0
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				return nil, types.NewExecutorError(
					fmt.Sprintf("%s failed", args.Command), waitErr, content)
			}
		}
		return &types.Result{
			Content:    content,
			Stderr:     stderrBuf.String(),
			ExitCode:   exitCode,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil

	case <-iom.PatternMatched():
		executor.SharedPM.Kill(handle)
		iom.Drain(1 * time.Second)
		<-stderrDone
		return &types.Result{
			Content:    iom.Collect(),
			Stderr:     stderrBuf.String(),
			ExitCode:   0,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil

	case <-timerC:
		executor.SharedPM.Kill(handle)
		iom.Drain(1 * time.Second)
		<-stderrDone
		content := iom.Collect()
		return &types.Result{
			Content:    content,
			Stderr:     stderrBuf.String(),
			ExitCode:   124,
			Partial:    true,
			DurationMS: time.Since(start).Milliseconds(),
			Error: types.NewTimeoutError(
				fmt.Sprintf("timed out after %ds", args.TimeoutSeconds), content),
		}, nil

	case <-ctx.Done():
		executor.SharedPM.Kill(handle)
		iom.Drain(1 * time.Second)
		<-stderrDone
		content := iom.Collect()
		return &types.Result{
			Content:    content,
			Stderr:     stderrBuf.String(),
			ExitCode:   130,
			Partial:    true,
			DurationMS: time.Since(start).Milliseconds(),
			Error:      types.NewExecutorError("cancelled", ctx.Err(), content),
		}, nil
	}
}

// sessionPM is the package-level ProcessManager for persistent sessions.
// Server calls SessionProcessManager().Shutdown() on server shutdown.
var sessionPM = executor.NewProcessManager()

// SessionProcessManager returns the ProcessManager used for persistent sessions.
func SessionProcessManager() *executor.ProcessManager {
	return sessionPM
}

// readChunk is a single read result from the lifetime reader goroutine.
type readChunk struct {
	data []byte
	err  error
}

// pipeSession holds a persistent CLI process for multi-turn interaction.
//
// Goroutine architecture (T001 fix):
//
//	One "lifetime reader" goroutine is started in Start() and lives until
//	stdout is closed (either by the process exiting or by Close()). It
//	continuously reads from s.stdout and sends chunks to s.readCh.
//
//	Send() consumes chunks from s.readCh using an inactivity timer.  When
//	no new data arrives for inactivityTimeout the response is considered
//	complete and Send() returns — the lifetime reader keeps running.
//
//	On Close(), s.stdout is closed, which causes the next Read in the
//	lifetime reader to return io.EOF, ending the goroutine cleanly.
type pipeSession struct {
	id     string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	handle *executor.ProcessHandle

	// inactivityTimeout is derived from SpawnArgs.InactivitySeconds at Start().
	inactivityTimeout time.Duration

	// readCh carries chunks from the lifetime reader to Send().
	// Buffered to reduce blocking between bursts.
	readCh chan readChunk

	// readerDone is closed when the lifetime reader goroutine exits.
	readerDone chan struct{}

	// closeOnce ensures Close() only runs its cleanup once.
	closeOnce sync.Once

	// mu serialises concurrent Send() calls (sessions are single-turn at a time).
	mu sync.Mutex
}

func (s *pipeSession) ID() string { return s.id }

// Send writes prompt to stdin and reads the response via the lifetime reader.
//
// Inactivity detection: the timer is reset on each incoming chunk.  When it
// fires without new data the response is treated as complete and Send returns
// with Partial=true.  The lifetime reader goroutine is NOT stopped — it
// persists for future Send() calls.
func (s *pipeSession) Send(ctx context.Context, prompt string) (*types.Result, error) {
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

func (s *pipeSession) Stream(ctx context.Context, prompt string) (<-chan types.Event, error) {
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

// Close terminates the session.  It closes stdout (which causes the lifetime
// reader goroutine to exit via EOF) and kills the underlying process.
func (s *pipeSession) Close() error {
	s.closeOnce.Do(func() {
		// Close stdout first so the lifetime reader unblocks and exits.
		_ = s.stdout.Close()
		// Wait for the reader to exit before killing — avoids a race on the handle.
		<-s.readerDone
		_ = s.stdin.Close()
		sessionPM.Kill(s.handle)
		sessionPM.Cleanup(s.handle)
	})
	return nil
}

func (s *pipeSession) Alive() bool {
	return sessionPM.IsAlive(s.handle)
}

func (s *pipeSession) PID() int {
	if s.handle != nil {
		return s.handle.PID
	}
	return 0
}

// Start begins a persistent session via stdin/stdout pipes.
func (e *Executor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	cmd := exec.Command(args.Command, args.Args...)
	cmd.Dir = args.CWD

	switch {
	case len(args.EnvList) > 0:
		cmd.Env = args.EnvList
	case len(args.Env) > 0:
		cmd.Env = mergeEnv(args.Env)
	}

	// Stdin pipe must be created before Spawn (which calls cmd.Start internally).
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, types.NewExecutorError("failed to create stdin pipe", err, "")
	}

	handle, err := sessionPM.Spawn(cmd)
	if err != nil {
		return nil, types.NewExecutorError(
			fmt.Sprintf("failed to start %s", args.Command), err, "")
	}

	// T002: derive inactivity timeout from args; fall back to 5s default.
	inactivity := time.Duration(args.InactivitySeconds) * time.Second
	if args.InactivitySeconds <= 0 {
		inactivity = defaultInactivitySeconds * time.Second
	}

	sess := &pipeSession{
		id:                uuid.NewString(),
		cmd:               cmd,
		stdin:             stdin,
		stdout:            handle.Stdout,
		handle:            handle,
		inactivityTimeout: inactivity,
		readCh:            make(chan readChunk, 32),
		readerDone:        make(chan struct{}),
	}

	// T001: start the single lifetime reader goroutine.
	// It owns the stdout pipe exclusively and sends all data to readCh.
	// It exits when stdout returns any error (EOF, closed pipe, etc.).
	go func() {
		defer close(sess.readerDone)
		tmp := make([]byte, 4096)
		for {
			n, readErr := sess.stdout.Read(tmp)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, tmp[:n])
				sess.readCh <- readChunk{data: cp}
			}
			if readErr != nil {
				sess.readCh <- readChunk{err: readErr}
				return
			}
		}
	}()

	return sess, nil
}

// cappedBuffer is an io.Writer that discards writes once the cap is reached.
// Used to drain stderr without unbounded memory growth.
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	cap int
}

func (cb *cappedBuffer) Write(p []byte) (int, error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	remaining := cb.cap - cb.buf.Len()
	if remaining <= 0 {
		return len(p), nil // silently discard; still report success to io.Copy
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	return cb.buf.Write(p)
}

func (cb *cappedBuffer) String() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.buf.String()
}

func mergeEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
