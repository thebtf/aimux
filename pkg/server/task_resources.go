package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/util"
)

const taskResourceMIMEType = "application/json"

func (s *Server) registerTaskResources() {
	s.mcp.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"aimux://tasks/{task_id}",
			"Task Snapshot",
			mcp.WithTemplateDescription("Compact Loom task snapshot with status, progress summary, and task artifact resource links"),
			mcp.WithTemplateMIMEType(taskResourceMIMEType),
		),
		s.handleTaskSnapshotResource,
	)
}

func (s *Server) handleTaskSnapshotResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	taskID, err := parseTaskSnapshotURI(request.Params.URI)
	if err != nil {
		return taskResourceJSON(request.Params.URI, map[string]any{
			"status": "invalid_uri",
			"error":  err.Error(),
		})
	}

	task, ok, err := s.getLoomTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return taskResourceJSON(request.Params.URI, taskResourceNotFoundPayload(taskID))
	}

	page := loom.TaskArtifactPage{TaskID: task.ID}
	if loomEngine := s.currentLoom(); loomEngine != nil {
		page, err = loomEngine.ListArtifacts(task.ID, loom.TaskArtifactListOptions{Limit: 1})
		if err != nil {
			return nil, err
		}
	}
	projectionStatus := loom.TaskArtifactProjectionStatusForTask(task, page)
	payload := taskSnapshotPayload(task, projectionStatus)
	return taskResourceJSON(request.Params.URI, payload)
}

func taskSnapshotPayload(task *loom.Task, projectionStatus loom.TaskArtifactProjectionStatus) map[string]any {
	canonicalURI := taskCanonicalURI(task.ID)
	eventsURI := taskEventsURI(task.ID)
	progressURI := taskProgressURI(task.ID)
	payload := map[string]any{
		"task_id":        task.ID,
		"status":         string(task.Status),
		"worker_type":    string(task.WorkerType),
		"project_id":     task.ProjectID,
		"request_id":     task.RequestID,
		"parent_task_id": task.ParentTaskID,
		"created_at":     formatTaskResourceTime(&task.CreatedAt),
		"dispatched_at":  formatTaskResourceTime(task.DispatchedAt),
		"completed_at":   formatTaskResourceTime(task.CompletedAt),
		"retries":        task.Retries,
		"canonical_uri":  canonicalURI,
		"events_uri":     eventsURI,
		"progress_uri":   progressURI,
		"progress": map[string]any{
			"last_output_line":    task.LastOutputLine,
			"progress_lines":      task.ProgressLines,
			"progress_updated_at": formatTaskResourceTime(task.ProgressUpdatedAt),
		},
		"artifacts": map[string]any{
			"projection_status": string(projectionStatus),
			"events_uri":        eventsURI,
			"progress_uri":      progressURI,
		},
	}
	if task.Result != "" {
		payload["result_summary"] = compactTaskResourceText(task.Result, 512)
		payload["result_length"] = len(task.Result)
	}
	if task.Error != "" {
		payload["error_summary"] = compactTaskResourceText(task.Error, 512)
	}
	return payload
}

func taskResourceNotFoundPayload(taskID string) map[string]any {
	return map[string]any{
		"status":        "not_found",
		"error":         "task not found",
		"task_id":       taskID,
		"canonical_uri": taskCanonicalURI(taskID),
	}
}

func taskResourceJSON(uri string, payload map[string]any) ([]mcp.ResourceContents, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      uri,
			MIMEType: taskResourceMIMEType,
			Text:     string(data),
		},
	}, nil
}

func parseTaskSnapshotURI(rawURI string) (string, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return "", fmt.Errorf("invalid task resource URI")
	}
	if u.Scheme != "aimux" || u.Host != "tasks" {
		return "", fmt.Errorf("invalid task resource URI")
	}
	taskID := strings.Trim(u.EscapedPath(), "/")
	if taskID == "" || strings.Contains(taskID, "/") {
		return "", fmt.Errorf("invalid task resource URI")
	}
	taskID, err = url.PathUnescape(taskID)
	if err != nil || strings.TrimSpace(taskID) == "" {
		return "", fmt.Errorf("invalid task resource URI")
	}
	return taskID, nil
}

func taskCanonicalURI(taskID string) string {
	return "aimux://tasks/" + url.PathEscape(taskID)
}

func taskEventsURI(taskID string) string {
	return taskCanonicalURI(taskID) + "/events"
}

func taskProgressURI(taskID string) string {
	return taskCanonicalURI(taskID) + "/progress"
}

func formatTaskResourceTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func compactTaskResourceText(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return util.TruncateUTF8(text, limit) + "...[truncated]"
}
