//go:build windows

package tenant

import (
	"os"
	"syscall"
)

// reloadSignal is the OS signal used to trigger hot-reload.
// On Windows, SIGHUP is not available; we use SIGTERM as a best-effort substitute.
// Operators on Windows should restart the daemon directly rather than relying on
// signal-based hot-reload (FR-14 acknowledged limitation).
var reloadSignal os.Signal = syscall.SIGTERM
