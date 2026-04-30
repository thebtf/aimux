package workers

import (
	"context"
	"sync"
	"testing"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/loom/deps"
)

// captureLogger is a deps.Logger that captures the most recent error message
// for test assertions.
type captureLogger struct {
	mu  sync.Mutex
	msg string
}

func (l *captureLogger) DebugContext(_ context.Context, _ string, _ ...any) {}
func (l *captureLogger) InfoContext(_ context.Context, _ string, _ ...any)  {}
func (l *captureLogger) WarnContext(_ context.Context, _ string, _ ...any)  {}
func (l *captureLogger) ErrorContext(_ context.Context, msg string, _ ...any) {
	l.mu.Lock()
	l.msg = msg
	l.mu.Unlock()
}

func (l *captureLogger) lastMsg() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.msg
}

// ensure captureLogger satisfies deps.Logger at compile time.
var _ deps.Logger = (*captureLogger)(nil)

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
	cl := &captureLogger{}
	s := &StreamingBase{
		Inner: inner,
		OnLine: func(line string) {
			if line == "line2" {
				panic("deliberate panic on line2")
			}
			got = append(got, line)
		},
		Logger: cl,
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
	if cl.lastMsg() == "" {
		t.Error("expected panic to be logged via deps.Logger")
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

// recordingSink captures (taskID, line) tuples for ProgressSink contract tests.
// It is safe for concurrent use even though current StreamingBase delivery is
// strictly serial — defensive against future parallel-delivery refactors.
type recordingSink struct {
	mu      sync.Mutex
	calls   []sinkCall
	failOn  string
	failErr error
}

type sinkCall struct {
	taskID string
	line   string
}

func (r *recordingSink) AppendProgress(taskID, line string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, sinkCall{taskID: taskID, line: line})
	if r.failOn != "" && line == r.failOn {
		return r.failErr
	}
	return nil
}

func (r *recordingSink) snapshot() []sinkCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sinkCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// TestStreamingBase_Sink_ForwardsAllLines verifies that every line emitted
// by the inner worker is forwarded to the configured ProgressSink with the
// task ID as the correlation key (DEF-13 / AIMUX-16 CR-005).
func TestStreamingBase_Sink_ForwardsAllLines(t *testing.T) {
	inner := &stubWorker{result: &loom.WorkerResult{Content: "alpha\nbeta\ngamma"}}
	sink := &recordingSink{}
	s := &StreamingBase{Inner: inner, Sink: sink}
	task := &loom.Task{ID: "task-sink-forward"}
	if _, err := s.Execute(context.Background(), task); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	calls := sink.snapshot()
	if len(calls) != 3 {
		t.Fatalf("sink calls = %d; want 3 (one per line)", len(calls))
	}
	want := []string{"alpha", "beta", "gamma"}
	for i, c := range calls {
		if c.taskID != task.ID {
			t.Errorf("calls[%d].taskID = %q; want %q", i, c.taskID, task.ID)
		}
		if c.line != want[i] {
			t.Errorf("calls[%d].line = %q; want %q", i, c.line, want[i])
		}
	}
}

// TestStreamingBase_Sink_NilTaskID_NoForward verifies that lines are NOT
// forwarded when task or task.ID is empty — the sink would have no usable
// correlation key. Pure defensive guard; engine dispatch always gives us a
// real ID, but a misconfigured caller must not surface as a SQL error from
// the sink.
func TestStreamingBase_Sink_EmptyTaskID_NoForward(t *testing.T) {
	inner := &stubWorker{result: &loom.WorkerResult{Content: "alpha"}}
	sink := &recordingSink{}
	s := &StreamingBase{Inner: inner, Sink: sink}
	task := &loom.Task{} // empty ID
	if _, err := s.Execute(context.Background(), task); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := sink.snapshot(); len(got) != 0 {
		t.Errorf("sink received %d calls for empty task.ID; want 0 (defensive guard)", len(got))
	}
}

// TestStreamingBase_Sink_ErrorDoesNotAbort verifies that a sink that returns
// an error on one line does not abort delivery of subsequent lines — the
// inner worker's result must still be returned unchanged. Progress is
// best-effort by design.
func TestStreamingBase_Sink_ErrorDoesNotAbort(t *testing.T) {
	inner := &stubWorker{result: &loom.WorkerResult{Content: "alpha\nbeta\ngamma"}}
	sink := &recordingSink{
		failOn:  "beta",
		failErr: errSinkBoom,
	}
	cl := &captureLogger{}
	s := &StreamingBase{Inner: inner, Sink: sink, Logger: cl}
	task := &loom.Task{ID: "task-sink-err"}
	result, err := s.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Content != "alpha\nbeta\ngamma" {
		t.Errorf("result.Content = %q; want unchanged", result.Content)
	}
	if got := sink.snapshot(); len(got) != 3 {
		t.Errorf("sink calls = %d; want 3 (delivery must continue past sink error)", len(got))
	}
	if cl.lastMsg() == "" {
		t.Error("sink error should have been logged")
	}
}

// TestStreamingBase_Sink_AndOnLineComposed verifies that Sink and OnLine
// fire side-by-side for every line — neither replaces the other.
func TestStreamingBase_Sink_AndOnLineComposed(t *testing.T) {
	inner := &stubWorker{result: &loom.WorkerResult{Content: "alpha\nbeta"}}
	sink := &recordingSink{}
	var onLineGot []string
	s := &StreamingBase{
		Inner:  inner,
		OnLine: func(line string) { onLineGot = append(onLineGot, line) },
		Sink:   sink,
	}
	task := &loom.Task{ID: "task-sink-and-online"}
	if _, err := s.Execute(context.Background(), task); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := len(onLineGot); got != 2 {
		t.Errorf("OnLine fired %d times; want 2", got)
	}
	if got := len(sink.snapshot()); got != 2 {
		t.Errorf("Sink fired %d times; want 2", got)
	}
}

// errSinkBoom is a sentinel returned by recordingSink.failOn-matching writes
// so tests can distinguish "sink intentionally failed" from "sink had no idea".
var errSinkBoom = sinkErr("sink boom")

type sinkErr string

func (e sinkErr) Error() string { return string(e) }
