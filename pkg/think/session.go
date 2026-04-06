package think

import (
	"sync"
	"time"
)

var (
	sessionsMu sync.RWMutex
	sessions   = make(map[string]*ThinkSession)
)

// GetOrCreateSession returns an existing session (with updated timestamp) or creates a new one.
func GetOrCreateSession(id, pattern string, initialState map[string]any) *ThinkSession {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)

	if existing, ok := sessions[id]; ok {
		updated := &ThinkSession{
			ID:             existing.ID,
			Pattern:        existing.Pattern,
			CreatedAt:      existing.CreatedAt,
			LastAccessedAt: now,
			State:          copyState(existing.State),
		}
		sessions[id] = updated
		return copySession(updated)
	}

	state := make(map[string]any)
	if initialState != nil {
		for k, v := range initialState {
			state[k] = v
		}
	}

	s := &ThinkSession{
		ID:             id,
		Pattern:        pattern,
		CreatedAt:      now,
		LastAccessedAt: now,
		State:          state,
	}
	sessions[id] = s
	return copySession(s)
}

// GetSession returns a session by ID with updated timestamp, or nil if not found.
func GetSession(id string) *ThinkSession {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	existing, ok := sessions[id]
	if !ok {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	updated := &ThinkSession{
		ID:             existing.ID,
		Pattern:        existing.Pattern,
		CreatedAt:      existing.CreatedAt,
		LastAccessedAt: now,
		State:          copyState(existing.State),
	}
	sessions[id] = updated
	return copySession(updated)
}

// UpdateSessionState merges patch into the session's state, creating a new copy.
func UpdateSessionState(id string, patch map[string]any) *ThinkSession {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	existing, ok := sessions[id]
	if !ok {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	newState := copyState(existing.State)
	for k, v := range patch {
		newState[k] = v
	}

	updated := &ThinkSession{
		ID:             existing.ID,
		Pattern:        existing.Pattern,
		CreatedAt:      existing.CreatedAt,
		LastAccessedAt: now,
		State:          newState,
	}
	sessions[id] = updated
	return copySession(updated)
}

// DeleteSession removes a session by ID. Returns true if it existed.
func DeleteSession(id string) bool {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	_, ok := sessions[id]
	if ok {
		delete(sessions, id)
	}
	return ok
}

// ClearSessions removes all sessions (testing only).
func ClearSessions() {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	sessions = make(map[string]*ThinkSession)
}

// GetSessionCount returns the number of active sessions.
func GetSessionCount() int {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	return len(sessions)
}

// copyState creates a shallow copy of a state map.
func copyState(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// copySession creates a new ThinkSession with copied state.
func copySession(s *ThinkSession) *ThinkSession {
	return &ThinkSession{
		ID:             s.ID,
		Pattern:        s.Pattern,
		CreatedAt:      s.CreatedAt,
		LastAccessedAt: s.LastAccessedAt,
		State:          copyState(s.State),
	}
}
