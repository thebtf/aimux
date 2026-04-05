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

	// Recover
	reg := session.NewRegistry()
	jm := session.NewJobManager()

	err = session.RecoverFromWAL(walPath, reg, jm)
	if err != nil {
		t.Fatalf("RecoverFromWAL: %v", err)
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
	jm := session.NewJobManager()

	err := session.RecoverFromWAL("/nonexistent.wal", reg, jm)
	if err != nil {
		t.Fatalf("should not error on missing WAL: %v", err)
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
