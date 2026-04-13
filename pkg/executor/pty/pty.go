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
	"time"

	"github.com/creack/pty"

	"github.com/thebtf/aimux/pkg/executor"
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
	if len(args.Env) > 0 {
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

// Start begins a persistent PTY session.
// Persistent sessions (multi-turn, stateful) are handled by the Pipe executor,
// which manages process lifecycle and stdin/stdout independently.
// PTY executor is designed for single-shot Run() calls with unbuffered output.
func (e *Executor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	if !e.available {
		return nil, types.NewExecutorError("PTY not available on this platform", nil, "")
	}
	return nil, fmt.Errorf("PTY executor handles single-shot runs only; use Pipe executor for persistent sessions")
}
