package loom

import (
	"context"
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
	TaskArtifactKindRuntime   TaskArtifactKind = "runtime"
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

// TaskArtifactAppend is caller-owned input for a projection row. Seq, EventSeq,
// and CreatedAt are assigned by the store so callers cannot forge cursor identity.
type TaskArtifactAppend struct {
	Kind          TaskArtifactKind
	EventType     string
	Channel       string
	Summary       string
	Payload       map[string]any
	ContentLength int64
	Redacted      bool
	Truncated     bool
}

// TaskArtifact is a durable projection row for a Loom task.
type TaskArtifact struct {
	Seq           int64            `json:"seq"`
	EventSeq      int64            `json:"event_seq"`
	TaskID        string           `json:"task_id"`
	Kind          TaskArtifactKind `json:"kind"`
	EventType     string           `json:"event_type,omitempty"`
	Channel       string           `json:"channel,omitempty"`
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
	Cursor     string
	Limit      int
	Kinds      []TaskArtifactKind
	EventTypes []string
	Channels   []string
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

// ListArtifacts returns a deterministic projection page through the Loom
// engine boundary. Canonical task state remains owned by Get/GetContext.
func (l *LoomEngine) ListArtifacts(taskID string, opts TaskArtifactListOptions) (TaskArtifactPage, error) {
	if l == nil || l.store == nil {
		return TaskArtifactPage{}, fmt.Errorf("loom: list artifacts: engine unavailable")
	}
	return l.store.ListArtifacts(taskID, opts)
}

// TaskRuntimeEventAppend is the normalized input shape for runtime evidence.
// Runtime rows are projection-only; canonical task status remains owned by the
// task table and lifecycle transitions.
type TaskRuntimeEventAppend struct {
	EventType     string
	Channel       string
	Summary       string
	Payload       map[string]any
	ContentLength int64
	Redacted      bool
	Truncated     bool
}

// AppendRuntimeEvent persists one runtime projection row through the Loom
// engine boundary without mutating canonical task status.
func (l *LoomEngine) AppendRuntimeEvent(taskID string, input TaskRuntimeEventAppend) (TaskArtifact, error) {
	if l == nil || l.store == nil {
		return TaskArtifact{}, fmt.Errorf("loom: append runtime event: engine unavailable")
	}
	return l.store.AppendRuntimeEvent(taskID, input)
}

// AppendRuntimeEvents persists one atomic runtime-event batch and publishes a
// payload-free wake-up only after the committed rows are visible to readers.
func (l *LoomEngine) AppendRuntimeEvents(taskID string, batch []TaskRuntimeEventAppend) ([]TaskArtifact, error) {
	return l.AppendRuntimeEventsContext(context.Background(), taskID, batch)
}

// AppendRuntimeEventsContext is the cancellation-aware form used by bounded
// runtime writers. It preserves the same atomic commit-before-notify contract.
func (l *LoomEngine) AppendRuntimeEventsContext(ctx context.Context, taskID string, batch []TaskRuntimeEventAppend) ([]TaskArtifact, error) {
	if l == nil || l.store == nil {
		return nil, fmt.Errorf("loom: append runtime events: engine unavailable")
	}
	result, err := l.store.appendRuntimeEventsContext(ctx, taskID, batch)
	if err != nil {
		return nil, err
	}
	if len(result.artifacts) == 0 {
		return result.artifacts, nil
	}
	l.events.Emit(TaskEvent{
		Type:      EventTaskArtifactsAppended,
		TaskID:    taskID,
		ProjectID: result.projectID,
		RequestID: result.requestID,
		Status:    result.status,
		Timestamp: l.clock.Now().UTC(),
	})
	return result.artifacts, nil
}

// AppendRuntimeEvent persists one normalized runtime projection row for taskID.
func (s *TaskStore) AppendRuntimeEvent(taskID string, input TaskRuntimeEventAppend) (TaskArtifact, error) {
	return s.AppendArtifact(taskID, TaskArtifactAppend{
		Kind:          TaskArtifactKindRuntime,
		EventType:     input.EventType,
		Channel:       input.Channel,
		Summary:       input.Summary,
		Payload:       input.Payload,
		ContentLength: input.ContentLength,
		Redacted:      input.Redacted,
		Truncated:     input.Truncated,
	})
}

// AppendRuntimeEvents persists a normalized runtime-event batch in one pinned
// SQLite transaction and returns only rows from the successful commit.
func (s *TaskStore) AppendRuntimeEvents(taskID string, batch []TaskRuntimeEventAppend) ([]TaskArtifact, error) {
	return s.AppendRuntimeEventsContext(context.Background(), taskID, batch)
}

// AppendRuntimeEventsContext persists an atomic runtime-event batch while
// honoring caller cancellation before commit.
func (s *TaskStore) AppendRuntimeEventsContext(ctx context.Context, taskID string, batch []TaskRuntimeEventAppend) ([]TaskArtifact, error) {
	result, err := s.appendRuntimeEventsContext(ctx, taskID, batch)
	if err != nil {
		return nil, err
	}
	return result.artifacts, nil
}

type preparedTaskArtifactAppend struct {
	kind          TaskArtifactKind
	eventType     string
	channel       string
	summary       string
	payloadJSON   string
	contentLength int64
	redacted      bool
	truncated     bool
	createdAt     time.Time
}

type runtimeEventBatchResult struct {
	artifacts []TaskArtifact
	projectID string
	requestID string
	status    TaskStatus
}

func prepareTaskArtifactAppend(input TaskArtifactAppend) (preparedTaskArtifactAppend, error) {
	if err := validateTaskArtifactKind(input.Kind); err != nil {
		return preparedTaskArtifactAppend{}, err
	}
	summary, redacted, truncated := prepareArtifactSummary(input.Summary)
	payload, payloadRedacted, payloadTruncated := prepareArtifactPayload(input.Payload)
	payloadJSON, err := marshalJSON(payload)
	if err != nil {
		return preparedTaskArtifactAppend{}, fmt.Errorf("loom store: append artifact marshal payload: %w", err)
	}
	contentLength := input.ContentLength
	if contentLength < 0 {
		contentLength = 0
	}
	if contentLength == 0 && input.Summary != "" {
		contentLength = int64(len(input.Summary))
	}
	return preparedTaskArtifactAppend{
		kind:          input.Kind,
		eventType:     input.EventType,
		channel:       input.Channel,
		summary:       summary,
		payloadJSON:   payloadJSON,
		contentLength: contentLength,
		redacted:      redacted || input.Redacted || payloadRedacted,
		truncated:     truncated || input.Truncated || payloadTruncated,
		createdAt:     time.Now().UTC(),
	}, nil
}

func (s *TaskStore) appendRuntimeEvents(taskID string, batch []TaskRuntimeEventAppend) (runtimeEventBatchResult, error) {
	return s.appendRuntimeEventsContext(context.Background(), taskID, batch)
}

func (s *TaskStore) appendRuntimeEventsContext(ctx context.Context, taskID string, batch []TaskRuntimeEventAppend) (runtimeEventBatchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return runtimeEventBatchResult{}, err
	}
	if strings.TrimSpace(taskID) == "" {
		return runtimeEventBatchResult{}, fmt.Errorf("loom store: append runtime events: missing task id")
	}
	if len(batch) == 0 {
		return runtimeEventBatchResult{artifacts: []TaskArtifact{}}, nil
	}
	prepared := make([]preparedTaskArtifactAppend, 0, len(batch))
	for _, input := range batch {
		artifact, err := prepareTaskArtifactAppend(TaskArtifactAppend{
			Kind:          TaskArtifactKindRuntime,
			EventType:     input.EventType,
			Channel:       input.Channel,
			Summary:       input.Summary,
			Payload:       input.Payload,
			ContentLength: input.ContentLength,
			Redacted:      input.Redacted,
			Truncated:     input.Truncated,
		})
		if err != nil {
			return runtimeEventBatchResult{}, err
		}
		prepared = append(prepared, artifact)
	}
	return s.appendPreparedArtifactsContext(ctx, taskID, prepared)
}

// AppendArtifact persists one projection row for a Loom task. It validates that
// the source task exists but never updates canonical task state.
func (s *TaskStore) AppendArtifact(taskID string, input TaskArtifactAppend) (TaskArtifact, error) {
	if strings.TrimSpace(taskID) == "" {
		return TaskArtifact{}, fmt.Errorf("loom store: append artifact: missing task id")
	}
	prepared, err := prepareTaskArtifactAppend(input)
	if err != nil {
		return TaskArtifact{}, err
	}
	result, err := s.appendPreparedArtifacts(taskID, []preparedTaskArtifactAppend{prepared})
	if err != nil {
		return TaskArtifact{}, err
	}
	return result.artifacts[0], nil
}

func (s *TaskStore) appendPreparedArtifacts(taskID string, batch []preparedTaskArtifactAppend) (runtimeEventBatchResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.appendPreparedArtifactsContext(ctx, taskID, batch)
}

func (s *TaskStore) appendPreparedArtifactsContext(ctx context.Context, taskID string, batch []preparedTaskArtifactAppend) (runtimeEventBatchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return runtimeEventBatchResult{}, fmt.Errorf("loom store: append artifacts begin: %w", err)
	}
	defer tx.rollback()

	result := runtimeEventBatchResult{artifacts: make([]TaskArtifact, 0, len(batch))}
	var nextEventSeq int64
	err = tx.conn.QueryRowContext(ctx, `
		SELECT t.project_id, t.request_id, t.status,
		       COALESCE((SELECT MAX(a.event_seq) FROM task_artifacts a WHERE a.task_id=t.id), 0)
		FROM tasks t WHERE t.id=?`, taskID).Scan(
		&result.projectID,
		&result.requestID,
		&result.status,
		&nextEventSeq,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runtimeEventBatchResult{}, ErrTaskNotFound
	}
	if err != nil {
		return runtimeEventBatchResult{}, fmt.Errorf("loom store: append artifacts load task: %w", err)
	}

	for _, input := range batch {
		nextEventSeq++
		var artifact TaskArtifact
		var payloadRaw string
		var redactedInt, truncatedInt int
		err := tx.conn.QueryRowContext(ctx, `
			INSERT INTO task_artifacts
				(task_id, event_seq, kind, event_type, channel, summary, payload_json, content_length, redacted, truncated, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			RETURNING seq, event_seq, task_id, kind, event_type, channel, summary, payload_json, content_length, redacted, truncated, created_at`,
			taskID,
			nextEventSeq,
			string(input.kind),
			input.eventType,
			input.channel,
			input.summary,
			input.payloadJSON,
			input.contentLength,
			boolToInt(input.redacted),
			boolToInt(input.truncated),
			input.createdAt,
		).Scan(
			&artifact.Seq,
			&artifact.EventSeq,
			&artifact.TaskID,
			&artifact.Kind,
			&artifact.EventType,
			&artifact.Channel,
			&artifact.Summary,
			&payloadRaw,
			&artifact.ContentLength,
			&redactedInt,
			&truncatedInt,
			&artifact.CreatedAt,
		)
		if err != nil {
			return runtimeEventBatchResult{}, fmt.Errorf("loom store: append artifact: %w", err)
		}
		artifact.Redacted = redactedInt != 0
		artifact.Truncated = truncatedInt != 0
		artifact.Payload = decodeArtifactPayload(payloadRaw)
		result.artifacts = append(result.artifacts, artifact)
	}
	if err := tx.commit(); err != nil {
		return runtimeEventBatchResult{}, fmt.Errorf("loom store: append artifacts commit: %w", err)
	}
	return result, nil
}

// CheckpointWAL performs a narrow passive checkpoint without exposing the
// underlying *sql.DB outside Loom.
func (l *LoomEngine) CheckpointWAL(ctx context.Context) error {
	if l == nil || l.store == nil {
		return fmt.Errorf("loom: checkpoint WAL: engine unavailable")
	}
	return l.store.CheckpointWAL(ctx)
}

// CheckpointWAL requests a passive SQLite WAL checkpoint.
func (s *TaskStore) CheckpointWAL(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("loom store: checkpoint WAL: store unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, taskStorePragmaTimeout)
		defer cancel()
	}
	var busy, logFrames, checkpointedFrames int
	if err := s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
		return fmt.Errorf("loom store: checkpoint WAL: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf("loom store: checkpoint WAL: SQLITE_BUSY (%d log frames, %d checkpointed)", logFrames, checkpointedFrames)
	}
	return nil
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
		SELECT seq, event_seq, task_id, kind, event_type, channel, summary, payload_json, content_length, redacted, truncated, created_at
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
	if len(opts.EventTypes) > 0 {
		placeholders := make([]string, 0, len(opts.EventTypes))
		for _, eventType := range opts.EventTypes {
			eventType = strings.TrimSpace(eventType)
			if eventType == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, eventType)
		}
		if len(placeholders) > 0 {
			query += " AND event_type IN (" + strings.Join(placeholders, ",") + ")"
		}
	}
	if len(opts.Channels) > 0 {
		placeholders := make([]string, 0, len(opts.Channels))
		for _, channel := range opts.Channels {
			channel = strings.TrimSpace(channel)
			if channel == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, channel)
		}
		if len(placeholders) > 0 {
			query += " AND channel IN (" + strings.Join(placeholders, ",") + ")"
		}
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
	nextCursor := opts.Cursor
	if len(items) > 0 {
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
	case TaskArtifactKindLifecycle, TaskArtifactKindProgress, TaskArtifactKindTerminal, TaskArtifactKindRuntime:
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

func prepareArtifactPayload(payload map[string]any) (map[string]any, bool, bool) {
	if payload == nil {
		return map[string]any{}, false, false
	}
	prepared := make(map[string]any, len(payload))
	redacted := false
	truncated := false
	for key, value := range payload {
		preparedValue, valueRedacted, valueTruncated := prepareArtifactPayloadValue(value)
		prepared[key] = preparedValue
		redacted = redacted || valueRedacted
		truncated = truncated || valueTruncated
	}
	return prepared, redacted, truncated
}

func prepareArtifactPayloadValue(value any) (any, bool, bool) {
	switch typed := value.(type) {
	case string:
		return prepareArtifactSummary(typed)
	case map[string]any:
		return prepareArtifactPayload(typed)
	case []any:
		out := make([]any, 0, len(typed))
		redacted := false
		truncated := false
		for _, item := range typed {
			prepared, itemRedacted, itemTruncated := prepareArtifactPayloadValue(item)
			out = append(out, prepared)
			redacted = redacted || itemRedacted
			truncated = truncated || itemTruncated
		}
		return out, redacted, truncated
	default:
		return value, false, false
	}
}

func scanTaskArtifact(rows *sql.Rows) (TaskArtifact, error) {
	var artifact TaskArtifact
	var payloadRaw string
	var redactedInt, truncatedInt int
	if err := rows.Scan(
		&artifact.Seq,
		&artifact.EventSeq,
		&artifact.TaskID,
		&artifact.Kind,
		&artifact.EventType,
		&artifact.Channel,
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
