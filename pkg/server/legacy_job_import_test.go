package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

func TestImportLegacyJobs_RunningJobVisibleViaLoomStatus(t *testing.T) {
	srv := testServerWithLoom(t)
	now := time.Now().UTC()
	job := &session.Job{
		ID:                "legacy-running",
		SessionID:         "legacy-session",
		CLI:               "codex",
		Status:            types.JobStatusRunning,
		Progress:          "first line\nsecond line",
		CreatedAt:         now.Add(-time.Minute),
		ProgressUpdatedAt: now.Add(-30 * time.Second),
	}

	imported, err := srv.importLegacyJobs([]*session.Job{job})
	if err != nil {
		t.Fatalf("importLegacyJobs: %v", err)
	}
	if imported != 1 {
		t.Fatalf("imported = %d, want 1", imported)
	}

	result, err := srv.handleStatus(context.Background(), makeRequest("status", map[string]any{"job_id": job.ID}))
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	data := parseResult(t, result)
	if data["status"] != string(types.JobStatusFailed) {
		t.Fatalf("status = %v, want failed", data["status"])
	}
	if !strings.Contains(data["error"].(string), "process restarted") {
		t.Fatalf("error = %v, want process restarted", data["error"])
	}
	if data["progress_tail"] != "second line" {
		t.Fatalf("progress_tail = %v, want second line", data["progress_tail"])
	}
	if data["progress_lines"].(float64) != 2 {
		t.Fatalf("progress_lines = %v, want 2", data["progress_lines"])
	}
	if data["progress"] != job.Progress {
		t.Fatalf("progress = %v, want legacy progress", data["progress"])
	}
}

func TestImportLegacyJobs_CompletedContentPreserved(t *testing.T) {
	srv := testServerWithLoom(t)
	now := time.Now().UTC()
	job := &session.Job{
		ID:                "legacy-completed",
		SessionID:         "legacy-session",
		CLI:               "codex",
		Status:            types.JobStatusCompleted,
		Content:           "legacy output",
		CreatedAt:         now.Add(-time.Minute),
		ProgressUpdatedAt: now.Add(-30 * time.Second),
		CompletedAt:       &now,
	}

	if _, err := srv.importLegacyJobs([]*session.Job{job}); err != nil {
		t.Fatalf("importLegacyJobs: %v", err)
	}

	result, err := srv.handleStatus(context.Background(), makeRequest("status", map[string]any{
		"job_id":          job.ID,
		"include_content": true,
	}))
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	data := parseResult(t, result)
	if data["status"] != string(types.JobStatusCompleted) {
		t.Fatalf("status = %v, want completed", data["status"])
	}
	if data["content"] != "legacy output" {
		t.Fatalf("content = %v, want legacy output", data["content"])
	}
}
