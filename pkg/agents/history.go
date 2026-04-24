package agents

import (
	"database/sql"
	"math"
	"time"
)

const dispatchHistorySchema = `CREATE TABLE IF NOT EXISTS dispatch_history (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	agent_name TEXT NOT NULL,
	task_category TEXT NOT NULL,
	outcome TEXT NOT NULL,
	duration_ms INTEGER,
	project_id TEXT,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
)`

// DispatchHistory stores agent dispatch outcomes in SQLite for feedback-driven scoring.
// The decay model uses a 7-day half-life: older records contribute less to the success rate,
// preventing stale data from dominating routing decisions.
type DispatchHistory struct {
	db *sql.DB
}

// NewDispatchHistory creates a DispatchHistory backed by the given *sql.DB.
// The dispatch_history table is created if it does not exist.
func NewDispatchHistory(db *sql.DB) (*DispatchHistory, error) {
	if _, err := db.Exec(dispatchHistorySchema); err != nil {
		return nil, err
	}
	return &DispatchHistory{db: db}, nil
}

// Record inserts a dispatch outcome into the history table.
// outcome should be "success" or "failure".
func (h *DispatchHistory) Record(agentName, taskCategory, outcome string, durationMS int64, projectID string) error {
	_, err := h.db.Exec(
		`INSERT INTO dispatch_history (agent_name, task_category, outcome, duration_ms, project_id) VALUES (?, ?, ?, ?, ?)`,
		agentName, taskCategory, outcome, durationMS, projectID,
	)
	return err
}

// halfLifeDays is the exponential decay half-life used when computing success rates.
// Records older than this contribute less; records exactly halfLifeDays old contribute 50%.
const halfLifeDays = 7.0

// GetSuccessRate returns the exponentially-decayed success rate for an agent+category pair.
// Each record is weighted by exp(-λ·age_days) where λ = ln(2)/halfLifeDays.
// Returns 0.5 if there are no records (neutral prior).
func (h *DispatchHistory) GetSuccessRate(agentName, taskCategory string) float64 {
	rows, err := h.db.Query(
		`SELECT outcome, created_at FROM dispatch_history WHERE agent_name = ? AND task_category = ? ORDER BY created_at DESC LIMIT 200`,
		agentName, taskCategory,
	)
	if err != nil {
		return 0.5
	}
	defer rows.Close()

	lambda := math.Log(2) / halfLifeDays

	var weightedSuccess float64
	var weightedTotal float64

	now := time.Now()
	for rows.Next() {
		var outcome string
		var createdAt time.Time
		if err := rows.Scan(&outcome, &createdAt); err != nil {
			continue
		}
		ageDays := now.Sub(createdAt).Hours() / 24
		weight := math.Exp(-lambda * ageDays)
		weightedTotal += weight
		if outcome == "success" {
			weightedSuccess += weight
		}
	}

	if err := rows.Err(); err != nil {
		return 0.5
	}

	if weightedTotal == 0 {
		return 0.5 // neutral prior — no data
	}
	return weightedSuccess / weightedTotal
}
