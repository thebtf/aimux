//go:build !windows

// Package logger: createFileIfNotExists — Unix implementation.
// Creates the file with mode 0600 (NFR-11, FR-14).
package logger

import "os"

// createFileIfNotExists creates the file at path with the given mode if it does
// not already exist. On Unix this enforces mode 0600 (NFR-11). No-op if the file
// already exists.
func createFileIfNotExists(path string, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	return f.Close()
}
