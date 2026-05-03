package session

import (
	"encoding/json"
	"fmt"

	"github.com/thebtf/aimux/pkg/types"
)

// RecoverFromWAL replays WAL entries to restore in-memory session state and
// returns legacy job snapshots for import into Loom.
// Running sessions are re-spawned via CLI resume if CLISessionID is available.
func RecoverFromWAL(walPath string, sessions *Registry) ([]*Job, error) {
	entries, err := Replay(walPath)
	if err != nil {
		return nil, fmt.Errorf("WAL replay: %w", err)
	}

	if len(entries) == 0 {
		return nil, nil
	}

	jobs := make(map[string]*Job)
	for _, entry := range entries {
		switch entry.Type {
		case "session_create":
			var sess Session
			if err := json.Unmarshal(entry.Data, &sess); err != nil {
				continue
			}
			sessions.Import(&sess)

		case "session_update":
			var update struct {
				Status       types.SessionStatus `json:"status"`
				CLISessionID string              `json:"cli_session_id"`
				Turns        int                 `json:"turns"`
				Metadata     map[string]any      `json:"metadata"`
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
				if update.Metadata != nil {
					s.Metadata = update.Metadata
				}
			})

		case "job_create":
			var job Job
			if err := json.Unmarshal(entry.Data, &job); err != nil {
				continue
			}
			jobs[job.ID] = &job

		case "job_update":
			var update struct {
				Status  types.JobStatus `json:"status"`
				Content string          `json:"content"`
			}
			if err := json.Unmarshal(entry.Data, &update); err != nil {
				continue
			}
			job, ok := jobs[entry.ID]
			if !ok {
				job = &Job{ID: entry.ID}
				jobs[entry.ID] = job
			}
			if update.Status != "" {
				job.Status = update.Status
			}
			if update.Content != "" {
				job.Content = update.Content
			}
		}
	}

	result := make([]*Job, 0, len(jobs))
	for _, job := range jobs {
		result = append(result, job)
	}
	return result, nil
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
