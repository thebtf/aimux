//go:build windows

package executor

// killUnix is a no-op on Windows; Kill() handles the platform-specific logic directly.
func killUnix(_ *ProcessHandle) {}
