package server

import (
	"context"
	"errors"
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
