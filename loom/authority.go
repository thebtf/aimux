package loom

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ErrAuthorityConflict marks a durable compare-and-swap loss. Only this error
// may carry a non-zero AuthorityResult; infrastructure and validation failures
// always return wholly zero wrappers.
var ErrAuthorityConflict = errors.New("loom: authority conflict")

type ActionStatus string

const (
	ActionStatusPending         ActionStatus = "pending"
	ActionStatusResponding      ActionStatus = "responding"
	ActionStatusApproved        ActionStatus = "approved"
	ActionStatusDeclined        ActionStatus = "declined"
	ActionStatusAnswered        ActionStatus = "answered"
	ActionStatusDeliveryUnknown ActionStatus = "delivery_unknown"
	ActionStatusTaskClosed      ActionStatus = "task_closed"
)

type PendingAction struct {
	ID                   string
	TaskID               string
	Kind                 string
	Status               ActionStatus
	ProviderRequestID    string
	ConnectionGeneration uint64
	RequestJSON          string
	ResponseJSON         string
	DeliveryJSON         string
	ExpiresAt            time.Time
	CreatedAt            time.Time
	RespondedAt          time.Time
	ResolvedAt           time.Time
}

type StopProofKind string

const (
	StopProofNativeAcknowledged StopProofKind = "native_acknowledged"
	StopProofProcessTreeStopped StopProofKind = "process_tree_stopped"
	StopProofProcessAbsent      StopProofKind = "process_absent"
)

type StopProcessIdentity struct {
	PID              int    `json:"pid"`
	StartFingerprint string `json:"start_fingerprint"`
	TreeID           string `json:"tree_id"`
}

type StopEvidence struct {
	Kind        StopProofKind        `json:"kind"`
	ExecutionID string               `json:"execution_id"`
	Process     *StopProcessIdentity `json:"process,omitempty"`
	ObservedAt  time.Time            `json:"observed_at"`
}

type CanonicalTaskState struct {
	TaskID            string
	Status            TaskStatus
	CreatedAt         time.Time
	Result            string
	Error             string
	DispatchedAt      *time.Time
	CancelRequestedAt *time.Time
	CompletedAt       *time.Time
}

type CanonicalActionState struct {
	ActionID             string
	TaskID               string
	Status               ActionStatus
	ProviderRequestID    string
	ConnectionGeneration uint64
	RespondedAt          *time.Time
	ResolvedAt           *time.Time
}

type AuthorityConflictKind string

const (
	ConflictTaskStatus           AuthorityConflictKind = "task_status"
	ConflictActionID             AuthorityConflictKind = "action_id"
	ConflictProviderCorrelation  AuthorityConflictKind = "provider_correlation"
	ConflictActionMissing        AuthorityConflictKind = "action_missing"
	ConflictDispatchTime         AuthorityConflictKind = "dispatch_time"
	ConflictStopEvidenceTime     AuthorityConflictKind = "stop_evidence_time"
	ConflictActionOwner          AuthorityConflictKind = "action_owner"
	ConflictActionSourceStatus   AuthorityConflictKind = "action_source_status"
	ConflictConnectionGeneration AuthorityConflictKind = "connection_generation"

	AuthorityConflictTaskStatus           AuthorityConflictKind = ConflictTaskStatus
	AuthorityConflictActionID             AuthorityConflictKind = ConflictActionID
	AuthorityConflictProviderCorrelation  AuthorityConflictKind = ConflictProviderCorrelation
	AuthorityConflictActionMissing        AuthorityConflictKind = ConflictActionMissing
	AuthorityConflictDispatchTime         AuthorityConflictKind = ConflictDispatchTime
	AuthorityConflictStopEvidenceTime     AuthorityConflictKind = ConflictStopEvidenceTime
	AuthorityConflictActionOwner          AuthorityConflictKind = ConflictActionOwner
	AuthorityConflictActionSourceStatus   AuthorityConflictKind = ConflictActionSourceStatus
	AuthorityConflictConnectionGeneration AuthorityConflictKind = ConflictConnectionGeneration
)

type AuthorityConflict struct {
	Kind   AuthorityConflictKind
	Action *CanonicalActionState
}

type AuthorityWinner struct {
	Task      CanonicalTaskState
	Action    *CanonicalActionState
	Conflicts []AuthorityConflict
}

type AuthorityResult struct {
	Applied           bool
	Winner            AuthorityWinner
	ArtifactSeq       int64
	ClosedActionCount int64
}

type CancelIntent struct {
	AuthorityResult
	RequiresStop bool
}

type CommitResult struct {
	AuthorityResult
}

type PendingActionAttempt struct {
	AuthorityResult
}

type CreateTask struct {
	TaskID       string
	WorkerType   WorkerType
	ProjectID    string
	RequestID    string
	ParentTaskID string
	TenantID     string
	Prompt       string
	CWD          string
	Env          map[string]string
	CLI          string
	Role         string
	Model        string
	Effort       string
	Timeout      int
	Metadata     map[string]any
	CreatedAt    time.Time
}

type RunTask struct {
	TaskID         string
	ExpectedStatus TaskStatus
	RunningAt      time.Time
}

type RetryTask struct {
	TaskID         string
	ExpectedStatus TaskStatus
	RetryingAt     time.Time
}

type DispatchTask struct {
	TaskID         string
	ExpectedStatus TaskStatus
	DispatchedAt   time.Time
}

type CompleteTask struct {
	TaskID         string
	ExpectedStatus TaskStatus
	Result         string
	CompletedAt    time.Time
}

type FailTask struct {
	TaskID         string
	ExpectedStatus TaskStatus
	Error          string
	CompletedAt    time.Time
}

type FailCrashedTask struct {
	TaskID         string
	ExpectedStatus TaskStatus
	Error          string
	CompletedAt    time.Time
}

type CancelTask struct {
	TaskID         string
	ExpectedStatus TaskStatus
	StopEvidence   StopEvidence
	CompletedAt    time.Time
}

type RequireInput struct {
	TaskID         string
	ExpectedStatus TaskStatus
	Action         PendingAction
	OccurredAt     time.Time
}

type BeginResponse struct {
	TaskID               string
	ActionID             string
	ExpectedTaskStatus   TaskStatus
	ExpectedActionStatus ActionStatus
	ResponseJSON         string
	ConnectionGeneration uint64
	RespondedAt          time.Time
}

type ResolveAction struct {
	TaskID               string
	ActionID             string
	ExpectedTaskStatus   TaskStatus
	ExpectedActionStatus ActionStatus
	Resolution           ActionStatus
	NextTaskStatus       TaskStatus
	ConnectionGeneration uint64
	DeliveryJSON         string
	ResolvedAt           time.Time
}

// TaskAuthority is the sole public compare-and-swap mutation surface for Loom
// task state and pending input actions.
type TaskAuthority interface {
	RequestCancel(context.Context, string, time.Time) (CancelIntent, error)
	CommitDispatched(context.Context, DispatchTask) (CommitResult, error)
	CommitCompleted(context.Context, CompleteTask) (CommitResult, error)
	CommitFailed(context.Context, FailTask) (CommitResult, error)
	CommitFailedCrash(context.Context, FailCrashedTask) (CommitResult, error)
	CommitCancelled(context.Context, CancelTask) (CommitResult, error)
	CommitInputRequired(context.Context, RequireInput) (CommitResult, error)
	BeginActionResponse(context.Context, BeginResponse) (PendingActionAttempt, error)
	CommitActionResolution(context.Context, ResolveAction) (CommitResult, error)
}

type authorityTransaction struct {
	conn   *sql.Conn
	ctx    context.Context
	active bool
}

const authorityRollbackTimeout = 5 * time.Second

func beginAuthorityTransaction(ctx context.Context, db *sql.DB) (*authorityTransaction, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("loom authority acquire connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("loom authority set busy timeout: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("loom authority begin immediate: %w", err)
	}
	return &authorityTransaction{conn: conn, ctx: ctx, active: true}, nil
}

func (tx *authorityTransaction) rollback() error {
	if tx == nil || !tx.active {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), authorityRollbackTimeout)
	defer cancel()
	_, err := tx.conn.ExecContext(cleanupCtx, "ROLLBACK")
	tx.active = false
	if err != nil {
		discardErr := tx.discard()
		if discardErr != nil {
			return fmt.Errorf("%v; discard connection: %w", err, discardErr)
		}
		return err
	}
	return tx.conn.Close()
}

// discard marks a physical connection non-reusable after a failed manual
// rollback. Returning it to database/sql's idle pool could preserve an open
// transaction and poison every later authority call on that connection.
func (tx *authorityTransaction) discard() error {
	if tx == nil || tx.conn == nil {
		return nil
	}
	rawErr := tx.conn.Raw(func(any) error { return driver.ErrBadConn })
	if rawErr != nil && !errors.Is(rawErr, driver.ErrBadConn) && !errors.Is(rawErr, sql.ErrConnDone) {
		return rawErr
	}
	if closeErr := tx.conn.Close(); closeErr != nil && !errors.Is(closeErr, sql.ErrConnDone) {
		return closeErr
	}
	return nil
}

func (tx *authorityTransaction) commit() error {
	if tx == nil || !tx.active {
		return errors.New("loom authority: inactive transaction")
	}
	_, err := tx.conn.ExecContext(tx.ctx, "COMMIT")
	if err != nil {
		rollbackErr := tx.rollback()
		if rollbackErr != nil {
			return fmt.Errorf("%v; rollback: %w", err, rollbackErr)
		}
		return err
	}
	tx.active = false
	return tx.conn.Close()
}

func finishAuthorityError(tx *authorityTransaction, cause error) (AuthorityResult, error) {
	if rollbackErr := tx.rollback(); rollbackErr != nil {
		return AuthorityResult{}, fmt.Errorf("%v; rollback: %w", cause, rollbackErr)
	}
	return AuthorityResult{}, cause
}

func finishAuthorityNotFound(tx *authorityTransaction) (AuthorityResult, error) {
	if rollbackErr := tx.rollback(); rollbackErr != nil {
		return AuthorityResult{}, fmt.Errorf("loom authority not found rollback: %w", rollbackErr)
	}
	return AuthorityResult{}, ErrTaskNotFound
}

func finishAuthorityConflict(
	tx *authorityTransaction,
	task CanonicalTaskState,
	action *CanonicalActionState,
	conflicts []AuthorityConflict,
) (AuthorityResult, error) {
	if rollbackErr := tx.rollback(); rollbackErr != nil {
		return AuthorityResult{}, fmt.Errorf("loom authority conflict rollback: %w", rollbackErr)
	}
	return AuthorityResult{
		Winner: AuthorityWinner{
			Task:      task,
			Action:    action,
			Conflicts: conflicts,
		},
	}, fmt.Errorf("%w: durable state changed", ErrAuthorityConflict)
}

func commitAuthorityResult(tx *authorityTransaction, result AuthorityResult) (AuthorityResult, error) {
	if err := tx.commit(); err != nil {
		return AuthorityResult{}, fmt.Errorf("loom authority commit: %w", err)
	}
	return result, nil
}

func authorityConflict(kind AuthorityConflictKind, action *CanonicalActionState) AuthorityConflict {
	return AuthorityConflict{Kind: kind, Action: action}
}

func authorityTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time.UTC()
	return &at
}

func loadAuthorityTask(ctx context.Context, tx *authorityTransaction, taskID string) (CanonicalTaskState, bool, error) {
	var (
		task                                   CanonicalTaskState
		status                                 string
		dispatched, cancelRequested, completed sql.NullTime
	)
	err := tx.conn.QueryRowContext(ctx,
		"SELECT id,status,created_at,result,error,dispatched_at,cancel_requested_at,completed_at FROM tasks WHERE id=?",
		taskID,
	).Scan(
		&task.TaskID,
		&status,
		&task.CreatedAt,
		&task.Result,
		&task.Error,
		&dispatched,
		&cancelRequested,
		&completed,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalTaskState{}, false, nil
	}
	if err != nil {
		return CanonicalTaskState{}, false, err
	}
	task.Error = redactErrorMsg(task.Error)
	task.Status = TaskStatus(status)
	task.CreatedAt = task.CreatedAt.UTC()
	task.DispatchedAt = authorityTimePointer(dispatched)
	task.CancelRequestedAt = authorityTimePointer(cancelRequested)
	task.CompletedAt = authorityTimePointer(completed)
	return task, true, nil
}

func scanCanonicalAction(row *sql.Row) (CanonicalActionState, bool, error) {
	var (
		action                  CanonicalActionState
		status                  string
		generation              int64
		respondedAt, resolvedAt sql.NullTime
	)
	err := row.Scan(
		&action.ActionID,
		&action.TaskID,
		&status,
		&action.ProviderRequestID,
		&generation,
		&respondedAt,
		&resolvedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalActionState{}, false, nil
	}
	if err != nil {
		return CanonicalActionState{}, false, err
	}
	if generation < 0 {
		return CanonicalActionState{}, false, fmt.Errorf("negative connection generation %d", generation)
	}
	action.Status = ActionStatus(status)
	action.ConnectionGeneration = uint64(generation)
	action.RespondedAt = authorityTimePointer(respondedAt)
	action.ResolvedAt = authorityTimePointer(resolvedAt)
	return action, true, nil
}

func loadAuthorityActionByID(
	ctx context.Context,
	tx *authorityTransaction,
	actionID string,
) (CanonicalActionState, bool, error) {
	return scanCanonicalAction(tx.conn.QueryRowContext(ctx,
		"SELECT id,task_id,status,provider_request_id,connection_generation,responded_at,resolved_at FROM pending_actions WHERE id=?",
		actionID,
	))
}

func loadAuthorityActionByCorrelation(
	ctx context.Context,
	tx *authorityTransaction,
	taskID, providerRequestID string,
	generation uint64,
) (CanonicalActionState, bool, error) {
	return scanCanonicalAction(tx.conn.QueryRowContext(ctx,
		"SELECT id,task_id,status,provider_request_id,connection_generation,responded_at,resolved_at FROM pending_actions WHERE task_id=? AND provider_request_id=? AND connection_generation=?",
		taskID,
		providerRequestID,
		generation,
	))
}

func loadOpenAuthorityActionIDs(ctx context.Context, tx *authorityTransaction, taskID string) ([]string, error) {
	rows, err := tx.conn.QueryContext(ctx,
		"SELECT id FROM pending_actions WHERE task_id=? AND status IN (?,?) ORDER BY id",
		taskID,
		ActionStatusPending,
		ActionStatusResponding,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func closeOpenAuthorityActions(
	ctx context.Context,
	tx *authorityTransaction,
	taskID string,
	resolvedAt time.Time,
) (int64, error) {
	result, err := tx.conn.ExecContext(ctx,
		"UPDATE pending_actions SET status=?,resolved_at=? WHERE task_id=? AND status IN (?,?)",
		ActionStatusTaskClosed,
		resolvedAt.UTC(),
		taskID,
		ActionStatusPending,
		ActionStatusResponding,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func appendAuthorityArtifact(
	ctx context.Context,
	tx *authorityTransaction,
	taskID, kind, eventType string,
	payload map[string]any,
	createdAt time.Time,
) (int64, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal artifact payload: %w", err)
	}
	result, err := tx.conn.ExecContext(ctx,
		"INSERT INTO task_artifacts(task_id,kind,event_type,channel,summary,payload_json,content_length,redacted,truncated,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)",
		taskID,
		kind,
		eventType,
		"",
		eventType,
		string(payloadJSON),
		len(payloadJSON),
		0,
		0,
		createdAt.UTC(),
	)
	if err != nil {
		return 0, err
	}
	seq, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return seq, nil
}

func authorityAppliedResult(
	task CanonicalTaskState,
	action *CanonicalActionState,
	artifactSeq, closedActionCount int64,
) AuthorityResult {
	return AuthorityResult{
		Applied: true,
		Winner: AuthorityWinner{
			Task:   task,
			Action: action,
		},
		ArtifactSeq:       artifactSeq,
		ClosedActionCount: closedActionCount,
	}
}

func validateAuthorityTaskID(taskID string) error {
	if strings.TrimSpace(taskID) == "" {
		return errors.New("loom authority: missing task id")
	}
	return nil
}

func validateAuthorityTime(name string, at time.Time) error {
	if at.IsZero() {
		return fmt.Errorf("loom authority: %s must not be zero", name)
	}
	return nil
}

func authorityTaskStatusAllowed(status TaskStatus, allowed ...TaskStatus) bool {
	for _, candidate := range allowed {
		if status == candidate {
			return true
		}
	}
	return false
}

func validateStopEvidence(command CancelTask) error {
	if err := validateAuthorityTaskID(command.TaskID); err != nil {
		return err
	}
	if command.ExpectedStatus != TaskStatusCancelling {
		return fmt.Errorf("loom authority: CommitCancelled expected status %q is illegal", command.ExpectedStatus)
	}
	if err := validateAuthorityTime("completed_at", command.CompletedAt); err != nil {
		return err
	}
	if err := validateAuthorityTime("stop observed_at", command.StopEvidence.ObservedAt); err != nil {
		return err
	}
	if command.StopEvidence.ObservedAt.After(command.CompletedAt) {
		return errors.New("loom authority: stop evidence occurs after completion")
	}
	if strings.TrimSpace(command.StopEvidence.ExecutionID) == "" {
		return errors.New("loom authority: stop evidence missing execution id")
	}
	switch command.StopEvidence.Kind {
	case StopProofNativeAcknowledged:
		if command.StopEvidence.Process != nil {
			return errors.New("loom authority: native stop evidence must not include process identity")
		}
	case StopProofProcessTreeStopped, StopProofProcessAbsent:
		process := command.StopEvidence.Process
		if process == nil || process.PID <= 0 ||
			strings.TrimSpace(process.StartFingerprint) == "" ||
			strings.TrimSpace(process.TreeID) == "" {
			return errors.New("loom authority: process stop evidence requires complete process identity")
		}
	default:
		return fmt.Errorf("loom authority: unknown stop proof kind %q", command.StopEvidence.Kind)
	}
	return nil
}

func execOneAuthorityRow(
	ctx context.Context,
	tx *authorityTransaction,
	query string,
	args ...any,
) error {
	result, err := tx.conn.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("loom authority: canonical write affected %d rows", affected)
	}
	return nil
}

func (s *TaskStore) RequestCancel(
	ctx context.Context,
	taskID string,
	requestedAt time.Time,
) (CancelIntent, error) {
	if err := validateAuthorityTaskID(taskID); err != nil {
		return CancelIntent{}, err
	}
	if err := validateAuthorityTime("cancel requested_at", requestedAt); err != nil {
		return CancelIntent{}, err
	}
	requestedAt = requestedAt.UTC()

	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return CancelIntent{}, err
	}
	defer tx.rollback()
	task, found, err := loadAuthorityTask(ctx, tx, taskID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CancelIntent{AuthorityResult: result}, finishErr
	}
	if !found {
		result, finishErr := finishAuthorityNotFound(tx)
		return CancelIntent{AuthorityResult: result}, finishErr
	}

	requiresStop := false
	nextStatus := TaskStatusCancelled
	switch task.Status {
	case TaskStatusPending:
		// A task without an execution can be cancelled directly.
	case TaskStatusDispatched, TaskStatusRunning, TaskStatusInputRequired, TaskStatusRetrying:
		requiresStop = true
		nextStatus = TaskStatusCancelling
	default:
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictTaskStatus, nil)},
		)
		return CancelIntent{AuthorityResult: result}, conflictErr
	}

	if _, err := loadOpenAuthorityActionIDs(ctx, tx, taskID); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CancelIntent{AuthorityResult: result}, finishErr
	}

	if requiresStop {
		err = execOneAuthorityRow(
			ctx,
			tx,
			"UPDATE tasks SET status=?,result='',error='',cancel_requested_at=?,completed_at=NULL WHERE id=? AND status=?",
			nextStatus,
			requestedAt,
			taskID,
			task.Status,
		)
	} else {
		err = execOneAuthorityRow(
			ctx,
			tx,
			"UPDATE tasks SET status=?,result='',error='',cancel_requested_at=?,completed_at=? WHERE id=? AND status=?",
			nextStatus,
			requestedAt,
			requestedAt,
			taskID,
			task.Status,
		)
	}
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CancelIntent{AuthorityResult: result}, finishErr
	}

	closed, err := closeOpenAuthorityActions(ctx, tx, taskID, requestedAt)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CancelIntent{AuthorityResult: result}, finishErr
	}
	payload := map[string]any{
		"status":              string(nextStatus),
		"cancel_requested_at": requestedAt,
		"requires_stop":       requiresStop,
		"closed_action_count": closed,
	}
	kind, event := "terminal", "task.cancelled"
	if requiresStop {
		kind, event = "lifecycle", "task.cancel_requested"
	}
	artifactSeq, err := appendAuthorityArtifact(ctx, tx, taskID, kind, event, payload, requestedAt)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CancelIntent{AuthorityResult: result}, finishErr
	}

	task.Status = nextStatus
	task.Result = ""
	task.Error = ""
	task.CancelRequestedAt = timePointer(requestedAt)
	if requiresStop {
		task.CompletedAt = nil
	} else {
		task.CompletedAt = timePointer(requestedAt)
	}
	result, err := commitAuthorityResult(tx, authorityAppliedResult(task, nil, artifactSeq, closed))
	if err != nil {
		return CancelIntent{}, err
	}
	return CancelIntent{AuthorityResult: result, RequiresStop: requiresStop}, nil
}

func timePointer(at time.Time) *time.Time {
	value := at.UTC()
	return &value
}

func validateCreateTask(command CreateTask) error {
	if err := validateAuthorityTaskID(command.TaskID); err != nil {
		return err
	}
	required := []struct {
		name  string
		value string
	}{
		{name: "worker type", value: string(command.WorkerType)},
		{name: "tenant id", value: command.TenantID},
		{name: "prompt", value: command.Prompt},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("loom authority: CommitCreated missing %s", field.name)
		}
	}
	if command.ParentTaskID != "" && strings.TrimSpace(command.ParentTaskID) == "" {
		return errors.New("loom authority: CommitCreated invalid parent task id")
	}
	if command.Timeout < 0 {
		return errors.New("loom authority: CommitCreated timeout must not be negative")
	}
	return validateAuthorityTime("created_at", command.CreatedAt)
}

func (s *TaskStore) CommitCreated(ctx context.Context, command CreateTask) (CommitResult, error) {
	if err := validateCreateTask(command); err != nil {
		return CommitResult{}, err
	}
	envJSON, err := marshalJSON(command.Env)
	if err != nil {
		return CommitResult{}, fmt.Errorf("loom authority: marshal create env: %w", err)
	}
	metadataJSON, err := marshalJSON(command.Metadata)
	if err != nil {
		return CommitResult{}, fmt.Errorf("loom authority: marshal create metadata: %w", err)
	}
	createdAt := command.CreatedAt.UTC()

	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return CommitResult{}, err
	}
	defer tx.rollback()

	existing, found, err := loadAuthorityTask(ctx, tx, command.TaskID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if found {
		result, conflictErr := finishAuthorityConflict(
			tx,
			existing,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictTaskStatus, nil)},
		)
		return CommitResult{AuthorityResult: result}, conflictErr
	}

	lastSeenAt := command.CreatedAt.UTC().Format(time.RFC3339)
	if err := execOneAuthorityRow(
		ctx,
		tx,
		`INSERT INTO tasks
			(id,status,worker_type,project_id,request_id,parent_task_id,prompt,cwd,env,cli,role,model,
			 effort,timeout,metadata,result,error,retries,created_at,dispatched_at,cancel_requested_at,completed_at,
			 daemon_uuid,last_seen_at,engine_name,tenant_id)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		command.TaskID,
		TaskStatusPending,
		command.WorkerType,
		command.ProjectID,
		command.RequestID,
		nullableString(command.ParentTaskID),
		command.Prompt,
		command.CWD,
		envJSON,
		command.CLI,
		command.Role,
		command.Model,
		command.Effort,
		command.Timeout,
		metadataJSON,
		"",
		"",
		0,
		createdAt,
		nil,
		nil,
		nil,
		s.daemonUUID,
		lastSeenAt,
		s.engineName,
		command.TenantID,
	); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}

	task, found, err := loadAuthorityTask(ctx, tx, command.TaskID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if !found {
		result, finishErr := finishAuthorityError(tx, errors.New("loom authority: created task missing before commit"))
		return CommitResult{AuthorityResult: result}, finishErr
	}
	closed, err := closeOpenAuthorityActions(ctx, tx, command.TaskID, createdAt)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if closed != 0 {
		result, finishErr := finishAuthorityError(tx, fmt.Errorf("loom authority: create action fence closed %d rows", closed))
		return CommitResult{AuthorityResult: result}, finishErr
	}
	artifactSeq, err := appendAuthorityArtifact(
		ctx,
		tx,
		command.TaskID,
		"lifecycle",
		"task.created",
		map[string]any{
			"status":              string(TaskStatusPending),
			"closed_action_count": int64(0),
		},
		createdAt,
	)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	result, err := commitAuthorityResult(tx, authorityAppliedResult(task, nil, artifactSeq, 0))
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{AuthorityResult: result}, nil
}

func (s *TaskStore) CommitRunning(ctx context.Context, command RunTask) (CommitResult, error) {
	if err := validateAuthorityTaskID(command.TaskID); err != nil {
		return CommitResult{}, err
	}
	if command.ExpectedStatus != TaskStatusDispatched {
		return CommitResult{}, fmt.Errorf("loom authority: CommitRunning expected status %q is illegal", command.ExpectedStatus)
	}
	if err := validateAuthorityTime("running_at", command.RunningAt); err != nil {
		return CommitResult{}, err
	}
	runningAt := command.RunningAt.UTC()

	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return CommitResult{}, err
	}
	defer tx.rollback()
	task, found, err := loadAuthorityTask(ctx, tx, command.TaskID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if !found {
		result, finishErr := finishAuthorityNotFound(tx)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if task.Status != command.ExpectedStatus {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictTaskStatus, nil)},
		)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	if task.DispatchedAt == nil || runningAt.Before(*task.DispatchedAt) {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictDispatchTime, nil)},
		)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	if _, err := loadOpenAuthorityActionIDs(ctx, tx, command.TaskID); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if err := execOneAuthorityRow(
		ctx,
		tx,
		"UPDATE tasks SET status=? WHERE id=? AND status=?",
		TaskStatusRunning,
		command.TaskID,
		task.Status,
	); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	closed, err := closeOpenAuthorityActions(ctx, tx, command.TaskID, runningAt)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	artifactSeq, err := appendAuthorityArtifact(
		ctx,
		tx,
		command.TaskID,
		"lifecycle",
		"task.running",
		map[string]any{
			"status":              string(TaskStatusRunning),
			"closed_action_count": closed,
		},
		runningAt,
	)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	task.Status = TaskStatusRunning
	result, err := commitAuthorityResult(tx, authorityAppliedResult(task, nil, artifactSeq, closed))
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{AuthorityResult: result}, nil
}

func (s *TaskStore) CommitRetrying(ctx context.Context, command RetryTask) (CommitResult, error) {
	if err := validateAuthorityTaskID(command.TaskID); err != nil {
		return CommitResult{}, err
	}
	if command.ExpectedStatus != TaskStatusRunning {
		return CommitResult{}, fmt.Errorf("loom authority: CommitRetrying expected status %q is illegal", command.ExpectedStatus)
	}
	if err := validateAuthorityTime("retrying_at", command.RetryingAt); err != nil {
		return CommitResult{}, err
	}
	retryingAt := command.RetryingAt.UTC()

	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return CommitResult{}, err
	}
	defer tx.rollback()
	task, found, err := loadAuthorityTask(ctx, tx, command.TaskID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if !found {
		result, finishErr := finishAuthorityNotFound(tx)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if task.Status != command.ExpectedStatus {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictTaskStatus, nil)},
		)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	if task.DispatchedAt == nil || retryingAt.Before(*task.DispatchedAt) {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictDispatchTime, nil)},
		)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	if _, err := loadOpenAuthorityActionIDs(ctx, tx, command.TaskID); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	var retryCount int64
	if err := tx.conn.QueryRowContext(
		ctx,
		"UPDATE tasks SET status=?,retries=retries+1 WHERE id=? AND status=? RETURNING retries",
		TaskStatusRetrying,
		command.TaskID,
		task.Status,
	).Scan(&retryCount); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	closed, err := closeOpenAuthorityActions(ctx, tx, command.TaskID, retryingAt)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	artifactSeq, err := appendAuthorityArtifact(
		ctx,
		tx,
		command.TaskID,
		"lifecycle",
		"task.retrying",
		map[string]any{
			"status":              string(TaskStatusRetrying),
			"retry_count":         retryCount,
			"closed_action_count": closed,
		},
		retryingAt,
	)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	task.Status = TaskStatusRetrying
	result, err := commitAuthorityResult(tx, authorityAppliedResult(task, nil, artifactSeq, closed))
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{AuthorityResult: result}, nil
}

func (s *TaskStore) CommitDispatched(ctx context.Context, command DispatchTask) (CommitResult, error) {
	if err := validateAuthorityTaskID(command.TaskID); err != nil {
		return CommitResult{}, err
	}
	if !authorityTaskStatusAllowed(command.ExpectedStatus, TaskStatusPending, TaskStatusRetrying) {
		return CommitResult{}, fmt.Errorf("loom authority: CommitDispatched expected status %q is illegal", command.ExpectedStatus)
	}
	if err := validateAuthorityTime("dispatched_at", command.DispatchedAt); err != nil {
		return CommitResult{}, err
	}
	dispatchedAt := command.DispatchedAt.UTC()

	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return CommitResult{}, err
	}
	defer tx.rollback()
	task, found, err := loadAuthorityTask(ctx, tx, command.TaskID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if !found {
		result, finishErr := finishAuthorityNotFound(tx)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if task.Status != command.ExpectedStatus {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictTaskStatus, nil)},
		)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	if dispatchedAt.Before(task.CreatedAt) ||
		(task.DispatchedAt != nil && dispatchedAt.Before(*task.DispatchedAt)) {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictDispatchTime, nil)},
		)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	if _, err := loadOpenAuthorityActionIDs(ctx, tx, command.TaskID); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if err := execOneAuthorityRow(
		ctx,
		tx,
		"UPDATE tasks SET status=?,dispatched_at=? WHERE id=? AND status=?",
		TaskStatusDispatched,
		dispatchedAt,
		command.TaskID,
		task.Status,
	); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	closed, err := closeOpenAuthorityActions(ctx, tx, command.TaskID, dispatchedAt)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	artifactSeq, err := appendAuthorityArtifact(
		ctx,
		tx,
		command.TaskID,
		"lifecycle",
		"task.dispatched",
		map[string]any{
			"status":              string(TaskStatusDispatched),
			"closed_action_count": closed,
		},
		dispatchedAt,
	)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	task.Status = TaskStatusDispatched
	task.DispatchedAt = timePointer(dispatchedAt)
	result, err := commitAuthorityResult(tx, authorityAppliedResult(task, nil, artifactSeq, closed))
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{AuthorityResult: result}, nil
}

type terminalAuthorityCommand struct {
	taskID         string
	expectedStatus TaskStatus
	completedAt    time.Time
	nextStatus     TaskStatus
	result         string
	taskError      string
	kind           string
	event          string
	errorCode      string
	allowed        []TaskStatus
}

func (s *TaskStore) commitTerminalAuthority(
	ctx context.Context,
	command terminalAuthorityCommand,
) (AuthorityResult, error) {
	// Error text is part of both the durable row and the returned canonical
	// winner. Scrub it once before either sink can observe it.
	command.taskError = redactErrorMsg(command.taskError)
	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return AuthorityResult{}, err
	}
	defer tx.rollback()
	task, found, err := loadAuthorityTask(ctx, tx, command.taskID)
	if err != nil {
		return finishAuthorityError(tx, err)
	}
	if !found {
		return finishAuthorityNotFound(tx)
	}
	if task.Status != command.expectedStatus {
		return finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictTaskStatus, nil)},
		)
	}
	if _, err := loadOpenAuthorityActionIDs(ctx, tx, command.taskID); err != nil {
		return finishAuthorityError(tx, err)
	}
	if err := execOneAuthorityRow(
		ctx,
		tx,
		"UPDATE tasks SET status=?,result=?,error=?,completed_at=? WHERE id=? AND status=?",
		command.nextStatus,
		command.result,
		command.taskError,
		command.completedAt,
		command.taskID,
		task.Status,
	); err != nil {
		return finishAuthorityError(tx, err)
	}
	closed, err := closeOpenAuthorityActions(ctx, tx, command.taskID, command.completedAt)
	if err != nil {
		return finishAuthorityError(tx, err)
	}
	payload := map[string]any{
		"status":              string(command.nextStatus),
		"closed_action_count": closed,
	}
	if command.errorCode != "" {
		payload["error_code"] = command.errorCode
	}
	artifactSeq, err := appendAuthorityArtifact(
		ctx,
		tx,
		command.taskID,
		command.kind,
		command.event,
		payload,
		command.completedAt,
	)
	if err != nil {
		return finishAuthorityError(tx, err)
	}
	task.Status = command.nextStatus
	task.Result = command.result
	task.Error = command.taskError
	task.CompletedAt = timePointer(command.completedAt)
	return commitAuthorityResult(tx, authorityAppliedResult(task, nil, artifactSeq, closed))
}

func validateTerminalAuthorityCommand(command terminalAuthorityCommand) error {
	if err := validateAuthorityTaskID(command.taskID); err != nil {
		return err
	}
	if !authorityTaskStatusAllowed(command.expectedStatus, command.allowed...) {
		return fmt.Errorf("loom authority: expected status %q is illegal for %s", command.expectedStatus, command.event)
	}
	return validateAuthorityTime("completed_at", command.completedAt)
}

func (s *TaskStore) CommitCompleted(ctx context.Context, input CompleteTask) (CommitResult, error) {
	command := terminalAuthorityCommand{
		taskID: input.TaskID, expectedStatus: input.ExpectedStatus,
		completedAt: input.CompletedAt.UTC(), nextStatus: TaskStatusCompleted,
		result: input.Result, kind: "terminal", event: "task.completed",
		allowed: []TaskStatus{TaskStatusRunning},
	}
	if err := validateTerminalAuthorityCommand(command); err != nil {
		return CommitResult{}, err
	}
	result, err := s.commitTerminalAuthority(ctx, command)
	return CommitResult{AuthorityResult: result}, err
}

func (s *TaskStore) CommitFailed(ctx context.Context, input FailTask) (CommitResult, error) {
	command := terminalAuthorityCommand{
		taskID: input.TaskID, expectedStatus: input.ExpectedStatus,
		completedAt: input.CompletedAt.UTC(), nextStatus: TaskStatusFailed,
		taskError: input.Error, kind: "terminal", event: "task.failed", errorCode: "task_failed",
		allowed: []TaskStatus{
			TaskStatusPending,
			TaskStatusDispatched,
			TaskStatusRunning,
			TaskStatusInputRequired,
			TaskStatusRetrying,
		},
	}
	if err := validateTerminalAuthorityCommand(command); err != nil {
		return CommitResult{}, err
	}
	result, err := s.commitTerminalAuthority(ctx, command)
	return CommitResult{AuthorityResult: result}, err
}

func (s *TaskStore) CommitFailedCrash(ctx context.Context, input FailCrashedTask) (CommitResult, error) {
	command := terminalAuthorityCommand{
		taskID: input.TaskID, expectedStatus: input.ExpectedStatus,
		completedAt: input.CompletedAt.UTC(), nextStatus: TaskStatusFailedCrash,
		taskError: input.Error, kind: "terminal", event: "task.failed_crash", errorCode: "task_failed_crash",
		allowed: []TaskStatus{
			TaskStatusDispatched,
			TaskStatusRunning,
			TaskStatusInputRequired,
			TaskStatusRetrying,
			TaskStatusCancelling,
		},
	}
	if err := validateTerminalAuthorityCommand(command); err != nil {
		return CommitResult{}, err
	}
	result, err := s.commitTerminalAuthority(ctx, command)
	return CommitResult{AuthorityResult: result}, err
}

func nullableAuthorityString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableAuthorityTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func (s *TaskStore) CommitCancelled(ctx context.Context, command CancelTask) (CommitResult, error) {
	if err := validateStopEvidence(command); err != nil {
		return CommitResult{}, err
	}
	command.CompletedAt = command.CompletedAt.UTC()
	command.StopEvidence.ObservedAt = command.StopEvidence.ObservedAt.UTC()

	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return CommitResult{}, err
	}
	defer tx.rollback()
	task, found, err := loadAuthorityTask(ctx, tx, command.TaskID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if !found {
		result, finishErr := finishAuthorityNotFound(tx)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if task.Status != command.ExpectedStatus {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictTaskStatus, nil)},
		)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	if task.CancelRequestedAt == nil ||
		command.StopEvidence.ObservedAt.Before(*task.CancelRequestedAt) {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictStopEvidenceTime, nil)},
		)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	if _, err := loadOpenAuthorityActionIDs(ctx, tx, command.TaskID); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if err := execOneAuthorityRow(
		ctx,
		tx,
		"UPDATE tasks SET status=?,result='',error='',completed_at=? WHERE id=? AND status=?",
		TaskStatusCancelled,
		command.CompletedAt,
		command.TaskID,
		task.Status,
	); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	closed, err := closeOpenAuthorityActions(ctx, tx, command.TaskID, command.CompletedAt)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	artifactSeq, err := appendAuthorityArtifact(
		ctx,
		tx,
		command.TaskID,
		"terminal",
		"task.cancelled",
		map[string]any{
			"status":              string(TaskStatusCancelled),
			"stop_evidence":       command.StopEvidence,
			"closed_action_count": closed,
		},
		command.CompletedAt,
	)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	task.Status = TaskStatusCancelled
	task.Result = ""
	task.Error = ""
	task.CompletedAt = timePointer(command.CompletedAt)
	result, err := commitAuthorityResult(tx, authorityAppliedResult(task, nil, artifactSeq, closed))
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{AuthorityResult: result}, nil
}

func validateRequireInput(command RequireInput) error {
	if err := validateAuthorityTaskID(command.TaskID); err != nil {
		return err
	}
	if command.ExpectedStatus != TaskStatusRunning {
		return fmt.Errorf("loom authority: CommitInputRequired expected status %q is illegal", command.ExpectedStatus)
	}
	if err := validateAuthorityTime("occurred_at", command.OccurredAt); err != nil {
		return err
	}
	action := command.Action
	if strings.TrimSpace(action.ID) == "" ||
		strings.TrimSpace(action.TaskID) == "" ||
		strings.TrimSpace(action.Kind) == "" ||
		strings.TrimSpace(action.ProviderRequestID) == "" ||
		strings.TrimSpace(action.RequestJSON) == "" {
		return errors.New("loom authority: pending action is incomplete")
	}
	if action.TaskID != command.TaskID {
		return errors.New("loom authority: pending action task id mismatch")
	}
	if action.Status != ActionStatusPending {
		return fmt.Errorf("loom authority: pending action status %q is illegal", action.Status)
	}
	if action.ConnectionGeneration == 0 {
		return errors.New("loom authority: pending action connection generation must be non-zero")
	}
	if err := validateAuthorityTime("action expires_at", action.ExpiresAt); err != nil {
		return err
	}
	return nil
}

const authorityRedactedValue = "[REDACTED]"

// sanitizeAuthorityJSON validates one JSON value and applies structural key
// and value redaction. Safe payloads retain their exact original bytes; only a
// payload that actually changes is deterministically re-marshaled.
func sanitizeAuthorityJSON(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", errors.New("invalid JSON: multiple values")
		}
		return "", fmt.Errorf("invalid JSON trailer: %w", err)
	}
	if err := validateAuthorityJSONUniqueKeys(raw); err != nil {
		return "", err
	}
	sanitized, changed := sanitizeAuthorityJSONValue(value)
	if !changed {
		return raw, nil
	}
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return "", fmt.Errorf("marshal sanitized JSON: %w", err)
	}
	return string(encoded), nil
}

type authorityJSONFrame struct {
	object    bool
	expectKey bool
	keys      map[string]struct{}
}

func validateAuthorityJSONUniqueKeys(raw string) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	frames := make([]authorityJSONFrame, 0, 8)
	completeParentValue := func() {
		if len(frames) == 0 {
			return
		}
		parent := &frames[len(frames)-1]
		if parent.object && !parent.expectKey {
			parent.expectKey = true
		}
	}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		switch typed := token.(type) {
		case json.Delim:
			switch typed {
			case '{':
				frames = append(frames, authorityJSONFrame{
					object: true, expectKey: true, keys: make(map[string]struct{}),
				})
			case '[':
				frames = append(frames, authorityJSONFrame{})
			case '}', ']':
				frames = frames[:len(frames)-1]
				completeParentValue()
			}
		case string:
			if len(frames) > 0 {
				frame := &frames[len(frames)-1]
				if frame.object && frame.expectKey {
					if _, duplicate := frame.keys[typed]; duplicate {
						return fmt.Errorf("invalid JSON: duplicate object key %q", typed)
					}
					frame.keys[typed] = struct{}{}
					frame.expectKey = false
					continue
				}
			}
			completeParentValue()
		default:
			completeParentValue()
		}
	}
}

func sanitizeAuthorityJSONValue(value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			if authoritySensitiveJSONKey(key) {
				result[key] = authorityRedactedValue
				if child != authorityRedactedValue {
					changed = true
				}
				continue
			}
			var childChanged bool
			result[key], childChanged = sanitizeAuthorityJSONValue(child)
			changed = changed || childChanged
		}
		return result, changed
	case []any:
		changed := false
		result := make([]any, len(typed))
		for i, child := range typed {
			var childChanged bool
			result[i], childChanged = sanitizeAuthorityJSONValue(child)
			changed = changed || childChanged
		}
		return result, changed
	case string:
		redacted := redactErrorMsg(typed)
		return redacted, redacted != typed
	default:
		return value, false
	}
}

func authoritySensitiveJSONKey(key string) bool {
	var normalized strings.Builder
	for _, char := range strings.ToLower(key) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			normalized.WriteRune(char)
		}
	}
	name := normalized.String()
	if strings.Contains(name, "token") || strings.Contains(name, "secret") ||
		strings.Contains(name, "password") || strings.Contains(name, "credential") ||
		strings.Contains(name, "apikey") {
		return true
	}
	switch name {
	case "authorization", "auth", "cookie", "setcookie", "privatekey",
		"privatereasoning", "reasoning", "analysis", "scratchpad",
		"chainofthought", "thought", "thinking":
		return true
	default:
		return false
	}
}

func (s *TaskStore) CommitInputRequired(ctx context.Context, command RequireInput) (CommitResult, error) {
	if err := validateRequireInput(command); err != nil {
		return CommitResult{}, err
	}
	requestJSON, err := sanitizeAuthorityJSON(command.Action.RequestJSON)
	if err != nil {
		return CommitResult{}, fmt.Errorf("loom authority: pending action request_json: %w", err)
	}
	responseJSON, err := sanitizeAuthorityJSON(command.Action.ResponseJSON)
	if err != nil {
		return CommitResult{}, fmt.Errorf("loom authority: pending action response_json: %w", err)
	}
	deliveryJSON, err := sanitizeAuthorityJSON(command.Action.DeliveryJSON)
	if err != nil {
		return CommitResult{}, fmt.Errorf("loom authority: pending action delivery_json: %w", err)
	}
	command.Action.RequestJSON = requestJSON
	command.Action.ResponseJSON = responseJSON
	command.Action.DeliveryJSON = deliveryJSON
	command.OccurredAt = command.OccurredAt.UTC()
	command.Action.ExpiresAt = command.Action.ExpiresAt.UTC()

	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return CommitResult{}, err
	}
	defer tx.rollback()
	task, found, err := loadAuthorityTask(ctx, tx, command.TaskID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if !found {
		result, finishErr := finishAuthorityNotFound(tx)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if task.Status != command.ExpectedStatus {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictTaskStatus, nil)},
		)
		return CommitResult{AuthorityResult: result}, conflictErr
	}

	idAction, idFound, err := loadAuthorityActionByID(ctx, tx, command.Action.ID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	correlationAction, correlationFound, err := loadAuthorityActionByCorrelation(
		ctx,
		tx,
		command.TaskID,
		command.Action.ProviderRequestID,
		command.Action.ConnectionGeneration,
	)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}

	var (
		winnerAction *CanonicalActionState
		conflicts    []AuthorityConflict
	)
	if idFound {
		idSnapshot := idAction
		winnerAction = &idSnapshot
		conflicts = append(conflicts, authorityConflict(AuthorityConflictActionID, &idSnapshot))
	}
	if correlationFound && (!idFound || correlationAction.ActionID != idAction.ActionID) {
		correlationSnapshot := correlationAction
		if winnerAction == nil {
			winnerAction = &correlationSnapshot
		}
		conflicts = append(conflicts, authorityConflict(AuthorityConflictProviderCorrelation, &correlationSnapshot))
	}
	if len(conflicts) > 0 {
		result, conflictErr := finishAuthorityConflict(tx, task, winnerAction, conflicts)
		return CommitResult{AuthorityResult: result}, conflictErr
	}

	if err := execOneAuthorityRow(
		ctx,
		tx,
		"UPDATE tasks SET status=? WHERE id=? AND status=?",
		TaskStatusInputRequired,
		command.TaskID,
		task.Status,
	); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if _, err := tx.conn.ExecContext(
		ctx,
		"INSERT INTO pending_actions(id,task_id,kind,status,provider_request_id,connection_generation,request_json,response_json,delivery_json,expires_at,created_at,responded_at,resolved_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)",
		command.Action.ID,
		command.TaskID,
		command.Action.Kind,
		ActionStatusPending,
		command.Action.ProviderRequestID,
		command.Action.ConnectionGeneration,
		command.Action.RequestJSON,
		nullableAuthorityString(command.Action.ResponseJSON),
		nullableAuthorityString(command.Action.DeliveryJSON),
		command.Action.ExpiresAt,
		command.OccurredAt,
		nullableAuthorityTime(command.Action.RespondedAt),
		nullableAuthorityTime(command.Action.ResolvedAt),
	); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	artifactSeq, err := appendAuthorityArtifact(
		ctx,
		tx,
		command.TaskID,
		"lifecycle",
		"task.input_required",
		map[string]any{
			"status":    string(TaskStatusInputRequired),
			"action_id": command.Action.ID,
		},
		command.OccurredAt,
	)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	task.Status = TaskStatusInputRequired
	action := CanonicalActionState{
		ActionID:             command.Action.ID,
		TaskID:               command.TaskID,
		Status:               ActionStatusPending,
		ProviderRequestID:    command.Action.ProviderRequestID,
		ConnectionGeneration: command.Action.ConnectionGeneration,
	}
	result, err := commitAuthorityResult(tx, authorityAppliedResult(task, &action, artifactSeq, 0))
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{AuthorityResult: result}, nil
}

func validateBeginResponse(command BeginResponse) error {
	if err := validateAuthorityTaskID(command.TaskID); err != nil {
		return err
	}
	if strings.TrimSpace(command.ActionID) == "" {
		return errors.New("loom authority: missing action id")
	}
	if command.ExpectedTaskStatus != TaskStatusInputRequired {
		return fmt.Errorf("loom authority: BeginActionResponse task status %q is illegal", command.ExpectedTaskStatus)
	}
	if command.ExpectedActionStatus != ActionStatusPending {
		return fmt.Errorf("loom authority: BeginActionResponse action status %q is illegal", command.ExpectedActionStatus)
	}
	if command.ConnectionGeneration == 0 {
		return errors.New("loom authority: response connection generation must be non-zero")
	}
	return validateAuthorityTime("responded_at", command.RespondedAt)
}

func actionAuthorityConflict(
	tx *authorityTransaction,
	task CanonicalTaskState,
	action CanonicalActionState,
	kind AuthorityConflictKind,
) (AuthorityResult, error) {
	snapshot := action
	return finishAuthorityConflict(
		tx,
		task,
		&snapshot,
		[]AuthorityConflict{authorityConflict(kind, &snapshot)},
	)
}

func (s *TaskStore) BeginActionResponse(
	ctx context.Context,
	command BeginResponse,
) (PendingActionAttempt, error) {
	if err := validateBeginResponse(command); err != nil {
		return PendingActionAttempt{}, err
	}
	responseJSON, err := sanitizeAuthorityJSON(command.ResponseJSON)
	if err != nil {
		return PendingActionAttempt{}, fmt.Errorf("loom authority: response_json: %w", err)
	}
	command.ResponseJSON = responseJSON
	command.RespondedAt = command.RespondedAt.UTC()

	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return PendingActionAttempt{}, err
	}
	defer tx.rollback()
	task, found, err := loadAuthorityTask(ctx, tx, command.TaskID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return PendingActionAttempt{AuthorityResult: result}, finishErr
	}
	if !found {
		result, finishErr := finishAuthorityNotFound(tx)
		return PendingActionAttempt{AuthorityResult: result}, finishErr
	}
	if task.Status != command.ExpectedTaskStatus {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictTaskStatus, nil)},
		)
		return PendingActionAttempt{AuthorityResult: result}, conflictErr
	}
	action, actionFound, err := loadAuthorityActionByID(ctx, tx, command.ActionID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return PendingActionAttempt{AuthorityResult: result}, finishErr
	}
	if !actionFound {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictActionMissing, nil)},
		)
		return PendingActionAttempt{AuthorityResult: result}, conflictErr
	}
	if action.TaskID != command.TaskID {
		result, conflictErr := actionAuthorityConflict(tx, task, action, AuthorityConflictActionOwner)
		return PendingActionAttempt{AuthorityResult: result}, conflictErr
	}
	if action.Status != command.ExpectedActionStatus {
		result, conflictErr := actionAuthorityConflict(tx, task, action, AuthorityConflictActionSourceStatus)
		return PendingActionAttempt{AuthorityResult: result}, conflictErr
	}
	if action.ConnectionGeneration != command.ConnectionGeneration {
		result, conflictErr := actionAuthorityConflict(tx, task, action, AuthorityConflictConnectionGeneration)
		return PendingActionAttempt{AuthorityResult: result}, conflictErr
	}
	if err := execOneAuthorityRow(
		ctx,
		tx,
		"UPDATE pending_actions SET status=?,response_json=?,responded_at=? WHERE id=? AND task_id=? AND status=? AND connection_generation=?",
		ActionStatusResponding,
		command.ResponseJSON,
		command.RespondedAt,
		command.ActionID,
		command.TaskID,
		action.Status,
		action.ConnectionGeneration,
	); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return PendingActionAttempt{AuthorityResult: result}, finishErr
	}
	artifactSeq, err := appendAuthorityArtifact(
		ctx,
		tx,
		command.TaskID,
		"lifecycle",
		"task.input_response_started",
		map[string]any{
			"status":                string(TaskStatusInputRequired),
			"action_id":             command.ActionID,
			"action_status":         string(ActionStatusResponding),
			"connection_generation": command.ConnectionGeneration,
		},
		command.RespondedAt,
	)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return PendingActionAttempt{AuthorityResult: result}, finishErr
	}
	action.Status = ActionStatusResponding
	action.RespondedAt = timePointer(command.RespondedAt)
	result, err := commitAuthorityResult(tx, authorityAppliedResult(task, &action, artifactSeq, 0))
	if err != nil {
		return PendingActionAttempt{}, err
	}
	return PendingActionAttempt{AuthorityResult: result}, nil
}

func validateResolveAction(command ResolveAction) error {
	if err := validateAuthorityTaskID(command.TaskID); err != nil {
		return err
	}
	if strings.TrimSpace(command.ActionID) == "" {
		return errors.New("loom authority: missing action id")
	}
	if command.ExpectedTaskStatus != TaskStatusInputRequired {
		return fmt.Errorf("loom authority: CommitActionResolution task status %q is illegal", command.ExpectedTaskStatus)
	}
	if command.ExpectedActionStatus != ActionStatusResponding {
		return fmt.Errorf("loom authority: CommitActionResolution action status %q is illegal", command.ExpectedActionStatus)
	}
	if command.ConnectionGeneration == 0 {
		return errors.New("loom authority: resolution connection generation must be non-zero")
	}
	switch command.Resolution {
	case ActionStatusAnswered, ActionStatusApproved, ActionStatusDeclined:
		if command.NextTaskStatus != TaskStatusRunning {
			return errors.New("loom authority: ordinary action resolution must resume running")
		}
	case ActionStatusDeliveryUnknown:
		if command.NextTaskStatus != TaskStatusFailed {
			return errors.New("loom authority: delivery_unknown must fail the task")
		}
	default:
		return fmt.Errorf("loom authority: illegal action resolution %q", command.Resolution)
	}
	return validateAuthorityTime("resolved_at", command.ResolvedAt)
}

func (s *TaskStore) CommitActionResolution(ctx context.Context, command ResolveAction) (CommitResult, error) {
	if err := validateResolveAction(command); err != nil {
		return CommitResult{}, err
	}
	deliveryJSON, err := sanitizeAuthorityJSON(command.DeliveryJSON)
	if err != nil {
		return CommitResult{}, fmt.Errorf("loom authority: delivery_json: %w", err)
	}
	command.DeliveryJSON = deliveryJSON
	command.ResolvedAt = command.ResolvedAt.UTC()

	tx, err := beginAuthorityTransaction(ctx, s.db)
	if err != nil {
		return CommitResult{}, err
	}
	defer tx.rollback()
	task, found, err := loadAuthorityTask(ctx, tx, command.TaskID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if !found {
		result, finishErr := finishAuthorityNotFound(tx)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if task.Status != command.ExpectedTaskStatus {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictTaskStatus, nil)},
		)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	action, actionFound, err := loadAuthorityActionByID(ctx, tx, command.ActionID)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if !actionFound {
		result, conflictErr := finishAuthorityConflict(
			tx,
			task,
			nil,
			[]AuthorityConflict{authorityConflict(AuthorityConflictActionMissing, nil)},
		)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	if action.TaskID != command.TaskID {
		result, conflictErr := actionAuthorityConflict(tx, task, action, AuthorityConflictActionOwner)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	if action.Status != command.ExpectedActionStatus {
		result, conflictErr := actionAuthorityConflict(tx, task, action, AuthorityConflictActionSourceStatus)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	if action.ConnectionGeneration != command.ConnectionGeneration {
		result, conflictErr := actionAuthorityConflict(tx, task, action, AuthorityConflictConnectionGeneration)
		return CommitResult{AuthorityResult: result}, conflictErr
	}
	if _, err := loadOpenAuthorityActionIDs(ctx, tx, command.TaskID); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}

	if err := execOneAuthorityRow(
		ctx,
		tx,
		"UPDATE pending_actions SET status=?,delivery_json=?,resolved_at=? WHERE id=? AND task_id=? AND status=? AND connection_generation=?",
		command.Resolution,
		command.DeliveryJSON,
		command.ResolvedAt,
		command.ActionID,
		command.TaskID,
		action.Status,
		action.ConnectionGeneration,
	); err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	if command.Resolution == ActionStatusDeliveryUnknown {
		err = execOneAuthorityRow(
			ctx,
			tx,
			"UPDATE tasks SET status=?,result='',error=?,completed_at=? WHERE id=? AND status=?",
			TaskStatusFailed,
			"action_delivery_unknown",
			command.ResolvedAt,
			command.TaskID,
			task.Status,
		)
	} else {
		err = execOneAuthorityRow(
			ctx,
			tx,
			"UPDATE tasks SET status=?,result='',error='',completed_at=NULL WHERE id=? AND status=?",
			command.NextTaskStatus,
			command.TaskID,
			task.Status,
		)
	}
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	closed, err := closeOpenAuthorityActions(ctx, tx, command.TaskID, command.ResolvedAt)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}
	kind, event := "lifecycle", "task.running"
	payload := map[string]any{
		"status":              string(command.NextTaskStatus),
		"action_id":           command.ActionID,
		"resolution":          string(command.Resolution),
		"closed_action_count": closed,
	}
	if command.Resolution == ActionStatusDeliveryUnknown {
		kind, event = "terminal", "task.failed"
		payload = map[string]any{
			"status":              string(TaskStatusFailed),
			"error_code":          "action_delivery_unknown",
			"action_id":           command.ActionID,
			"action_status":       string(ActionStatusDeliveryUnknown),
			"closed_action_count": closed,
		}
	}
	artifactSeq, err := appendAuthorityArtifact(
		ctx,
		tx,
		command.TaskID,
		kind,
		event,
		payload,
		command.ResolvedAt,
	)
	if err != nil {
		result, finishErr := finishAuthorityError(tx, err)
		return CommitResult{AuthorityResult: result}, finishErr
	}

	action.Status = command.Resolution
	action.ResolvedAt = timePointer(command.ResolvedAt)
	task.Status = command.NextTaskStatus
	task.Result = ""
	task.Error = ""
	task.CompletedAt = nil
	if command.Resolution == ActionStatusDeliveryUnknown {
		task.Status = TaskStatusFailed
		task.Error = "action_delivery_unknown"
		task.CompletedAt = timePointer(command.ResolvedAt)
	}
	result, err := commitAuthorityResult(tx, authorityAppliedResult(task, &action, artifactSeq, closed))
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{AuthorityResult: result}, nil
}
