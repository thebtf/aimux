package session_test

import (
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

func TestGCReaper_ReapsExpiredSessions(t *testing.T) {
	reg := session.NewRegistry()
	jm := session.NewJobManager()

	log, _ := logger.New(t.TempDir()+"/test.log", logger.LevelError)
	defer log.Close()

	// Create an old completed session
	sess := reg.Create("codex", types.SessionModeOnceStateful, "/tmp")
	reg.Update(sess.ID, func(s *session.Session) {
		s.Status = types.SessionStatusCompleted
		s.LastActiveAt = time.Now().Add(-25 * time.Hour) // expired
	})

	gc := session.NewGCReaper(reg, jm, log, 24) // 24h TTL
	reaped := gc.CollectOnce()

	if reaped != 1 {
		t.Errorf("expected 1 reaped, got %d", reaped)
	}
	if reg.Count() != 0 {
		t.Errorf("expected 0 sessions, got %d", reg.Count())
	}
}

func TestGCReaper_KeepsActiveSessions(t *testing.T) {
	reg := session.NewRegistry()
	jm := session.NewJobManager()

	log, _ := logger.New(t.TempDir()+"/test.log", logger.LevelError)
	defer log.Close()

	// Create a recent running session
	sess := reg.Create("codex", types.SessionModeOnceStateful, "/tmp")
	reg.Update(sess.ID, func(s *session.Session) {
		s.Status = types.SessionStatusRunning
	})

	gc := session.NewGCReaper(reg, jm, log, 24)
	gc.CollectOnce()

	if reg.Count() != 1 {
		t.Errorf("running session should not be reaped, got count=%d", reg.Count())
	}
}
