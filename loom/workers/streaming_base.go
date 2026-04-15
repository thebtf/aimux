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

// StreamingBase wraps an inner Worker and adds line-by-line progress callbacks.
// The inner worker runs to completion; StreamingBase tails its captured output
// and fires ProgressHandler for each line.
//
// This is useful for any subprocess-based worker whose output is already in
// WorkerResult.Content — after the worker returns, each line of Content is
// delivered to ProgressHandler in order, then the original result is returned
// unchanged.
type StreamingBase struct {
	Inner  loom.Worker
	OnLine ProgressHandler
	// Logger receives error messages on ProgressHandler panics.
	// If nil, a noop logger is used (panics are silently discarded).
	Logger deps.Logger
}

// Execute runs the inner worker and fires ProgressHandler for each line of output.
// Panics in ProgressHandler are recovered (consistent with FR-14 subscriber isolation).
func (s *StreamingBase) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	result, err := s.Inner.Execute(ctx, task)
	if result == nil || s.OnLine == nil {
		return result, err
	}

	lines := strings.Split(result.Content, "\n")
	for i, line := range lines {
		// Skip a trailing empty string produced by a final newline.
		if i == len(lines)-1 && line == "" {
			break
		}
		s.deliver(line)
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
