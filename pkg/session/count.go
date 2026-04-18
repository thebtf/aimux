package session

import (
	"database/sql"
	"fmt"

	"github.com/thebtf/aimux/pkg/types"
)

// Filter holds optional criteria for filtering session queries.
// An empty Filter matches all sessions.
type Filter struct {
	Status types.SessionStatus // empty string = all statuses
}

// Count returns the number of sessions matching the filter using SQL COUNT.
// This satisfies the optional Counter interface used by the budget package (FR-4, C2).
func (s *Store) Count(filter Filter) (int, error) {
	var row *sql.Row
	if filter.Status == "" {
		row = s.db.QueryRow(`SELECT COUNT(*) FROM sessions`)
	} else {
		row = s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE status = ?`, string(filter.Status))
	}
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("session store: count: %w", err)
	}
	return count, nil
}

// CountFiltered returns the number of sessions in the registry matching the filter.
// Supplements the existing Count() method (no filter) on Registry.
func (r *Registry) CountFiltered(filter Filter) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if filter.Status == "" {
		return len(r.sessions)
	}
	count := 0
	for _, s := range r.sessions {
		if s.Status == filter.Status {
			count++
		}
	}
	return count
}
