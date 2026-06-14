package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
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
	s.mcp.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"aimux://tasks/{task_id}/events",
			"Task Events",
			mcp.WithTemplateDescription("Bounded Loom lifecycle and terminal artifact page for one task"),
			mcp.WithTemplateMIMEType(taskResourceMIMEType),
		),
		s.handleTaskEventsResource,
	)
	s.mcp.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"aimux://tasks/{task_id}/progress",
			"Task Progress",
			mcp.WithTemplateDescription("Bounded Loom progress artifact page for one task"),
			mcp.WithTemplateMIMEType(taskResourceMIMEType),
		),
		s.handleTaskProgressResource,
	)
}

func (s *Server) handleTaskSnapshotResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	parsed, err := parseTaskResourceURI(request.Params.URI, taskResourceSnapshot)
	if err != nil {
		return taskResourceJSON(request.Params.URI, map[string]any{
			"status": "invalid_uri",
			"error":  err.Error(),
		})
	}
	taskID := parsed.taskID

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

func (s *Server) handleTaskEventsResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return s.handleTaskArtifactPageResource(ctx, request, taskResourceEvents, []loom.TaskArtifactKind{
		loom.TaskArtifactKindLifecycle,
		loom.TaskArtifactKindTerminal,
	})
}

func (s *Server) handleTaskProgressResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return s.handleTaskArtifactPageResource(ctx, request, taskResourceProgress, []loom.TaskArtifactKind{
		loom.TaskArtifactKindProgress,
	})
}

func (s *Server) handleTaskArtifactPageResource(ctx context.Context, request mcp.ReadResourceRequest, resource taskResourceKind, artifactKinds []loom.TaskArtifactKind) ([]mcp.ResourceContents, error) {
	parsed, err := parseTaskResourceURI(request.Params.URI, resource)
	if err != nil {
		return taskResourceJSON(request.Params.URI, map[string]any{
			"status": "invalid_uri",
			"error":  err.Error(),
		})
	}
	task, ok, err := s.getLoomTask(ctx, parsed.taskID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return taskResourceJSON(request.Params.URI, taskResourceNotFoundPayload(parsed.taskID))
	}
	limit, err := parseTaskResourceLimit(parsed.query)
	if err != nil {
		return taskResourceJSON(request.Params.URI, map[string]any{
			"status": "invalid_limit",
			"error":  "invalid limit",
		})
	}
	cursor := parsed.query.Get("cursor")

	loomEngine := s.currentLoom()
	if loomEngine == nil {
		return nil, fmt.Errorf("loom unavailable")
	}
	page, err := loomEngine.ListArtifacts(task.ID, loom.TaskArtifactListOptions{
		Cursor: cursor,
		Limit:  limit,
		Kinds:  artifactKinds,
	})
	if err != nil {
		if errors.Is(err, loom.ErrInvalidArtifactCursor) {
			return taskResourceJSON(request.Params.URI, map[string]any{
				"status":  "invalid_cursor",
				"error":   "invalid cursor",
				"task_id": task.ID,
				"cursor":  cursor,
			})
		}
		return nil, err
	}

	payload := taskArtifactPagePayload(task, page, resource, request.Params.URI)
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
	if metadata := taskSnapshotMetadataPayload(task.Metadata); len(metadata) > 0 {
		payload["metadata"] = metadata
	}
	return payload
}

func taskSnapshotMetadataPayload(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, key := range taskSnapshotMetadataKeys {
		value, ok := metadata[key]
		if !ok {
			continue
		}
		if cloned, ok := cloneTaskResourceMetadataValue(value); ok {
			out[key] = cloned
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

var taskSnapshotMetadataKeys = []string{
	"task_class",
	"worker_type",
	"driver_cli",
	"navigator_cli",
	"rounds",
	"confidence_score",
	"gate_result",
	"verdict",
	"recipe_id",
	"recipe_title",
	"recipe_read_only",
	"recipe_phases",
	"recipe_output_resources",
	"recipe_policy_enforced",
	"recipe_policy_selected_cli",
	"recipe_policy_requested",
	"recipe_policy_supported",
	"recipe_replay_key_version",
	"recipe_replay_fingerprint",
	"recipe_replay_cache_hit",
	"recipe_replay_source_task_id",
	"worktree_path",
	"worktree_branch",
	"worktree_base_sha",
	"worktree_preserve_reason",
}

func cloneTaskResourceMetadataValue(value any) (any, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case bool:
		return v, true
	case int:
		return v, true
	case int64:
		return v, true
	case float64:
		return v, true
	case []string:
		out := make([]string, len(v))
		copy(out, v)
		return out, true
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			switch typed := item.(type) {
			case string, bool, int, int64, float64:
				out = append(out, typed)
			}
		}
		return out, true
	default:
		return nil, false
	}
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

type taskResourceKind string

const (
	taskResourceSnapshot taskResourceKind = "snapshot"
	taskResourceEvents   taskResourceKind = "events"
	taskResourceProgress taskResourceKind = "progress"
)

type parsedTaskResourceURI struct {
	taskID string
	query  url.Values
}

func parseTaskResourceURI(rawURI string, expected taskResourceKind) (parsedTaskResourceURI, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return parsedTaskResourceURI{}, fmt.Errorf("invalid task resource URI")
	}
	if u.Scheme != "aimux" || u.Host != "tasks" {
		return parsedTaskResourceURI{}, fmt.Errorf("invalid task resource URI")
	}
	segments := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(segments) == 0 || segments[0] == "" {
		return parsedTaskResourceURI{}, fmt.Errorf("invalid task resource URI")
	}
	taskID, err := url.PathUnescape(segments[0])
	if err != nil || strings.TrimSpace(taskID) == "" {
		return parsedTaskResourceURI{}, fmt.Errorf("invalid task resource URI")
	}
	switch expected {
	case taskResourceSnapshot:
		if len(segments) != 1 {
			return parsedTaskResourceURI{}, fmt.Errorf("invalid task resource URI")
		}
	case taskResourceEvents, taskResourceProgress:
		if len(segments) != 2 || segments[1] != string(expected) {
			return parsedTaskResourceURI{}, fmt.Errorf("invalid task resource URI")
		}
	default:
		return parsedTaskResourceURI{}, fmt.Errorf("invalid task resource URI")
	}
	return parsedTaskResourceURI{taskID: taskID, query: u.Query()}, nil
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

func parseTaskResourceLimit(query url.Values) (int, error) {
	raw := strings.TrimSpace(query.Get("limit"))
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return limit, nil
}

func taskArtifactPagePayload(task *loom.Task, page loom.TaskArtifactPage, resource taskResourceKind, uri string) map[string]any {
	payload := map[string]any{
		"task_id":           task.ID,
		"canonical_uri":     taskCanonicalURI(task.ID),
		"resource_uri":      uri,
		"cursor":            page.Cursor,
		"next_cursor":       page.NextCursor,
		"limit":             page.Limit,
		"has_more":          page.HasMore,
		"projection_status": string(loom.TaskArtifactProjectionStatusForTask(task, page)),
		"items":             taskArtifactItems(page.Items),
	}
	if resource == taskResourceProgress {
		payload["progress"] = map[string]any{
			"last_output_line":    task.LastOutputLine,
			"progress_lines":      task.ProgressLines,
			"progress_updated_at": formatTaskResourceTime(task.ProgressUpdatedAt),
		}
	}
	return payload
}

func taskArtifactItems(items []loom.TaskArtifact) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"seq":            item.Seq,
			"kind":           string(item.Kind),
			"event_type":     item.EventType,
			"summary":        item.Summary,
			"payload":        item.Payload,
			"content_length": item.ContentLength,
			"redacted":       item.Redacted,
			"truncated":      item.Truncated,
			"created_at":     formatTaskResourceTime(&item.CreatedAt),
		})
	}
	return out
}
