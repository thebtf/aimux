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

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
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
	if len(args.Env) > 0 {
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
			ExitCode:   exitCode,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil

	case <-iom.PatternMatched():
		executor.SharedPM.Kill(handle)
		iom.Drain(1 * time.Second)
		return &types.Result{
			Content:    iom.Collect(),
			ExitCode:   0,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil

	case <-timerC:
		executor.SharedPM.Kill(handle)
		iom.Drain(1 * time.Second)
		content := iom.Collect()
		return &types.Result{
			Content:    content,
			ExitCode:   124,
			Partial:    true,
			DurationMS: time.Since(start).Milliseconds(),
			Error: types.NewTimeoutError(
				fmt.Sprintf("timed out after %ds", args.TimeoutSeconds), content),
		}, nil

	case <-ctx.Done():
		executor.SharedPM.Kill(handle)
		iom.Drain(1 * time.Second)
		content := iom.Collect()
		return &types.Result{
			Content:    content,
			ExitCode:   130,
			Partial:    true,
			DurationMS: time.Since(start).Milliseconds(),
			Error: types.NewExecutorError("cancelled", ctx.Err(), content),
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

// Start begins a persistent session via stdin/stdout pipes.
func (e *Executor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	cmd := exec.Command(args.Command, args.Args...)
	cmd.Dir = args.CWD

	if len(args.Env) > 0 {
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

	return &pipeSession{
		cmd:    cmd,
		stdin:  stdin,
		stdout: handle.Stdout,
		handle: handle,
		ctx:    ctx,
	}, nil
}

type pipeSession struct {
	id     string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	handle *executor.ProcessHandle
	ctx    context.Context
	mu     sync.Mutex
}

func (s *pipeSession) ID() string { return s.id }

func (s *pipeSession) Send(ctx context.Context, prompt string) (*types.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	start := time.Now()

	_, err := fmt.Fprintln(s.stdin, prompt)
	if err != nil {
		return nil, types.NewExecutorError("failed to write to stdin", err, "")
	}

	// Read response with inactivity timeout (500ms without new data = response complete).
	// A single long-running reader goroutine owns its buffer — no shared-slice data race.
	// On timeout/cancel we close stdout to unblock the blocking Read call.
	const inactivityTimeout = 500 * time.Millisecond

	type chunk struct {
		data []byte
		err  error
	}
	readCh := make(chan chunk, 16)

	// Single reader goroutine — owns its own tmp buffer exclusively.
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, readErr := s.stdout.Read(tmp)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, tmp[:n])
				readCh <- chunk{data: cp}
			}
			if readErr != nil {
				readCh <- chunk{err: readErr}
				return
			}
		}
	}()

	var buf bytes.Buffer
	timer := time.NewTimer(inactivityTimeout)
	defer timer.Stop()

	for {
		select {
		case c := <-readCh:
			if len(c.data) > 0 {
				buf.Write(c.data)
				// Reset inactivity timer — more data may be coming.
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(inactivityTimeout)
			}
			if c.err != nil {
				// Pipe closed or EOF — reader goroutine has exited.
				goto done
			}
		case <-timer.C:
			// No data for 500ms — treat as end of response.
			// Close stdout to unblock the reader goroutine so it exits cleanly.
			_ = s.stdout.Close()
			goto done
		case <-ctx.Done():
			_ = s.stdout.Close()
			goto done
		}
	}

done:
	return &types.Result{
		Content:    buf.String(),
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

func (s *pipeSession) Close() error {
	_ = s.stdin.Close()
	sessionPM.Kill(s.handle)
	sessionPM.Cleanup(s.handle)
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

func mergeEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

