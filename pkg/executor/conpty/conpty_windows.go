//go:build windows

// Package conpty — Win32 ConPTY backend.
//
// AIMUX-16 CR-004: real CreatePseudoConsole/ResizePseudoConsole/ClosePseudoConsole
// implementation, replacing the pre-CR-004 documented stub that announced ConPTY
// support but used a plain exec.Command pipe. Operator directive: NO silent pipe
// downgrade. The probe is honest about availability, and when ConPTY is supported
// the child process inherits a real pseudo-console as stdio so isatty() returns
// true (codex chat / aider interactive flows depend on this).
//
// Library: github.com/UserExistsError/conpty v0.1.4 (MIT). Pure-Go wrapper around
// Win32 CreatePseudoConsole API. Pinned in go.mod; fork to thebtf/conpty if
// upstream becomes abandoned.
package conpty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	upconpty "github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"

	"github.com/thebtf/aimux/pkg/executor"
)

// ErrPlatformUnsupported on Windows means CreatePseudoConsole was missing or
// the OS build is older than 10.0.17763 (Win10 1809). This is the same error
// type as the non-Windows stub exposes — callers can compare equality
// regardless of build target.
var ErrPlatformUnsupported = errors.New(
	"conpty: Win32 CreatePseudoConsole unavailable (requires Windows 10 1809 / build 17763 or later)")

// minimumConPTYBuild is the first Windows build that ships CreatePseudoConsole.
// Win10 1809 = build 17763. Earlier builds expose neither the export nor the
// kernel handle type. probeConPTY returns false below this threshold and logs
// an explicit warning — silent downgrade is the bug being fixed (see
// feedback_aimux_interactive_required.md).
const minimumConPTYBuild = 17763

// kernel32 ConPTY exports we verify before claiming availability. Verifying
// the exports (not just the OS version) protects against environments where
// the API is missing for other reasons (kernel32 patched, Windows-on-ARM
// compatibility layers, etc).
var (
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procCreatePseudoConsole = kernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole = kernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole  = kernel32.NewProc("ClosePseudoConsole")
)

// versionProvider returns major.minor.build of the running Windows OS.
// Indirected via a package-level var so tests can stub the version check
// without requiring an actual older Windows build (EC-4.1).
var versionProvider = realRtlGetVersion

// kernel32Probe returns nil if all three ConPTY exports resolve, or an error
// describing which one was missing. Indirected via a package-level var so
// tests can simulate a kernel32 missing the ConPTY family (T004-9 acceptance).
var kernel32Probe = realKernel32Probe

// realRtlGetVersion calls RtlGetVersion to return the current Windows build.
// Returns build number (e.g. 17763 for Win10 1809, 22621 for Win11 22H2).
// RtlGetVersion is documented to always succeed (Win NT internals fact —
// see x/sys/windows source comment); the error return is kept on the type
// only because tests mock this with a build-failure scenario (T004-9).
func realRtlGetVersion() (uint32, error) {
	info := windows.RtlGetVersion()
	if info == nil {
		return 0, fmt.Errorf("RtlGetVersion returned nil")
	}
	return info.BuildNumber, nil
}

// realKernel32Probe verifies that all three ConPTY exports resolve in
// kernel32.dll. Returns nil on success, error describing the missing export
// otherwise. Order matters — CreatePseudoConsole is the gate: if it's missing
// the others are guaranteed missing too on every documented Windows variant.
func realKernel32Probe() error {
	if err := procCreatePseudoConsole.Find(); err != nil {
		return fmt.Errorf("CreatePseudoConsole missing in kernel32.dll: %w", err)
	}
	if err := procResizePseudoConsole.Find(); err != nil {
		return fmt.Errorf("ResizePseudoConsole missing in kernel32.dll: %w", err)
	}
	if err := procClosePseudoConsole.Find(); err != nil {
		return fmt.Errorf("ClosePseudoConsole missing in kernel32.dll: %w", err)
	}
	return nil
}

// probeConPTY returns true iff this Windows host supports CreatePseudoConsole.
// EC-4.1 — on Win10 < 1809 (build < 17763) we LOG an explicit warning and
// return false. Operator directive (feedback_aimux_interactive_required.md):
// NO silent pipe downgrade — log every refusal so the operator sees the
// reason in the daemon log instead of guessing why TUI is not engaging.
//
// Layered defence: we check OS build first (cheap RtlGetVersion call) and
// verify the kernel32 exports second (LazyDLL Find). Either failure disables
// ConPTY. The library's own IsConPtyAvailable() is consulted last as a final
// guard — if upstream considers the env unsupported we trust that signal.
func probeConPTY() bool {
	build, err := versionProvider()
	if err != nil {
		log.Printf("conpty: probeConPTY: failed to read OS version: %v "+
			"(disabling ConPTY — operator directive: no silent pipe downgrade)", err)
		return false
	}
	if build < minimumConPTYBuild {
		log.Printf("conpty: probeConPTY: Windows build %d < %d (Win10 1809) "+
			"— ConPTY disabled, falling back to pipe with WARNING (operator: TUI mode unavailable on this host)",
			build, minimumConPTYBuild)
		return false
	}
	if err := kernel32Probe(); err != nil {
		log.Printf("conpty: probeConPTY: kernel32 ConPTY exports unavailable: %v "+
			"(disabling ConPTY — no silent downgrade)", err)
		return false
	}
	if !upconpty.IsConPtyAvailable() {
		log.Printf("conpty: probeConPTY: upstream library reports ConPTY unavailable " +
			"despite OS build + kernel32 exports passing (disabling ConPTY)")
		return false
	}
	return true
}

// openWindowsConPTY creates a Win32 pseudo-console, spawns the child process
// attached to it, and returns a winConsoleHandle that exposes the io pair plus
// a synthetic *executor.ProcessHandle compatible with session.BaseSession.
//
// On any error the pseudo-console is closed before return — EC-4.3 (no leak
// on spawn failure). Resize is invoked exactly once on success with default
// geometry (T004-4).
func openWindowsConPTY(ctx context.Context, p openParams) (*winConsoleHandle, error) {
	if !probeConPTY() {
		return nil, ErrPlatformUnsupported
	}

	cmdLine := buildCommandLine(p.command, p.args)

	opts := []upconpty.ConPtyOption{
		upconpty.ConPtyDimensions(defaultConPTYWidth, defaultConPTYHeight),
	}
	if p.cwd != "" {
		opts = append(opts, upconpty.ConPtyWorkDir(p.cwd))
	}
	env := buildEnv(p.envList, p.envMap)
	if len(env) > 0 {
		opts = append(opts, upconpty.ConPtyEnv(env))
	}

	cp, err := upconpty.Start(cmdLine, opts...)
	if err != nil {
		return nil, fmt.Errorf("CreatePseudoConsole/spawn failed for %q: %w", p.command, err)
	}

	h := &winConsoleHandle{
		cp:        cp,
		startedAt: time.Now(),
		ctx:       ctx,
	}

	// Adapter to existing executor.ProcessHandle contract:
	// Done channel closed when process exits; Wait runs in a goroutine.
	pid := cp.Pid()
	doneCh := make(chan error, 1)
	processHandle := &executor.ProcessHandle{
		PID:       pid,
		Cmd:       buildSyntheticCmd(p.command, p.args, pid),
		Stdout:    h, // h implements io.ReadCloser via .Read and .Close
		Stderr:    nopReadCloser{}, // ConPTY merges stderr onto pseudo-console output
		Done:      doneCh,
		StartedAt: h.startedAt,
	}
	h.processHandle = processHandle
	h.doneCh = doneCh

	go h.reapProcess()

	return h, nil
}

// winConsoleHandleWin is the Windows-only state attached to a winConsoleHandle.
// On non-Windows builds winConsoleHandle is a placeholder type with no fields;
// on Windows the type aliases this struct so the platform-specific state is
// addressable from method receivers in this file.
//
// The handle owns the pseudo-console lifecycle. Close is idempotent
// (sync.Once) — EC-4.4 race between ProcessHandle.Done and external Close.
type winConsoleHandle struct {
	cp            *upconpty.ConPty
	startedAt     time.Time
	ctx           context.Context
	processHandle *executor.ProcessHandle
	doneCh        chan error

	closeOnce sync.Once
	closed    atomic.Bool
}

// Stdin returns the writer end of the pseudo-console (writes to child's stdin).
// The upstream library exposes Read+Write on the ConPty itself; we adapt that
// into the io.ReadCloser/io.WriteCloser pair session.BaseSession expects.
func (h *winConsoleHandle) Stdin() io.WriteCloser { return conptyWriter{h: h} }

// Stdout returns the reader end of the pseudo-console (reads child's output,
// stderr included — ConPTY merges them by design).
func (h *winConsoleHandle) Stdout() io.ReadCloser { return h }

// Read forwards reads to the upstream ConPty.
// Implementing io.Reader directly on the handle (instead of via a wrapper
// type) lets us use the same handle in both Stdout() and the
// ProcessHandle.Stdout slot.
func (h *winConsoleHandle) Read(p []byte) (int, error) {
	if h.closed.Load() {
		return 0, io.EOF
	}
	n, err := h.cp.Read(p)
	if err != nil && h.closed.Load() {
		return n, io.EOF
	}
	return n, err
}

// ProcessHandle returns the synthetic executor.ProcessHandle that BaseSession
// consumes. PID is the real child PID; Done is signalled when the child exits.
func (h *winConsoleHandle) ProcessHandle() *executor.ProcessHandle { return h.processHandle }

// Resize forwards to ResizePseudoConsole. EC-4.2 — failure is logged but not
// fatal to the session.
func (h *winConsoleHandle) Resize(width, height int) error {
	if h.closed.Load() {
		return errors.New("conpty: handle closed")
	}
	return h.cp.Resize(width, height)
}

// Close tears down the pseudo-console and releases all handles. Idempotent —
// safe to call after the child process exits (reapProcess) or from external
// session teardown. EC-4.4 — race-safe.
func (h *winConsoleHandle) Close() error {
	var err error
	h.closeOnce.Do(func() {
		h.closed.Store(true)
		err = h.cp.Close()
	})
	return err
}

// reapProcess waits for the child to exit and signals the synthetic Done
// channel. Mirrors executor.ProcessManager.Spawn's reap goroutine so existing
// IsAlive/Done consumers see the same signalling shape.
//
// Lifetime invariants (CR-004 review feedback — coderabbit CRIT, gemini #1):
//
//   - Wait MUST use context.Background(), NOT h.ctx. h.ctx is the request
//     context passed into openWindowsConPTY; for persistent sessions started
//     via Executor.Start(), that context can be cancelled or expire AFTER the
//     session is established (e.g., the spawn handler returned long ago, but
//     the session is still alive and serving multi-turn traffic). Using h.ctx
//     would cause Wait to return STILL_ACTIVE prematurely and falsely mark
//     the still-running child as exited. The pseudo-console is torn down
//     explicitly via handle.Close() — that is the only legitimate exit path
//     for the reap goroutine.
//
//   - MarkExited() is called ONLY on a real process exit. If Wait returns
//     because the upstream library was closed (handle.Close → ClosePseudoConsole),
//     the child may briefly outlive the call; we still mark it exited because
//     the pseudo-console is gone and IsAlive should report dead from the
//     session's perspective. context.Canceled / DeadlineExceeded must NOT
//     reach here under the new design (we pass Background), but we guard
//     defensively in case a future caller wires a cancellable context: if
//     Wait returns one of those errors AND the child is still STILL_ACTIVE,
//     skip MarkExited so IsAlive stays true.
//
// Order matters: MarkExited() MUST be called BEFORE doneCh is signalled so
// the happens-before guarantee documented on ProcessManager.IsAlive is
// preserved (a goroutine selecting on <-Done sees IsAlive == false the
// instant Done unblocks).
func (h *winConsoleHandle) reapProcess() {
	// Use Background — see invariants above. h.ctx is retained on the struct
	// for diagnostics / future request-bound logic but MUST NOT scope the
	// child process lifetime.
	exitCode, waitErr := h.cp.Wait(context.Background())

	// STILL_ACTIVE = 259 (Win32 GetExitCodeProcess return when the process
	// hasn't exited). If Wait was somehow cancelled/deadlined (defensive
	// path; see invariants above), do NOT mark exited — the child is still
	// running and IsAlive must reflect that.
	const stillActive = 259
	processStillRunning := waitErr != nil &&
		(errors.Is(waitErr, context.Canceled) || errors.Is(waitErr, context.DeadlineExceeded)) &&
		int(exitCode) == stillActive

	if h.processHandle != nil && !processStillRunning {
		h.processHandle.ExitCode = int(exitCode)
		h.processHandle.MarkExited()
	}
	if waitErr != nil {
		h.doneCh <- waitErr
	} else if exitCode != 0 {
		h.doneCh <- fmt.Errorf("conpty: child exited with code %d", exitCode)
	} else {
		h.doneCh <- nil
	}
	close(h.doneCh)
}

// conptyWriter adapts io.WriteCloser semantics over a *winConsoleHandle.
// We write to the upstream conpty.ConPty (which writes to child stdin); Close
// is a no-op on the writer because the pseudo-console close is owned by the
// handle and would race with the reader.
type conptyWriter struct {
	h *winConsoleHandle
}

func (w conptyWriter) Write(p []byte) (int, error) {
	if w.h.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	return w.h.cp.Write(p)
}

// Close on the writer is a no-op; the pseudo-console is closed by the handle.
// Closing it twice would invoke ClosePseudoConsole twice, which the kernel
// rejects with ERROR_INVALID_HANDLE. Callers (BaseSession.Close) close stdin
// then close the handle — only the second close should reach the kernel.
//
// KNOWN LIMITATION (CR-004 review feedback — gemini): a no-op Close means
// callers cannot signal EOF on the child's stdin without tearing down the
// pseudo-console as a whole. CLI tools that read stdin to EOF before
// producing output (compilers reading from `<`, formatters in pipe mode,
// `cat`-like tools) will hang until the executor timeout fires.
//
// Why this is acceptable for ConPTY's design contract:
//   - ConPTY exists to serve INTERACTIVE TUI flows (codex chat, aider,
//     claude-code REPL). Those tools read stdin line-by-line and respond
//     after each newline — they do NOT block on EOF.
//   - Batch / pipeline-style invocations (cat-like, compilers, formatters)
//     belong on the pipe executor. The selector routes them there based on
//     the CLI profile's capability flags; ConPTY is not chosen for these.
//   - The upstream library github.com/UserExistsError/conpty does NOT expose
//     a way to close the input pipe independently of the output pipe — both
//     are owned by the single ConPty handle and ClosePseudoConsole tears
//     down both sides. Closing the input alone would require a fork of the
//     library to expose the underlying input HANDLE. This is tracked as a
//     follow-up; for the CR-004 acceptance the limitation is documented and
//     the selector is responsible for not routing EOF-dependent tools here.
//
// Operator action: if a CLI is mistakenly routed to ConPTY and hangs at the
// timeout, surface that in the daemon log (the ConPTY stub branch logged
// every fallback; the same discipline applies to misroutes — capture the
// CLI name + completion-pattern miss in the timeout error).
func (w conptyWriter) Close() error { return nil }

// nopReadCloser is an empty io.ReadCloser used for the Stderr slot — ConPTY
// merges stderr onto the pseudo-console output stream, so Stderr is always
// EOF immediately. Returning a real (closed) pipe here would require an extra
// goroutine; nopReadCloser keeps the contract without the bookkeeping.
type nopReadCloser struct{}

func (nopReadCloser) Read(_ []byte) (int, error) { return 0, io.EOF }
func (nopReadCloser) Close() error               { return nil }

// buildCommandLine assembles a Windows command line string from program +
// args. The upstream library expects a single string (CreateProcess form),
// not a []string; we quote arguments containing spaces to keep them as one
// token on the child side. Mirrors os/exec.Cmd.argv → CommandLine logic in
// the Go stdlib (syscall.makeCmdLine) for the common case.
func buildCommandLine(prog string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteArg(prog))
	for _, a := range args {
		parts = append(parts, quoteArg(a))
	}
	return strings.Join(parts, " ")
}

// quoteArg wraps an argument in double quotes when it contains characters
// CreateProcess would otherwise split on (space, tab, double-quote). Empty
// arg becomes "" so the child sees one argument, not zero. Embedded quotes
// are escaped per CreateProcess rules.
//
// CR-004 review feedback (gemini): Windows command-line parsing
// (CommandLineToArgvW / Microsoft CRT) treats backslashes as escape
// characters ONLY when they precede a double quote. Indiscriminately
// escaping every backslash corrupts file paths — `C:\Temp\file` would
// become `C:\\Temp\\file` and many Windows applications would interpret
// that as a UNC root or fail to find the file. The correct rule (as
// implemented by Go's syscall.makeCmdLine):
//
//   - A backslash preceding a `"` is doubled (so the `"` becomes literal
//     after escaping).
//   - A backslash NOT preceding a `"` is emitted verbatim.
//   - A run of N backslashes followed by a `"` becomes 2N backslashes plus
//     `\"` (escapes both the backslashes and the quote).
//   - A run of N trailing backslashes (closing quote follows) becomes 2N
//     backslashes (the closing quote is the wrapper, not a literal).
//
// This mirrors the algorithm in golang/go/src/syscall/exec_windows.go
// (EscapeArg / makeCmdLine) and avoids the file-path corruption issue.
func quoteArg(a string) string {
	if a == "" {
		return `""`
	}
	if !strings.ContainsAny(a, " \t\"") {
		return a
	}
	var b strings.Builder
	b.Grow(len(a) + 2)
	b.WriteByte('"')
	for i := 0; i < len(a); {
		// Count a run of backslashes.
		backslashes := 0
		for i < len(a) && a[i] == '\\' {
			backslashes++
			i++
		}
		switch {
		case i == len(a):
			// Trailing run before the closing quote — double them so the
			// closing quote we append below is recognized as wrapper, not
			// escaped.
			b.WriteString(strings.Repeat(`\`, backslashes*2))
		case a[i] == '"':
			// Run before a literal quote — double the backslashes and
			// escape the quote with one more.
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteByte('"')
			i++
		default:
			// Backslashes do NOT precede a quote — emit verbatim.
			b.WriteString(strings.Repeat(`\`, backslashes))
			b.WriteByte(a[i])
			i++
		}
	}
	b.WriteByte('"')
	return b.String()
}

// buildEnv produces a flat KEY=VALUE slice from the two SpawnArgs env shapes.
// Returns empty slice when both are empty so the upstream library inherits
// the parent process environment (its documented default when ConPtyEnv is
// not set).
func buildEnv(envList []string, envMap map[string]string) []string {
	if len(envList) > 0 {
		out := make([]string, len(envList))
		copy(out, envList)
		return out
	}
	if len(envMap) == 0 {
		return nil
	}
	out := make([]string, 0, len(envMap))
	for k, v := range envMap {
		out = append(out, k+"="+v)
	}
	return out
}

// buildSyntheticCmd populates an *exec.Cmd with the bare minimum fields the
// existing ProcessHandle consumers (Kill, Cleanup) read: ProcessState is set
// after Wait completes, but Process needs a non-nil PID-bearing handle so
// fallback paths that call Cmd.Process.Kill don't panic.
//
// We do NOT call cmd.Start — the upstream library already started the child.
// The synthetic Cmd is a presentation shell only.
//
// CR-004 review feedback (coderabbit MAJOR, gemini): Cmd.Process MUST be
// populated from the real PID via os.FindProcess. ProcessManager.Kill guards
// `if h.Cmd.Process == nil { return }`, so leaving Process nil meant
// ConPTY-owned processes could not be killed via the standard kill path —
// BaseSession.Close would silently no-op the kill, leaking the child until
// daemon shutdown. os.FindProcess on Windows is cheap (no syscall in the
// success path) and returns a process handle suitable for Process.Kill().
func buildSyntheticCmd(name string, args []string, pid int) *exec.Cmd {
	cmd := exec.Command(name, args...) //nolint:gosec // synthetic, never executed
	if pid > 0 {
		// On Windows, os.FindProcess always returns a non-nil *os.Process
		// without performing a syscall (the OpenProcess call is deferred to
		// Process.Kill / Process.Signal). The error return on Windows is
		// always nil; we still check defensively in case stdlib behaviour
		// changes or this code is exercised from a Windows compatibility
		// layer that diverges. If FindProcess fails, leave Process nil —
		// ProcessManager.Kill will early-return as before, which is the
		// pre-fix behaviour and not a regression.
		if proc, err := os.FindProcess(pid); err == nil {
			cmd.Process = proc
		} else {
			log.Printf("conpty: buildSyntheticCmd: os.FindProcess(%d) failed: %v "+
				"(ProcessManager.Kill will be a no-op for this handle)", pid, err)
		}
	}
	return cmd
}
