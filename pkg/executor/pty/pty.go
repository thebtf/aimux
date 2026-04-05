// Package pty implements the Unix PTY executor for Linux and macOS.
// Uses creack/pty for pseudo-terminal allocation, providing unbuffered
// text output (Constitution P4: ConPTY-first, PTY on Unix, Pipe fallback).
package pty

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"time"

	"github.com/creack/pty"

	"github.com/thebtf/aimux/pkg/executor/pipeline"
	"github.com/thebtf/aimux/pkg/types"
)

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
		for k, v := range args.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	// Start with PTY
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, types.NewExecutorError(
			fmt.Sprintf("PTY start failed for %s", args.Command), err, "")
	}
	defer ptmx.Close()

	// Write stdin if provided
	if args.Stdin != "" {
		go func() {
			ptmx.Write([]byte(args.Stdin))
			ptmx.Write([]byte{4}) // EOT
		}()
	}

	// Read output in background
	var output bytes.Buffer
	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, ptmx)
		readDone <- err
	}()

	// Wait for process with timeout
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
		// Process exited — wait for read to finish
		<-readDone

		content := pipeline.StripANSI(output.String())
		exitCode := 0
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}
		return &types.Result{
			Content:    content,
			ExitCode:   exitCode,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil

	case <-timerC:
		cmd.Process.Kill()
		<-procDone
		<-readDone
		content := pipeline.StripANSI(output.String())
		return &types.Result{
			Content:    content,
			ExitCode:   124,
			Partial:    true,
			DurationMS: time.Since(start).Milliseconds(),
			Error: types.NewTimeoutError(
				fmt.Sprintf("PTY timed out after %ds", args.TimeoutSeconds), content),
		}, nil

	case <-ctx.Done():
		cmd.Process.Kill()
		<-procDone
		<-readDone
		content := pipeline.StripANSI(output.String())
		return &types.Result{
			Content:    content,
			ExitCode:   130,
			Partial:    true,
			DurationMS: time.Since(start).Milliseconds(),
			Error: types.NewExecutorError("PTY cancelled", ctx.Err(), content),
		}, nil
	}
}

// Start begins a persistent PTY session.
func (e *Executor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	if !e.available {
		return nil, types.NewExecutorError("PTY not available on this platform", nil, "")
	}
	return nil, fmt.Errorf("PTY persistent sessions not yet implemented")
}
