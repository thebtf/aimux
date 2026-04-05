package orchestrator

// SessionHandoff tracks CLI session IDs across plan→execute phases.
// Planning phase saves session IDs; execution phase resumes them.
type SessionHandoff struct {
	entries map[string]string // task_id → session_id
}

// NewSessionHandoff creates an empty handoff tracker.
func NewSessionHandoff() *SessionHandoff {
	return &SessionHandoff{
		entries: make(map[string]string),
	}
}

// Save records a session ID for a task.
func (h *SessionHandoff) Save(taskID, sessionID string) {
	h.entries[taskID] = sessionID
}

// Get retrieves the session ID for a task.
func (h *SessionHandoff) Get(taskID string) (string, bool) {
	sid, ok := h.entries[taskID]
	return sid, ok
}

// All returns all stored handoffs.
func (h *SessionHandoff) All() map[string]string {
	result := make(map[string]string, len(h.entries))
	for k, v := range h.entries {
		result[k] = v
	}
	return result
}
