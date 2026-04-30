// Package conpty implements the Windows ConPTY executor.
//
// AIMUX-16 CR-004: real Win32 CreatePseudoConsole / ResizePseudoConsole /
// ClosePseudoConsole implementation, replacing the pre-CR-004 stub that
// announced ConPTY availability but used a plain exec.Command pipe. Operator
// directive (feedback_aimux_interactive_required.md):
//
//   - TUI mode is obligatory; deferral not acceptable.
//   - Available() reports honestly: false on Win10 < 1809, false on non-Windows.
//   - NO silent pipe downgrade — the selector explicitly chooses pipe when
//     Available() is false. Inside this package, every refusal is logged.
//
// On Windows: spawns a child process attached to a pseudo-console; the child
// sees stdin/stdout as a TTY (isatty() returns true), enabling codex chat /
// aider interactive flows. On other platforms: Available() returns false and
// the executor selector picks PTY or Pipe instead.
package conpty

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
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

// Compile-time assertion: *Executor must implement types.SessionFactory (T007 / FR-1).
var _ types.SessionFactory = (*Executor)(nil)

// Executor spawns CLI processes via Windows ConPTY for unbuffered text output.
// On Windows 10 1809+: child sees stdin/stdout as a TTY → line-buffered output
// without --json overhead and TUI flows work. On older Windows / non-Windows:
// Available() returns false → executor selector skips this adapter.
type Executor struct {
	available bool
}

// packageAvailable caches the probe result for the package-level Available() func.
// Populated on first call via probeOnce; all subsequent calls read without locking.
var (
	probeOnce   sync.Once
	probeResult bool
)

// Available returns whether ConPTY is supported on this host. The result
// is cached after the first call (probe runs exactly once per process). This
// package-level function is used by buildFallbackCandidates to skip TTY-only
// CLIs when ConPTY is unavailable, without needing an *Executor instance.
//
// Per operator directive: a false return MUST surface as a logged warning
// (probeConPTY does this on Windows). Callers MUST NOT silently fall back —
// they MUST either select an alternative executor (selector path) or surface
// the unavailability to the operator explicitly.
func Available() bool {
	probeOnce.Do(func() {
		probeResult = probeConPTY()
	})
	return probeResult
}

// New creates a ConPTY executor. Uses the cached Available() result so future
// expensive Win32 probes are not repeated per-call.
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
// On Windows 1809+: spawns process inside a pseudo-console (real
// CreatePseudoConsole). The child sees a TTY → unbuffered output, ANSI
// escapes preserved on the wire (IOManager strips them per line).
//
// EC-4.3 — if the spawn fails after the pseudo-console allocation succeeded,
// openWindowsConPTY closes the pseudo-console before returning the error
// (no handle leak). EC-4.4 — handle.Close is idempotent (sync.Once) and
// safe to call from both this Run path and from external session teardown.
func (e *Executor) Run(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
	if !e.available {
		return nil, types.NewExecutorError("ConPTY not available on this platform", nil, "")
	}
	start := time.Now()

	handle, err := openWindowsConPTY(ctx, openParams{
		command: args.Command,
		args:    args.Args,
		cwd:     args.CWD,
		envList: args.EnvList,
		envMap:  args.Env,
	})
	if err != nil {
		return nil, types.NewExecutorError(
			fmt.Sprintf("ConPTY start failed for %s", args.Command), err, "")
	}
	defer handle.Close()

	// Write stdin upfront if provided. Pseudo-console echoes input back on
	// the output stream — IOManager.StreamLines reads it together with the
	// child's actual output. For the Run() path that's acceptable: callers
	// concatenate prompt+response into Result.Content.
	//
	// CR-004 review feedback (coderabbit MAJOR): write/close errors used to
	// be silently dropped, leading to misleading timeout/partial-output
	// results when the child closed stdin or accepted only part of the input.
	// Now we surface the real cause as an ExecutorError so callers can
	// distinguish "stdin write failed" from "child timed out". The
	// pseudo-console handle is torn down via the deferred handle.Close —
	// no leak.
	//
	// Note: handle.Stdin() returns a fresh conptyWriter each call (it's a
	// thin value type, not a cached field), so we capture it once. The
	// writer's Close is intentionally a no-op (see conptyWriter.Close docs);
	// we still call it to honour the io.WriteCloser contract and surface
	// any future error if the upstream library starts returning one.
	if args.Stdin != "" {
		stdin := handle.Stdin()
		if n, werr := io.WriteString(stdin, args.Stdin); werr != nil || n != len(args.Stdin) {
			if werr == nil {
				werr = io.ErrShortWrite
			}
			return nil, types.NewExecutorError(
				fmt.Sprintf("ConPTY stdin write failed for %s (wrote %d of %d bytes)",
					args.Command, n, len(args.Stdin)),
				werr, "")
		}
		if cerr := stdin.Close(); cerr != nil {
			return nil, types.NewExecutorError(
				fmt.Sprintf("ConPTY stdin close failed for %s", args.Command),
				cerr, "")
		}
	}

	// ConPTY merges stderr onto the pseudo-console output stream by design.
	// We keep an empty stderrBuf to preserve the Result.Stderr field shape
	// (callers may check it; pre-CR-004 path produced stderr separately for
	// the legacy exec.Command path).
	stderrBuf := &cappedBuffer{cap: stderrCapBytes}

	// Data plane (IOManager strips ANSI per line — no need for pipeline.StripANSI here)
	iom := executor.NewIOManager(handle.Stdout(), args.CompletionPattern, args.OnOutput)
	iom.StreamLines()

	var timerC <-chan time.Time
	if args.TimeoutSeconds > 0 {
		timer := time.NewTimer(time.Duration(args.TimeoutSeconds) * time.Second)
		defer timer.Stop()
		timerC = timer.C
	}

	ph := handle.ProcessHandle()
	select {
	case waitErr := <-ph.Done:
		iom.Drain(1 * time.Second)
		content := iom.Collect()
		exitCode := 0
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				// Non-zero exit codes come back as a synthetic error from
				// reapProcess; surface the message but treat as a normal exit
				// (set exitCode from ProcessHandle).
				exitCode = ph.ExitCode
				if exitCode == 0 {
					return nil, types.NewExecutorError(
						fmt.Sprintf("%s failed", args.Command), waitErr, content)
				}
			}
		}
		return &types.Result{
			Content:    content,
			Stderr:     stderrBuf.String(),
			ExitCode:   exitCode,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil

	case <-iom.PatternMatched():
		_ = handle.Close() // forces reapProcess to observe EOF
		iom.Drain(1 * time.Second)
		return &types.Result{
			Content:    iom.Collect(),
			Stderr:     stderrBuf.String(),
			ExitCode:   0,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil

	case <-timerC:
		_ = handle.Close()
		iom.Drain(1 * time.Second)
		content := iom.Collect()
		return &types.Result{
			Content:    content,
			Stderr:     stderrBuf.String(),
			ExitCode:   124,
			Partial:    true,
			DurationMS: time.Since(start).Milliseconds(),
			Error:      types.NewTimeoutError(fmt.Sprintf("ConPTY timed out after %ds", args.TimeoutSeconds), content),
		}, nil

	case <-ctx.Done():
		_ = handle.Close()
		iom.Drain(1 * time.Second)
		content := iom.Collect()
		return &types.Result{
			Content:    content,
			Stderr:     stderrBuf.String(),
			ExitCode:   130,
			Partial:    true,
			DurationMS: time.Since(start).Milliseconds(),
			Error:      types.NewExecutorError("ConPTY cancelled", ctx.Err(), content),
		}, nil
	}
}

// conptySessionPM is the package-level ProcessManager for persistent ConPTY
// sessions. Although the new winConsoleHandle owns its own lifecycle, the PM
// is retained for shutdown bookkeeping — server shutdown calls
// ConPTYSessionProcessManager().Shutdown() to ensure no session leaks at
// daemon exit.
var conptySessionPM = executor.NewProcessManager()

// ConPTYSessionProcessManager returns the ProcessManager used for persistent ConPTY sessions.
// Called by server shutdown to kill all tracked ConPTY session processes.
func ConPTYSessionProcessManager() *executor.ProcessManager {
	return conptySessionPM
}

const defaultConPTYInactivitySeconds = 5

// Start begins a persistent ConPTY session for multi-turn interaction.
//
// On Windows 1809+: real CreatePseudoConsole — child process inherits the
// pseudo-console as stdio, GetConsoleMode returns ENABLE_VIRTUAL_TERMINAL_PROCESSING
// for the child, and isatty() returns true. This is what enables codex chat
// and aider's interactive TUI to function.
//
// EC-4.4 — winConsoleHandle.Close is sync.Once-guarded so BaseSession.Close
// (which closes stdin then signals the reader to exit) cannot race with
// ProcessHandle.Done's reap goroutine on ConPTY teardown.
func (e *Executor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	if !e.available {
		return nil, types.NewExecutorError("ConPTY not available on this platform", nil, "")
	}

	handle, err := openWindowsConPTY(ctx, openParams{
		command: args.Command,
		args:    args.Args,
		cwd:     args.CWD,
		envList: args.EnvList,
		envMap:  args.Env,
	})
	if err != nil {
		return nil, types.NewExecutorError(
			fmt.Sprintf("ConPTY session start failed for %s", args.Command), err, "")
	}

	inactivity := time.Duration(args.InactivitySeconds) * time.Second
	if args.InactivitySeconds <= 0 {
		inactivity = defaultConPTYInactivitySeconds * time.Second
	}

	ph := handle.ProcessHandle()

	// BaseSession owns stdout/stdin from this point. When Close fires it
	// closes stdout via handle.Close (idempotent) which also closes the
	// pseudo-console — the reader goroutine inside BaseSession sees EOF and
	// exits cleanly.
	sess := session.New(
		"",
		handle.Stdin(),
		handle.Stdout(),
		inactivity,
		ph,
		conptySessionPM,
		args.CompletionPattern,
	)
	return sess, nil
}

// StartSession implements types.SessionFactory. It delegates to Start() to
// create a persistent ConPTY session for multi-turn interaction.
//
// When ConPTY is unavailable (non-Windows or Windows < 1809), returns an
// error describing the platform limitation. Callers MUST gate StartSession
// invocation on Info().Capabilities.PersistentSessions (via swarm.MaybeStartSession)
// to avoid this path; the error is a defensive guard, not a graceful fallback.
func (e *Executor) StartSession(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	return e.Start(ctx, args)
}
