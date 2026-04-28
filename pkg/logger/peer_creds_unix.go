//go:build linux

// Package logger: PeerPid implementation for Linux Unix-domain sockets.
// Uses SO_PEERCRED / getsockopt (T005 — AIMUX-11 Phase 1).
package logger

import (
	"fmt"
	"net"
	"syscall"
)

// PeerPid returns the process ID of the client on the other end of the given
// Unix-domain socket connection using SO_PEERCRED (Linux-specific).
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

	var ucred *syscall.Ucred
	var credsErr error

	controlErr := rawConn.Control(func(fd uintptr) {
		ucred, credsErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if controlErr != nil {
		return 0, fmt.Errorf("PeerPid: Control: %w", controlErr)
	}
	if credsErr != nil {
		return 0, fmt.Errorf("PeerPid: SO_PEERCRED: %w", credsErr)
	}
	return int(ucred.Pid), nil
}
