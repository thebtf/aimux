package workers

import (
	"context"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/loom/deps"
)

// ProgressHandler is called synchronously for each line of output captured
// during Execute. Panics in ProgressHandler are recovered and logged — they
// do NOT affect delivery of subsequent lines or the inner worker's outcome.
type ProgressHandler func(line string)

// ProgressSink is the minimal interface needed by StreamingBase to forward
// progress lines into a persistent store (typically *loom.LoomEngine). It is
// declared as an interface rather than a concrete type so workers that do
// not need persistence can leave the field nil and so tests can substitute
// an in-memory recorder.
//
// AppendProgress is invoked once per delivered line on the dispatch
// goroutine. Implementations MUST be safe for concurrent use across tasks
// (different task IDs may race) and SHOULD return quickly — slow sinks
// block the streaming loop and delay the inner worker's result.
type ProgressSink interface {
	AppendProgress(taskID, line string) error
}

// StreamingBase wraps an inner Worker and adds line-by-line progress callbacks.
// The inner worker runs to completion; StreamingBase tails its captured output
// and fires ProgressHandler for each line.
//
// This is useful for any subprocess-based worker whose output is already in
// WorkerResult.Content — after the worker returns, each line of Content is
// delivered to ProgressHandler in order, then the original result is returned
// unchanged.
//
// When Sink is non-nil, every delivered line is also forwarded to the sink
// alongside the user-supplied OnLine callback (DEF-13 / AIMUX-16 CR-005).
// Sink errors are logged and do NOT abort streaming or affect the inner
// worker's outcome — progress is best-effort.
type StreamingBase struct {
	Inner  loom.Worker
	OnLine ProgressHandler
	// Sink, when non-nil, receives every delivered line via AppendProgress
	// using the running task's ID. It is independent of OnLine — both fire
	// for each line. Workers that opt into the loom progress signal pass
	// the engine here; workers that only need a callback leave it nil.
	Sink ProgressSink
	// Logger receives error messages on ProgressHandler panics.
	// If nil, a noop logger is used (panics are silently discarded).
	Logger deps.Logger
}

// Execute runs the inner worker and fires ProgressHandler for each line of output.
// Panics in ProgressHandler are recovered (consistent with FR-14 subscriber isolation).
//
// When Sink is non-nil, every delivered line is also forwarded to the sink
// using task.ID as the correlation key. Sink errors are logged but do not
// abort streaming or affect the inner worker's result — progress is
// best-effort by design (DEF-13 / AIMUX-16 CR-005).
func (s *StreamingBase) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	result, err := s.Inner.Execute(ctx, task)
	if result == nil {
		return result, err
	}
	if s.OnLine == nil && s.Sink == nil {
		return result, err
	}

	lines := strings.Split(result.Content, "\n")
	for i, line := range lines {
		// Skip a trailing empty string produced by a final newline.
		if i == len(lines)-1 && line == "" {
			break
		}
		if s.OnLine != nil {
			s.deliver(line)
		}
		if s.Sink != nil && task != nil && task.ID != "" {
			s.appendToSink(task.ID, line)
		}
	}
	return result, err
}

// Type delegates to the inner worker.
func (s *StreamingBase) Type() loom.WorkerType {
	return s.Inner.Type()
}

// deliver calls OnLine with panic recovery so a panicking handler cannot
// break the streaming loop or affect the inner worker's result.
func (s *StreamingBase) deliver(line string) {
	defer func() {
		if r := recover(); r != nil {
			l := s.Logger
			if l == nil {
				l = deps.NoopLogger()
			}
			l.ErrorContext(context.Background(), "streaming base: progress handler panic",
				"module", "loom",
				"error", fmt.Sprintf("%v", r),
			)
		}
	}()
	s.OnLine(line)
}

// appendToSink forwards a delivered line to the configured ProgressSink with
// panic + error recovery. Sink failures are logged at error level but do
// NOT propagate — progress recording is best-effort and must never break
// the streaming loop or affect the inner worker's result.
func (s *StreamingBase) appendToSink(taskID, line string) {
	l := s.Logger
	if l == nil {
		l = deps.NoopLogger()
	}
	defer func() {
		if r := recover(); r != nil {
			l.ErrorContext(context.Background(), "streaming base: progress sink panic",
				"module", "loom",
				"task_id", taskID,
				"error", fmt.Sprintf("%v", r),
			)
		}
	}()
	if err := s.Sink.AppendProgress(taskID, line); err != nil {
		l.ErrorContext(context.Background(), "streaming base: progress sink append failed",
			"module", "loom",
			"task_id", taskID,
			"error", err.Error(),
		)
	}
}
