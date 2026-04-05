package session_test

import (
	"path/filepath"
	"testing"

	"github.com/thebtf/aimux/pkg/session"
)

func TestWAL_AppendAndReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := session.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}

	// Append entries
	if err := wal.Append("session_create", "s1", map[string]string{"cli": "codex"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := wal.Append("job_create", "j1", map[string]string{"session_id": "s1"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if err := wal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Replay
	entries, err := session.Replay(path)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Type != "session_create" {
		t.Errorf("entry 0 type = %q, want session_create", entries[0].Type)
	}
	if entries[1].ID != "j1" {
		t.Errorf("entry 1 id = %q, want j1", entries[1].ID)
	}
}

func TestWAL_ReplayNonexistent(t *testing.T) {
	entries, err := session.Replay("/nonexistent/path.wal")
	if err != nil {
		t.Fatalf("Replay nonexistent should return nil error, got %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestWAL_Truncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := session.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}

	wal.Append("test", "t1", nil)
	wal.Truncate()
	wal.Close()

	entries, _ := session.Replay(path)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after truncate, got %d", len(entries))
	}
}
