package session_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

func TestRestoreJobs_RunningBecomeFailed(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Create a job in running state and snapshot it to SQLite.
	jm := session.NewJobManager()
	job := jm.Create("session-1", "codex")
	jm.StartJob(job.ID, 12345)

	if err := store.SnapshotJob(jm.Get(job.ID)); err != nil {
		t.Fatalf("SnapshotJob: %v", err)
	}

	// Restore into a fresh JobManager (simulating a process restart).
	jm2 := session.NewJobManager()
	n, err := store.RestoreJobs(jm2)
	if err != nil {
		t.Fatalf("RestoreJobs: %v", err)
	}
	if n != 1 {
		t.Errorf("restored %d jobs, want 1", n)
	}

	restored := jm2.Get(job.ID)
	if restored == nil {
		t.Fatal("restored job not found in JobManager")
	}
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

func TestRestoreJobs_CompletedPreserved(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	jm := session.NewJobManager()
	job := jm.Create("session-2", "gemini")
	jm.StartJob(job.ID, 42)
	jm.CompleteJob(job.ID, "some output", 0)

	if err := store.SnapshotJob(jm.Get(job.ID)); err != nil {
		t.Fatalf("SnapshotJob: %v", err)
	}

	jm2 := session.NewJobManager()
	n, err := store.RestoreJobs(jm2)
	if err != nil {
		t.Fatalf("RestoreJobs: %v", err)
	}
	if n != 1 {
		t.Errorf("restored %d jobs, want 1", n)
	}

	restored := jm2.Get(job.ID)
	if restored == nil {
		t.Fatal("restored job not found")
	}
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

func TestRestoreJobs_EmptyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")

	store, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	jm := session.NewJobManager()
	n, err := store.RestoreJobs(jm)
	if err != nil {
		t.Fatalf("RestoreJobs on empty DB: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 restored jobs, got %d", n)
	}
}
