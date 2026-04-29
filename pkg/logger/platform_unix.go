//go:build !windows

// Package logger: platform helpers — Unix variant.
package logger

// isUnixOS returns true on Unix-like operating systems (Linux, macOS, etc.).
func isUnixOS() bool { return true }
