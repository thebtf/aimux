package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/loom"
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
		"aimux://tasks/{task_id}": false,
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

func readTaskSnapshotResource(t *testing.T, srv *Server, ctx context.Context, uri string) map[string]any {
	t.Helper()
	contents, err := srv.handleTaskSnapshotResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: uri},
	})
	if err != nil {
		t.Fatalf("handleTaskSnapshotResource(%s): %v", uri, err)
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
