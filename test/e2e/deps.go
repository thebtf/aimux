//go:build tools

// Package deps pins test-only dependencies so go mod tidy does not prune them.
// fsnotify is imported by T011 (test/e2e/shim_startup_test.go) in Phase 3.
// The tools build tag prevents this file from appearing in production builds.
package deps

import _ "github.com/fsnotify/fsnotify"
