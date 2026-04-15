package loom

import (
	"context"
	"testing"
)

func TestWithRequestID_RoundTrip(t *testing.T) {
	ctx := context.Background()
	want := "req-abc-123"
	ctx = WithRequestID(ctx, want)

	got := RequestIDFrom(ctx)
	if got != want {
		t.Errorf("RequestIDFrom: got %q, want %q", got, want)
	}
}

func TestRequestIDFrom_MissingKey_ReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	got := RequestIDFrom(ctx)
	if got != "" {
		t.Errorf("RequestIDFrom on empty context: got %q, want \"\"", got)
	}
}

func TestRequestIDFrom_WrongType_ReturnsEmpty(t *testing.T) {
	// Inject an integer under RequestIDKey — should not panic, just return "".
	ctx := context.WithValue(context.Background(), RequestIDKey, 42)
	got := RequestIDFrom(ctx)
	if got != "" {
		t.Errorf("RequestIDFrom with wrong type: got %q, want \"\"", got)
	}
}

func TestWithRequestID_EmptyString_RoundTrip(t *testing.T) {
	ctx := WithRequestID(context.Background(), "")
	got := RequestIDFrom(ctx)
	if got != "" {
		t.Errorf("RequestIDFrom with empty string: got %q, want \"\"", got)
	}
}

func TestWithRequestID_OverwritesPrevious(t *testing.T) {
	ctx := WithRequestID(context.Background(), "first")
	ctx = WithRequestID(ctx, "second")
	got := RequestIDFrom(ctx)
	if got != "second" {
		t.Errorf("RequestIDFrom after double set: got %q, want \"second\"", got)
	}
}
