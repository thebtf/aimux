package server

import (
	"context"
	"errors"
	"testing"

	"github.com/thebtf/aimux/loom"
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

	_, err := client.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		TenantID:   "tenant-a",
		Prompt:     "over quota",
	})
	if !errors.Is(err, loom.ErrLoomQuotaExceeded) {
		t.Fatalf("tenant-a Submit error = %v, want ErrLoomQuotaExceeded", err)
	}

	tenantBTaskID, err := client.Submit(context.Background(), loom.TaskRequest{
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
