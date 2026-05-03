package session_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

func TestRegistry_CreateAndGet(t *testing.T) {
	reg := session.NewRegistry()

	sess := reg.Create("codex", types.SessionModeLive, "/tmp")
	if sess.ID == "" {
		t.Error("expected non-empty session ID")
	}
	if sess.CLI != "codex" {
		t.Errorf("CLI = %q, want codex", sess.CLI)
	}
	if sess.Mode != types.SessionModeLive {
		t.Errorf("Mode = %q, want live", sess.Mode)
	}
	if sess.Status != types.SessionStatusCreated {
		t.Errorf("Status = %q, want created", sess.Status)
	}

	got := reg.Get(sess.ID)
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.ID != sess.ID {
		t.Errorf("Get ID = %q, want %q", got.ID, sess.ID)
	}
}

func TestRegistry_Update(t *testing.T) {
	reg := session.NewRegistry()
	sess := reg.Create("codex", types.SessionModeOnceStateful, "/tmp")

	ok := reg.Update(sess.ID, func(s *session.Session) {
		s.Status = types.SessionStatusRunning
		s.PID = 12345
	})
	if !ok {
		t.Error("Update returned false")
	}

	got := reg.Get(sess.ID)
	if got.Status != types.SessionStatusRunning {
		t.Errorf("Status = %q, want running", got.Status)
	}
	if got.PID != 12345 {
		t.Errorf("PID = %d, want 12345", got.PID)
	}
}

func TestRegistry_List(t *testing.T) {
	reg := session.NewRegistry()
	reg.Create("codex", types.SessionModeLive, "/tmp")
	s2 := reg.Create("gemini", types.SessionModeOnceStateful, "/tmp")

	reg.Update(s2.ID, func(s *session.Session) {
		s.Status = types.SessionStatusCompleted
	})

	all := reg.List("")
	if len(all) != 2 {
		t.Errorf("List() = %d, want 2", len(all))
	}

	completed := reg.List(types.SessionStatusCompleted)
	if len(completed) != 1 {
		t.Errorf("List(completed) = %d, want 1", len(completed))
	}
}

func TestRegistry_Delete(t *testing.T) {
	reg := session.NewRegistry()
	sess := reg.Create("codex", types.SessionModeLive, "/tmp")

	if !reg.Delete(sess.ID) {
		t.Error("Delete returned false")
	}
	if reg.Get(sess.ID) != nil {
		t.Error("session still exists after delete")
	}
	if reg.Delete(sess.ID) {
		t.Error("double delete should return false")
	}
}
