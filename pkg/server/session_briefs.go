package server

import (
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/types"
)

// SessionBrief is the per-row summary for sessions/list responses.
// Full session data is available via sessions(action=info).
type SessionBrief struct {
	ID        string              `json:"id"`
	Status    types.SessionStatus `json:"status"`
	CLI       string              `json:"cli"`
	CreatedAt time.Time           `json:"created_at"`
	JobCount  int                 `json:"job_count"`
}

// LoomTaskBrief is the per-row summary for loom tasks in sessions/list responses.
// ProgressLineCount is always 0 in Phase 3 (loom.Task has no Progress field yet).
type LoomTaskBrief struct {
	ID                string          `json:"id"`
	Status            loom.TaskStatus `json:"status"`
	Kind              string          `json:"kind"`
	CreatedAt         time.Time       `json:"created_at"`
	ProgressLineCount int64           `json:"progress_line_count"`
}

// JobBrief is the per-job summary for sessions/info responses.
// Content body is omitted by default; use include_content=true to restore it.
type JobBrief struct {
	ID            string          `json:"id"`
	Status        types.JobStatus `json:"status"`
	Progress      string          `json:"progress,omitempty"`
	ContentLength int             `json:"content_length"`
	Content       string          `json:"content,omitempty"`
}
