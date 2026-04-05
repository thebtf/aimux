package session

import (
	"encoding/json"
	"fmt"

	"github.com/thebtf/aimux/pkg/types"
)

// RecoverFromWAL replays WAL entries to restore in-memory state after restart.
// Running sessions are re-spawned via CLI resume if CLISessionID is available.
func RecoverFromWAL(walPath string, sessions *Registry, jobs *JobManager) error {
	entries, err := Replay(walPath)
	if err != nil {
		return fmt.Errorf("WAL replay: %w", err)
	}

	if len(entries) == 0 {
		return nil
	}

	for _, entry := range entries {
		switch entry.Type {
		case "session_create":
			var sess Session
			if err := json.Unmarshal(entry.Data, &sess); err != nil {
				continue
			}
			// Re-register session (status may be stale)
			sessions.sessions[sess.ID] = &sess

		case "session_update":
			var update struct {
				Status       types.SessionStatus `json:"status"`
				CLISessionID string              `json:"cli_session_id"`
				Turns        int                 `json:"turns"`
			}
			if err := json.Unmarshal(entry.Data, &update); err != nil {
				continue
			}
			sessions.Update(entry.ID, func(s *Session) {
				if update.Status != "" {
					s.Status = update.Status
				}
				if update.CLISessionID != "" {
					s.CLISessionID = update.CLISessionID
				}
				if update.Turns > 0 {
					s.Turns = update.Turns
				}
			})

		case "job_create":
			var job Job
			if err := json.Unmarshal(entry.Data, &job); err != nil {
				continue
			}
			jobs.jobs[job.ID] = &job

		case "job_update":
			var update struct {
				Status  types.JobStatus `json:"status"`
				Content string          `json:"content"`
			}
			if err := json.Unmarshal(entry.Data, &update); err != nil {
				continue
			}
			if j := jobs.Get(entry.ID); j != nil {
				if update.Status != "" {
					j.Status = update.Status
				}
				if update.Content != "" {
					j.Content = update.Content
				}
			}
		}
	}

	return nil
}

// SessionsForResume returns sessions that were running at crash time
// and have CLI session IDs for resume.
func SessionsForResume(sessions *Registry) []*Session {
	var resumable []*Session
	for _, sess := range sessions.List(types.SessionStatusRunning) {
		if sess.CLISessionID != "" {
			resumable = append(resumable, sess)
		}
	}
	return resumable
}
