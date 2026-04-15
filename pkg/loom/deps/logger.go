// Package deps contains injectable dependency interfaces for LoomEngine.
// Each interface has a default production implementation and a lightweight
// stub for deterministic tests.
package deps

import (
	"context"
	"io"
	"log/slog"
)

// Logger is the subset of *slog.Logger methods that LoomEngine uses.
// A real *slog.Logger satisfies this interface without an adapter.
type Logger interface {
	DebugContext(ctx context.Context, msg string, args ...any)
	InfoContext(ctx context.Context, msg string, args ...any)
	WarnContext(ctx context.Context, msg string, args ...any)
	ErrorContext(ctx context.Context, msg string, args ...any)
}

// NoopLogger returns a Logger backed by slog.NewTextHandler writing to io.Discard.
// All log records are discarded. This is the default when no logger is injected.
func NoopLogger() Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
