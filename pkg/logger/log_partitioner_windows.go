//go:build windows

// Package logger: createFileIfNotExists — Windows implementation.
// Windows does not support Unix file mode bits; we create the file without
// mode enforcement (FR-14: mode 0600 is Unix-only).
package logger

import "os"

// createFileIfNotExists creates the file at path if it does not already exist.
// On Windows, mode is passed to os.OpenFile but has no ACL effect (FR-14).
func createFileIfNotExists(path string, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	return f.Close()
}
