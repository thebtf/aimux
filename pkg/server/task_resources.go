package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/util"
)

const (
	taskResourceMIMEType = "application/json"
	taskViewerMIMEType   = "text/html; charset=utf-8"
)

func (s *Server) registerTaskResources() {
	s.mcp.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"aimux://tasks",
			"Task List",
			mcp.WithTemplateDescription("Bounded read-only Loom task list with snapshot and viewer links"),
			mcp.WithTemplateMIMEType(taskResourceMIMEType),
		),
		s.handleTaskListResource,
	)
	s.mcp.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"aimux://tasks{?limit,status}",
			"Task List With Query",
			mcp.WithTemplateDescription("Bounded read-only Loom task list with optional limit/status filters"),
			mcp.WithTemplateMIMEType(taskResourceMIMEType),
		),
		s.handleTaskListResource,
	)
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
			"aimux://tasks/{task_id}/viewer",
			"Task Viewer",
			mcp.WithTemplateDescription("Read-only browser-readable task detail, event, and progress view"),
			mcp.WithTemplateMIMEType(taskViewerMIMEType),
		),
		s.handleTaskViewerResource,
	)
	s.mcp.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"aimux://tasks/{task_id}/events",
			"Task Events",
			mcp.WithTemplateDescription("Bounded Loom lifecycle, runtime, and terminal artifact page for one task"),
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

func (s *Server) handleTaskListResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	query, err := parseTaskListResourceURI(request.Params.URI)
	if err != nil {
		return taskResourceJSON(request.Params.URI, map[string]any{
			"status": "invalid_uri",
			"error":  err.Error(),
		})
	}
	limit, err := parseTaskListLimit(query)
	if err != nil {
		return taskResourceJSON(request.Params.URI, map[string]any{
			"status": "invalid_limit",
			"error":  "invalid limit",
		})
	}
	statuses, err := parseTaskListStatuses(query)
	if err != nil {
		return taskResourceJSON(request.Params.URI, map[string]any{
			"status": "invalid_status",
			"error":  err.Error(),
		})
	}
	tasks, err := s.listTaskResourceTasks(ctx, statuses...)
	if err != nil {
		return nil, err
	}
	payload := taskListPayload(request.Params.URI, tasks, limit)
	return taskResourceJSON(request.Params.URI, payload)
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

func (s *Server) handleTaskViewerResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	parsed, err := parseTaskResourceURI(request.Params.URI, taskResourceViewer)
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
	var eventsPage, progressPage loom.TaskArtifactPage
	if loomEngine := s.currentLoom(); loomEngine != nil {
		eventsPage, err = loomEngine.ListArtifacts(task.ID, loom.TaskArtifactListOptions{
			Limit: 20,
			Kinds: []loom.TaskArtifactKind{
				loom.TaskArtifactKindLifecycle,
				loom.TaskArtifactKindTerminal,
			},
		})
		if err != nil {
			return nil, err
		}
		progressPage, err = loomEngine.ListArtifacts(task.ID, loom.TaskArtifactListOptions{
			Limit: 20,
			Kinds: []loom.TaskArtifactKind{
				loom.TaskArtifactKindProgress,
			},
		})
		if err != nil {
			return nil, err
		}
	}
	projectionStatus := loom.TaskArtifactProjectionStatusForTask(task, eventsPage)
	snapshot := taskSnapshotPayload(task, projectionStatus)
	return taskResourceHTML(request.Params.URI, taskViewerHTML(task, snapshot, eventsPage, progressPage)), nil
}

func (s *Server) handleTaskEventsResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return s.handleTaskArtifactPageResource(ctx, request, taskResourceEvents, []loom.TaskArtifactKind{
		loom.TaskArtifactKindLifecycle,
		loom.TaskArtifactKindTerminal,
		loom.TaskArtifactKindRuntime,
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
	artifactKinds, err = parseTaskArtifactKinds(parsed.query, artifactKinds)
	if err != nil {
		return taskResourceJSON(request.Params.URI, map[string]any{
			"status": "invalid_kind",
			"error":  err.Error(),
		})
	}
	eventTypes := parseTaskResourceCSV(parsed.query.Get("event_type"))
	channels := parseTaskResourceCSV(parsed.query.Get("channel"))

	loomEngine := s.currentLoom()
	if loomEngine == nil {
		return nil, fmt.Errorf("loom unavailable")
	}
	page, err := loomEngine.ListArtifacts(task.ID, loom.TaskArtifactListOptions{
		Cursor:     cursor,
		Limit:      limit,
		Kinds:      artifactKinds,
		EventTypes: eventTypes,
		Channels:   channels,
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

func (s *Server) listTaskResourceTasks(ctx context.Context, statuses ...loom.TaskStatus) ([]*loom.Task, error) {
	return s.listLoomTasksForContext(ctx, statuses...)
}

func taskListPayload(uri string, tasks []*loom.Task, limit int) map[string]any {
	items, hasMore := taskListRows(tasks, limit)
	return map[string]any{
		"status":       "ok",
		"resource_uri": uri,
		"limit":        limit,
		"has_more":     hasMore,
		"items":        items,
	}
}

func taskListRows(tasks []*loom.Task, limit int) ([]map[string]any, bool) {
	if limit <= 0 {
		limit = 20
	}
	rows := make([]map[string]any, 0, min(limit, len(tasks)))
	for i := len(tasks) - 1; i >= 0; i-- {
		if len(rows) >= limit {
			return rows, true
		}
		task := tasks[i]
		if task == nil {
			continue
		}
		row := map[string]any{
			"task_id":        task.ID,
			"status":         string(task.Status),
			"worker_type":    string(task.WorkerType),
			"project_id":     task.ProjectID,
			"request_id":     task.RequestID,
			"parent_task_id": task.ParentTaskID,
			"created_at":     formatTaskResourceTime(&task.CreatedAt),
			"dispatched_at":  formatTaskResourceTime(task.DispatchedAt),
			"completed_at":   formatTaskResourceTime(task.CompletedAt),
			"canonical_uri":  taskCanonicalURI(task.ID),
			"viewer_uri":     taskViewerURI(task.ID),
			"events_uri":     taskEventsURI(task.ID),
			"progress_uri":   taskProgressURI(task.ID),
			"progress": map[string]any{
				"last_output_line":    task.LastOutputLine,
				"progress_lines":      task.ProgressLines,
				"progress_updated_at": formatTaskResourceTime(task.ProgressUpdatedAt),
			},
		}
		if task.Result != "" {
			row["result_length"] = len(task.Result)
		}
		if task.Error != "" {
			row["error_summary"] = compactTaskResourceText(task.Error, 180)
		}
		if metadata := taskSnapshotMetadataPayload(task.Metadata); len(metadata) > 0 {
			row["metadata"] = metadata
		}
		rows = append(rows, row)
	}
	return rows, false
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
	"recipe_workflow_id",
	"recipe_workflow_source",
	"recipe_workflow_steps",
	"workflow_result_status",
	"workflow_step_count",
	"workflow_step_statuses",
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

func taskResourceHTML(uri string, html string) []mcp.ResourceContents {
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      uri,
			MIMEType: taskViewerMIMEType,
			Text:     html,
		},
	}
}

type taskResourceKind string

const (
	taskResourceSnapshot taskResourceKind = "snapshot"
	taskResourceEvents   taskResourceKind = "events"
	taskResourceProgress taskResourceKind = "progress"
	taskResourceViewer   taskResourceKind = "viewer"
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
	case taskResourceEvents, taskResourceProgress, taskResourceViewer:
		if len(segments) != 2 || segments[1] != string(expected) {
			return parsedTaskResourceURI{}, fmt.Errorf("invalid task resource URI")
		}
	default:
		return parsedTaskResourceURI{}, fmt.Errorf("invalid task resource URI")
	}
	return parsedTaskResourceURI{taskID: taskID, query: u.Query()}, nil
}

func parseTaskListResourceURI(rawURI string) (url.Values, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("invalid task list resource URI")
	}
	if u.Scheme != "aimux" || u.Host != "tasks" || strings.Trim(u.EscapedPath(), "/") != "" {
		return nil, fmt.Errorf("invalid task list resource URI")
	}
	return u.Query(), nil
}

func taskCanonicalURI(taskID string) string {
	return "aimux://tasks/" + url.PathEscape(taskID)
}

func taskViewerURI(taskID string) string {
	return taskCanonicalURI(taskID) + "/viewer"
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

func parseTaskListLimit(query url.Values) (int, error) {
	limit, err := parseTaskResourceLimit(query)
	if err != nil {
		return 0, err
	}
	if limit <= 0 {
		return 20, nil
	}
	if limit > 100 {
		return 100, nil
	}
	return limit, nil
}

func parseTaskListStatuses(query url.Values) ([]loom.TaskStatus, error) {
	raw := strings.TrimSpace(query.Get("status"))
	if raw == "" || strings.EqualFold(raw, "all") {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	statuses := make([]loom.TaskStatus, 0, len(parts))
	for _, part := range parts {
		status := strings.TrimSpace(part)
		if status == "" {
			continue
		}
		switch loom.TaskStatus(status) {
		case loom.TaskStatusPending, loom.TaskStatusDispatched, loom.TaskStatusRunning,
			loom.TaskStatusCompleted, loom.TaskStatusFailed, loom.TaskStatusFailedCrash,
			loom.TaskStatusRetrying:
			statuses = append(statuses, loom.TaskStatus(status))
		default:
			return nil, fmt.Errorf("unsupported task status %q", status)
		}
	}
	return statuses, nil
}

func parseTaskArtifactKinds(query url.Values, defaults []loom.TaskArtifactKind) ([]loom.TaskArtifactKind, error) {
	copyDefaults := func() []loom.TaskArtifactKind {
		out := make([]loom.TaskArtifactKind, len(defaults))
		copy(out, defaults)
		return out
	}

	raw := strings.TrimSpace(query.Get("kind"))
	if raw == "" {
		return copyDefaults(), nil
	}
	parts := parseTaskResourceCSV(raw)
	if len(parts) == 0 {
		return copyDefaults(), nil
	}
	allowed := make(map[loom.TaskArtifactKind]struct{}, len(defaults))
	for _, kind := range defaults {
		allowed[kind] = struct{}{}
	}
	kinds := make([]loom.TaskArtifactKind, 0, len(parts))
	for _, part := range parts {
		kind := loom.TaskArtifactKind(part)
		if _, ok := allowed[kind]; !ok {
			return nil, fmt.Errorf("unsupported task artifact kind %q", part)
		}
		kinds = append(kinds, kind)
	}
	return kinds, nil
}

func parseTaskResourceCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
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
			"channel":        item.Channel,
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

func taskViewerHTML(task *loom.Task, snapshot map[string]any, eventsPage loom.TaskArtifactPage, progressPage loom.TaskArtifactPage) string {
	var b strings.Builder
	b.WriteString("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\">")
	b.WriteString("<title>Task Viewer</title>")
	b.WriteString("<style>")
	b.WriteString("body{font-family:system-ui,sans-serif;margin:24px;color:#172033;background:#f8fafc;line-height:1.45}")
	b.WriteString("main{max-width:1120px;margin:0 auto}section{margin:18px 0;padding:16px;background:#fff;border:1px solid #dbe3ef;border-radius:8px}")
	b.WriteString("h1,h2{margin:0 0 12px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:8px}")
	b.WriteString("table{width:100%;border-collapse:collapse}th,td{padding:8px;border-bottom:1px solid #e5edf7;text-align:left;vertical-align:top}")
	b.WriteString("code,pre{background:#edf2f7;border-radius:4px;padding:2px 4px}pre{white-space:pre-wrap;overflow:auto;padding:8px}")
	b.WriteString(".muted{color:#5f6f86}")
	b.WriteString("</style></head><body><main>")
	b.WriteString("<h1>Task Viewer</h1>")
	b.WriteString("<p class=\"muted\">Read-only task projection. No execution controls are included.</p>")
	b.WriteString("<section><h2>Snapshot</h2><div class=\"grid\">")
	taskViewerKV(&b, "Task ID", task.ID)
	taskViewerKV(&b, "Status", string(task.Status))
	taskViewerKV(&b, "Worker", string(task.WorkerType))
	taskViewerKV(&b, "Project", task.ProjectID)
	taskViewerKV(&b, "Created", snapshotValue(snapshot, "created_at"))
	taskViewerKV(&b, "Completed", snapshotValue(snapshot, "completed_at"))
	b.WriteString("</div>")
	if result, ok := snapshot["result_summary"].(string); ok && result != "" {
		b.WriteString("<h3>Result Summary</h3><pre>")
		b.WriteString(html.EscapeString(result))
		b.WriteString("</pre>")
	}
	if errSummary, ok := snapshot["error_summary"].(string); ok && errSummary != "" {
		b.WriteString("<h3>Error Summary</h3><pre>")
		b.WriteString(html.EscapeString(errSummary))
		b.WriteString("</pre>")
	}
	b.WriteString("</section>")

	if metadata, ok := snapshot["metadata"].(map[string]any); ok && len(metadata) > 0 {
		b.WriteString("<section><h2>Metadata</h2>")
		taskViewerMapTable(&b, metadata)
		b.WriteString("</section>")
	}

	b.WriteString("<section><h2>Progress</h2>")
	if progress, ok := snapshot["progress"].(map[string]any); ok {
		taskViewerMapTable(&b, progress)
	}
	taskViewerArtifacts(&b, progressPage.Items)
	b.WriteString("</section>")

	b.WriteString("<section><h2>Events</h2>")
	taskViewerArtifacts(&b, eventsPage.Items)
	b.WriteString("</section>")
	b.WriteString("</main></body></html>")
	return b.String()
}

func taskViewerKV(b *strings.Builder, key string, value any) {
	b.WriteString("<div><span class=\"muted\">")
	b.WriteString(html.EscapeString(key))
	b.WriteString("</span><br><code>")
	b.WriteString(html.EscapeString(fmt.Sprint(value)))
	b.WriteString("</code></div>")
}

func taskViewerMapTable(b *strings.Builder, values map[string]any) {
	b.WriteString("<table><tbody>")
	for _, key := range sortedMapKeys(values) {
		b.WriteString("<tr><th>")
		b.WriteString(html.EscapeString(key))
		b.WriteString("</th><td><code>")
		b.WriteString(html.EscapeString(compactTaskResourceText(formatTaskViewerValue(values[key]), 512)))
		b.WriteString("</code></td></tr>")
	}
	b.WriteString("</tbody></table>")
}

func taskViewerArtifacts(b *strings.Builder, items []loom.TaskArtifact) {
	if len(items) == 0 {
		b.WriteString("<p class=\"muted\">No artifacts recorded.</p>")
		return
	}
	b.WriteString("<table><thead><tr><th>Seq</th><th>Kind</th><th>Event</th><th>Channel</th><th>Summary</th><th>Created</th></tr></thead><tbody>")
	for _, item := range items {
		b.WriteString("<tr><td>")
		b.WriteString(strconv.FormatInt(item.Seq, 10))
		b.WriteString("</td><td>")
		b.WriteString(html.EscapeString(string(item.Kind)))
		b.WriteString("</td><td>")
		b.WriteString(html.EscapeString(item.EventType))
		b.WriteString("</td><td>")
		b.WriteString(html.EscapeString(item.Channel))
		b.WriteString("</td><td>")
		b.WriteString(html.EscapeString(compactTaskResourceText(item.Summary, 256)))
		b.WriteString("</td><td>")
		b.WriteString(html.EscapeString(fmt.Sprint(formatTaskResourceTime(&item.CreatedAt))))
		b.WriteString("</td></tr>")
	}
	b.WriteString("</tbody></table>")
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func formatTaskViewerValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []string:
		data, _ := json.Marshal(typed)
		return string(data)
	case []any, map[string]any:
		data, _ := json.Marshal(typed)
		return string(data)
	default:
		return fmt.Sprint(value)
	}
}

func snapshotValue(snapshot map[string]any, key string) any {
	if value, ok := snapshot[key]; ok {
		return value
	}
	return ""
}
