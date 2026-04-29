//go:build !windows && !linux && !darwin

// Package logger: PeerPid fallback for unsupported platforms.
// Returns error so callers fall back to the [shim-?marker] path per FR-12.
package logger

import (
	"fmt"
	"net"
	"runtime"
)

// PeerPid is unsupported on this platform. Callers must handle the error
// per FR-12 by falling back to envelope-derived markers.
func PeerPid(conn net.Conn) (int, error) {
	return 0, fmt.Errorf("PeerPid: unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
}
