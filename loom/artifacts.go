package loom

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	artifactSummaryMaxBytes = 512
	defaultArtifactPageSize = 20
	maxArtifactPageSize     = 100
)

// ErrInvalidArtifactCursor is returned when a task artifact page cursor cannot
// be parsed. Callers can treat this as a client/input error.
var ErrInvalidArtifactCursor = errors.New("loom: invalid task artifact cursor")

// TaskArtifactKind classifies projection rows without giving them authority
// over canonical Loom task status.
type TaskArtifactKind string

const (
	TaskArtifactKindLifecycle TaskArtifactKind = "lifecycle"
	TaskArtifactKindProgress  TaskArtifactKind = "progress"
	TaskArtifactKindTerminal  TaskArtifactKind = "terminal"
)

// TaskArtifactProjectionStatus describes whether artifact projection data is
// available. Loom task rows remain canonical even when projection data is empty
// or partial.
type TaskArtifactProjectionStatus string

const (
	TaskArtifactProjectionOK      TaskArtifactProjectionStatus = "ok"
	TaskArtifactProjectionEmpty   TaskArtifactProjectionStatus = "empty"
	TaskArtifactProjectionPartial TaskArtifactProjectionStatus = "partial"
)

// TaskArtifactAppend is caller-owned input for a projection row. Seq and
// CreatedAt are assigned by the store so callers cannot forge cursor identity.
type TaskArtifactAppend struct {
	Kind          TaskArtifactKind
	EventType     string
	Summary       string
	Payload       map[string]any
	ContentLength int64
	Redacted      bool
	Truncated     bool
}

// TaskArtifact is a durable projection row for a Loom task.
type TaskArtifact struct {
	Seq           int64            `json:"seq"`
	TaskID        string           `json:"task_id"`
	Kind          TaskArtifactKind `json:"kind"`
	EventType     string           `json:"event_type,omitempty"`
	Summary       string           `json:"summary"`
	Payload       map[string]any   `json:"payload,omitempty"`
	ContentLength int64            `json:"content_length"`
	Redacted      bool             `json:"redacted"`
	Truncated     bool             `json:"truncated"`
	CreatedAt     time.Time        `json:"created_at"`
}

// TaskArtifactListOptions controls deterministic artifact pagination. Cursor
// is intentionally a string so CR-002 can expose it as opaque public API.
type TaskArtifactListOptions struct {
	Cursor string
	Limit  int
	Kinds  []TaskArtifactKind
}

// TaskArtifactPage is the stable page shape consumed by task resources.
type TaskArtifactPage struct {
	TaskID           string                       `json:"task_id"`
	Items            []TaskArtifact               `json:"items"`
	Cursor           string                       `json:"cursor,omitempty"`
	NextCursor       string                       `json:"next_cursor,omitempty"`
	Limit            int                          `json:"limit"`
	HasMore          bool                         `json:"has_more"`
	ProjectionStatus TaskArtifactProjectionStatus `json:"projection_status"`
}

// TaskArtifactProjectionStatusForTask converts artifact-page evidence into the
// status CR-002 resources should expose alongside a canonical Loom task. When
// the task exists but projection rows are missing, the task is still readable
// and the projection is partial rather than a missing task.
func TaskArtifactProjectionStatusForTask(task *Task, page TaskArtifactPage) TaskArtifactProjectionStatus {
	if task == nil {
		return page.ProjectionStatus
	}
	if len(page.Items) == 0 {
		return TaskArtifactProjectionPartial
	}
	if page.ProjectionStatus != "" {
		return page.ProjectionStatus
	}
	return TaskArtifactProjectionOK
}

// AppendArtifact persists one projection row for a Loom task. It validates that
// the source task exists but never updates canonical task state.
func (s *TaskStore) AppendArtifact(taskID string, input TaskArtifactAppend) (TaskArtifact, error) {
	if strings.TrimSpace(taskID) == "" {
		return TaskArtifact{}, fmt.Errorf("loom store: append artifact: missing task id")
	}
	if err := validateTaskArtifactKind(input.Kind); err != nil {
		return TaskArtifact{}, err
	}
	if _, err := s.Get(taskID); err != nil {
		if isNoRows(err) {
			return TaskArtifact{}, ErrTaskNotFound
		}
		return TaskArtifact{}, fmt.Errorf("loom store: append artifact get task: %w", err)
	}

	summary, redacted, truncated := prepareArtifactSummary(input.Summary)
	redacted = redacted || input.Redacted
	truncated = truncated || input.Truncated
	payloadJSON, err := marshalJSON(input.Payload)
	if err != nil {
		return TaskArtifact{}, fmt.Errorf("loom store: append artifact marshal payload: %w", err)
	}

	contentLength := input.ContentLength
	if contentLength < 0 {
		contentLength = 0
	}
	if contentLength == 0 && input.Summary != "" {
		contentLength = int64(len(input.Summary))
	}
	now := time.Now().UTC()

	var artifact TaskArtifact
	var redactedInt, truncatedInt int
	var payloadRaw string
	err = s.db.QueryRow(`
		INSERT INTO task_artifacts
			(task_id, kind, event_type, summary, payload_json, content_length, redacted, truncated, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING seq, task_id, kind, event_type, summary, payload_json, content_length, redacted, truncated, created_at`,
		taskID,
		string(input.Kind),
		input.EventType,
		summary,
		payloadJSON,
		contentLength,
		boolToInt(redacted),
		boolToInt(truncated),
		now,
	).Scan(
		&artifact.Seq,
		&artifact.TaskID,
		&artifact.Kind,
		&artifact.EventType,
		&artifact.Summary,
		&payloadRaw,
		&artifact.ContentLength,
		&redactedInt,
		&truncatedInt,
		&artifact.CreatedAt,
	)
	if err != nil {
		return TaskArtifact{}, fmt.Errorf("loom store: append artifact: %w", err)
	}
	artifact.Redacted = redactedInt != 0
	artifact.Truncated = truncatedInt != 0
	artifact.Payload = decodeArtifactPayload(payloadRaw)
	return artifact, nil
}

// ListArtifacts returns a deterministic page of artifact rows for one Loom
// task. Rows from other tasks cannot appear because task_id is part of the
// WHERE clause before the cursor condition.
func (s *TaskStore) ListArtifacts(taskID string, opts TaskArtifactListOptions) (TaskArtifactPage, error) {
	if strings.TrimSpace(taskID) == "" {
		return TaskArtifactPage{}, fmt.Errorf("loom store: list artifacts: missing task id")
	}
	afterSeq, err := parseArtifactCursor(opts.Cursor)
	if err != nil {
		return TaskArtifactPage{}, err
	}
	limit := normalizeArtifactLimit(opts.Limit)

	query := `
		SELECT seq, task_id, kind, event_type, summary, payload_json, content_length, redacted, truncated, created_at
		FROM task_artifacts
		WHERE task_id = ? AND seq > ?`
	args := []any{taskID, afterSeq}
	if len(opts.Kinds) > 0 {
		placeholders := make([]string, 0, len(opts.Kinds))
		for _, kind := range opts.Kinds {
			if err := validateTaskArtifactKind(kind); err != nil {
				return TaskArtifactPage{}, err
			}
			placeholders = append(placeholders, "?")
			args = append(args, string(kind))
		}
		query += " AND kind IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY seq ASC LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return TaskArtifactPage{}, fmt.Errorf("loom store: list artifacts: %w", err)
	}
	defer rows.Close()

	items := make([]TaskArtifact, 0, limit)
	for rows.Next() {
		artifact, err := scanTaskArtifact(rows)
		if err != nil {
			return TaskArtifactPage{}, err
		}
		items = append(items, artifact)
	}
	if err := rows.Err(); err != nil {
		return TaskArtifactPage{}, fmt.Errorf("loom store: list artifacts rows: %w", err)
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	nextCursor := ""
	if hasMore && len(items) > 0 {
		nextCursor = formatArtifactCursor(items[len(items)-1].Seq)
	}
	status := TaskArtifactProjectionEmpty
	if len(items) > 0 {
		status = TaskArtifactProjectionOK
	}
	return TaskArtifactPage{
		TaskID:           taskID,
		Items:            items,
		Cursor:           opts.Cursor,
		NextCursor:       nextCursor,
		Limit:            limit,
		HasMore:          hasMore,
		ProjectionStatus: status,
	}, nil
}

func validateTaskArtifactKind(kind TaskArtifactKind) error {
	switch kind {
	case TaskArtifactKindLifecycle, TaskArtifactKindProgress, TaskArtifactKindTerminal:
		return nil
	default:
		return fmt.Errorf("loom store: invalid artifact kind %q", kind)
	}
}

func parseArtifactCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	seq, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil || seq < 0 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidArtifactCursor, cursor)
	}
	return seq, nil
}

func formatArtifactCursor(seq int64) string {
	if seq <= 0 {
		return ""
	}
	return strconv.FormatInt(seq, 10)
}

func normalizeArtifactLimit(limit int) int {
	if limit <= 0 {
		return defaultArtifactPageSize
	}
	if limit > maxArtifactPageSize {
		return maxArtifactPageSize
	}
	return limit
}

func prepareArtifactSummary(summary string) (string, bool, bool) {
	redactedSummary := redactErrorMsg(summary)
	redacted := redactedSummary != summary
	truncated := len(redactedSummary) > artifactSummaryMaxBytes
	if truncated {
		redactedSummary = truncateUTF8(redactedSummary, artifactSummaryMaxBytes)
	}
	return redactedSummary, redacted, truncated
}

func scanTaskArtifact(rows *sql.Rows) (TaskArtifact, error) {
	var artifact TaskArtifact
	var payloadRaw string
	var redactedInt, truncatedInt int
	if err := rows.Scan(
		&artifact.Seq,
		&artifact.TaskID,
		&artifact.Kind,
		&artifact.EventType,
		&artifact.Summary,
		&payloadRaw,
		&artifact.ContentLength,
		&redactedInt,
		&truncatedInt,
		&artifact.CreatedAt,
	); err != nil {
		return TaskArtifact{}, fmt.Errorf("loom store: scan artifact: %w", err)
	}
	artifact.Redacted = redactedInt != 0
	artifact.Truncated = truncatedInt != 0
	artifact.Payload = decodeArtifactPayload(payloadRaw)
	return artifact, nil
}

func decodeArtifactPayload(raw string) map[string]any {
	if raw == "" || raw == "{}" || raw == "null" {
		return map[string]any{}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return map[string]any{"decode_error": err.Error()}
	}
	if payload == nil {
		return map[string]any{}
	}
	return payload
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
