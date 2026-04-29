// Package pty implements the Unix PTY executor for Linux and macOS.
// Uses creack/pty for pseudo-terminal allocation, providing unbuffered
// text output (Constitution P4: ConPTY-first, PTY on Unix, Pipe fallback).
package pty

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/session"
	"github.com/thebtf/aimux/pkg/types"
)

// T003 note: pty.Start() attaches the PTY master to the process's stdin, stdout,
// AND stderr simultaneously. There is no separate stderr file descriptor to drain.
// Both stdout and stderr output flow through ptmx, so IOManager captures all of it.
// No additional stderr draining is needed or possible for the PTY executor.

// Executor spawns CLI processes via Unix PTY for unbuffered text output.
type Executor struct {
	available bool
}

// New creates a PTY executor. Checks platform support on creation.
func New() *Executor {
	return &Executor{
		available: runtime.GOOS == "linux" || runtime.GOOS == "darwin",
	}
}

// Name returns the executor name.
func (e *Executor) Name() string { return "pty" }

// Available returns true on Linux and macOS.
func (e *Executor) Available() bool { return e.available }

// Run executes a single prompt via PTY and returns the result.
// The process sees a TTY → produces unbuffered line-oriented output.
func (e *Executor) Run(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
	if !e.available {
		return nil, types.NewExecutorError("PTY not available on this platform", nil, "")
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

	// PTY spawning — pty.Start() manages the pseudo-terminal
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, types.NewExecutorError(
			fmt.Sprintf("PTY start failed for %s", args.Command), err, "")
	}
	defer ptmx.Close()

	// Write stdin via PTY (context-aware to avoid leaking goroutine)
	if args.Stdin != "" {
		go func() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			ptmx.Write([]byte(args.Stdin))
			ptmx.Write([]byte{4}) // EOT
		}()
	}

	// Data plane: IOManager reads from ptmx (strips ANSI per line, checks pattern)
	iom := executor.NewIOManager(ptmx, args.CompletionPattern, args.OnOutput)
	iom.StreamLines()

	// Process exit tracking
	procDone := make(chan error, 1)
	go func() {
		procDone <- cmd.Wait()
	}()

	var timerC <-chan time.Time
	if args.TimeoutSeconds > 0 {
		timer := time.NewTimer(time.Duration(args.TimeoutSeconds) * time.Second)
		defer timer.Stop()
		timerC = timer.C
	}

	select {
	case waitErr := <-procDone:
		iom.Drain(1 * time.Second)
		content := iom.Collect()
		exitCode := 0
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}
		return &types.Result{Content: content, ExitCode: exitCode, DurationMS: time.Since(start).Milliseconds()}, nil

	case <-iom.PatternMatched():
		_ = cmd.Process.Kill()
		<-procDone
		iom.Drain(1 * time.Second)
		return &types.Result{Content: iom.Collect(), ExitCode: 0, DurationMS: time.Since(start).Milliseconds()}, nil

	case <-timerC:
		_ = cmd.Process.Kill()
		<-procDone
		iom.Drain(1 * time.Second)
		content := iom.Collect()
		return &types.Result{
			Content: content, ExitCode: 124, Partial: true,
			DurationMS: time.Since(start).Milliseconds(),
			Error:      types.NewTimeoutError(fmt.Sprintf("PTY timed out after %ds", args.TimeoutSeconds), content),
		}, nil

	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-procDone
		iom.Drain(1 * time.Second)
		content := iom.Collect()
		return &types.Result{
			Content: content, ExitCode: 130, Partial: true,
			DurationMS: time.Since(start).Milliseconds(),
			Error:      types.NewExecutorError("PTY cancelled", ctx.Err(), content),
		}, nil
	}
}

// ptySessionPM is the package-level ProcessManager for persistent PTY sessions.
var ptySessionPM = executor.NewProcessManager()

// PTYSessionProcessManager returns the ProcessManager used for persistent PTY sessions.
// Called by server shutdown to kill all tracked PTY session processes.
func PTYSessionProcessManager() *executor.ProcessManager {
	return ptySessionPM
}

const defaultPTYInactivitySeconds = 5

// Start begins a persistent PTY session for multi-turn interaction.
//
// T003 note: pty.Start() attaches the PTY master to the process's stdin, stdout,
// AND stderr simultaneously. There is no separate stderr file descriptor to drain.
// Both stdout and stderr output flow through ptmx, so the session reader captures
// all of it without additional stderr draining.
//
// The session uses session.BaseSession for the lifetime reader goroutine +
// inactivity-based response detection pattern (same as pipe executor).
func (e *Executor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	if !e.available {
		return nil, types.NewExecutorError("PTY not available on this platform", nil, "")
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

	// pty.Start() returns a single bidirectional file (ptmx) that serves as
	// both stdin and stdout for the process. Wrap it as both writer and reader.
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, types.NewExecutorError(
			fmt.Sprintf("PTY session start failed for %s", args.Command), err, "")
	}

	// Track the process via a minimal handle. PTY processes are not spawned via
	// ProcessManager.Spawn (which expects separate stdout/stderr pipes), so we
	// manage lifecycle manually: track via ptySessionPM, kill on Close via cmd.Process.
	handle := &ptyHandle{cmd: cmd, ptmx: ptmx}

	inactivity := time.Duration(args.InactivitySeconds) * time.Second
	if args.InactivitySeconds <= 0 {
		inactivity = defaultPTYInactivitySeconds * time.Second
	}

	// ptmx is both the writer (stdin to process) and reader (stdout from process).
	// session.BaseSession accepts io.WriteCloser and io.ReadCloser separately;
	// nopWriteCloser and nopReadCloser adapters share the same underlying *os.File.
	sess := session.New("", ptmxWriter{ptmx}, ptmxReader{ptmx}, inactivity, nil, nil, args.CompletionPattern)
	_ = handle // lifecycle managed via ptySession wrapper below

	return &ptySession{BaseSession: sess, handle: handle}, nil
}

// ptyHandle holds the cmd and ptmx for a PTY session.
type ptyHandle struct {
	cmd  *exec.Cmd
	ptmx *os.File
}

// ptySession wraps session.BaseSession and overrides Close/Alive/PID to manage
// the PTY process lifecycle directly (pty.Start does not use ProcessManager.Spawn).
type ptySession struct {
	*session.BaseSession
	handle *ptyHandle
	once   sync.Once
}

// Close terminates the PTY session: delegates to BaseSession.Close() to stop
// the lifetime reader, then kills the underlying process.
func (s *ptySession) Close() error {
	s.BaseSession.Close()
	s.once.Do(func() {
		if s.handle.cmd.Process != nil {
			s.handle.cmd.Process.Kill() //nolint:errcheck
		}
		s.handle.ptmx.Close() //nolint:errcheck
	})
	return nil
}

// Alive returns true if the underlying process has not yet exited.
func (s *ptySession) Alive() bool {
	if s.handle.cmd.ProcessState != nil {
		return false // ProcessState is set only after cmd.Wait() returns
	}
	if s.handle.cmd.Process == nil {
		return false
	}
	// Send signal 0 to check if the process is still alive.
	return s.handle.cmd.Process.Signal(syscall.Signal(0)) == nil
}

// PID returns the OS process ID.
func (s *ptySession) PID() int {
	if s.handle.cmd.Process != nil {
		return s.handle.cmd.Process.Pid
	}
	return 0
}

// ptmxWriter wraps *os.File as io.WriteCloser (Close is a no-op — ptmxReader owns Close).
type ptmxWriter struct{ f *os.File }

func (w ptmxWriter) Write(p []byte) (int, error) { return w.f.Write(p) }
func (w ptmxWriter) Close() error                { return nil } // ptmxReader.Close owns the file

// ptmxReader wraps *os.File as io.ReadCloser (Close closes the underlying file).
type ptmxReader struct{ f *os.File }

func (r ptmxReader) Read(p []byte) (int, error) { return r.f.Read(p) }
func (r ptmxReader) Close() error                { return r.f.Close() }
