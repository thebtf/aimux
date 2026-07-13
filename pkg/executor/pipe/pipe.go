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

// Compile-time assertion: *Executor must implement types.SessionFactory (T007 / FR-1).
var _ types.SessionFactory = (*Executor)(nil)
var _ types.EventExecutor = (*Executor)(nil)

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
	var stdin io.WriteCloser
	if args.Stdin != "" {
		var err error
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, types.NewExecutorError("failed to create stdin pipe", err, "")
		}
	}

	// Control plane: spawn process via shared manager (tracked for server shutdown)
	handle, err := executor.SharedPM.Spawn(cmd)
	if err != nil {
		if stdin != nil {
			_ = stdin.Close()
		}
		return nil, types.NewExecutorError(
			fmt.Sprintf("failed to start %s", args.Command), err, "")
	}
	defer executor.SharedPM.Cleanup(handle)
	handle.ArmDrainWait()
	defer handle.MarkDrained()

	stdinDone := make(chan struct{})
	close(stdinDone)
	if stdin != nil {
		stdinDone = make(chan struct{})
		go func() {
			defer close(stdinDone)
			defer stdin.Close()
			_, _ = io.WriteString(stdin, args.Stdin)
		}()
	}

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
		iom.Drain(10 * time.Second)
		select {
		case <-stderrDone:
		case <-time.After(10 * time.Second):
		}
		<-stdinDone
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
		<-stdinDone
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
		<-stdinDone
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
		<-stdinDone
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

// SendEvents streams raw bytes from both pipes. It deliberately does not use
// IOManager: line framing would turn invalid or unterminated bytes into text.
func (e *Executor) SendEvents(ctx context.Context, executionID types.ExecutionID, msg types.Message, emit func(types.ExecutorEvent)) (*types.Response, error) {
	if err := executionID.Validate(); err != nil {
		return nil, err
	}
	args := eventSpawnArgs(msg)
	if args.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(args.TimeoutSeconds)*time.Second)
		defer cancel()
	}
	cmd := exec.Command(args.Command, args.Args...)
	cmd.Dir = args.CWD
	if len(args.Env) > 0 {
		cmd.Env = mergeEnv(args.Env)
	}
	var stdin io.WriteCloser
	var err error
	if args.Stdin != "" {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, types.NewExecutorError("failed to create stdin pipe", err, "")
		}
	}
	h, err := executor.SharedPM.Spawn(cmd)
	if err != nil {
		return nil, types.NewExecutorError("failed to start "+args.Command, err, "")
	}
	defer executor.SharedPM.Cleanup(h)
	h.ArmDrainWait()
	defer h.MarkDrained()
	if stdin != nil {
		go func() { _, _ = io.WriteString(stdin, args.Stdin); _ = stdin.Close() }()
	}
	var stdout, stderr bytes.Buffer
	var emitMu sync.Mutex
	emitEvent := func(event types.ExecutorEvent) {
		emitMu.Lock()
		defer emitMu.Unlock()
		emit(event)
	}
	var drains sync.WaitGroup
	drain := func(channel string, reader io.Reader, dst *bytes.Buffer) {
		defer drains.Done()
		buf := make([]byte, 32<<10)
		for {
			n, readErr := reader.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				_, _ = dst.Write(chunk)
				emitEvent(types.ExecutorEvent{Channel: channel, Type: "output", Content: chunk})
			}
			if readErr != nil {
				return
			}
		}
	}
	drains.Add(2)
	go drain("stdout", h.Stdout, &stdout)
	go drain("stderr", h.Stderr, &stderr)
	var waitErr error
	select {
	case waitErr = <-h.Done:
	case <-ctx.Done():
		executor.SharedPM.Kill(h)
		waitErr = ctx.Err()
	}
	drains.Wait()
	resp := &types.Response{Content: stdout.String()}
	if waitErr != nil {
		resp.ExitCode = 1
	}
	emitEvent(types.ExecutorEvent{Channel: "terminal", Type: "terminal", Terminal: true})
	if ctx.Err() != nil {
		return resp, nil
	}
	if waitErr != nil {
		return resp, waitErr
	}
	_ = stderr // stderr is represented only by byte events on this native path.
	return resp, nil
}

func eventSpawnArgs(msg types.Message) types.SpawnArgs {
	args := types.SpawnArgs{Stdin: msg.Content}
	if msg.Metadata == nil {
		return args
	}
	if value, ok := msg.Metadata["command"].(string); ok {
		args.Command = value
	}
	switch value := msg.Metadata["args"].(type) {
	case []string:
		args.Args = value
	case []any:
		for _, item := range value {
			if arg, ok := item.(string); ok {
				args.Args = append(args.Args, arg)
			}
		}
	}
	if value, ok := msg.Metadata["stdin"].(string); ok {
		args.Stdin = value
	}
	if value, ok := msg.Metadata["cwd"].(string); ok {
		args.CWD = value
	}
	if value, ok := msg.Metadata["timeout"].(int); ok {
		args.TimeoutSeconds = value
	}
	switch value := msg.Metadata["env"].(type) {
	case map[string]string:
		args.Env = value
	case map[string]any:
		args.Env = make(map[string]string, len(value))
		for key, item := range value {
			if stringValue, ok := item.(string); ok {
				args.Env[key] = stringValue
			}
		}
	}
	return args
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

// StartSession implements types.SessionFactory. It delegates to Start() to
// create a persistent pipe session for multi-turn interaction. Pipe sessions
// are available on all platforms (Available() always returns true), so this
// method never returns an ErrNotSupported-class error in production.
//
// Callers MUST gate StartSession invocation on
// Info().Capabilities.PersistentSessions (via swarm.MaybeStartSession).
func (e *Executor) StartSession(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	return e.Start(ctx, args)
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
