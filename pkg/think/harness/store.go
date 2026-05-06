package harness

import (
	"context"
	"sync"
	"time"
)

type Store interface {
	Create(ctx context.Context, session ThinkingSession) (ThinkingSession, error)
	Get(ctx context.Context, id string) (ThinkingSession, error)
	Update(ctx context.Context, id string, fn func(ThinkingSession) (ThinkingSession, error)) (ThinkingSession, error)
	Prune(ctx context.Context, ttl time.Duration) (int, error)
}

type InMemoryStore struct {
	mu       sync.RWMutex
	sessions map[string]ThinkingSession
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{sessions: make(map[string]ThinkingSession)}
}

func (s *InMemoryStore) Create(ctx context.Context, session ThinkingSession) (ThinkingSession, error) {
	if err := ctx.Err(); err != nil {
		return ThinkingSession{}, err
	}
	if session.ID == "" {
		return ThinkingSession{}, invalidInputError("session requires id", "Create the session with a non-empty session_id.")
	}
	if _, err := NewTaskFrame(session.Frame); err != nil {
		return ThinkingSession{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[session.ID]; exists {
		return ThinkingSession{}, duplicateSessionError(session.ID)
	}
	cloned := session.clone()
	s.sessions[session.ID] = cloned
	return cloned.clone(), nil
}

func (s *InMemoryStore) Get(ctx context.Context, id string) (ThinkingSession, error) {
	if err := ctx.Err(); err != nil {
		return ThinkingSession{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	if !ok {
		return ThinkingSession{}, unknownSessionError(id)
	}
	return session.clone(), nil
}

func (s *InMemoryStore) Update(ctx context.Context, id string, fn func(ThinkingSession) (ThinkingSession, error)) (ThinkingSession, error) {
	if err := ctx.Err(); err != nil {
		return ThinkingSession{}, err
	}
	if fn == nil {
		return ThinkingSession{}, invalidInputError("update requires function", "Provide a session update function.")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.sessions[id]
	if !ok {
		return ThinkingSession{}, unknownSessionError(id)
	}

	next, err := fn(current.clone())
	if err != nil {
		return ThinkingSession{}, err
	}
	if next.ID == "" {
		next.ID = id
	}
	if next.ID != id {
		return ThinkingSession{}, invalidInputError("session update cannot change id", "Return a session with the same session_id.")
	}
	s.sessions[id] = next.clone()
	return next.clone(), nil
}

func (s *InMemoryStore) Prune(ctx context.Context, ttl time.Duration) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if ttl <= 0 {
		return 0, invalidInputError("prune requires positive ttl", "Provide a positive session ttl.")
	}

	cutoff := time.Now().UTC().Add(-ttl)
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for id, session := range s.sessions {
		lastSeen := session.UpdatedAt
		if lastSeen.IsZero() {
			lastSeen = session.StartedAt
		}
		if lastSeen.IsZero() || lastSeen.Before(cutoff) {
			delete(s.sessions, id)
			removed++
		}
	}
	return removed, nil
}
