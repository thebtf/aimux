package session

import (
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

// Job is the legacy async job row shape persisted in the historical jobs table
// and WAL entries. Runtime task state is owned by loom.Task.
//
// Deprecated: use loom.Task for all new runtime code.
type Job struct {
	ID                string               `json:"id"`
	SessionID         string               `json:"session_id"`
	CLI               string               `json:"cli"`
	Status            types.JobStatus      `json:"status"`
	Progress          string               `json:"progress,omitempty"`
	Content           string               `json:"content,omitempty"`
	ExitCode          int                  `json:"exit_code"`
	Error             *types.TypedError    `json:"error,omitempty"`
	PollCount         int                  `json:"poll_count"`
	Pheromones        map[string]string    `json:"pheromones,omitempty"`
	Pipeline          *types.PipelineStats `json:"pipeline,omitempty"`
	PID               int                  `json:"pid"`
	CreatedAt         time.Time            `json:"created_at"`
	ProgressUpdatedAt time.Time            `json:"progress_updated_at"`
	LastOutputAt      time.Time            `json:"last_output_at,omitempty"`
	CompletedAt       *time.Time           `json:"completed_at,omitempty"`
	LastOutputLine    string               `json:"last_output_line,omitempty"`
	ProgressLines     int64                `json:"progress_lines,omitempty"`
	TenantID          string               `json:"tenant_id,omitempty"`
}
