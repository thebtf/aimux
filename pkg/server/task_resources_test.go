package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/review"
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
		"aimux://tasks":                    false,
		"aimux://tasks{?limit,status}":     false,
		"aimux://tasks/{task_id}":          false,
		"aimux://tasks/{task_id}/viewer":   false,
		"aimux://tasks/{task_id}/events":   false,
		"aimux://tasks/{task_id}/progress": false,
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

func TestTaskSnapshotResourceBoundsAndRedactsReviewReason(t *testing.T) {
	const rawSecret = "sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	rawReason := "review backend failed " + rawSecret + " " + strings.Repeat("界", 300)
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-review-reason")

	taskID, err := srv.loom.Submit(ctx, loom.TaskRequest{
		WorkerType: loom.WorkerType("missing-review-reason-worker"),
		ProjectID:  projectID,
		Prompt:     "exercise bounded review reason projection",
		Metadata:   map[string]any{"reason": rawReason},
	})
	if err != nil {
		t.Fatalf("loom.Submit: %v", err)
	}

	got := readTaskSnapshotResource(t, srv, ctx, "aimux://tasks/"+taskID)
	metadata, ok := got["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata type = %T, want map; payload=%v", got["metadata"], got)
	}
	reason, _ := metadata["reason"].(string)
	assertSafeReviewReasonProjection(t, reason, rawSecret)
}

func TestTaskSnapshotPayloadReviewSummaryExcludesRawDecisionEvidence(t *testing.T) {
	tests := []struct {
		name          string
		result        string
		forbidden     string
		wantDecision  string
		wantMalformed bool
	}{
		{
			name:         "blocking finding body",
			result:       `{"decision":"block","reason":"safe blocking reason","findings":[{"severity":"error","file":"blocker.go","body":"BLOCK-FINDING-BODY-MARKER"}],"summary":"aggregate block summary","passes_completed":["structural","behavioural","adversarial"],"severity":"error","blocking":true,"review_complete":true,"confidence_score":1}`,
			forbidden:    "BLOCK-FINDING-BODY-MARKER",
			wantDecision: "block",
		},
		{
			name:         "allow aggregate summary",
			result:       `{"decision":"allow","reason":"safe allow reason","findings":[],"summary":"ALLOW-SUMMARY-RAW-MARKER","passes_completed":["structural","behavioural","adversarial"],"blocking":false,"review_complete":true,"confidence_score":1}`,
			forbidden:    "ALLOW-SUMMARY-RAW-MARKER",
			wantDecision: "allow",
		},
		{
			name:          "malformed review result",
			result:        `MALFORMED-REVIEW-RAW-MARKER`,
			forbidden:     "MALFORMED-REVIEW-RAW-MARKER",
			wantMalformed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &loom.Task{
				ID:         "review-summary-task",
				WorkerType: review.WorkerTypeReview,
				Status:     loom.TaskStatusCompleted,
				Result:     tt.result,
				Metadata:   map[string]any{"task_class": "review"},
			}
			payload := taskSnapshotPayload(task, loom.TaskArtifactProjectionOK)
			summary, _ := payload["result_summary"].(string)
			if summary == "" {
				t.Fatalf("result_summary missing: %v", payload)
			}
			if strings.Contains(summary, tt.forbidden) {
				t.Fatalf("result_summary leaked raw review evidence %q: %s", tt.forbidden, summary)
			}
			if len(summary) > 512 {
				t.Fatalf("result_summary length = %d, want <= 512", len(summary))
			}

			var got map[string]any
			if err := json.Unmarshal([]byte(summary), &got); err != nil {
				t.Fatalf("result_summary must remain structured JSON: %v; raw=%q", err, summary)
			}
			for _, forbiddenKey := range []string{"findings", "summary"} {
				if _, ok := got[forbiddenKey]; ok {
					t.Fatalf("result_summary exposed raw review field %q: %v", forbiddenKey, got)
				}
			}
			if tt.wantMalformed {
				if omitted, _ := got["content_omitted"].(bool); !omitted {
					t.Fatalf("malformed review summary = %v, want content_omitted=true", got)
				}
				return
			}
			if got["decision"] != tt.wantDecision {
				t.Fatalf("decision = %v, want %s; summary=%v", got["decision"], tt.wantDecision, got)
			}
		})
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

func TestTaskArtifactItems_ProjectsGlobalAndTaskLocalSequence(t *testing.T) {
	artifact := loom.TaskArtifact{Seq: 41}
	if eventSeq := reflect.ValueOf(&artifact).Elem().FieldByName("EventSeq"); eventSeq.IsValid() {
		eventSeq.SetInt(7)
	}

	items := taskArtifactItems([]loom.TaskArtifact{artifact})
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	if got := items[0]["seq"]; got != int64(41) {
		t.Fatalf("seq = %v, want unchanged global seq 41", got)
	}
	eventSeq, ok := items[0]["event_seq"]
	if !ok {
		t.Fatal("event_seq missing from public task artifact item")
	}
	if eventSeq != int64(7) {
		t.Fatalf("event_seq = %v, want task-local seq 7", eventSeq)
	}
}

func TestTaskEventsResource_IncludesRuntimeEventsAndFiltersByKindEventTypeChannel(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-runtime-events")
	taskID, _ := submitBlockingLoomTask(t, srv, projectID, "")
	waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusRunning)

	textEvent, err := srv.loom.AppendRuntimeEvent(taskID, loom.TaskRuntimeEventAppend{
		EventType: "text_delta",
		Channel:   "stdout",
		Summary:   "assistant token with sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789",
		Payload: map[string]any{
			"text": "hello sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789",
		},
	})
	if err != nil {
		t.Fatalf("AppendRuntimeEvent text_delta: %v", err)
	}
	statusEvent, err := srv.loom.AppendRuntimeEvent(taskID, loom.TaskRuntimeEventAppend{
		EventType: "status",
		Channel:   "stderr",
		Summary:   "codex selected model",
		Payload:   map[string]any{"state": "model_selected"},
	})
	if err != nil {
		t.Fatalf("AppendRuntimeEvent status: %v", err)
	}

	all := readTaskEventsResource(t, srv, ctx, "aimux://tasks/"+taskID+"/events?limit=10")
	allRuntime := findTaskResourceEventBySeq(t, all, textEvent.Seq)
	assertTaskResourceRuntimeEvent(t, allRuntime, textEvent.Seq, "text_delta", "stdout")

	textOnly := readTaskEventsResource(t, srv, ctx, "aimux://tasks/"+taskID+"/events?kind=runtime&event_type=text_delta&channel=stdout&limit=10")
	textItems := taskResourceItems(t, textOnly)
	if len(textItems) != 1 {
		t.Fatalf("text_delta stdout filtered items = %#v; want exactly one runtime event", textOnly["items"])
	}
	textItem := textItems[0].(map[string]any)
	assertTaskResourceRuntimeEvent(t, textItem, textEvent.Seq, "text_delta", "stdout")
	rawTextPayload, err := json.Marshal(textOnly)
	if err != nil {
		t.Fatalf("marshal text runtime payload: %v", err)
	}
	if strings.Contains(string(rawTextPayload), "sk-proj-") {
		t.Fatalf("runtime events resource leaked raw secret: %s", rawTextPayload)
	}
	if textItem["redacted"] != true {
		t.Fatalf("text runtime event redacted = %v; want true", textItem["redacted"])
	}

	statusOnly := readTaskEventsResource(t, srv, ctx, "aimux://tasks/"+taskID+"/events?kind=runtime&event_type=status&channel=stderr&limit=10")
	statusItems := taskResourceItems(t, statusOnly)
	if len(statusItems) != 1 {
		t.Fatalf("status stderr filtered items = %#v; want exactly one runtime event", statusOnly["items"])
	}
	assertTaskResourceRuntimeEvent(t, statusItems[0].(map[string]any), statusEvent.Seq, "status", "stderr")
}

func TestTaskEventsResource_RunningTaskEmptyRuntimeProjectionIsPartial(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-runtime-empty")
	taskID, _ := submitBlockingLoomTask(t, srv, projectID, "")
	waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusRunning)

	got := readTaskEventsResource(t, srv, ctx, "aimux://tasks/"+taskID+"/events?kind=runtime&limit=5")
	if got["task_id"] != taskID {
		t.Fatalf("task_id = %v, want %s", got["task_id"], taskID)
	}
	if got["projection_status"] != string(loom.TaskArtifactProjectionPartial) {
		t.Fatalf("projection_status = %v, want partial for running task without runtime events", got["projection_status"])
	}
	items := taskResourceItems(t, got)
	if len(items) != 0 {
		t.Fatalf("runtime event items = %#v; want empty projection for running task", got["items"])
	}
	task, err := srv.loom.Get(taskID)
	if err != nil {
		t.Fatalf("loom.Get(%s): %v", taskID, err)
	}
	if task.Status != loom.TaskStatusRunning {
		t.Fatalf("canonical task status = %s; want running", task.Status)
	}
}

func TestTaskEventsResource_MidFlightRuntimeEventReadObservesAccumulation(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-runtime-accumulation")
	taskID, _ := submitBlockingLoomTask(t, srv, projectID, "")
	waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusRunning)

	first, err := srv.loom.AppendRuntimeEvent(taskID, loom.TaskRuntimeEventAppend{
		EventType: "text_delta",
		Channel:   "stdout",
		Summary:   "first live token",
		Payload:   map[string]any{"text": "first"},
	})
	if err != nil {
		t.Fatalf("AppendRuntimeEvent first: %v", err)
	}
	firstRead := readTaskEventsResource(t, srv, ctx, "aimux://tasks/"+taskID+"/events?kind=runtime&event_type=text_delta&channel=stdout&limit=10")
	firstItems := taskResourceItems(t, firstRead)
	if len(firstItems) != 1 {
		t.Fatalf("first mid-flight read items = %#v; want one runtime event", firstRead["items"])
	}
	assertTaskResourceRuntimeEvent(t, firstItems[0].(map[string]any), first.Seq, "text_delta", "stdout")

	second, err := srv.loom.AppendRuntimeEvent(taskID, loom.TaskRuntimeEventAppend{
		EventType: "text_delta",
		Channel:   "stdout",
		Summary:   "second live token",
		Payload:   map[string]any{"text": "second"},
	})
	if err != nil {
		t.Fatalf("AppendRuntimeEvent second: %v", err)
	}
	secondRead := readTaskEventsResource(t, srv, ctx, "aimux://tasks/"+taskID+"/events?kind=runtime&event_type=text_delta&channel=stdout&limit=10")
	secondItems := taskResourceItems(t, secondRead)
	if len(secondItems) != 2 {
		t.Fatalf("second mid-flight read items = %#v; want accumulated runtime events", secondRead["items"])
	}
	assertTaskResourceRuntimeEvent(t, secondItems[0].(map[string]any), first.Seq, "text_delta", "stdout")
	assertTaskResourceRuntimeEvent(t, secondItems[1].(map[string]any), second.Seq, "text_delta", "stdout")
	if first.Seq >= second.Seq {
		t.Fatalf("runtime event append order inverted: first seq %d second seq %d", first.Seq, second.Seq)
	}
	task, err := srv.loom.Get(taskID)
	if err != nil {
		t.Fatalf("loom.Get(%s): %v", taskID, err)
	}
	if task.Status != loom.TaskStatusRunning {
		t.Fatalf("mid-flight read observed terminal task status %s; want running", task.Status)
	}
}

func TestTaskArtifactResource_KindFilterIsBoundedByResourceFamily(t *testing.T) {
	srv := testServerWithLoom(t)
	ctx, projectID := projectCtxAndID("proj-resource-kind-boundary")
	taskID, _ := submitBlockingLoomTask(t, srv, projectID, "")
	waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusRunning)

	runtimeEvent, err := srv.loom.AppendRuntimeEvent(taskID, loom.TaskRuntimeEventAppend{
		EventType: "raw",
		Channel:   "stdout",
		Summary:   "runtime line",
		Payload:   map[string]any{"line": "runtime line"},
	})
	if err != nil {
		t.Fatalf("AppendRuntimeEvent: %v", err)
	}
	if err := srv.loom.AppendProgress(taskID, "progress line"); err != nil {
		t.Fatalf("AppendProgress: %v", err)
	}

	eventsAllowed := readTaskEventsResource(t, srv, ctx, "aimux://tasks/"+taskID+"/events?kind=runtime&limit=5")
	assertTaskResourceRuntimeEvent(t, findTaskResourceEventBySeq(t, eventsAllowed, runtimeEvent.Seq), runtimeEvent.Seq, "raw", "stdout")

	progressAllowed := readTaskProgressResource(t, srv, ctx, "aimux://tasks/"+taskID+"/progress?kind=progress&limit=5")
	progressItems := taskResourceItems(t, progressAllowed)
	if len(progressItems) != 1 {
		t.Fatalf("progress items = %#v, want one progress artifact", progressAllowed["items"])
	}
	if item := progressItems[0].(map[string]any); item["kind"] != string(loom.TaskArtifactKindProgress) {
		t.Fatalf("progress item kind = %v, want progress", item["kind"])
	}

	progressRejected := readTaskProgressResource(t, srv, ctx, "aimux://tasks/"+taskID+"/progress?kind=runtime&limit=5")
	if progressRejected["status"] != "invalid_kind" {
		t.Fatalf("progress runtime status = %v, want invalid_kind; payload=%v", progressRejected["status"], progressRejected)
	}
	if _, leaked := progressRejected["items"]; leaked {
		t.Fatalf("invalid progress kind leaked items: %v", progressRejected)
	}

	eventsRejected := readTaskEventsResource(t, srv, ctx, "aimux://tasks/"+taskID+"/events?kind=progress&limit=5")
	if eventsRejected["status"] != "invalid_kind" {
		t.Fatalf("events progress status = %v, want invalid_kind; payload=%v", eventsRejected["status"], eventsRejected)
	}
	if _, leaked := eventsRejected["items"]; leaked {
		t.Fatalf("invalid events kind leaked items: %v", eventsRejected)
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

func taskResourceItems(t *testing.T, got map[string]any) []any {
	t.Helper()
	items, ok := got["items"].([]any)
	if !ok {
		t.Fatalf("items type = %T, want []any; payload=%v", got["items"], got)
	}
	return items
}

func findTaskResourceEventBySeq(t *testing.T, got map[string]any, seq int64) map[string]any {
	t.Helper()
	for _, item := range taskResourceItems(t, got) {
		event, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("event item type = %T, want map", item)
		}
		if event["seq"] == float64(seq) {
			return event
		}
	}
	t.Fatalf("runtime event seq %d not found in items %#v", seq, got["items"])
	return nil
}

func assertTaskResourceRuntimeEvent(t *testing.T, item map[string]any, seq int64, eventType, channel string) {
	t.Helper()
	if item["seq"] != float64(seq) {
		t.Fatalf("event seq = %v, want %d; item=%v", item["seq"], seq, item)
	}
	if item["kind"] != string(loom.TaskArtifactKindRuntime) {
		t.Fatalf("event kind = %v, want runtime; item=%v", item["kind"], item)
	}
	if item["event_type"] != eventType {
		t.Fatalf("event_type = %v, want %s; item=%v", item["event_type"], eventType, item)
	}
	if item["channel"] != channel {
		t.Fatalf("channel = %v, want %s; item=%v", item["channel"], channel, item)
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
