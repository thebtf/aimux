package loom

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// ActiveTaskStatuses returns a fresh list of non-terminal task statuses.
func ActiveTaskStatuses() []TaskStatus {
	return []TaskStatus{TaskStatusPending, TaskStatusDispatched, TaskStatusRunning, TaskStatusRetrying}
}

// FailedTaskInfo describes a task that was force-failed by an administrative
// operation such as session kill, shutdown, or stale-task GC.
type FailedTaskInfo struct {
	Task       *Task
	FromStatus TaskStatus
}

// ListEngine returns tasks owned by this engine, optionally filtered by status.
func (l *LoomEngine) ListEngine(statuses ...TaskStatus) ([]*Task, error) {
	return l.store.ListEngine(statuses...)
}

// FailActive marks a non-terminal task failed and signals its live cancel
// function when one exists. Completed tasks are left untouched.
func (l *LoomEngine) FailActive(taskID, errMsg string) (bool, error) {
	l.mu.RLock()
	cancel := l.cancels[taskID]
	l.mu.RUnlock()

	info, err := l.store.FailActive(taskID, errMsg)
	if err != nil || info == nil {
		return info != nil, err
	}
	if cancel != nil {
		cancel()
	}
	l.emitAdminFailed(*info, errMsg)
	return true, nil
}

// FailActiveByProject marks all active tasks for a project as failed.
func (l *LoomEngine) FailActiveByProject(projectID, errMsg string) (int, error) {
	tasks, err := l.store.List(projectID, ActiveTaskStatuses()...)
	if err != nil {
		return 0, err
	}
	return l.failActiveTasks(tasks, errMsg)
}

// FailActiveAll marks all active tasks owned by this engine as failed.
func (l *LoomEngine) FailActiveAll(errMsg string) (int, error) {
	tasks, err := l.store.ListEngine(ActiveTaskStatuses()...)
	if err != nil {
		return 0, err
	}
	return l.failActiveTasks(tasks, errMsg)
}

// FailStaleRunning marks running tasks with no recent progress as failed.
// A task with no progress timestamp uses CreatedAt as its activity baseline.
func (l *LoomEngine) FailStaleRunning(maxIdle time.Duration, errMsg string) (int, error) {
	tasks, err := l.store.ListEngine(TaskStatusRunning)
	if err != nil {
		return 0, err
	}
	now := l.clock.Now().UTC()
	stale := make([]*Task, 0, len(tasks))
	for _, task := range tasks {
		if now.Sub(taskActivityBaseline(task)) > maxIdle {
			stale = append(stale, task)
		}
	}
	return l.failActiveTasks(stale, errMsg)
}

func (l *LoomEngine) failActiveTasks(tasks []*Task, errMsg string) (int, error) {
	count := 0
	for _, task := range tasks {
		ok, err := l.FailActive(task.ID, errMsg)
		if err != nil {
			return count, err
		}
		if ok {
			count++
		}
	}
	return count, nil
}

func taskActivityBaseline(task *Task) time.Time {
	baseline := task.CreatedAt
	if task.DispatchedAt != nil {
		baseline = *task.DispatchedAt
	}
	if task.ProgressUpdatedAt != nil {
		baseline = *task.ProgressUpdatedAt
	}
	return baseline
}

func (l *LoomEngine) emitAdminFailed(info FailedTaskInfo, errMsg string) {
	ctx := context.Background()
	redactedErr := redactErrorMsg(errMsg)
	l.events.Emit(TaskEvent{
		Type:      EventTaskFailed,
		TaskID:    info.Task.ID,
		ProjectID: info.Task.ProjectID,
		RequestID: info.Task.RequestID,
		Status:    TaskStatusFailed,
		Timestamp: l.clock.Now().UTC(),
	})
	l.taskFailedCounter.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("worker_type", string(info.Task.WorkerType)),
		attribute.String("project_id", info.Task.ProjectID),
	))
	l.logger.ErrorContext(ctx, "task failed",
		"module", "loom",
		"task_id", info.Task.ID,
		"project_id", info.Task.ProjectID,
		"worker_type", string(info.Task.WorkerType),
		"task_status", string(TaskStatusFailed),
		"previous_status", string(info.FromStatus),
		"request_id", info.Task.RequestID,
		"error_code", "admin_failed",
		"error", redactedErr,
	)
}

// ListEngine returns tasks scoped to this store's engine_name.
func (s *TaskStore) ListEngine(statuses ...TaskStatus) ([]*Task, error) {
	base := `
		SELECT ` + taskSelectColumns + `
		FROM tasks WHERE engine_name = ?`
	args := []interface{}{s.engineName}

	if len(statuses) > 0 {
		base += ` AND status IN (`
		for i, st := range statuses {
			if i > 0 {
				base += ","
			}
			base += "?"
			args = append(args, string(st))
		}
		base += ")"
	}
	rows, err := s.db.Query(base+` ORDER BY created_at ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("loom store: list engine tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("loom store: scan engine task: %w", scanErr)
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// FailActive marks a non-terminal task failed. It returns nil when the task is
// missing, owned by another engine, or already terminal.
func (s *TaskStore) FailActive(taskID, errMsg string) (*FailedTaskInfo, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("loom store: fail active begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	task, err := scanTask(tx.QueryRow(`
		SELECT `+taskSelectColumns+`
		FROM tasks WHERE id = ? AND engine_name = ?`, taskID, s.engineName))
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("loom store: fail active get: %w", err)
	}
	if !task.Status.IsActive() {
		return nil, nil
	}
	fromStatus := task.Status
	now := time.Now().UTC()
	res, err := tx.Exec(`
		UPDATE tasks
		SET status = ?, result = '', error = ?, completed_at = ?
		WHERE id = ? AND engine_name = ? AND status = ?`,
		string(TaskStatusFailed), redactErrorMsg(errMsg), now, taskID, s.engineName, string(fromStatus),
	)
	if err != nil {
		return nil, fmt.Errorf("loom store: fail active update: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("loom store: fail active rows affected: %w", err)
	}
	if rows == 0 {
		return nil, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("loom store: fail active commit: %w", err)
	}

	task.Status = TaskStatusFailed
	task.Result = ""
	task.Error = redactErrorMsg(errMsg)
	task.CompletedAt = &now
	return &FailedTaskInfo{Task: task, FromStatus: fromStatus}, nil
}
