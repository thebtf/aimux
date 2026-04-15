package loom

import "context"

// Worker executes a task and returns the result.
type Worker interface {
	Execute(ctx context.Context, task *Task) (*WorkerResult, error)
	Type() WorkerType
}

// WorkerResult holds the output from a worker execution.
type WorkerResult struct {
	Content    string         `json:"content"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	DurationMS int64          `json:"duration_ms"`
}
