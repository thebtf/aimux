//go:build !windows

package tenant

import (
	"os"
	"syscall"
)

// reloadSignal is the OS signal used to trigger hot-reload.
// On Unix platforms this is SIGHUP (the conventional reload signal).
var reloadSignal os.Signal = syscall.SIGHUP
