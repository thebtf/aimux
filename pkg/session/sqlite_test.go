package session_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

func TestLoadLegacyJobs_RunningBecomeFailed(t *testing.T) {
	store := newSQLiteTestStore(t, "running.db")
	job := legacyTestJob("job-running", "session-1", "codex", types.JobStatusRunning)

	if err := store.SnapshotJob(job); err != nil {
		t.Fatalf("SnapshotJob: %v", err)
	}

	jobs, err := store.LoadLegacyJobs()
	if err != nil {
		t.Fatalf("LoadLegacyJobs: %v", err)
	}
	restored := requireLegacyJob(t, jobs, job.ID)
	if restored.Status != types.JobStatusFailed {
		t.Errorf("status = %q, want failed", restored.Status)
	}
	if restored.Error == nil {
		t.Fatal("expected non-nil error on restored running job")
	}
	if !strings.Contains(restored.Error.Message, "process restarted") {
		t.Errorf("error message = %q, want to contain 'process restarted'", restored.Error.Message)
	}
	if restored.CompletedAt == nil {
		t.Error("expected CompletedAt to be set on failed restored job")
	}
}

func TestLoadLegacyJobs_CompletingBecomeFailed(t *testing.T) {
	store := newSQLiteTestStore(t, "completing.db")
	job := legacyTestJob("job-completing", "session-3", "codex", types.JobStatusCompleting)

	if err := store.SnapshotJob(job); err != nil {
		t.Fatalf("SnapshotJob: %v", err)
	}

	jobs, err := store.LoadLegacyJobs()
	if err != nil {
		t.Fatalf("LoadLegacyJobs: %v", err)
	}
	restored := requireLegacyJob(t, jobs, job.ID)
	if restored.Status != types.JobStatusFailed {
		t.Errorf("status = %q, want failed (completing -> failed on restart)", restored.Status)
	}
	if restored.Error == nil || !strings.Contains(restored.Error.Message, "process restarted") {
		t.Errorf("error = %+v, want 'process restarted'", restored.Error)
	}
}

func TestLoadLegacyJobs_BackdatedRunningRestoredAndFailed(t *testing.T) {
	store := newSQLiteTestStore(t, "backdated.db")
	job := legacyTestJob("job-backdated", "session-4", "codex", types.JobStatusRunning)
	job.CreatedAt = time.Now().Add(-2 * time.Hour)
	job.ProgressUpdatedAt = job.CreatedAt

	if err := store.SnapshotJob(job); err != nil {
		t.Fatalf("SnapshotJob: %v", err)
	}

	jobs, err := store.LoadLegacyJobs()
	if err != nil {
		t.Fatalf("LoadLegacyJobs: %v", err)
	}
	restored := requireLegacyJob(t, jobs, job.ID)
	if restored.Status != types.JobStatusFailed {
		t.Errorf("status = %q, want failed (running -> failed on restart)", restored.Status)
	}
	if restored.Error == nil || !strings.Contains(restored.Error.Message, "process restarted") {
		t.Errorf("error = %+v, want 'process restarted'", restored.Error)
	}
}

func TestLoadLegacyJobs_CompletedPreserved(t *testing.T) {
	store := newSQLiteTestStore(t, "completed.db")
	job := legacyTestJob("job-completed", "session-2", "gemini", types.JobStatusCompleted)
	job.Content = "some output"
	job.CompletedAt = ptrTime(time.Now())

	if err := store.SnapshotJob(job); err != nil {
		t.Fatalf("SnapshotJob: %v", err)
	}

	jobs, err := store.LoadLegacyJobs()
	if err != nil {
		t.Fatalf("LoadLegacyJobs: %v", err)
	}
	restored := requireLegacyJob(t, jobs, job.ID)
	if restored.Status != types.JobStatusCompleted {
		t.Errorf("status = %q, want completed", restored.Status)
	}
	if restored.Content != "some output" {
		t.Errorf("content = %q, want 'some output'", restored.Content)
	}
	if restored.CompletedAt == nil {
		t.Error("expected CompletedAt to be preserved")
	}
}

func TestLoadLegacyJobs_EmptyDB(t *testing.T) {
	store := newSQLiteTestStore(t, "empty.db")

	jobs, err := store.LoadLegacyJobs()
	if err != nil {
		t.Fatalf("LoadLegacyJobs on empty DB: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("expected 0 legacy jobs, got %d", len(jobs))
	}
}

func newSQLiteTestStore(t *testing.T, name string) *session.Store {
	t.Helper()

	store, err := session.NewStore(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	return store
}

func legacyTestJob(id, sessionID, cli string, status types.JobStatus) *session.Job {
	now := time.Now()
	return &session.Job{
		ID:                id,
		SessionID:         sessionID,
		CLI:               cli,
		Status:            status,
		Pheromones:        map[string]string{},
		PID:               12345,
		CreatedAt:         now,
		ProgressUpdatedAt: now,
	}
}

func requireLegacyJob(t *testing.T, jobs []*session.Job, id string) *session.Job {
	t.Helper()
	for _, job := range jobs {
		if job.ID == id {
			return job
		}
	}
	t.Fatalf("legacy job %s not found in %d jobs", id, len(jobs))
	return nil
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
