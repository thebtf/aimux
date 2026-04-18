package session_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

func TestStore_CountAll(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")

	store, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	if _, err := store.Count(session.Filter{}); err != nil {
		t.Fatalf("initial Count: %v", err)
	}

	err = store.SnapshotSession(&session.Session{
		ID:           "s1",
		CLI:          "cli1",
		Mode:         types.SessionModeLive,
		CLISessionID: "cli1",
		Status:       types.SessionStatusRunning,
		Turns:        1,
		CWD:          "/tmp/1",
		CreatedAt:    time.Now().UTC(),
		LastActiveAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SnapshotSession s1: %v", err)
	}
	err = store.SnapshotSession(&session.Session{
		ID:           "s2",
		CLI:          "cli2",
		Mode:         types.SessionModeLive,
		Status:       types.SessionStatusCompleted,
		Turns:        1,
		CWD:          "/tmp/2",
		CreatedAt:    time.Now().UTC(),
		LastActiveAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SnapshotSession s2: %v", err)
	}

	got, err := store.Count(session.Filter{})
	if err != nil {
		t.Fatalf("Count all: %v", err)
	}
	if got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
}

func TestStore_CountByStatus(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")
	store, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	sessions := []*session.Session{
		{
			ID:           "s1",
			CLI:          "cli",
			Mode:         types.SessionModeLive,
			Status:       types.SessionStatusRunning,
			CWD:          "/tmp/a",
			CreatedAt:    time.Now().UTC(),
			LastActiveAt: time.Now().UTC(),
		},
		{
			ID:           "s2",
			CLI:          "cli",
			Mode:         types.SessionModeLive,
			Status:       types.SessionStatusRunning,
			CWD:          "/tmp/b",
			CreatedAt:    time.Now().UTC(),
			LastActiveAt: time.Now().UTC(),
		},
		{
			ID:           "s3",
			CLI:          "cli",
			Mode:         types.SessionModeLive,
			Status:       types.SessionStatusCompleted,
			CWD:          "/tmp/c",
			CreatedAt:    time.Now().UTC(),
			LastActiveAt: time.Now().UTC(),
		},
	}
	for _, s := range sessions {
		if err := store.SnapshotSession(s); err != nil {
			t.Fatalf("SnapshotSession: %v", err)
		}
	}

	got, err := store.Count(session.Filter{Status: types.SessionStatusRunning})
	if err != nil {
		t.Fatalf("Count(status=running): %v", err)
	}
	if got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
}

func TestStore_CountReturnsErrorAfterClose(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")
	store, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := store.Count(session.Filter{}); err == nil {
		t.Fatal("expected error when counting on closed store")
	}
}

func TestRegistry_CountFilteredAll(t *testing.T) {
	reg := session.NewRegistry()
	r1 := reg.Create("cli", types.SessionModeLive, "/tmp/1")
	r2 := reg.Create("cli", types.SessionModeLive, "/tmp/2")
	_ = r1
	_ = r2
	reg.Update(r1.ID, func(s *session.Session) {
		s.Status = types.SessionStatusRunning
	})
	reg.Update(r2.ID, func(s *session.Session) {
		s.Status = types.SessionStatusCompleted
	})

	got := reg.CountFiltered(session.Filter{})
	if got != 2 {
		t.Fatalf("CountFiltered() = %d, want 2", got)
	}
}

func TestRegistry_CountFilteredByStatus(t *testing.T) {
	reg := session.NewRegistry()
	r1 := reg.Create("cli", types.SessionModeLive, "/tmp/1")
	r2 := reg.Create("cli", types.SessionModeLive, "/tmp/2")
	r3 := reg.Create("cli", types.SessionModeLive, "/tmp/3")
	reg.Update(r1.ID, func(s *session.Session) {
		s.Status = types.SessionStatusRunning
	})
	reg.Update(r2.ID, func(s *session.Session) {
		s.Status = types.SessionStatusCompleted
	})
	reg.Update(r3.ID, func(s *session.Session) {
		s.Status = types.SessionStatusRunning
	})

	got := reg.CountFiltered(session.Filter{Status: types.SessionStatusRunning})
	if got != 2 {
		t.Fatalf("CountFiltered(running) = %d, want 2", got)
	}
}
