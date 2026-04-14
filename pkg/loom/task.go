package loom

import (
	"time"
)

// TaskStatus represents the lifecycle state of a task.
type TaskStatus string

const (
	TaskStatusPending     TaskStatus = "pending"
	TaskStatusDispatched  TaskStatus = "dispatched"
	TaskStatusRunning     TaskStatus = "running"
	TaskStatusCompleted   TaskStatus = "completed"
	TaskStatusFailed      TaskStatus = "failed"
	TaskStatusFailedCrash TaskStatus = "failed_crash"
	TaskStatusRetrying    TaskStatus = "retrying"
)

// validTransitions defines the state machine.
// State machine from spec:
//
//	pending → dispatched → running → completed (terminal)
//	                        │     → failed (terminal)
//	                        │     → retrying → dispatched (loop, max 2)
//	[crash restart]
//	dispatched → failed_crash (terminal)
//	running → failed_crash (terminal)
var validTransitions = map[TaskStatus][]TaskStatus{
	TaskStatusPending:    {TaskStatusDispatched},
	TaskStatusDispatched: {TaskStatusRunning, TaskStatusFailedCrash},
	TaskStatusRunning:    {TaskStatusCompleted, TaskStatusFailed, TaskStatusRetrying, TaskStatusFailedCrash},
	TaskStatusRetrying:   {TaskStatusDispatched},
}

// CanTransitionTo checks if transitioning from current status to target is valid.
func (s TaskStatus) CanTransitionTo(target TaskStatus) bool {
	targets, ok := validTransitions[s]
	if !ok {
		return false // terminal states have no valid transitions
	}
	for _, t := range targets {
		if t == target {
			return true
		}
	}
	return false
}

// IsTerminal returns true if the status is a terminal state.
func (s TaskStatus) IsTerminal() bool {
	switch s {
	case TaskStatusCompleted, TaskStatusFailed, TaskStatusFailedCrash:
		return true
	}
	return false
}

// WorkerType identifies which worker handles a task.
type WorkerType string

const (
	WorkerTypeCLI          WorkerType = "cli"
	WorkerTypeThinker      WorkerType = "thinker"
	WorkerTypeInvestigator WorkerType = "investigator"
	WorkerTypeOrchestrator WorkerType = "orchestrator"
)

// Task represents a unit of work managed by LoomEngine.
type Task struct {
	ID           string            `json:"id"`
	Status       TaskStatus        `json:"status"`
	WorkerType   WorkerType        `json:"worker_type"`
	ProjectID    string            `json:"project_id"`
	Prompt       string            `json:"prompt"`
	CWD          string            `json:"cwd,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	CLI          string            `json:"cli,omitempty"`
	Role         string            `json:"role,omitempty"`
	Model        string            `json:"model,omitempty"`
	Effort       string            `json:"effort,omitempty"`
	Timeout      int               `json:"timeout,omitempty"`
	Metadata     map[string]any    `json:"metadata,omitempty"`
	Result       string            `json:"result,omitempty"`
	Error        string            `json:"error,omitempty"`
	Retries      int               `json:"retries"`
	CreatedAt    time.Time         `json:"created_at"`
	DispatchedAt *time.Time        `json:"dispatched_at,omitempty"`
	CompletedAt  *time.Time        `json:"completed_at,omitempty"`
}

// TaskRequest is the input for submitting a new task.
type TaskRequest struct {
	WorkerType WorkerType
	ProjectID  string
	Prompt     string
	CWD        string
	Env        map[string]string
	CLI        string
	Role       string
	Model      string
	Effort     string
	Timeout    int
	Metadata   map[string]any
}
