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
	"regexp"
	"strings"
	"sync"
	"time"

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
func (e *Executor) Run(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
	start := time.Now()

	// Use exec.Command (NOT CommandContext) — we manage kill ourselves
	// to correctly distinguish timeout vs cancel vs normal exit.
	cmd := exec.Command(args.Command, args.Args...)
	cmd.Dir = args.CWD

	if len(args.Env) > 0 {
		cmd.Env = mergeEnv(args.Env)
	}

	var stderr bytes.Buffer
	stdout := &safeBuffer{}
	cmd.Stdout = stdout
	cmd.Stderr = &stderr

	if args.Stdin != "" {
		cmd.Stdin = strings.NewReader(args.Stdin)
	}

	if err := cmd.Start(); err != nil {
		return nil, types.NewExecutorError(
			fmt.Sprintf("failed to start %s", args.Command), err, "")
	}

	// Wait in background goroutine
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Completion pattern: poll stdout for pattern match (process may not exit after completing)
	var patternDone <-chan struct{}
	if args.CompletionPattern != "" {
		re, reErr := regexp.Compile(args.CompletionPattern)
		if reErr != nil {
			// Invalid regex — skip pattern matching, process runs to natural exit
			re = nil
		}
		if re != nil {
			patternCh := make(chan struct{}, 1)
			patternDone = patternCh
			go func() {
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						if re.MatchString(stdout.String()) {
							patternCh <- struct{}{}
							return
						}
					case <-done:
						return // process exited, stop checking
					}
				}
			}()
		}
	}

	// Build optional timeout channel
	var timerC <-chan time.Time
	if args.TimeoutSeconds > 0 {
		timer := time.NewTimer(time.Duration(args.TimeoutSeconds) * time.Second)
		defer timer.Stop()
		timerC = timer.C
	}

	select {
	case waitErr := <-done:
		// Process exited on its own
		exitCode := 0
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				return nil, types.NewExecutorError(
					fmt.Sprintf("%s failed", args.Command), waitErr, stdout.String())
			}
		}
		return &types.Result{
			Content:    stdout.String(),
			ExitCode:   exitCode,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil

	case <-patternDone:
		// Completion pattern matched — kill process and return output
		_ = killProcess(cmd)
		<-done
		return &types.Result{
			Content:    stdout.String(),
			ExitCode:   0,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil

	case <-timerC:
		// Timeout
		_ = killProcess(cmd)
		<-done // wait for goroutine to finish
		return &types.Result{
			Content:    stdout.String(),
			ExitCode:   124,
			Partial:    true,
			DurationMS: time.Since(start).Milliseconds(),
			Error: types.NewTimeoutError(
				fmt.Sprintf("timed out after %ds", args.TimeoutSeconds),
				stdout.String()),
		}, nil

	case <-ctx.Done():
		// Context cancelled
		_ = killProcess(cmd)
		<-done // wait for goroutine to finish
		return &types.Result{
			Content:    stdout.String(),
			ExitCode:   130,
			Partial:    true,
			DurationMS: time.Since(start).Milliseconds(),
			Error: types.NewExecutorError("cancelled", ctx.Err(), stdout.String()),
		}, nil
	}
}

// Start begins a persistent session via stdin/stdout pipes.
func (e *Executor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	cmd := exec.Command(args.Command, args.Args...)
	cmd.Dir = args.CWD

	if len(args.Env) > 0 {
		cmd.Env = mergeEnv(args.Env)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, types.NewExecutorError("failed to create stdin pipe", err, "")
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, types.NewExecutorError("failed to create stdout pipe", err, "")
	}

	if err := cmd.Start(); err != nil {
		return nil, types.NewExecutorError(
			fmt.Sprintf("failed to start %s", args.Command), err, "")
	}

	return &pipeSession{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		ctx:    ctx,
	}, nil
}

type pipeSession struct {
	id     string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
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
	// Pipes return partial reads — cannot use buffer-fill as completion signal.
	var buf bytes.Buffer
	tmp := make([]byte, 4096)
	inactivityTimeout := 500 * time.Millisecond
	readCh := make(chan readResult, 1)

	for {
		go func() {
			n, err := s.stdout.Read(tmp)
			readCh <- readResult{n, err}
		}()

		select {
		case r := <-readCh:
			if r.n > 0 {
				buf.Write(tmp[:r.n])
			}
			if r.err != nil {
				goto done
			}
			// Got data — reset inactivity timer, continue reading
		case <-time.After(inactivityTimeout):
			// No data for 500ms — response complete
			goto done
		case <-ctx.Done():
			goto done
		}
	}

done:
	return &types.Result{
		Content:    buf.String(),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

type readResult struct {
	n   int
	err error
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
	return killProcess(s.cmd)
}

func (s *pipeSession) Alive() bool {
	return s.cmd.ProcessState == nil
}

func (s *pipeSession) PID() int {
	if s.cmd.Process != nil {
		return s.cmd.Process.Pid
	}
	return 0
}

func killProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func mergeEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// safeBuffer is a goroutine-safe bytes.Buffer wrapper.
// Solves BUG-001: concurrent reads (completion pattern polling) and writes (OS pipe).
type safeBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (sb *safeBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *safeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

func (sb *safeBuffer) Len() int {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Len()
}
