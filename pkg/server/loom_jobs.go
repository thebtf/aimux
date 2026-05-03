package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/guidance"
	"github.com/thebtf/aimux/pkg/server/budget"
	"github.com/thebtf/aimux/pkg/types"
)

func (s *Server) getLoomTask(ctx context.Context, taskID string) (*loom.Task, bool, error) {
	if s == nil || s.loom == nil {
		return nil, false, nil
	}
	if scoped, ok := TenantScopedLoomFromContext(ctx); ok {
		task, err := scoped.Get(taskID)
		if err == nil {
			return task, true, nil
		}
		if errors.Is(err, loom.ErrTaskNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	task, err := s.loom.Get(taskID)
	if err == nil {
		return task, true, nil
	}
	if errors.Is(err, loom.ErrTaskNotFound) || errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return nil, false, err
}

func (s *Server) appendLoomProgressIfTask(taskID, line string) bool {
	if s == nil || s.loom == nil {
		return false
	}
	if _, err := s.loom.Get(taskID); err != nil {
		return false
	}
	if err := s.loom.AppendProgress(taskID, line); err != nil {
		if s.log != nil {
			s.log.Warn("loom progress append failed for task %s: %v", taskID, err)
		}
	}
	return true
}

func (s *Server) listLoomTasksForContext(ctx context.Context, statuses ...loom.TaskStatus) ([]*loom.Task, error) {
	if s == nil || s.loom == nil {
		return nil, nil
	}
	projectID := projectIDFromContext(ctx)
	if scoped, ok := TenantScopedLoomFromContext(ctx); ok && projectID != "" {
		return scoped.List(projectID, statuses...)
	}
	if projectID != "" {
		return s.loom.List(projectID, statuses...)
	}
	return s.loom.ListEngine(statuses...)
}

func (s *Server) countLoomBySession(ctx context.Context) map[string]int {
	tasks, err := s.listLoomTasksForContext(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Warn("loom session count failed: %v", err)
		}
		return map[string]int{}
	}
	counts := make(map[string]int)
	for _, task := range tasks {
		if sessionID := loomTaskSessionID(task); sessionID != "" {
			counts[sessionID]++
		}
	}
	return counts
}

func (s *Server) loomRunningCount(ctx context.Context) (int, error) {
	if s == nil || s.loom == nil {
		return 0, nil
	}
	projectID := projectIDFromContext(ctx)
	if scoped, ok := TenantScopedLoomFromContext(ctx); ok && projectID != "" {
		tasks, err := scoped.List(projectID, loom.TaskStatusRunning)
		return len(tasks), err
	}
	if projectID != "" {
		return s.loom.Count(loom.TaskFilter{
			ProjectID: projectID,
			Statuses:  []loom.TaskStatus{loom.TaskStatusRunning},
		})
	}
	tasks, err := s.loom.ListEngine(loom.TaskStatusRunning)
	return len(tasks), err
}

func (s *Server) loomTasksForSession(ctx context.Context, sessionID string) ([]*loom.Task, error) {
	tasks, err := s.listLoomTasksForContext(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]*loom.Task, 0, len(tasks))
	for _, task := range tasks {
		if loomTaskSessionID(task) == sessionID {
			filtered = append(filtered, task)
		}
	}
	return filtered, nil
}

func (s *Server) failLoomTasksForSession(ctx context.Context, sessionID, errMsg string) (int, error) {
	tasks, err := s.loomTasksForSession(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	failed := 0
	for _, task := range tasks {
		if !task.Status.IsActive() {
			continue
		}
		ok, failErr := s.loom.FailActive(task.ID, errMsg)
		if failErr != nil {
			return failed, failErr
		}
		if ok {
			failed++
		}
	}
	return failed, nil
}

func loomTaskSessionID(task *loom.Task) string {
	if task == nil || task.Metadata == nil {
		return ""
	}
	for _, key := range []string{"session_id", "sessionID", "session"} {
		if value, ok := task.Metadata[key].(string); ok {
			return value
		}
	}
	return ""
}

func loomStatusResult(s *Server, task *loom.Task, bp budget.BudgetParams, jobID string) map[string]any {
	result := map[string]any{
		"job_id":         task.ID,
		"status":         string(task.Status),
		"progress_tail":  task.LastOutputLine,
		"progress_lines": task.ProgressLines,
	}
	if sessionID := loomTaskSessionID(task); sessionID != "" {
		result["session_id"] = sessionID
	}
	if task.ProgressUpdatedAt != nil {
		progressAt := task.ProgressUpdatedAt.UTC().Format(time.RFC3339)
		result["progress_updated_at"] = progressAt
		result["last_seen_at"] = progressAt
	} else {
		result["progress_updated_at"] = nil
		result["last_seen_at"] = loomTaskActivityBaseline(task).UTC().Format(time.RFC3339)
	}

	if task.Status == loom.TaskStatusRunning {
		tier := evaluateInactivityTier(loomTaskActivityBaseline(task), &s.cfg.Server)
		applyStallGuidance(result, tier, task.ID)
	}

	if task.Status.IsTerminal() {
		contentLen := len(task.Result)
		if bp.Tail > 0 {
			tail := task.Result
			if len(tail) > bp.Tail {
				tail = tail[len(tail)-bp.Tail:]
			}
			result["content_tail"] = tail
			result["content_length"] = contentLen
			meta := budget.BuildTruncationMeta(nil, contentLen, fmt.Sprintf("Use status(job_id=%s, include_content=true) for full output.", jobID))
			budget.AttachTruncation(&guidance.ResponseEnvelope{Result: result}, meta)
		} else if bp.IncludeContent {
			result["content"] = task.Result
		} else {
			result["content_length"] = contentLen
			meta := budget.BuildTruncationMeta(nil, contentLen, fmt.Sprintf("Use status(job_id=%s, include_content=true) for full output.", jobID))
			budget.AttachTruncation(&guidance.ResponseEnvelope{Result: result}, meta)
		}
		if task.Error != "" {
			result["error"] = task.Error
		}
	}
	return result
}

func loomTaskBrief(task *loom.Task, includeContent bool) JobBrief {
	brief := JobBrief{
		ID:            task.ID,
		Status:        types.JobStatus(task.Status),
		Progress:      task.LastOutputLine,
		ContentLength: len(task.Result),
	}
	if includeContent {
		brief.Content = task.Result
	}
	return brief
}

func loomTaskActivityBaseline(task *loom.Task) time.Time {
	baseline := task.CreatedAt
	if task.DispatchedAt != nil {
		baseline = *task.DispatchedAt
	}
	if task.ProgressUpdatedAt != nil {
		baseline = *task.ProgressUpdatedAt
	}
	return baseline
}

func (s *Server) runLoomGC(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if failed, err := s.loom.FailStaleRunning(15*time.Minute, "GC reaped: no progress for 15+ minutes"); err != nil {
				s.log.Warn("loom GC: stale task reap failed: %v", err)
			} else if failed > 0 {
				s.log.Info("loom GC: reaped %d stale running tasks", failed)
			}
		}
	}
}
