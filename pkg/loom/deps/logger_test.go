package deps_test

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/loom/deps"
)

// TestNoopLogger_DoesNotPanic verifies that every method on the noop logger
// can be called without panicking.
func TestNoopLogger_DoesNotPanic(t *testing.T) {
	l := deps.NoopLogger()
	ctx := context.Background()

	l.DebugContext(ctx, "debug msg", "key", "val")
	l.InfoContext(ctx, "info msg")
	l.WarnContext(ctx, "warn msg", "k", 1)
	l.ErrorContext(ctx, "error msg")
}

// TestNoopLogger_SatisfiesInterface verifies that the returned value is
// assignable to the Logger interface, confirming the injection boundary works.
func TestNoopLogger_SatisfiesInterface(t *testing.T) {
	var l deps.Logger = deps.NoopLogger()
	if l == nil {
		t.Fatal("NoopLogger returned nil")
	}
	// Call through the interface type to confirm dispatch reaches the concrete impl.
	ctx := context.Background()
	l.InfoContext(ctx, "through-interface")
}
