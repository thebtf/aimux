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
	"github.com/thebtf/aimux/pkg/executor/session"
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

	// T001: session.New starts the lifetime reader goroutine and returns a
	// *BaseSession that implements types.Session (ID/Send/Stream/Close/Alive/PID).
	sess := session.New("", stdin, handle.Stdout, inactivity, handle, sessionPM, args.CompletionPattern)
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
