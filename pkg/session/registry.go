// Package session manages in-memory session and job state with WAL persistence.
package session

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thebtf/aimux/pkg/types"
)

// Session represents a tracked CLI session.
type Session struct {
	ID           string              `json:"id"`
	CLI          string              `json:"cli"`
	Mode         types.SessionMode   `json:"mode"`
	CLISessionID string              `json:"cli_session_id,omitempty"`
	PID          int                 `json:"pid"`
	Status       types.SessionStatus `json:"status"`
	Turns        int                 `json:"turns"`
	CWD          string              `json:"cwd"`
	CreatedAt    time.Time           `json:"created_at"`
	LastActiveAt time.Time           `json:"last_active_at"`
}

// Registry manages all active sessions in memory.
type Registry struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewRegistry creates an empty session registry.
func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*Session),
	}
}

// Create registers a new session and returns it.
func (r *Registry) Create(cli string, mode types.SessionMode, cwd string) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New() // V4 fallback — never panics
	}

	s := &Session{
		ID:           id.String(),
		CLI:          cli,
		Mode:         mode,
		Status:       types.SessionStatusCreated,
		CWD:          cwd,
		CreatedAt:    now,
		LastActiveAt: now,
	}

	r.sessions[s.ID] = s
	return s
}

// Import inserts a session from recovery (WAL replay). Thread-safe.
func (r *Registry) Import(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.ID] = s
}

// Get returns a session by ID, or nil if not found.
func (r *Registry) Get(id string) *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[id]
}

// Update modifies a session atomically via a callback.
// Returns false if session not found.
func (r *Registry) Update(id string, fn func(s *Session)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.sessions[id]
	if !ok {
		return false
	}
	fn(s)
	return true
}

// List returns all sessions matching the optional status filter.
func (r *Registry) List(statusFilter types.SessionStatus) []*Session {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*Session
	for _, s := range r.sessions {
		if statusFilter == "" || s.Status == statusFilter {
			result = append(result, s)
		}
	}
	return result
}

// Delete removes a session from the registry.
func (r *Registry) Delete(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.sessions[id]; !ok {
		return false
	}
	delete(r.sessions, id)
	return true
}

// Count returns the total number of sessions.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}
