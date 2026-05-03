package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/util"
)

const legacyJobRestartError = "process restarted"

func (s *Server) importLegacyJobsFromSQLite() (int, error) {
	if s == nil || s.store == nil || s.loom == nil {
		return 0, nil
	}
	jobs, err := s.store.LoadLegacyJobs()
	if err != nil {
		return 0, err
	}
	return s.importLegacyJobs(jobs)
}

func (s *Server) importLegacyJobs(jobs []*session.Job) (int, error) {
	if s == nil || s.loom == nil || len(jobs) == 0 {
		return 0, nil
	}
	now := time.Now().UTC()
	imported := 0
	for _, job := range jobs {
		task := legacyJobToLoomTask(job, now)
		if task == nil {
			continue
		}
		if err := s.loom.Import(task); err != nil {
			return imported, fmt.Errorf("import legacy job %s: %w", job.ID, err)
		}
		imported++
	}
	return imported, nil
}

func legacyJobToLoomTask(job *session.Job, now time.Time) *loom.Task {
	if job == nil || job.ID == "" {
		return nil
	}
	status, errMsg, completedAt := normalizeLegacyJobStatus(job, now)
	progressTail, progressLines := legacyProgressSummary(job)
	createdAt := job.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	progressUpdatedAt := legacyProgressUpdatedAt(job)
	tenantID := job.TenantID
	if tenantID == "" {
		tenantID = tenant.LegacyDefault
	}
	projectID := job.SessionID
	if projectID == "" {
		projectID = "legacy-jobs"
	}

	metadata := map[string]any{
		"session_id":         job.SessionID,
		"legacy_job":         true,
		"legacy_status":      string(job.Status),
		"legacy_poll_count":  job.PollCount,
		"legacy_exit_code":   job.ExitCode,
		"legacy_pid":         job.PID,
		"legacy_imported_at": now.Format(time.RFC3339),
	}
	if job.Progress != "" {
		metadata["legacy_progress"] = job.Progress
	}
	if len(job.Pheromones) > 0 {
		metadata["legacy_pheromones"] = job.Pheromones
	}
	if job.Pipeline != nil {
		metadata["legacy_pipeline"] = job.Pipeline
	}

	return &loom.Task{
		ID:                job.ID,
		Status:            status,
		WorkerType:        loom.WorkerTypeCLI,
		ProjectID:         projectID,
		TenantID:          tenantID,
		Prompt:            "legacy async job",
		CLI:               job.CLI,
		Metadata:          metadata,
		Result:            job.Content,
		Error:             errMsg,
		CreatedAt:         createdAt,
		CompletedAt:       completedAt,
		LastOutputLine:    progressTail,
		ProgressLines:     progressLines,
		ProgressUpdatedAt: progressUpdatedAt,
	}
}

func normalizeLegacyJobStatus(job *session.Job, now time.Time) (loom.TaskStatus, string, *time.Time) {
	switch job.Status {
	case types.JobStatusCompleted:
		return loom.TaskStatusCompleted, "", legacyCompletedAt(job, now)
	case types.JobStatusFailed:
		return loom.TaskStatusFailed, legacyErrorMessage(job), legacyCompletedAt(job, now)
	case types.JobStatusAborted:
		msg := legacyErrorMessage(job)
		if msg == "" {
			msg = legacyJobRestartError
		}
		return loom.TaskStatusFailed, msg, legacyCompletedAt(job, now)
	default:
		return loom.TaskStatusFailed, legacyJobRestartError, &now
	}
}

func legacyCompletedAt(job *session.Job, fallback time.Time) *time.Time {
	if job.CompletedAt != nil {
		completedAt := job.CompletedAt.UTC()
		return &completedAt
	}
	return &fallback
}

func legacyErrorMessage(job *session.Job) string {
	if job.Error == nil {
		return ""
	}
	return job.Error.Error()
}

func legacyProgressUpdatedAt(job *session.Job) *time.Time {
	if !job.LastOutputAt.IsZero() {
		lastOutputAt := job.LastOutputAt.UTC()
		return &lastOutputAt
	}
	if !job.ProgressUpdatedAt.IsZero() {
		progressUpdatedAt := job.ProgressUpdatedAt.UTC()
		return &progressUpdatedAt
	}
	return nil
}

func legacyProgressSummary(job *session.Job) (string, int64) {
	if job.LastOutputLine != "" || job.ProgressLines > 0 {
		return job.LastOutputLine, job.ProgressLines
	}
	if job.Progress == "" {
		return "", 0
	}
	return util.TruncateUTF8(lastNonEmptyLegacyProgressLine(job.Progress), 100), int64(strings.Count(job.Progress, "\n") + 1)
}

func lastNonEmptyLegacyProgressLine(progress string) string {
	for {
		idx := strings.LastIndex(progress, "\n")
		line := progress[idx+1:]
		if strings.TrimSpace(line) != "" {
			return line
		}
		if idx == -1 {
			return ""
		}
		progress = progress[:idx]
	}
}
