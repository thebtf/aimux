package workers

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/loom"
)

// stubWorker is a minimal loom.Worker that returns a fixed result.
type stubWorker struct {
	result *loom.WorkerResult
	err    error
}

func (w *stubWorker) Execute(_ context.Context, _ *loom.Task) (*loom.WorkerResult, error) {
	return w.result, w.err
}

func (w *stubWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }

// TestStreamingBase_LinesDeliveredInOrder verifies all lines are delivered in order.
func TestStreamingBase_LinesDeliveredInOrder(t *testing.T) {
	inner := &stubWorker{result: &loom.WorkerResult{Content: "line1\nline2\nline3"}}
	var got []string
	s := &StreamingBase{
		Inner:  inner,
		OnLine: func(line string) { got = append(got, line) },
	}
	task := &loom.Task{ID: "s1"}
	result, err := s.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result.Content != "line1\nline2\nline3" {
		t.Errorf("result.Content must be unchanged: got %q", result.Content)
	}
	want := []string{"line1", "line2", "line3"}
	if len(got) != len(want) {
		t.Fatalf("expected %d lines, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("line[%d]: want %q, got %q", i, w, got[i])
		}
	}
}

// TestStreamingBase_PanicIsolation verifies that a panicking handler on line 2
// does not prevent delivery of other lines and the result is preserved.
func TestStreamingBase_PanicIsolation(t *testing.T) {
	inner := &stubWorker{result: &loom.WorkerResult{Content: "line1\nline2\nline3"}}
	var got []string
	var loggedPanic string
	s := &StreamingBase{
		Inner: inner,
		OnLine: func(line string) {
			if line == "line2" {
				panic("deliberate panic on line2")
			}
			got = append(got, line)
		},
		Logger: func(msg string) { loggedPanic = msg },
	}
	task := &loom.Task{ID: "s2"}
	result, err := s.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	// Result must be the original unchanged result.
	if result.Content != "line1\nline2\nline3" {
		t.Errorf("result.Content must be unchanged, got %q", result.Content)
	}
	// Lines 1 and 3 must have been delivered despite the panic on line 2.
	want := []string{"line1", "line3"}
	if len(got) != len(want) {
		t.Fatalf("expected lines %v, got %v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("line[%d]: want %q, got %q", i, w, got[i])
		}
	}
	// Panic should have been logged.
	if loggedPanic == "" {
		t.Error("expected panic to be logged")
	}
}

// TestStreamingBase_EmptyOutput verifies zero handler calls and result preserved.
func TestStreamingBase_EmptyOutput(t *testing.T) {
	inner := &stubWorker{result: &loom.WorkerResult{Content: ""}}
	callCount := 0
	s := &StreamingBase{
		Inner:  inner,
		OnLine: func(_ string) { callCount++ },
	}
	task := &loom.Task{ID: "s3"}
	result, err := s.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result.Content != "" {
		t.Errorf("result.Content must be empty, got %q", result.Content)
	}
	if callCount != 0 {
		t.Errorf("expected 0 handler calls for empty output, got %d", callCount)
	}
}

// TestStreamingBase_NilOnLine verifies that a nil OnLine is safe.
func TestStreamingBase_NilOnLine(t *testing.T) {
	inner := &stubWorker{result: &loom.WorkerResult{Content: "data"}}
	s := &StreamingBase{Inner: inner, OnLine: nil}
	task := &loom.Task{ID: "s4"}
	result, err := s.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result.Content != "data" {
		t.Errorf("unexpected content: %q", result.Content)
	}
}
