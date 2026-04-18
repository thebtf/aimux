package loom

import "fmt"

// TaskFilter holds optional filtering criteria for loom task queries.
// An empty TaskFilter (zero value) matches all tasks.
type TaskFilter struct {
	ProjectID string
	Statuses  []TaskStatus
}

// Count returns the number of tasks matching the filter using SQL COUNT.
// This is an optional capability that can be used by budget-layer callers (FR-4, C2).
func (e *LoomEngine) Count(filter TaskFilter) (int, error) {
	return e.store.Count(filter)
}

// Count returns the number of tasks matching the filter.
// Uses SQL COUNT for efficiency — avoids loading full rows.
func (s *TaskStore) Count(filter TaskFilter) (int, error) {
	if filter.ProjectID == "" && len(filter.Statuses) == 0 {
		var count int
		err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&count)
		if err != nil {
			return 0, fmt.Errorf("loom store: count: %w", err)
		}
		return count, nil
	}

	query := `SELECT COUNT(*) FROM tasks WHERE 1=1`
	var args []interface{}

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
