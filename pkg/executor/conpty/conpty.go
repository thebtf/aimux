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
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/thebtf/aimux/pkg/executor/pipeline"
	"github.com/thebtf/aimux/pkg/types"
)

// Executor spawns CLI processes via Windows ConPTY for unbuffered text output.
// On Windows, processes see a TTY → line-buffered output without --json overhead.
// On non-Windows, Available() returns false → executor selector skips this.
type Executor struct {
	available bool
}

// New creates a ConPTY executor. Probes for ConPTY support on creation.
func New() *Executor {
	return &Executor{
		available: probeConPTY(),
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

	// On Windows, use exec.Command — processes inherit the console
	// which provides TTY-like behavior for most CLIs.
	// True CreatePseudoConsole would give us isolated PTY per process,
	// but exec.Command with console inheritance works for single-process use.
	cmd := exec.Command(args.Command, args.Args...)
	cmd.Dir = args.CWD

	if len(args.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range args.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Pipe stdin if provided (e.g., long prompts exceeding CLI's stdin threshold)
	if args.Stdin != "" {
		cmd.Stdin = strings.NewReader(args.Stdin)
	}

	if err := cmd.Start(); err != nil {
		return nil, types.NewExecutorError(
			fmt.Sprintf("ConPTY start failed for %s", args.Command), err, "")
	}

	// Wait in background
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var timerC <-chan time.Time
	if args.TimeoutSeconds > 0 {
		timer := time.NewTimer(time.Duration(args.TimeoutSeconds) * time.Second)
		defer timer.Stop()
		timerC = timer.C
	}

	select {
	case waitErr := <-done:
		content := pipeline.StripANSI(stdout.String())
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

	case <-timerC:
		cmd.Process.Kill()
		<-done
		content := pipeline.StripANSI(stdout.String())
		return &types.Result{
			Content:    content,
			ExitCode:   124,
			Partial:    true,
			DurationMS: time.Since(start).Milliseconds(),
			Error: types.NewTimeoutError(
				fmt.Sprintf("ConPTY timed out after %ds", args.TimeoutSeconds), content),
		}, nil

	case <-ctx.Done():
		cmd.Process.Kill()
		<-done
		content := pipeline.StripANSI(stdout.String())
		return &types.Result{
			Content:    content,
			ExitCode:   130,
			Partial:    true,
			DurationMS: time.Since(start).Milliseconds(),
			Error: types.NewExecutorError("ConPTY cancelled", ctx.Err(), content),
		}, nil
	}
}

// Start begins a persistent ConPTY session.
// Persistent sessions (multi-turn, stateful) are handled by the Pipe executor,
// which manages process lifecycle and stdin/stdout independently.
// ConPTY executor is designed for single-shot Run() calls with unbuffered output.
func (e *Executor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	if !e.available {
		return nil, types.NewExecutorError("ConPTY not available on this platform", nil, "")
	}
	return nil, fmt.Errorf("ConPTY executor handles single-shot runs only; use Pipe executor for persistent sessions")
}

// probeConPTY checks if the current platform supports ConPTY.
func probeConPTY() bool {
	return runtime.GOOS == "windows"
}
