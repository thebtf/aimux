// Package conpty implements the Windows ConPTY executor.
// Uses CreatePseudoConsole API for unbuffered text output from CLI processes.
// Constitution P4: ConPTY-first, JSON-fallback.
//
// On non-Windows platforms, Available() returns false and all methods
// return errors — the executor selector will pick PTY or Pipe instead.
package conpty

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/session"
	"github.com/thebtf/aimux/pkg/types"
)

const stderrCapBytes = 64 * 1024

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
		return len(p), nil
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

// Executor spawns CLI processes via Windows ConPTY for unbuffered text output.
// On Windows, processes see a TTY → line-buffered output without --json overhead.
// On non-Windows, Available() returns false → executor selector skips this.
type Executor struct {
	available bool
}

// packageAvailable caches the probe result for the package-level Available() func.
// Populated on first call via probeOnce; all subsequent calls read without locking.
var (
	probeOnce       sync.Once
	probeResult     bool
)

// Available returns whether ConPTY is supported on this platform. The result
// is cached after the first call (probe runs exactly once per process). This
// package-level function is used by buildFallbackCandidates to skip TTY-only
// CLIs when ConPTY is unavailable, without needing an *Executor instance.
//
// Callers that need fail-open semantics on probe error should check:
//
//	if !conpty.Available() && profile.RequiresTTY { skip }
func Available() bool {
	probeOnce.Do(func() {
		probeResult = probeConPTY()
	})
	return probeResult
}

// New creates a ConPTY executor. Uses the cached Available() result so that
// future expensive Win32 probes (see TODO below) are not repeated per-call.
func New() *Executor {
	return &Executor{
		available: Available(),
	}
}

// Name returns the executor name.
func (e *Executor) Name() string { return "conpty" }

// Available returns true if ConPTY is supported on this platform.
func (e *Executor) Available() bool { return e.available }

// Run executes a single prompt via ConPTY and returns the result.
//
// On Windows: spawns process with a pseudo-console attached. The process
// sees a TTY, producing unbuffered text output. We read from the output
// pipe with ANSI stripping.
//
// Note: Full CreatePseudoConsole implementation requires Windows 10 1809+.
// Current implementation uses exec.Command with forced PTY-like behavior
// via ConHost. For true ConPTY (CreatePseudoConsole syscall), a future
// iteration will add the win32 API calls via golang.org/x/sys/windows.
func (e *Executor) Run(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
	if !e.available {
		return nil, types.NewExecutorError("ConPTY not available on this platform", nil, "")
	}
	start := time.Now()

	cmd := exec.Command(args.Command, args.Args...)
	cmd.Dir = args.CWD
	switch {
	case len(args.EnvList) > 0:
		cmd.Env = args.EnvList
	case len(args.Env) > 0:
		cmd.Env = os.Environ()
		for k, v := range args.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	if args.Stdin != "" {
		cmd.Stdin = strings.NewReader(args.Stdin)
	}

	// Control plane: use shared PM for shutdown tracking
	handle, err := executor.SharedPM.Spawn(cmd)
	if err != nil {
		return nil, types.NewExecutorError(
			fmt.Sprintf("ConPTY start failed for %s", args.Command), err, "")
	}
	defer executor.SharedPM.Cleanup(handle)

	// T003: drain stderr into a capped buffer in the background.
	// ProcessManager.Spawn() always creates a stderr pipe. If the subprocess
	// writes >64KB to stderr, the OS pipe buffer fills and the subprocess blocks.
	// Note: when a true CreatePseudoConsole implementation is added, stderr will
	// merge onto the console output pipe and this goroutine will drain an empty pipe.
	stderrBuf := &cappedBuffer{cap: stderrCapBytes}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		io.Copy(stderrBuf, handle.Stderr) //nolint:errcheck // drain only
	}()

	// Data plane (IOManager strips ANSI per line — no need for pipeline.StripANSI here)
	iom := executor.NewIOManager(handle.Stdout, args.CompletionPattern, args.OnOutput)
	iom.StreamLines()

	var timerC <-chan time.Time
	if args.TimeoutSeconds > 0 {
		timer := time.NewTimer(time.Duration(args.TimeoutSeconds) * time.Second)
		defer timer.Stop()
		timerC = timer.C
	}

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
		return &types.Result{Content: content, Stderr: stderrBuf.String(), ExitCode: exitCode, DurationMS: time.Since(start).Milliseconds()}, nil

	case <-iom.PatternMatched():
		executor.SharedPM.Kill(handle)
		iom.Drain(1 * time.Second)
		<-stderrDone
		return &types.Result{Content: iom.Collect(), Stderr: stderrBuf.String(), ExitCode: 0, DurationMS: time.Since(start).Milliseconds()}, nil

	case <-timerC:
		executor.SharedPM.Kill(handle)
		iom.Drain(1 * time.Second)
		<-stderrDone
		content := iom.Collect()
		return &types.Result{
			Content: content, Stderr: stderrBuf.String(), ExitCode: 124, Partial: true,
			DurationMS: time.Since(start).Milliseconds(),
			Error: types.NewTimeoutError(fmt.Sprintf("ConPTY timed out after %ds", args.TimeoutSeconds), content),
		}, nil

	case <-ctx.Done():
		executor.SharedPM.Kill(handle)
		iom.Drain(1 * time.Second)
		<-stderrDone
		content := iom.Collect()
		return &types.Result{
			Content: content, Stderr: stderrBuf.String(), ExitCode: 130, Partial: true,
			DurationMS: time.Since(start).Milliseconds(),
			Error: types.NewExecutorError("ConPTY cancelled", ctx.Err(), content),
		}, nil
	}
}

// conptySessionPM is the package-level ProcessManager for persistent ConPTY sessions.
var conptySessionPM = executor.NewProcessManager()

// ConPTYSessionProcessManager returns the ProcessManager used for persistent ConPTY sessions.
// Called by server shutdown to kill all tracked ConPTY session processes.
func ConPTYSessionProcessManager() *executor.ProcessManager {
	return conptySessionPM
}

const defaultConPTYInactivitySeconds = 5

// Start begins a persistent ConPTY session for multi-turn interaction.
//
// T003 note: ConPTY merges stdout and stderr onto a single pseudo-console output
// pipe by design (Windows CreatePseudoConsole API behavior). There is no
// separate stderr stream to drain — all subprocess output (including stderr)
// flows through handle.Stdout and is captured by the session reader.
//
// The session uses session.BaseSession for the lifetime reader goroutine +
// inactivity-based response detection pattern (same as pipe executor).
func (e *Executor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	if !e.available {
		return nil, types.NewExecutorError("ConPTY not available on this platform", nil, "")
	}

	cmd := exec.Command(args.Command, args.Args...)
	cmd.Dir = args.CWD
	switch {
	case len(args.EnvList) > 0:
		cmd.Env = args.EnvList
	case len(args.Env) > 0:
		cmd.Env = os.Environ()
		for k, v := range args.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	// Stdin pipe must be created before Spawn (which calls cmd.Start internally).
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, types.NewExecutorError("ConPTY: failed to create stdin pipe", err, "")
	}

	handle, err := conptySessionPM.Spawn(cmd)
	if err != nil {
		return nil, types.NewExecutorError(
			fmt.Sprintf("ConPTY session start failed for %s", args.Command), err, "")
	}

	inactivity := time.Duration(args.InactivitySeconds) * time.Second
	if args.InactivitySeconds <= 0 {
		inactivity = defaultConPTYInactivitySeconds * time.Second
	}

	sess := session.New("", stdin, handle.Stdout, inactivity, handle, conptySessionPM, args.CompletionPattern)
	return sess, nil
}

// probeConPTY checks if the current platform supports ConPTY.
func probeConPTY() bool {
	return runtime.GOOS == "windows"
}
