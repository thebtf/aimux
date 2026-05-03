package session_test

import (
	"path/filepath"
	"testing"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

func TestRecoverFromWAL(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")

	// Write some WAL entries
	wal, err := session.NewWAL(walPath)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}

	wal.Append("session_create", "s1", &session.Session{
		ID:     "s1",
		CLI:    "codex",
		Mode:   types.SessionModeLive,
		Status: types.SessionStatusRunning,
	})
	wal.Close()

	reg := session.NewRegistry()

	jobs, err := session.RecoverFromWAL(walPath, reg)
	if err != nil {
		t.Fatalf("RecoverFromWAL: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("RecoverFromWAL returned %d jobs, want 0", len(jobs))
	}

	sess := reg.Get("s1")
	if sess == nil {
		t.Fatal("session s1 not recovered")
	}
	if sess.CLI != "codex" {
		t.Errorf("CLI = %q, want codex", sess.CLI)
	}
}

func TestRecoverFromWAL_Empty(t *testing.T) {
	reg := session.NewRegistry()

	jobs, err := session.RecoverFromWAL("/nonexistent.wal", reg)
	if err != nil {
		t.Fatalf("should not error on missing WAL: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("missing WAL returned %d jobs, want 0", len(jobs))
	}
}

func TestRecoverFromWAL_ReturnsLegacyJobSnapshots(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "jobs.wal")

	wal, err := session.NewWAL(walPath)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	if err := wal.Append("job_create", "job-1", &session.Job{
		ID:        "job-1",
		SessionID: "session-1",
		CLI:       "codex",
		Status:    types.JobStatusRunning,
	}); err != nil {
		t.Fatalf("append job_create: %v", err)
	}
	if err := wal.Append("job_update", "job-1", map[string]any{
		"status":  types.JobStatusCompleted,
		"content": "wal output",
	}); err != nil {
		t.Fatalf("append job_update: %v", err)
	}
	wal.Close()

	jobs, err := session.RecoverFromWAL(walPath, session.NewRegistry())
	if err != nil {
		t.Fatalf("RecoverFromWAL: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if jobs[0].ID != "job-1" {
		t.Fatalf("job ID = %q, want job-1", jobs[0].ID)
	}
	if jobs[0].Status != types.JobStatusCompleted {
		t.Fatalf("status = %q, want completed", jobs[0].Status)
	}
	if jobs[0].Content != "wal output" {
		t.Fatalf("content = %q, want wal output", jobs[0].Content)
	}
}

func TestSessionsForResume(t *testing.T) {
	reg := session.NewRegistry()

	s1 := reg.Create("codex", types.SessionModeLive, "/tmp")
	reg.Update(s1.ID, func(s *session.Session) {
		s.Status = types.SessionStatusRunning
		s.CLISessionID = "codex-session-123"
	})

	s2 := reg.Create("gemini", types.SessionModeLive, "/tmp")
	reg.Update(s2.ID, func(s *session.Session) {
		s.Status = types.SessionStatusRunning
		// No CLISessionID — cannot resume
	})

	resumable := session.SessionsForResume(reg)
	if len(resumable) != 1 {
		t.Errorf("expected 1 resumable, got %d", len(resumable))
	}
	if len(resumable) > 0 && resumable[0].CLISessionID != "codex-session-123" {
		t.Error("wrong session returned")
	}
}
