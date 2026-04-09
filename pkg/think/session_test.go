package think

import (
	"testing"
	"time"
)

func TestGetOrCreateSession_New(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	s := GetOrCreateSession("s1", "think", map[string]any{"count": 0})
	if s.ID != "s1" {
		t.Errorf("ID = %q, want s1", s.ID)
	}
	if s.Pattern != "think" {
		t.Errorf("Pattern = %q, want think", s.Pattern)
	}
	if s.State["count"] != 0 {
		t.Errorf("State[count] = %v, want 0", s.State["count"])
	}
	if GetSessionCount() != 1 {
		t.Errorf("count = %d, want 1", GetSessionCount())
	}
}

func TestGetOrCreateSession_Existing(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	GetOrCreateSession("s1", "think", map[string]any{"count": 0})
	s := GetOrCreateSession("s1", "think", map[string]any{"count": 99})

	// Should return existing, not overwrite with new initial state
	if s.State["count"] != 0 {
		t.Errorf("State[count] = %v, want 0 (existing session)", s.State["count"])
	}
}

func TestGetSession_Missing(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	s := GetSession("nonexistent")
	if s != nil {
		t.Error("expected nil for missing session")
	}
}

func TestUpdateSessionState(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	GetOrCreateSession("s1", "think", map[string]any{"a": 1})
	updated := UpdateSessionState("s1", map[string]any{"b": 2})

	if updated == nil {
		t.Fatal("expected updated session, got nil")
	}
	if updated.State["a"] != 1 {
		t.Errorf("State[a] = %v, want 1", updated.State["a"])
	}
	if updated.State["b"] != 2 {
		t.Errorf("State[b] = %v, want 2", updated.State["b"])
	}
}

func TestUpdateSessionState_Missing(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	updated := UpdateSessionState("nonexistent", map[string]any{"x": 1})
	if updated != nil {
		t.Error("expected nil for missing session update")
	}
}

func TestDeleteSession(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	GetOrCreateSession("s1", "think", nil)
	if !DeleteSession("s1") {
		t.Error("expected true for existing session delete")
	}
	if DeleteSession("s1") {
		t.Error("expected false for already-deleted session")
	}
	if GetSessionCount() != 0 {
		t.Error("expected 0 sessions after delete")
	}
}

func TestSessionImmutability(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	s1 := GetOrCreateSession("s1", "think", map[string]any{"val": 1})
	s1.State["val"] = 999 // mutate the returned copy

	s2 := GetSession("s1")
	if s2.State["val"] != 1 {
		t.Errorf("mutation leaked: State[val] = %v, want 1", s2.State["val"])
	}
}

func TestClearSessions(t *testing.T) {
	ClearSessions()
	GetOrCreateSession("a", "think", nil)
	GetOrCreateSession("b", "think", nil)
	ClearSessions()

	if GetSessionCount() != 0 {
		t.Errorf("count = %d, want 0 after clear", GetSessionCount())
	}
}

func TestGCSessions_RemovesExpired(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	// Create sessions — they get current timestamp
	GetOrCreateSession("fresh", "think", nil)
	GetOrCreateSession("stale", "think", nil)

	// Manually backdate the stale session's LastAccessedAt
	sessionsMu.Lock()
	if s, ok := sessions["stale"]; ok {
		old := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
		sessions["stale"] = &ThinkSession{
			ID: s.ID, Pattern: s.Pattern, CreatedAt: s.CreatedAt,
			LastAccessedAt: old, State: s.State,
		}
	}
	sessionsMu.Unlock()

	removed := GCSessions(1 * time.Hour)
	if removed != 1 {
		t.Errorf("GCSessions removed %d, want 1", removed)
	}
	if GetSessionCount() != 1 {
		t.Errorf("count = %d, want 1 after GC", GetSessionCount())
	}
	if GetSession("fresh") == nil {
		t.Error("fresh session should survive GC")
	}
	if GetSession("stale") != nil {
		t.Error("stale session should be removed by GC")
	}
}

func TestGCSessions_KeepsActive(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	GetOrCreateSession("a", "think", nil)
	GetOrCreateSession("b", "think", nil)

	removed := GCSessions(1 * time.Hour)
	if removed != 0 {
		t.Errorf("GCSessions removed %d, want 0 (all fresh)", removed)
	}
	if GetSessionCount() != 2 {
		t.Errorf("count = %d, want 2", GetSessionCount())
	}
}
