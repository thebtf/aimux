package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/workflow"
)

func TestTaskSnapshotResourceTemplatesRegistered(t *testing.T) {
	srv := testServerWithLoom(t)

	response := srv.mcp.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"resources/templates/list","params":{}}`))
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response %s: %v", raw, err)
	}
	result, ok := decoded["result"].(map[string]any)
	if !ok {
		t.Fatalf("resources/templates/list result missing or wrong type: %s", raw)
	}
	templates, ok := result["resourceTemplates"].([]any)
	if !ok {
		t.Fatalf("resourceTemplates missing or wrong type: %s", raw)
	}

	want := map[string]bool{
		"aimux://tasks":                  false,
		"aimux://tasks{?limit,status}":   false,
		"aimux://tasks/{task_id}":        false,
		"aimux://tasks/{task_id}/viewer": false,
	}
	for _, item := range templates {
		template, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("resource template item type = %T, want map", item)
		}
		if uri, ok := template["uriTemplate"].(string); ok {
			if _, tracked := want[uri]; tracked {
				want[uri] = true
			}
		}
	}
	for uri, found := range want {
		if !found {
			t.Fatalf("resource template %q not registered; templates=%v", uri, templates)
		}
	}
}

func TestTaskListResource_ReadViaMCPWithQuery(t *testing.T) {
	srv := testServerWithLoom(t)

	_ = srv.mcp.HandleMessage(context.Background(), json.RawMessage(`{
		"jsonrpc":"2.0",
		"id":1,
		"method":"initialize",
		"params":{"protocolVersion":"2024-11-05"}
	}`))

	response := srv.mcp.HandleMessage(context.Background(), json.RawMessage(`{
		"jsonrpc":"2.0",
		"id":2,
		"method":"resources/read",
		"params":{"uri":"aimux://tasks?limit=5"}
	}`))
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response %s: %v", raw, err)
	}
	if errObj, hasError := decoded["error"]; hasError {
		t.Fatalf("resources/read returned error: %v", errObj)
	}
	result, ok := decoded["result"].(map[string]any)
	if !ok {
		t.Fatalf("resources/read result missing or wrong type: %s", raw)
	}
	contents, ok := result["contents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("contents = %#v, want one item", result["contents"])
	}
	text, ok := contents[0].(map[string]any)["text"].(string)
	if !ok {
		t.Fatalf("content text missing in %v", contents[0])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("unmarshal task list payload %q: %v", text, err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("status = %v, want ok; payload=%v", payload["status"], payload)
	}
	if payload["limit"] != float64(5) {
		t.Fatalf("limit = %v, want 5; payload=%v", payload["limit"], payload)
	}
}

func TestTaskListResource_ReadOnlyRows(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-list")
	firstID, err := srv.loom.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		Prompt:     "fail because no worker is registered",
		Env:        map[string]string{"SECRET_TOKEN": "should-not-leak"},
	})
	if err != nil {
		t.Fatalf("loom.Submit first: %v", err)
	}
	secondID, err := srv.loom.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		Prompt:     "fail again because no worker is registered",
	})
	if err != nil {
		t.Fatalf("loom.Submit second: %v", err)
	}
	waitForTaskResourceStatus(t, srv, firstID, loom.TaskStatusFailed)
	waitForTaskResourceStatus(t, srv, secondID, loom.TaskStatusFailed)

	got := readTaskListResource(t, srv, ctx, "aimux://tasks?limit=5")
	if got["status"] != "ok" {
		t.Fatalf("status = %v, want ok; payload=%v", got["status"], got)
	}
	items, ok := got["items"].([]any)
	if !ok || len(items) < 2 {
		t.Fatalf("items = %#v, want at least two rows", got["items"])
	}
	row := findTaskListRow(t, items, secondID)
	if row["canonical_uri"] != "aimux://tasks/"+secondID {
		t.Fatalf("canonical_uri = %v", row["canonical_uri"])
	}
	if row["viewer_uri"] != "aimux://tasks/"+secondID+"/viewer" {
		t.Fatalf("viewer_uri = %v", row["viewer_uri"])
	}
	if row["events_uri"] != "aimux://tasks/"+secondID+"/events" {
		t.Fatalf("events_uri = %v", row["events_uri"])
	}
	if _, leaked := row["prompt"]; leaked {
		t.Fatalf("task list row leaked prompt: %v", row)
	}
	if _, leaked := row["env"]; leaked {
		t.Fatalf("task list row leaked env: %v", row)
	}
	if _, leaked := row["result"]; leaked {
		t.Fatalf("task list row leaked result: %v", row)
	}
}

func TestTaskViewerResource_RendersReadOnlyHTML(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-viewer")
	taskID, err := srv.loom.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		Prompt:     "fail because no worker is registered",
		Metadata: map[string]any{
			"recipe_replay_key_version": "v1",
			"recipe_replay_fingerprint": "abc123",
			"recipe_replay_cache_hit":   false,
			"worktree_path":             "D:\\Dev\\aimux",
		},
		Env: map[string]string{"SECRET_TOKEN": "should-not-leak"},
	})
	if err != nil {
		t.Fatalf("loom.Submit: %v", err)
	}
	waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusFailed)

	html := readTaskViewerResource(t, srv, ctx, "aimux://tasks/"+taskID+"/viewer")
	for _, want := range []string{
		"Task Viewer",
		taskID,
		"Events",
		"Progress",
		"recipe_replay_fingerprint",
		"worktree_path",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("viewer HTML missing %q:\n%s", want, html)
		}
	}
	for _, forbidden := range []string{"<form", "<button", "<script", "task(", "SECRET_TOKEN"} {
		if strings.Contains(strings.ToLower(html), strings.ToLower(forbidden)) {
			t.Fatalf("viewer HTML contains forbidden %q:\n%s", forbidden, html)
		}
	}
}

func TestTaskViewerResource_NotFoundUsesCompactShape(t *testing.T) {
	srv := testServerWithLoom(t)
	got := readTaskViewerNotFoundResource(t, srv, context.Background(), "aimux://tasks/missing-task/viewer")
	assertTaskResourceNotFound(t, got, "missing-task")
}

func TestTaskSnapshotResource_NotFoundAndForeignShareCompactShape(t *testing.T) {
	srv := testServerWithLoom(t)
	tenantATaskID, _ := submitBlockingLoomTaskForTenant(t, srv, "proj-resource-a", "tenant-a")

	unknown := readTaskSnapshotResource(t, srv, tenantScopedNoProjectContext(srv, "tenant-b"), "aimux://tasks/missing-task")
	foreign := readTaskSnapshotResource(t, srv, tenantScopedNoProjectContext(srv, "tenant-b"), "aimux://tasks/"+tenantATaskID)

	assertTaskResourceNotFound(t, unknown, "missing-task")
	assertTaskResourceNotFound(t, foreign, tenantATaskID)
	if unknown["error"] != foreign["error"] || unknown["status"] != foreign["status"] {
		t.Fatalf("unknown and foreign not-found shape differs: unknown=%v foreign=%v", unknown, foreign)
	}
}

func TestTaskSnapshotResource_FailedTaskIncludesCanonicalStatusAndLinks(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-failed")
	taskID, err := srv.loom.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		Prompt:     "fail because no worker is registered",
	})
	if err != nil {
		t.Fatalf("loom.Submit: %v", err)
	}
	task := waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusFailed)
	if task.Error == "" {
		t.Fatal("failed task has empty canonical error")
	}

	got := readTaskSnapshotResource(t, srv, ctx, "aimux://tasks/"+taskID)
	if got["task_id"] != taskID {
		t.Fatalf("task_id = %v, want %s", got["task_id"], taskID)
	}
	if got["status"] != string(loom.TaskStatusFailed) {
		t.Fatalf("status = %v, want failed", got["status"])
	}
	if got["worker_type"] != string(loom.WorkerTypeCLI) {
		t.Fatalf("worker_type = %v, want cli", got["worker_type"])
	}
	if got["canonical_uri"] != "aimux://tasks/"+taskID {
		t.Fatalf("canonical_uri = %v", got["canonical_uri"])
	}
	if got["events_uri"] != "aimux://tasks/"+taskID+"/events" {
		t.Fatalf("events_uri = %v", got["events_uri"])
	}
	if got["progress_uri"] != "aimux://tasks/"+taskID+"/progress" {
		t.Fatalf("progress_uri = %v", got["progress_uri"])
	}
	if errorSummary, _ := got["error_summary"].(string); !strings.Contains(errorSummary, "worker") {
		t.Fatalf("error_summary = %q, want worker failure summary", errorSummary)
	}
	progress, ok := got["progress"].(map[string]any)
	if !ok {
		t.Fatalf("progress type = %T, want map", got["progress"])
	}
	if _, ok := progress["progress_lines"]; !ok {
		t.Fatalf("progress summary missing progress_lines: %v", progress)
	}
	artifacts, ok := got["artifacts"].(map[string]any)
	if !ok {
		t.Fatalf("artifacts type = %T, want map", got["artifacts"])
	}
	if artifacts["projection_status"] == "" {
		t.Fatalf("projection_status empty: %v", artifacts)
	}
	if _, leaked := got["env"]; leaked {
		t.Fatalf("snapshot exposed raw env: %v", got)
	}
}

func TestTaskSnapshotResource_IncludesWorktreePreservationMetadata(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-worktree-metadata")
	taskID, err := srv.loom.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		Prompt:     "fail because no worker is registered",
		Metadata: map[string]any{
			"worktree_path":            "D:\\Dev\\aimux",
			"worktree_branch":          "master",
			"worktree_base_sha":        "2990fd8",
			"worktree_preserve_reason": "code task mutates caller worktree",
		},
		Env: map[string]string{"SECRET_TOKEN": "should-not-leak"},
	})
	if err != nil {
		t.Fatalf("loom.Submit: %v", err)
	}
	waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusFailed)

	got := readTaskSnapshotResource(t, srv, ctx, "aimux://tasks/"+taskID)
	metadata, ok := got["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata type = %T, want map; payload=%v", got["metadata"], got)
	}
	assertTaskResourceMetadata(t, metadata, "worktree_path", "D:\\Dev\\aimux")
	assertTaskResourceMetadata(t, metadata, "worktree_branch", "master")
	assertTaskResourceMetadata(t, metadata, "worktree_base_sha", "2990fd8")
	assertTaskResourceMetadata(t, metadata, "worktree_preserve_reason", "code task mutates caller worktree")
	if _, leaked := got["env"]; leaked {
		t.Fatalf("snapshot exposed raw env: %v", got)
	}
}

func TestTaskSnapshotResourceIncludesWorkflowRecipeMetadata(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-workflow-recipe-metadata")
	steps := recipeWorkflowStepNames(workflow.SecurityAuditSteps())
	taskID, err := srv.loom.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		Prompt:     "fail because no worker is registered",
		Metadata: map[string]any{
			"recipe_id":               "security-audit",
			"recipe_title":            "Security Audit",
			"recipe_read_only":        true,
			"recipe_output_resources": []string{"task_snapshot", "task_events", "task_progress"},
			"recipe_workflow_id":      "secaudit",
			"recipe_workflow_steps":   steps,
			"recipe_workflow_source":  "pkg/workflow/secaudit.go",
			"workflow_result_status":  "completed",
			"workflow_step_count":     len(steps),
			"workflow_step_statuses":  []string{"discover=completed", "analyze=completed", "report=completed"},
		},
		Env: map[string]string{"SECRET_TOKEN": "should-not-leak"},
	})
	if err != nil {
		t.Fatalf("loom.Submit: %v", err)
	}
	waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusFailed)

	got := readTaskSnapshotResource(t, srv, ctx, "aimux://tasks/"+taskID)
	metadata, ok := got["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata type = %T, want map; payload=%v", got["metadata"], got)
	}
	assertTaskResourceMetadata(t, metadata, "recipe_id", "security-audit")
	assertTaskResourceMetadata(t, metadata, "recipe_title", "Security Audit")
	assertTaskResourceMetadataBool(t, metadata, "recipe_read_only", true)
	assertMetadataStringSlice(t, metadata, "recipe_output_resources", []string{"task_snapshot", "task_events", "task_progress"})
	assertTaskResourceMetadata(t, metadata, "recipe_workflow_id", "secaudit")
	assertMetadataStringSlice(t, metadata, "recipe_workflow_steps", steps)
	assertTaskResourceMetadata(t, metadata, "recipe_workflow_source", "pkg/workflow/secaudit.go")
	assertTaskResourceMetadata(t, metadata, "workflow_result_status", "completed")
	assertMetadataInt(t, metadata, "workflow_step_count", len(steps))
	assertMetadataStringSlice(t, metadata, "workflow_step_statuses", []string{"discover=completed", "analyze=completed", "report=completed"})
	if _, leaked := got["env"]; leaked {
		t.Fatalf("snapshot exposed raw env: %v", got)
	}
}

func TestTaskArtifactResource_EventsPaginationWithCursorAndLimit(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-events")
	taskID, err := srv.loom.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		Prompt:     "fail to create lifecycle and terminal artifacts",
	})
	if err != nil {
		t.Fatalf("loom.Submit: %v", err)
	}
	waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusFailed)

	first := readTaskEventsResource(t, srv, ctx, "aimux://tasks/"+taskID+"/events?limit=1")
	if first["task_id"] != taskID {
		t.Fatalf("task_id = %v, want %s", first["task_id"], taskID)
	}
	if first["limit"] != float64(1) {
		t.Fatalf("limit = %v, want 1", first["limit"])
	}
	if first["has_more"] != true {
		t.Fatalf("has_more = %v, want true; page=%v", first["has_more"], first)
	}
	nextCursor, _ := first["next_cursor"].(string)
	if nextCursor == "" {
		t.Fatalf("next_cursor empty on first page: %v", first)
	}
	items, ok := first["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("first items = %#v, want one item", first["items"])
	}

	second := readTaskEventsResource(t, srv, ctx, "aimux://tasks/"+taskID+"/events?cursor="+nextCursor+"&limit=1")
	secondItems, ok := second["items"].([]any)
	if !ok || len(secondItems) != 1 {
		t.Fatalf("second items = %#v, want one item", second["items"])
	}
	if secondItems[0].(map[string]any)["seq"] == items[0].(map[string]any)["seq"] {
		t.Fatalf("cursor page repeated seq %v", secondItems[0].(map[string]any)["seq"])
	}
}

func TestTaskArtifactResource_InvalidCursorReturnsCompactError(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-bad-cursor")
	taskID, _ := submitBlockingLoomTask(t, srv, projectID, "")

	got := readTaskEventsResource(t, srv, ctx, "aimux://tasks/"+taskID+"/events?cursor=not-a-number")
	if got["status"] != "invalid_cursor" {
		t.Fatalf("status = %v, want invalid_cursor; payload=%v", got["status"], got)
	}
	if got["error"] == "" {
		t.Fatalf("invalid cursor payload missing compact error: %v", got)
	}
	if _, leaked := got["items"]; leaked {
		t.Fatalf("invalid cursor payload should not include items: %v", got)
	}
}

func TestTaskArtifactResource_OverLimitClampsMetadata(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-limit")
	taskID, _ := submitBlockingLoomTask(t, srv, projectID, "")

	got := readTaskEventsResource(t, srv, ctx, "aimux://tasks/"+taskID+"/events?limit=999")
	if got["limit"] != float64(100) {
		t.Fatalf("limit = %v, want clamp to 100", got["limit"])
	}
}

func TestTaskProgressResource_EmptyProjectionIncludesCanonicalProgressSummary(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-progress-empty")
	taskID, _ := submitBlockingLoomTask(t, srv, projectID, "")

	got := readTaskProgressResource(t, srv, ctx, "aimux://tasks/"+taskID+"/progress?limit=2")
	if got["task_id"] != taskID {
		t.Fatalf("task_id = %v, want %s", got["task_id"], taskID)
	}
	if got["limit"] != float64(2) {
		t.Fatalf("limit = %v, want 2", got["limit"])
	}
	if got["projection_status"] != string(loom.TaskArtifactProjectionPartial) {
		t.Fatalf("projection_status = %v, want partial for existing task without progress rows", got["projection_status"])
	}
	progress, ok := got["progress"].(map[string]any)
	if !ok {
		t.Fatalf("progress type = %T, want map", got["progress"])
	}
	if progress["progress_lines"] != float64(0) {
		t.Fatalf("progress_lines = %v, want 0", progress["progress_lines"])
	}
	items, ok := got["items"].([]any)
	if !ok || len(items) != 0 {
		t.Fatalf("items = %#v, want empty progress artifact page", got["items"])
	}
}

func TestTaskProgressResource_DoesNotExposeRawEnvOrTranscriptPayloads(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-progress-redaction")
	worker := newBlockingLoomWorker()
	srv.loom.RegisterWorker(loom.WorkerTypeCLI, worker)
	taskID, err := srv.loom.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		Prompt:     "progress privacy",
		Env: map[string]string{
			"OPENAI_API_KEY": "sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789",
		},
	})
	if err != nil {
		t.Fatalf("loom.Submit: %v", err)
	}
	select {
	case <-worker.started:
	case <-time.After(2 * time.Second):
		t.Fatal("loom worker did not start")
	}
	t.Cleanup(func() {
		select {
		case <-worker.release:
		default:
			close(worker.release)
		}
	})
	if err := srv.loom.AppendProgress(taskID, "safe progress line"); err != nil {
		t.Fatalf("AppendProgress: %v", err)
	}

	got := readTaskProgressResource(t, srv, ctx, "aimux://tasks/"+taskID+"/progress?limit=5")
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal progress payload: %v", err)
	}
	if strings.Contains(string(raw), "sk-proj-AbCdEf") {
		t.Fatalf("progress payload leaked raw secret: %s", raw)
	}
	for _, forbiddenKey := range []string{"env", "transcript", "content", "result"} {
		if _, leaked := got[forbiddenKey]; leaked {
			t.Fatalf("progress payload exposed top-level %q: %v", forbiddenKey, got)
		}
	}
	items, ok := got["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("progress items = %#v, want at least one artifact", got["items"])
	}
	item := items[0].(map[string]any)
	payload, ok := item["payload"].(map[string]any)
	if !ok {
		t.Fatalf("progress item payload = %T, want map", item["payload"])
	}
	for _, forbiddenKey := range []string{"env", "transcript", "content", "result"} {
		if _, leaked := payload[forbiddenKey]; leaked {
			t.Fatalf("progress item payload exposed %q: %v", forbiddenKey, payload)
		}
	}
}

func TestTaskProgressResource_DoesNotRegisterJobsAliasWithoutActiveConsumers(t *testing.T) {
	srv := testServerWithLoom(t)

	response := srv.mcp.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"resources/templates/list","params":{}}`))
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response %s: %v", raw, err)
	}
	result := decoded["result"].(map[string]any)
	templates := result["resourceTemplates"].([]any)
	want := map[string]bool{
		"aimux://tasks/{task_id}/events":   false,
		"aimux://tasks/{task_id}/progress": false,
	}
	for _, item := range templates {
		template := item.(map[string]any)
		uri, _ := template["uriTemplate"].(string)
		if strings.HasPrefix(uri, "aimux://jobs/") {
			t.Fatalf("unexpected jobs alias template registered without P002 consumer evidence: %s", uri)
		}
		if _, ok := want[uri]; ok {
			want[uri] = true
		}
	}
	for uri, found := range want {
		if !found {
			t.Fatalf("resource template %q not registered; templates=%v", uri, templates)
		}
	}
}

func readTaskSnapshotResource(t *testing.T, srv *Server, ctx context.Context, uri string) map[string]any {
	t.Helper()
	contents, err := srv.handleTaskSnapshotResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	})
	return decodeTaskResourceContents(t, contents, err, uri)
}

func readTaskEventsResource(t *testing.T, srv *Server, ctx context.Context, uri string) map[string]any {
	t.Helper()
	contents, err := srv.handleTaskEventsResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	})
	return decodeTaskResourceContents(t, contents, err, uri)
}

func readTaskProgressResource(t *testing.T, srv *Server, ctx context.Context, uri string) map[string]any {
	t.Helper()
	contents, err := srv.handleTaskProgressResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	})
	return decodeTaskResourceContents(t, contents, err, uri)
}

func readTaskListResource(t *testing.T, srv *Server, ctx context.Context, uri string) map[string]any {
	t.Helper()
	contents, err := srv.handleTaskListResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	})
	return decodeTaskResourceContents(t, contents, err, uri)
}

func readTaskViewerResource(t *testing.T, srv *Server, ctx context.Context, uri string) string {
	t.Helper()
	contents, err := srv.handleTaskViewerResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	})
	if err != nil {
		t.Fatalf("viewer read(%s): %v", uri, err)
	}
	if len(contents) != 1 {
		t.Fatalf("viewer contents len = %d, want 1", len(contents))
	}
	text, ok := contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("viewer content type = %T, want TextResourceContents", contents[0])
	}
	if text.URI != uri {
		t.Fatalf("viewer URI = %q, want %q", text.URI, uri)
	}
	if text.MIMEType != taskViewerMIMEType {
		t.Fatalf("viewer MIMEType = %q, want %s", text.MIMEType, taskViewerMIMEType)
	}
	return text.Text
}

func readTaskViewerNotFoundResource(t *testing.T, srv *Server, ctx context.Context, uri string) map[string]any {
	t.Helper()
	contents, err := srv.handleTaskViewerResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	})
	return decodeTaskResourceContents(t, contents, err, uri)
}

func decodeTaskResourceContents(t *testing.T, contents []mcp.ResourceContents, err error, uri string) map[string]any {
	t.Helper()
	if err != nil {
		t.Fatalf("resource read(%s): %v", uri, err)
	}
	if len(contents) != 1 {
		t.Fatalf("resource contents len = %d, want 1", len(contents))
	}
	text, ok := contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("resource content type = %T, want TextResourceContents", contents[0])
	}
	if text.URI != uri {
		t.Fatalf("resource URI = %q, want %q", text.URI, uri)
	}
	if text.MIMEType != "application/json" {
		t.Fatalf("resource MIMEType = %q, want application/json", text.MIMEType)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(text.Text), &data); err != nil {
		t.Fatalf("resource JSON %s: %v", text.Text, err)
	}
	return data
}

func findTaskListRow(t *testing.T, items []any, taskID string) map[string]any {
	t.Helper()
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("task list row type = %T, want map", item)
		}
		if row["task_id"] == taskID {
			return row
		}
	}
	t.Fatalf("task_id %s not found in rows %v", taskID, items)
	return nil
}

func assertTaskResourceNotFound(t *testing.T, got map[string]any, taskID string) {
	t.Helper()
	if got["status"] != "not_found" {
		t.Fatalf("status = %v, want not_found; payload=%v", got["status"], got)
	}
	if got["error"] != "task not found" {
		t.Fatalf("error = %v, want task not found; payload=%v", got["error"], got)
	}
	if got["task_id"] != taskID {
		t.Fatalf("task_id = %v, want %s", got["task_id"], taskID)
	}
	if _, leaked := got["tenant_id"]; leaked {
		t.Fatalf("not-found payload leaked tenant_id: %v", got)
	}
}

func assertTaskResourceMetadata(t *testing.T, metadata map[string]any, key string, want string) {
	t.Helper()
	got, ok := metadata[key].(string)
	if !ok || got != want {
		t.Fatalf("metadata[%s] = %#v, want %q", key, metadata[key], want)
	}
}

func waitForTaskResourceStatus(t *testing.T, srv *Server, taskID string, want loom.TaskStatus) *loom.Task {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, err := srv.loom.Get(taskID)
		if err != nil {
			t.Fatalf("loom.Get(%s): %v", taskID, err)
		}
		if task.Status == want {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, err := srv.loom.Get(taskID)
	if err != nil {
		t.Fatalf("loom.Get(%s) after timeout: %v", taskID, err)
	}
	t.Fatalf("task status = %s, want %s", task.Status, want)
	return nil
}
