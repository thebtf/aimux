//go:build windows

// Package logger: platform helpers — Windows variant.
package logger

// isUnixOS returns false on Windows (file mode 0600 is not enforced, FR-14).
func isUnixOS() bool { return false }
