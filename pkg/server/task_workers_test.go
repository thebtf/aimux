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
