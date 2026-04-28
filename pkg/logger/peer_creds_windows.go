//go:build windows

// Package logger: PeerPid implementation for Windows named pipes.
// Uses GetNamedPipeClientProcessId (T004 — AIMUX-11 Phase 1).
package logger

import (
	"fmt"
	"net"

	"golang.org/x/sys/windows"
)

// PeerPid returns the process ID of the client on the other end of the given
// connection. On Windows the connection must be a named-pipe handle.
//
// Security note (FR-12): the returned PID is obtained from the OS, not from
// the log entry envelope. Envelope-level pid claims are ignored.
func PeerPid(conn net.Conn) (int, error) {
	type hasSyscallConn interface {
		SyscallConn() (interface{ Control(func(uintptr)) error }, error)
	}

	// Unwrap to find the underlying Handle.
	// go-winio net.Conn wraps the pipe; the raw Handle is accessible via
	// the internal *win32Pipe.handle field — but we cannot reach it directly.
	// Instead we use the file descriptor approach via net.Conn.File() where
	// available, or fall back to the go-winio PipeConn interface.
	type pipeConn interface {
		// go-winio exposes *win32Pipe which implements net.Conn but has no
		// exported Handle field. We cast to interface{} and type-assert for the
		// concrete go-winio type's exported method.
		GetHandle() windows.Handle
	}

	if pc, ok := conn.(pipeConn); ok {
		h := pc.GetHandle()
		var pid uint32
		if err := windows.GetNamedPipeClientProcessId(h, &pid); err != nil {
			return 0, fmt.Errorf("PeerPid: GetNamedPipeClientProcessId: %w", err)
		}
		return int(pid), nil
	}

	// Fallback: muxcore wraps the raw handle in its own conn type.
	// Try the rawConn approach.
	type rawHandleConn interface {
		RawHandle() windows.Handle
	}
	if rc, ok := conn.(rawHandleConn); ok {
		h := rc.RawHandle()
		var pid uint32
		if err := windows.GetNamedPipeClientProcessId(h, &pid); err != nil {
			return 0, fmt.Errorf("PeerPid: GetNamedPipeClientProcessId (raw): %w", err)
		}
		return int(pid), nil
	}

	return 0, fmt.Errorf("PeerPid: connection type %T does not expose a named-pipe handle", conn)
}
