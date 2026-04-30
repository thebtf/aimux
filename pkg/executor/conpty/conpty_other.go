//go:build !windows

// Package conpty — non-Windows stub.
//
// Win32 ConPTY (CreatePseudoConsole) is a Windows-only API. On any other
// platform every entrypoint returns ErrPlatformUnsupported and probeConPTY
// returns false. The selector skips this executor in favour of pty/pipe.
package conpty

import (
	"context"
	"errors"
	"io"

	"github.com/thebtf/aimux/pkg/executor"
)

// ErrPlatformUnsupported is returned by every ConPTY entrypoint on non-Windows.
//
// Win32 ConPTY (CreatePseudoConsole / ResizePseudoConsole / ClosePseudoConsole)
// is a Windows-only kernel API; there is no portable equivalent. AIMUX-16 CR-004
// makes the failure mode explicit instead of pretending availability and silently
// downgrading to pipe — silent downgrade was the bug being fixed.
var ErrPlatformUnsupported = errors.New(
	"conpty: not supported on this platform — Win32 CreatePseudoConsole is Windows-only")

// probeConPTY always returns false on non-Windows.
//
// Win10 1809+ on Windows is the only environment where ConPTY exists. Returning
// false here causes Available() to be false, the executor selector skips the
// ConPTY adapter, and pty/pipe paths handle Linux/macOS. NO silent downgrade —
// the selector receives an explicit unavailable signal, not a working executor
// that internally fakes its work.
func probeConPTY() bool {
	return false
}

// openWindowsConPTY is the platform-specific session opener. On non-Windows it
// always returns ErrPlatformUnsupported. The caller (conpty.Executor.Start /
// .Run) gates this call on Available(); the error path is a defensive guard
// preventing accidental invocation from non-gated code.
func openWindowsConPTY(_ context.Context, _ openParams) (*winConsoleHandle, error) {
	return nil, ErrPlatformUnsupported
}

// winConsoleHandle is a placeholder type on non-Windows so the cross-platform
// conpty.go file references a defined type. It carries no state and every
// method returns ErrPlatformUnsupported.
type winConsoleHandle struct{}

// Stdin always returns an unsupported writer on non-Windows.
func (*winConsoleHandle) Stdin() io.WriteCloser { return unsupportedRW{} }

// Stdout always returns an unsupported reader on non-Windows.
func (*winConsoleHandle) Stdout() io.ReadCloser { return unsupportedRW{} }

// ProcessHandle returns nil on non-Windows; callers gate on Available().
func (*winConsoleHandle) ProcessHandle() *executor.ProcessHandle { return nil }

// Resize is a no-op on non-Windows.
func (*winConsoleHandle) Resize(_, _ int) error { return ErrPlatformUnsupported }

// Close is a no-op on non-Windows.
func (*winConsoleHandle) Close() error { return nil }

// unsupportedRW is an io.ReadCloser + io.WriteCloser that always returns
// ErrPlatformUnsupported. Used only on non-Windows where winConsoleHandle is
// a placeholder type.
type unsupportedRW struct{}

func (unsupportedRW) Read(_ []byte) (int, error)  { return 0, ErrPlatformUnsupported }
func (unsupportedRW) Write(_ []byte) (int, error) { return 0, ErrPlatformUnsupported }
func (unsupportedRW) Close() error                { return nil }
