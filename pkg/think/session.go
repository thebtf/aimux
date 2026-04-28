package think

import (
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// EnsureSessionID returns a freshly generated UUID when sid is empty, or sid
// unchanged when the caller already holds a session identifier.
// This prevents all cold-start callers from sharing the session store key "".
func EnsureSessionID(sid string) string {
	if sid == "" {
		return uuid.NewString()
	}
	return sid
}

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

// GCSessions removes sessions that haven't been accessed within the given TTL.
// Returns the number of sessions removed.
// Sessions whose LastAccessedAt timestamp cannot be parsed are treated as
// expired and removed; the parse failure is logged so operators can detect
// data corruption.
func GCSessions(ttl time.Duration) int {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	cutoff := time.Now().Add(-ttl)
	removed := 0
	parseErrors := 0
	for id, s := range sessions {
		lastAccessed, err := time.Parse(time.RFC3339, s.LastAccessedAt)
		if err != nil {
			parseErrors++
			log.Printf("think: GCSessions: session %s has unparseable LastAccessedAt %q, treating as expired: %v",
				id, s.LastAccessedAt, err)
			delete(sessions, id)
			removed++
			continue
		}
		if lastAccessed.Before(cutoff) {
			delete(sessions, id)
			removed++
		}
	}
	if parseErrors > 0 {
		log.Printf("think: GCSessions: removed %d sessions with corrupt timestamps", parseErrors)
	}
	return removed
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

// patternStackKey is the session state key that stores the pattern stack.
const patternStackKey = "_patternStack"

// patternStackSnapshotKey is the session state key that stores the state
// snapshots corresponding to each pushed pattern entry.
const patternStackSnapshotKey = "_patternStackSnapshots"

// patternStackBaseKey is the session state key that stores the original base
// pattern, preserved on the first push so that popping the entire stack
// restores the correct pattern name.
const patternStackBaseKey = "_patternStackBase"

// maxPatternStackDepth is the maximum allowed depth of the pattern stack.
const maxPatternStackDepth = 5

// PushPattern pushes a new pattern onto the session's pattern stack and saves a
// snapshot of the current state. Returns false if the stack is already at max depth.
func PushPattern(sessionID, name string) bool {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	existing, ok := sessions[sessionID]
	if !ok {
		return false
	}

	stack := patternStack(existing.State)
	if len(stack) >= maxPatternStackDepth {
		return false
	}

	snapshots := patternSnapshots(existing.State)

	newState := copyState(existing.State)

	// On the first push, record the base pattern so it can be restored on full pop.
	if len(stack) == 0 {
		newState[patternStackBaseKey] = existing.Pattern
	}

	// Capture a snapshot of current state (without the stack meta keys).
	snapshot := make(map[string]any, len(existing.State))
	for k, v := range existing.State {
		if k != patternStackKey && k != patternStackSnapshotKey && k != patternStackBaseKey {
			snapshot[k] = v
		}
	}
	snapshots = append(snapshots, snapshot)

	stack = append(stack, name)
	newState[patternStackKey] = stack
	newState[patternStackSnapshotKey] = snapshots

	now := time.Now().UTC().Format(time.RFC3339)
	sessions[sessionID] = &ThinkSession{
		ID:             existing.ID,
		Pattern:        name,
		CreatedAt:      existing.CreatedAt,
		LastAccessedAt: now,
		State:          newState,
	}
	return true
}

// PopPattern pops the top pattern from the session's pattern stack and restores
// the state snapshot saved at push time. Returns the popped pattern name, or ""
// if the stack is empty or the session does not exist.
func PopPattern(sessionID string) string {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	existing, ok := sessions[sessionID]
	if !ok {
		return ""
	}

	stack := patternStack(existing.State)
	if len(stack) == 0 {
		return ""
	}

	popped := stack[len(stack)-1]
	stack = stack[:len(stack)-1]

	snapshots := patternSnapshots(existing.State)
	var restoredState map[string]any
	if len(snapshots) > 0 {
		restoredState = snapshots[len(snapshots)-1]
		snapshots = snapshots[:len(snapshots)-1]
	} else {
		restoredState = make(map[string]any)
	}

	restoredState[patternStackKey] = stack
	restoredState[patternStackSnapshotKey] = snapshots

	// Determine which pattern name to restore.
	var restoredPattern string
	if len(stack) > 0 {
		restoredPattern = stack[len(stack)-1]
	} else {
		// Stack is now empty — restore the original base pattern.
		if base, ok := existing.State[patternStackBaseKey].(string); ok && base != "" {
			restoredPattern = base
		} else {
			restoredPattern = existing.Pattern
		}
		// Clear the base key from restored state since stack is empty.
		delete(restoredState, patternStackBaseKey)
	}
	// Carry base key forward while stack is non-empty.
	if len(stack) > 0 {
		if base, ok := existing.State[patternStackBaseKey]; ok {
			restoredState[patternStackBaseKey] = base
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sessions[sessionID] = &ThinkSession{
		ID:             existing.ID,
		Pattern:        restoredPattern,
		CreatedAt:      existing.CreatedAt,
		LastAccessedAt: now,
		State:          restoredState,
	}
	return popped
}

// CurrentPattern returns the name of the pattern currently on top of the stack
// for the session, or the session's base pattern if the stack is empty.
// Returns "" if the session does not exist.
func CurrentPattern(sessionID string) string {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()

	existing, ok := sessions[sessionID]
	if !ok {
		return ""
	}

	stack := patternStack(existing.State)
	if len(stack) == 0 {
		return existing.Pattern
	}
	return stack[len(stack)-1]
}

// patternStack reads the pattern stack from session state.
func patternStack(state map[string]any) []string {
	v, ok := state[patternStackKey]
	if !ok {
		return nil
	}
	switch val := v.(type) {
	case []string:
		out := make([]string, len(val))
		copy(out, val)
		return out
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// patternSnapshots reads the snapshot slice from session state.
func patternSnapshots(state map[string]any) []map[string]any {
	v, ok := state[patternStackSnapshotKey]
	if !ok {
		return nil
	}
	raw, ok := v.([]map[string]any)
	if ok {
		out := make([]map[string]any, len(raw))
		copy(out, raw)
		return out
	}
	// Also handle []any (e.g. after round-tripping through JSON or UpdateSessionState).
	rawAny, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(rawAny))
	for _, item := range rawAny {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
