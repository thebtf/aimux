package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/tenant"
)

func TestTenantAwareSubtaskLoomEnforcesTenantQuota(t *testing.T) {
	srv := testServerWithLoom(t)
	projectID := "quota-project"
	submitBlockingLoomTaskForTenant(t, srv, projectID, "tenant-a")

	client := tenantAwareSubtaskLoom{
		engine: srv.loom,
		quotaFor: func(tenantID string) *loom.TenantQuotaConfig {
			if tenantID == "tenant-a" {
				return &loom.TenantQuotaConfig{MaxLoomTasksQueued: 1}
			}
			return &loom.TenantQuotaConfig{MaxLoomTasksQueued: 10}
		},
	}

	ctxA, cancelA := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelA()
	_, err := client.Submit(ctxA, loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		TenantID:   "tenant-a",
		Prompt:     "over quota",
	})
	if !errors.Is(err, loom.ErrLoomQuotaExceeded) {
		t.Fatalf("tenant-a Submit error = %v, want ErrLoomQuotaExceeded", err)
	}

	ctxB, cancelB := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelB()
	tenantBTaskID, err := client.Submit(ctxB, loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		TenantID:   "tenant-b",
		Prompt:     "within quota",
	})
	if err != nil {
		t.Fatalf("tenant-b Submit returned error: %v", err)
	}
	if tenantBTaskID == "" {
		t.Fatal("tenant-b task ID is empty")
	}
}

func TestTenantAwareSubtaskLoomGetContextScopesTenant(t *testing.T) {
	srv := testServerWithLoom(t)
	projectID := "scoped-get-project"
	taskID, _ := submitBlockingLoomTaskForTenant(t, srv, projectID, "tenant-a")
	client := tenantAwareSubtaskLoom{engine: srv.loom}

	tenantBCtx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-b"})
	if _, err := client.GetContext(tenantBCtx, taskID); !errors.Is(err, loom.ErrTaskNotFound) {
		t.Fatalf("tenant-b GetContext error = %v, want ErrTaskNotFound", err)
	}

	tenantACtx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-a"})
	task, err := client.GetContext(tenantACtx, taskID)
	if err != nil {
		t.Fatalf("tenant-a GetContext returned error: %v", err)
	}
	if task.ID != taskID {
		t.Fatalf("task ID = %q, want %q", task.ID, taskID)
	}
}

func TestAdaptReviewPassOutputFailsClosedOnMalformedJSON(t *testing.T) {
	task := &loom.Task{Metadata: map[string]any{"review_pass": "structural"}}

	content, meta, err := adaptReviewPassOutput(task, "not json")
	if err == nil {
		t.Fatal("expected malformed review pass output to fail closed")
	}
	if content != "" {
		t.Fatalf("content = %q, want empty on error", content)
	}
	if meta != nil {
		t.Fatalf("meta = %v, want nil on error", meta)
	}
	if !strings.Contains(err.Error(), "structured JSON") {
		t.Fatalf("error = %q, want structured JSON detail", err)
	}
}

func TestAdaptReviewPassOutputAcceptsStructuredJSON(t *testing.T) {
	input := `{"findings":[],"summary":"review pass complete"}`

	content, meta, err := adaptReviewPassOutput(&loom.Task{}, input)
	if err != nil {
		t.Fatalf("adaptReviewPassOutput: %v", err)
	}
	if content != input {
		t.Fatalf("content = %q, want original JSON", content)
	}
	if meta == nil {
		t.Fatal("meta is nil, want empty map")
	}
	if len(meta) != 0 {
		t.Fatalf("meta = %v, want empty map", meta)
	}
}

func TestAdaptReviewPassOutputRejectsEmptySummary(t *testing.T) {
	input := `{"findings":[],"summary":"   "}`

	content, meta, err := adaptReviewPassOutput(&loom.Task{}, input)
	if err == nil {
		t.Fatal("expected empty summary to be rejected")
	}
	if content != "" {
		t.Fatalf("content = %q, want empty on error", content)
	}
	if meta != nil {
		t.Fatalf("meta = %v, want nil on error", meta)
	}
	if !strings.Contains(err.Error(), "non-empty summary") {
		t.Fatalf("error = %q, want non-empty summary detail", err)
	}
}

func TestRuntimeEventsFromOutputLine_NormalizesStructuredJSONL(t *testing.T) {
	textEvents := runtimeEventsFromOutputLine("jsonl", `{"type":"item.completed","item":{"type":"agent_message","text":"hello"}}`)
	if len(textEvents) != 1 {
		t.Fatalf("text events len = %d; want 1", len(textEvents))
	}
	if textEvents[0].EventType != "text_delta" || textEvents[0].Channel != "stdout" || textEvents[0].Summary != "hello" {
		t.Fatalf("text event = %#v; want text_delta/stdout hello", textEvents[0])
	}

	statusEvents := runtimeEventsFromOutputLine("jsonl", `{"type":"turn.started"}`)
	if len(statusEvents) != 1 || statusEvents[0].EventType != "status" || statusEvents[0].Channel != "stdout" {
		t.Fatalf("status event = %#v; want status/stdout", statusEvents)
	}

	rawEvents := runtimeEventsFromOutputLine("jsonl", `{"type":"unknown.future","payload":"kept"}`)
	if len(rawEvents) != 1 || rawEvents[0].EventType != "raw" || rawEvents[0].Channel != "stdout" {
		t.Fatalf("raw event = %#v; want raw/stdout fallback", rawEvents)
	}
}

func TestRuntimeEventsFromOutputLine_PreservesPlainLinesAsRawStdout(t *testing.T) {
	events := runtimeEventsFromOutputLine("text", "plain harness line")
	if len(events) != 1 {
		t.Fatalf("events len = %d; want 1", len(events))
	}
	if events[0].EventType != "raw" || events[0].Channel != "stdout" || events[0].Summary != "plain harness line" {
		t.Fatalf("plain line event = %#v; want raw/stdout truthful fallback", events[0])
	}
}

func TestProfileTaskWorkerProgressSinkPersistsRuntimeSliceAndProgressTail(t *testing.T) {
	srv := testServerWithLoom(t)
	_, projectID := projectCtxAndID("proj-runtime-progress-sink")
	taskID, _ := submitBlockingLoomTask(t, srv, projectID, "")
	waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusRunning)

	worker := profileTaskWorker{server: srv}
	sink := worker.progressSink(taskID, "jsonl")
	if sink == nil {
		t.Fatal("progress sink is nil")
	}
	sink(`{"type":"item.completed","item":{"type":"agent_message","text":"hello sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"}}`)

	page, err := srv.loom.ListArtifacts(taskID, loom.TaskArtifactListOptions{
		Kinds:      []loom.TaskArtifactKind{loom.TaskArtifactKindRuntime},
		EventTypes: []string{"text_delta"},
		Channels:   []string{"stdout"},
	})
	if err != nil {
		t.Fatalf("ListArtifacts runtime: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("runtime items = %#v; want one text_delta", page.Items)
	}
	item := page.Items[0]
	if item.Kind != loom.TaskArtifactKindRuntime || item.EventType != "text_delta" || item.Channel != "stdout" {
		t.Fatalf("runtime identity = kind %q event_type %q channel %q", item.Kind, item.EventType, item.Channel)
	}
	if strings.Contains(item.Summary, "sk-proj-") || !item.Redacted {
		t.Fatalf("runtime redaction failed: summary=%q redacted=%v", item.Summary, item.Redacted)
	}

	task, err := srv.loom.Get(taskID)
	if err != nil {
		t.Fatalf("loom.Get(%s): %v", taskID, err)
	}
	if task.LastOutputLine == "" || strings.Contains(task.LastOutputLine, "sk-proj-") {
		t.Fatalf("progress tail = %q; want parsed redacted text", task.LastOutputLine)
	}
}
