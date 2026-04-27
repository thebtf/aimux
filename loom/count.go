package loom

import "fmt"

// TaskFilter holds optional filtering criteria for loom task queries.
// An empty TaskFilter (zero value) matches tasks scoped to the current engine.
// Use CountAll for cross-engine total count.
type TaskFilter struct {
	ProjectID string
	Statuses  []TaskStatus
}

// Count returns the number of tasks matching the filter, scoped to the store's engine_name.
// This is an optional capability that can be used by budget-layer callers (FR-4, C2).
func (e *LoomEngine) Count(filter TaskFilter) (int, error) {
	return e.store.Count(filter)
}

// CountAll returns the total number of tasks across all engines.
// This is an optional capability for cross-engine diagnostics.
func (e *LoomEngine) CountAll() (int, error) {
	return e.store.CountAll()
}

// Count returns the number of tasks matching the filter, scoped to the store's engine_name.
// Uses SQL COUNT for efficiency — avoids loading full rows.
func (s *TaskStore) Count(filter TaskFilter) (int, error) {
	query := `SELECT COUNT(*) FROM tasks WHERE engine_name = ?`
	args := []interface{}{s.engineName}

	if filter.ProjectID != "" {
		query += ` AND project_id = ?`
		args = append(args, filter.ProjectID)
	}

	if len(filter.Statuses) > 0 {
		query += ` AND status IN (`
		for i, st := range filter.Statuses {
			if i > 0 {
				query += ","
			}
			query += "?"
			args = append(args, string(st))
		}
		query += ")"
	}

	var count int
	err := s.db.QueryRow(query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("loom store: count: %w", err)
	}
	return count, nil
}

// CountAll returns the total number of tasks across all engines.
// Unlike Count, this applies no engine_name filter.
func (s *TaskStore) CountAll() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("loom store: count all: %w", err)
	}
	return count, nil
}
