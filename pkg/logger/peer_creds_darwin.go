//go:build darwin

// Package logger: PeerPid implementation for macOS Unix-domain sockets.
// Uses LOCAL_PEERPID via getsockopt (T005 — AIMUX-11 Phase 1).
//
// macOS does not expose SO_PEERCRED (Linux-only). The portable equivalent is
// LOCAL_PEERPID, which returns just the peer PID — sufficient for FR-12.
package logger

import (
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// PeerPid returns the process ID of the client on the other end of the given
// Unix-domain socket connection using LOCAL_PEERPID (macOS-specific).
//
// Security note (FR-12): the returned PID is obtained from the OS, not from
// the log entry envelope. Envelope-level pid claims are ignored.
func PeerPid(conn net.Conn) (int, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return 0, fmt.Errorf("PeerPid: connection type %T does not implement syscall.Conn", conn)
	}

	rawConn, err := sc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("PeerPid: SyscallConn: %w", err)
	}

	var pid int
	var pidErr error

	controlErr := rawConn.Control(func(fd uintptr) {
		pid, pidErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	})
	if controlErr != nil {
		return 0, fmt.Errorf("PeerPid: Control: %w", controlErr)
	}
	if pidErr != nil {
		return 0, fmt.Errorf("PeerPid: LOCAL_PEERPID: %w", pidErr)
	}
	return pid, nil
}
