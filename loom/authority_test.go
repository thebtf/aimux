package loom

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// These are the only compile anchors in this file. Both names exist in the
// immutable T005 v1 overlay and are deliberately absent from the clean T004
// base. Every v2.1-only type and member is discovered through reflection so
// this exact test patch can execute against the frozen overlay.
var _ TaskAuthority = (*TaskStore)(nil)

var t004AuthorityAt = time.Date(2031, 2, 3, 4, 5, 6, 123456789, time.UTC)

const (
	t004Pending       = TaskStatus("pending")
	t004Dispatched    = TaskStatus("dispatched")
	t004Running       = TaskStatus("running")
	t004InputRequired = TaskStatus("input_required")
	t004Retrying      = TaskStatus("retrying")
	t004Cancelling    = TaskStatus("cancelling")
	t004Completed     = TaskStatus("completed")
	t004Failed        = TaskStatus("failed")
	t004FailedCrash   = TaskStatus("failed_crash")
	t004Cancelled     = TaskStatus("cancelled")
)

type t004ObservedTask struct {
	TaskID            string
	Status            string
	CreatedAt         time.Time
	Result            string
	Error             string
	DispatchedAt      *time.Time
	CancelRequestedAt *time.Time
	CompletedAt       *time.Time
}

type t004ObservedAction struct {
	ActionID             string
	TaskID               string
	Status               string
	ProviderRequestID    string
	ConnectionGeneration uint64
	RespondedAt          *time.Time
	ResolvedAt           *time.Time
}

type t004ObservedConflict struct {
	Kind   string
	Action *t004ObservedAction
}

type t004ObservedWinner struct {
	Task      t004ObservedTask
	Action    *t004ObservedAction
	Conflicts []t004ObservedConflict
}

type authorityObservedResult struct {
	Applied           bool
	Winner            t004ObservedWinner
	ArtifactSeq       int64
	ClosedActionCount int64
	RequiresStop      bool
}

type t004StopSpec struct {
	Kind             string
	ExecutionID      string
	PID              int
	StartFingerprint string
	TreeID           string
	ObservedAt       time.Time
}

type t004Command struct {
	Method               string
	TaskID               string
	ActionID             string
	ExpectedTaskStatus   TaskStatus
	ExpectedActionStatus string
	Resolution           string
	NextTaskStatus       TaskStatus
	ConnectionGeneration uint64
	At                   time.Time
	Result               string
	Error                string
	ResponseJSON         string
	DeliveryJSON         string
	ActionStatus         string
	ProviderRequestID    string
	ActionKind           string
	ActionExpiresAt      time.Time
	Stop                 t004StopSpec
}

type t004Fixture struct {
	store  *TaskStore
	target any
	id     string
	action string
}

type t004Row map[string]string
type t004Rows map[string]t004Row
type t004State struct {
	tasks     t004Rows
	actions   t004Rows
	artifacts t004Rows
}

type t004Artifact struct {
	Seq       int64
	TaskID    string
	Kind      string
	EventType string
	Payload   map[string]any
	CreatedAt time.Time
}

var errT004MethodMissing = errors.New("t004: reflected authority method missing")

func t004Indirect(value reflect.Value) reflect.Value {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}

func t004Field(value reflect.Value, name string) reflect.Value {
	value = t004Indirect(value)
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	return value.FieldByName(name)
}

func t004String(value reflect.Value, name string) string {
	field := t004Indirect(t004Field(value, name))
	if field.IsValid() && field.Kind() == reflect.String {
		return field.String()
	}
	return ""
}

func t004Bool(value reflect.Value, name string) bool {
	field := t004Indirect(t004Field(value, name))
	return field.IsValid() && field.Kind() == reflect.Bool && field.Bool()
}

func t004Int64(value reflect.Value, name string) int64 {
	field := t004Indirect(t004Field(value, name))
	if !field.IsValid() {
		return 0
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return field.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(field.Uint())
	default:
		return 0
	}
}

func t004Uint64(value reflect.Value, name string) uint64 {
	field := t004Indirect(t004Field(value, name))
	if !field.IsValid() {
		return 0
	}
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return field.Uint()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint64(field.Int())
	default:
		return 0
	}
}

func t004Time(value reflect.Value, name string) time.Time {
	field := t004Indirect(t004Field(value, name))
	if field.IsValid() && field.Type() == reflect.TypeOf(time.Time{}) {
		return field.Interface().(time.Time)
	}
	return time.Time{}
}

func t004TimePointer(value reflect.Value, name string) *time.Time {
	field := t004Field(value, name)
	if !field.IsValid() || field.Kind() != reflect.Pointer || field.IsNil() || field.Elem().Type() != reflect.TypeOf(time.Time{}) {
		return nil
	}
	got := field.Elem().Interface().(time.Time)
	return &got
}

func t004ObserveTask(value reflect.Value) t004ObservedTask {
	return t004ObservedTask{
		TaskID:            t004String(value, "TaskID"),
		Status:            t004String(value, "Status"),
		CreatedAt:         t004Time(value, "CreatedAt"),
		Result:            t004String(value, "Result"),
		Error:             t004String(value, "Error"),
		DispatchedAt:      t004TimePointer(value, "DispatchedAt"),
		CancelRequestedAt: t004TimePointer(value, "CancelRequestedAt"),
		CompletedAt:       t004TimePointer(value, "CompletedAt"),
	}
}

func t004ObserveAction(value reflect.Value) *t004ObservedAction {
	value = t004Indirect(value)
	if !value.IsValid() {
		return nil
	}
	return &t004ObservedAction{
		ActionID:             t004String(value, "ActionID"),
		TaskID:               t004String(value, "TaskID"),
		Status:               t004String(value, "Status"),
		ProviderRequestID:    t004String(value, "ProviderRequestID"),
		ConnectionGeneration: t004Uint64(value, "ConnectionGeneration"),
		RespondedAt:          t004TimePointer(value, "RespondedAt"),
		ResolvedAt:           t004TimePointer(value, "ResolvedAt"),
	}
}

func t004NormalizeResult(value reflect.Value) authorityObservedResult {
	value = t004Indirect(value)
	result := authorityObservedResult{
		Applied:           t004Bool(value, "Applied"),
		ArtifactSeq:       t004Int64(value, "ArtifactSeq"),
		ClosedActionCount: t004Int64(value, "ClosedActionCount"),
		RequiresStop:      t004Bool(value, "RequiresStop"),
	}
	winner := t004Indirect(t004Field(value, "Winner"))
	if winner.IsValid() {
		result.Winner.Task = t004ObserveTask(t004Field(winner, "Task"))
		result.Winner.Action = t004ObserveAction(t004Field(winner, "Action"))
		conflicts := t004Indirect(t004Field(winner, "Conflicts"))
		if conflicts.IsValid() && conflicts.Kind() == reflect.Slice {
			for i := 0; i < conflicts.Len(); i++ {
				item := t004Indirect(conflicts.Index(i))
				result.Winner.Conflicts = append(result.Winner.Conflicts, t004ObservedConflict{
					Kind:   t004String(item, "Kind"),
					Action: t004ObserveAction(t004Field(item, "Action")),
				})
			}
		}
		return result
	}
	// Frozen v1 exposes only flat fields. Observe them without querying SQLite;
	// absent v2.1 proof fields intentionally remain zero and fail behaviorally.
	result.Winner.Task.TaskID = t004String(value, "TaskID")
	result.Winner.Task.Status = t004String(value, "Status")
	if actionID := t004String(value, "ActionID"); actionID != "" {
		result.Winner.Action = &t004ObservedAction{
			ActionID:             actionID,
			TaskID:               result.Winner.Task.TaskID,
			Status:               t004String(value, "Status"),
			ConnectionGeneration: t004Uint64(value, "ConnectionGeneration"),
		}
	}
	return result
}

func t004Set(field reflect.Value, value any) bool {
	field = t004IndirectSettable(field)
	if !field.IsValid() || !field.CanSet() {
		return false
	}
	if value == nil {
		field.Set(reflect.Zero(field.Type()))
		return true
	}
	if at, ok := value.(time.Time); ok && field.Type() == reflect.TypeOf(time.Time{}) {
		field.Set(reflect.ValueOf(at))
		return true
	}
	input := reflect.ValueOf(value)
	if input.Type().AssignableTo(field.Type()) {
		field.Set(input)
		return true
	}
	if input.Type().ConvertibleTo(field.Type()) {
		field.Set(input.Convert(field.Type()))
		return true
	}
	switch field.Kind() {
	case reflect.String:
		field.SetString(fmt.Sprint(value))
		return true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch number := value.(type) {
		case uint64:
			field.SetUint(number)
		case int:
			field.SetUint(uint64(number))
		default:
			return false
		}
		return true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch number := value.(type) {
		case int:
			field.SetInt(int64(number))
		case int64:
			field.SetInt(number)
		default:
			return false
		}
		return true
	case reflect.Bool:
		boolean, ok := value.(bool)
		if ok {
			field.SetBool(boolean)
		}
		return ok
	default:
		return false
	}
}

func t004IndirectSettable(value reflect.Value) reflect.Value {
	if value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() && value.CanSet() {
			value.Set(reflect.New(value.Type().Elem()))
		}
		if !value.IsNil() {
			return value.Elem()
		}
	}
	return value
}

func t004SetNamed(value reflect.Value, name string, input any) bool {
	value = t004Indirect(value)
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return false
	}
	field := value.FieldByName(name)
	if !field.IsValid() {
		return false
	}
	return t004Set(field, input)
}

func t004StopEvidence(spec t004StopSpec) StopEvidence {
	var evidence StopEvidence
	value := reflect.ValueOf(&evidence).Elem()
	t004SetNamed(value, "Kind", spec.Kind)
	t004SetNamed(value, "ExecutionID", spec.ExecutionID)
	t004SetNamed(value, "ObservedAt", spec.ObservedAt)
	if process := value.FieldByName("Process"); process.IsValid() && process.Kind() == reflect.Pointer && spec.PID != 0 {
		identity := reflect.New(process.Type().Elem())
		t004SetNamed(identity, "PID", spec.PID)
		t004SetNamed(identity, "StartFingerprint", spec.StartFingerprint)
		t004SetNamed(identity, "TreeID", spec.TreeID)
		process.Set(identity)
	}
	// Frozen v1 can only exercise its native path. This compatibility write is
	// not accepted by any final shape or payload assertion.
	t004SetNamed(value, "NativeAcknowledged", true)
	return evidence
}

func t004FillAction(action reflect.Value, cmd t004Command) {
	action = t004IndirectSettable(action)
	t004SetNamed(action, "ID", cmd.ActionID)
	t004SetNamed(action, "TaskID", cmd.TaskID)
	t004SetNamed(action, "Kind", cmd.ActionKind)
	t004SetNamed(action, "Status", cmd.ActionStatus)
	t004SetNamed(action, "ProviderRequestID", cmd.ProviderRequestID)
	t004SetNamed(action, "ConnectionGeneration", cmd.ConnectionGeneration)
	t004SetNamed(action, "RequestJSON", `{"prompt":"continue?"}`)
	t004SetNamed(action, "ExpiresAt", cmd.ActionExpiresAt)
	t004SetNamed(action, "CreatedAt", cmd.At)
}

func t004BuildCommand(commandType reflect.Type, cmd t004Command) reflect.Value {
	value := reflect.New(commandType).Elem()
	t004SetNamed(value, "TaskID", cmd.TaskID)
	t004SetNamed(value, "ActionID", cmd.ActionID)
	legacyExpectedStatus := any(cmd.ExpectedTaskStatus)
	if cmd.Method == "BeginActionResponse" {
		// Frozen-v1 BeginActionResponse used ExpectedStatus for the action CAS;
		// task commands used the same field name for the task CAS.
		legacyExpectedStatus = cmd.ExpectedActionStatus
	}
	t004SetNamed(value, "ExpectedStatus", legacyExpectedStatus)
	t004SetNamed(value, "ExpectedTaskStatus", cmd.ExpectedTaskStatus)
	t004SetNamed(value, "ExpectedActionStatus", cmd.ExpectedActionStatus)
	t004SetNamed(value, "Result", cmd.Result)
	t004SetNamed(value, "Error", cmd.Error)
	t004SetNamed(value, "CompletedAt", cmd.At)
	t004SetNamed(value, "DispatchedAt", cmd.At)
	t004SetNamed(value, "OccurredAt", cmd.At)
	t004SetNamed(value, "RespondedAt", cmd.At)
	t004SetNamed(value, "ResolvedAt", cmd.At)
	t004SetNamed(value, "ResponseJSON", cmd.ResponseJSON)
	t004SetNamed(value, "DeliveryJSON", cmd.DeliveryJSON)
	t004SetNamed(value, "ConnectionGeneration", cmd.ConnectionGeneration)
	t004SetNamed(value, "Resolution", cmd.Resolution)
	t004SetNamed(value, "NextTaskStatus", cmd.NextTaskStatus)
	if action := value.FieldByName("Action"); action.IsValid() {
		t004FillAction(action, cmd)
	}
	if stop := value.FieldByName("StopEvidence"); stop.IsValid() {
		t004Set(stop, t004StopEvidence(cmd.Stop))
	}
	return value
}

type t004HarnessTaskCommand struct {
	ExpectedStatus TaskStatus
}

type t004HarnessActionCommand struct {
	ExpectedStatus string
}

type t004HarnessExpectedStatusTarget struct{}

func (t004HarnessExpectedStatusTarget) CommitDispatched(context.Context, t004HarnessTaskCommand) (struct{}, error) {
	return struct{}{}, nil
}

func (t004HarnessExpectedStatusTarget) BeginActionResponse(context.Context, t004HarnessActionCommand) (struct{}, error) {
	return struct{}{}, nil
}

func TestTaskAuthority_HarnessExpectedStatusMapping(t *testing.T) {
	target := reflect.ValueOf(t004HarnessExpectedStatusTarget{})
	base := t004Command{
		ExpectedTaskStatus:   t004Retrying,
		ExpectedActionStatus: "responding",
	}
	for _, testCase := range []struct {
		method string
		want   string
	}{
		{method: "CommitDispatched", want: string(t004Retrying)},
		{method: "BeginActionResponse", want: "responding"},
	} {
		command := base
		command.Method = testCase.method
		method := target.MethodByName(testCase.method)
		if !method.IsValid() {
			t.Fatalf("surrogate method %s unavailable", testCase.method)
		}
		built := t004BuildCommand(method.Type().In(1), command)
		if got := t004String(built, "ExpectedStatus"); got != testCase.want {
			t.Fatalf("%s ExpectedStatus=%q want=%q", testCase.method, got, testCase.want)
		}
	}
}

func t004AssertBuiltTaskExpectedStatus(t *testing.T, target any, cmd t004Command) {
	t.Helper()
	method := reflect.ValueOf(target).MethodByName(cmd.Method)
	if !method.IsValid() || method.Type().NumIn() < 2 {
		t.Fatalf("t004 oracle: %s command method unavailable", cmd.Method)
	}
	built := t004BuildCommand(method.Type().In(1), cmd)
	field := built.FieldByName("ExpectedStatus")
	if !field.IsValid() {
		t.Fatalf("t004 oracle: %s command has no ExpectedStatus", cmd.Method)
	}
	if got, want := t004String(built, "ExpectedStatus"), string(cmd.ExpectedTaskStatus); got != want {
		t.Fatalf("t004 oracle: %s ExpectedStatus=%q want task status %q", cmd.Method, got, want)
	}
}

func t004Invoke(ctx context.Context, target any, cmd t004Command) (authorityObservedResult, error) {
	targetValue := reflect.ValueOf(target)
	if !targetValue.IsValid() || (targetValue.Kind() == reflect.Pointer && targetValue.IsNil()) {
		return authorityObservedResult{}, fmt.Errorf("t004: %s target is nil", cmd.Method)
	}
	method := targetValue.MethodByName(cmd.Method)
	if !method.IsValid() {
		return authorityObservedResult{}, fmt.Errorf("%w: %s", errT004MethodMissing, cmd.Method)
	}
	methodType := method.Type()
	wantInputs := 2
	if cmd.Method == "RequestCancel" {
		wantInputs = 3
	}
	if methodType.NumIn() != wantInputs {
		return authorityObservedResult{}, fmt.Errorf("t004: %s input count=%d want=%d", cmd.Method, methodType.NumIn(), wantInputs)
	}
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	if methodType.NumOut() != 2 || !methodType.Out(1).Implements(errorType) {
		return authorityObservedResult{}, fmt.Errorf("t004: %s output shape=(%d,%v) want (result,error)", cmd.Method, methodType.NumOut(), func() any {
			if methodType.NumOut() > 1 {
				return methodType.Out(1)
			}
			return "missing"
		}())
	}
	args := []reflect.Value{reflect.ValueOf(ctx)}
	if cmd.Method == "RequestCancel" {
		args = append(args, reflect.ValueOf(cmd.TaskID), reflect.ValueOf(cmd.At))
	} else {
		commandType := methodType.In(1)
		if commandType.Kind() != reflect.Struct {
			return authorityObservedResult{}, fmt.Errorf("t004: %s command type=%v want struct", cmd.Method, commandType)
		}
		args = append(args, t004BuildCommand(commandType, cmd))
	}
	var output []reflect.Value
	var callPanic any
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				callPanic = recovered
			}
		}()
		output = method.Call(args)
	}()
	if callPanic != nil {
		return authorityObservedResult{}, fmt.Errorf("t004: %s reflection call panic: %v", cmd.Method, callPanic)
	}
	if len(output) != 2 {
		return authorityObservedResult{}, fmt.Errorf("t004: %s returned %d values", cmd.Method, len(output))
	}
	result := t004NormalizeResult(output[0])
	errorValue := output[1]
	if !errorValue.IsValid() || !errorValue.Type().Implements(errorType) {
		return authorityObservedResult{}, fmt.Errorf("t004: %s runtime error result=%v want error", cmd.Method, errorValue)
	}
	if errorValue.Kind() != reflect.Interface && errorValue.Kind() != reflect.Pointer {
		return authorityObservedResult{}, fmt.Errorf("t004: %s error result kind=%s is not nil-capable", cmd.Method, errorValue.Kind())
	}
	if !errorValue.IsNil() {
		invokeErr, ok := errorValue.Interface().(error)
		if !ok {
			return authorityObservedResult{}, fmt.Errorf("t004: %s runtime error result does not implement error", cmd.Method)
		}
		return result, invokeErr
	}
	return result, nil
}

func t004DefaultCommand(method, taskID, actionID string) t004Command {
	return t004Command{
		Method: method, TaskID: taskID, ActionID: actionID,
		ExpectedTaskStatus: t004Running, ExpectedActionStatus: "pending",
		Resolution: "answered", NextTaskStatus: t004Running,
		ConnectionGeneration: 7, At: t004AuthorityAt,
		Result: "done", Error: "sanitized failure",
		ResponseJSON: `{"answer":"yes"}`, DeliveryJSON: `{"delivered":true}`,
		ActionStatus: "pending", ProviderRequestID: "provider-" + actionID,
		ActionKind: "input", ActionExpiresAt: t004AuthorityAt.Add(time.Hour),
		Stop: t004StopSpec{Kind: "native_acknowledged", ExecutionID: "exec-" + taskID, ObservedAt: t004AuthorityAt.Add(-time.Second)},
	}
}

func t004AuthorityPair(t *testing.T) (*TaskStore, *TaskStore) {
	t.Helper()
	path := t.TempDir() + "/authority.db"
	return t004NewStore(t, t004OpenDB(t, path), "authority-a"), t004NewStore(t, t004OpenDB(t, path), "authority-b")
}

func t004SeedTask(t *testing.T, store *TaskStore, id string, status TaskStatus) {
	t.Helper()
	task := makeTask(id, "project-"+id, status)
	task.RequestID = "request-" + id
	task.Prompt = "prompt-" + id
	task.CreatedAt = t004AuthorityAt.Add(-time.Hour)
	t004Must(t, store.Create(task))
	_, err := store.db.Exec(`UPDATE tasks SET result='seed-result', error='seed-error' WHERE id=?`, id)
	t004Must(t, err)
}

func t004NewFixture(t *testing.T, id string, status TaskStatus) *t004Fixture {
	t.Helper()
	store, _ := t004AuthorityPair(t)
	t004SeedTask(t, store, id, status)
	canary := id + "-canary"
	t004SeedTask(t, store, canary, t004Running)
	t004SeedAction(t, store.db, canary+"-action", canary, "canary-provider", 91, "answered")
	return &t004Fixture{store: store, target: store, id: id, action: id + "-action"}
}

func t004SeedAction(t *testing.T, db *sql.DB, id, taskID, provider string, generation uint64, status string) {
	t.Helper()
	var response, delivery, responded, resolved any
	if status == "responding" {
		response, responded = `{"seed":"response"}`, t004AuthorityAt.Add(-2*time.Minute)
	}
	if status == "answered" || status == "approved" || status == "declined" || status == "delivery_unknown" || status == "task_closed" {
		response, delivery = `{"seed":"response"}`, `{"seed":"delivery"}`
		responded, resolved = t004AuthorityAt.Add(-3*time.Minute), t004AuthorityAt.Add(-2*time.Minute)
	}
	_, err := db.Exec(`INSERT INTO pending_actions
		(id,task_id,kind,status,provider_request_id,connection_generation,request_json,response_json,delivery_json,expires_at,created_at,responded_at,resolved_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, taskID, "input", status, provider, generation,
		`{"prompt":"seed"}`, response, delivery, t004AuthorityAt.Add(time.Hour), t004AuthorityAt.Add(-time.Minute), responded, resolved)
	t004Must(t, err)
}

func t004Cell(value any) string {
	switch value := value.(type) {
	case nil:
		return "<NULL>"
	case time.Time:
		return value.UTC().Format(time.RFC3339Nano)
	case []byte:
		return string(value)
	default:
		return fmt.Sprint(value)
	}
}

func t004ReadRows(t *testing.T, db *sql.DB, table, key string) t004Rows {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY ` + key)
	t004Must(t, err)
	defer rows.Close()
	columns, err := rows.Columns()
	t004Must(t, err)
	keyIndex := -1
	for index, column := range columns {
		if column == key {
			keyIndex = index
		}
	}
	if keyIndex < 0 {
		t.Fatalf("%s missing key %s", table, key)
	}
	result := t004Rows{}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for index := range values {
			targets[index] = &values[index]
		}
		t004Must(t, rows.Scan(targets...))
		row := t004Row{}
		for index, column := range columns {
			row[column] = t004Cell(values[index])
		}
		result[t004Cell(values[keyIndex])] = row
	}
	t004Must(t, rows.Err())
	return result
}

func t004ReadState(t *testing.T, db *sql.DB) t004State {
	t.Helper()
	return t004State{
		tasks:     t004ReadRows(t, db, "tasks", "id"),
		actions:   t004ReadRows(t, db, "pending_actions", "id"),
		artifacts: t004ReadRows(t, db, "task_artifacts", "seq"),
	}
}

func t004ArtifactBaseline(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var baseline int64
	t004Must(t, db.QueryRow(`SELECT coalesce(max(seq),0) FROM task_artifacts`).Scan(&baseline))
	return baseline
}

func t004ReadNewArtifacts(t *testing.T, db *sql.DB, baseline int64) []t004Artifact {
	t.Helper()
	rows, err := db.Query(`SELECT seq,task_id,kind,event_type,payload_json,created_at FROM task_artifacts WHERE seq>? ORDER BY seq`, baseline)
	t004Must(t, err)
	defer rows.Close()
	var result []t004Artifact
	for rows.Next() {
		var artifact t004Artifact
		var raw string
		t004Must(t, rows.Scan(&artifact.Seq, &artifact.TaskID, &artifact.Kind, &artifact.EventType, &raw, &artifact.CreatedAt))
		if err := json.Unmarshal([]byte(raw), &artifact.Payload); err != nil {
			t.Fatalf("decode artifact %d payload %q: %v", artifact.Seq, raw, err)
		}
		result = append(result, artifact)
	}
	t004Must(t, rows.Err())
	return result
}

func t004AssertExactPayload(t *testing.T, got, want map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Errorf("payload=%s want=%s", gotJSON, wantJSON)
	}
}

func t004AssertOneArtifact(t *testing.T, db *sql.DB, baseline int64, result authorityObservedResult, kind, event string, payload map[string]any, at time.Time) t004Artifact {
	t.Helper()
	artifacts := t004ReadNewArtifacts(t, db, baseline)
	if len(artifacts) != 1 {
		t.Errorf("new artifacts=%d want=1: %#v", len(artifacts), artifacts)
		return t004Artifact{}
	}
	artifact := artifacts[0]
	if artifact.TaskID == "" || artifact.Kind != kind || artifact.EventType != event {
		t.Errorf("artifact identity=%#v want kind=%s event=%s", artifact, kind, event)
	}
	if result.ArtifactSeq != artifact.Seq {
		t.Errorf("result artifact_seq=%d row seq=%d", result.ArtifactSeq, artifact.Seq)
	}
	if !artifact.CreatedAt.Equal(at.UTC()) {
		t.Errorf("created_at=%s want=%s", artifact.CreatedAt.Format(time.RFC3339Nano), at.UTC().Format(time.RFC3339Nano))
	}
	t004AssertExactPayload(t, artifact.Payload, payload)
	return artifact
}

func t004AssertZeroResult(t *testing.T, result authorityObservedResult) {
	t.Helper()
	if !reflect.DeepEqual(result, authorityObservedResult{}) {
		t.Errorf("result=%#v want wholly zero", result)
	}
}

func t004AssertConflict(t *testing.T, result authorityObservedResult, taskID string, kinds ...string) {
	t.Helper()
	if result.Applied || result.ArtifactSeq != 0 || result.ClosedActionCount != 0 {
		t.Errorf("conflict result applied/seq/count=%v/%d/%d", result.Applied, result.ArtifactSeq, result.ClosedActionCount)
	}
	if result.Winner.Task.TaskID != taskID {
		t.Errorf("winner task=%q want=%q", result.Winner.Task.TaskID, taskID)
	}
	got := make([]string, len(result.Winner.Conflicts))
	for index := range result.Winner.Conflicts {
		got[index] = result.Winner.Conflicts[index].Kind
	}
	if !reflect.DeepEqual(got, kinds) {
		t.Errorf("conflicts=%v want=%v", got, kinds)
	}
}

func t004IsConflict(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "authority conflict")
}

func t004RequireApplied(t *testing.T, result authorityObservedResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("command error: %v", err)
	}
	if !result.Applied {
		t.Errorf("Applied=false for committed command: %#v", result)
	}
	if len(result.Winner.Conflicts) != 0 {
		t.Errorf("success conflicts=%#v want empty", result.Winner.Conflicts)
	}
}

func t004Run(t *testing.T, fixture *t004Fixture, command t004Command) authorityObservedResult {
	t.Helper()
	result, err := t004Invoke(context.Background(), fixture.target, command)
	t004RequireApplied(t, result, err)
	return result
}

func t004Method(t *testing.T, authorityType reflect.Type, name string) reflect.Method {
	t.Helper()
	method, ok := authorityType.MethodByName(name)
	if !ok {
		t.Errorf("TaskAuthority missing %s", name)
		return reflect.Method{}
	}
	return method
}

func t004AssertStructFields(t *testing.T, typ reflect.Type, names []string) {
	t.Helper()
	if typ == nil {
		t.Errorf("missing struct type; want fields=%v", names)
		return
	}
	if typ.Kind() != reflect.Struct {
		t.Errorf("%v kind=%s want struct", typ, typ.Kind())
		return
	}
	got := make([]string, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		got[index] = typ.Field(index).Name
	}
	if !reflect.DeepEqual(got, names) {
		t.Errorf("%s fields=%v want=%v", typ, got, names)
	}
}

func t004ShapeMethod(t *testing.T, authorityType reflect.Type, name string, wantInputs int) (reflect.Method, bool) {
	t.Helper()
	method := t004Method(t, authorityType, name)
	if method.Type == nil {
		return reflect.Method{}, false
	}
	valid := true
	if method.Type.NumIn() != wantInputs {
		t.Errorf("%s input count=%d want=%d", name, method.Type.NumIn(), wantInputs)
		valid = false
	}
	if method.Type.NumOut() != 2 {
		t.Errorf("%s output count=%d want=2", name, method.Type.NumOut())
		valid = false
	} else if !method.Type.Out(1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		t.Errorf("%s second output=%v want error", name, method.Type.Out(1))
		valid = false
	}
	return method, valid
}

func t004ShapeField(t *testing.T, typ reflect.Type, name string) (reflect.StructField, bool) {
	t.Helper()
	if typ == nil || typ.Kind() != reflect.Struct {
		t.Errorf("cannot inspect field %s on non-struct %v", name, typ)
		return reflect.StructField{}, false
	}
	field, ok := typ.FieldByName(name)
	if !ok {
		t.Errorf("%v missing field %s", typ, name)
		return reflect.StructField{}, false
	}
	return field, true
}

func TestTaskAuthority_ResultDTOShape(t *testing.T) {
	authorityType := reflect.TypeOf((*TaskAuthority)(nil)).Elem()
	methodInputs := []struct {
		name   string
		inputs int
	}{
		{"RequestCancel", 3}, {"CommitDispatched", 2}, {"CommitCompleted", 2},
		{"CommitFailed", 2}, {"CommitFailedCrash", 2}, {"CommitCancelled", 2},
		{"CommitInputRequired", 2}, {"BeginActionResponse", 2}, {"CommitActionResolution", 2},
	}

	t.Run("task-authority-methods", func(t *testing.T) {
		if authorityType.NumMethod() != len(methodInputs) {
			t.Errorf("TaskAuthority method count=%d want=%d", authorityType.NumMethod(), len(methodInputs))
		}
		for _, expected := range methodInputs {
			t004ShapeMethod(t, authorityType, expected.name, expected.inputs)
		}
	})

	t.Run("result-wrappers-and-canonical-types", func(t *testing.T) {
		wrapperMethods := []struct{ method, wrapper string }{
			{"RequestCancel", "CancelIntent"}, {"CommitDispatched", "CommitResult"}, {"CommitCompleted", "CommitResult"},
			{"CommitFailed", "CommitResult"}, {"CommitFailedCrash", "CommitResult"}, {"CommitCancelled", "CommitResult"},
			{"CommitInputRequired", "CommitResult"}, {"BeginActionResponse", "PendingActionAttempt"}, {"CommitActionResolution", "CommitResult"},
		}
		var authorityResultType reflect.Type
		for _, expected := range wrapperMethods {
			wantInputs := 2
			if expected.method == "RequestCancel" {
				wantInputs = 3
			}
			method, valid := t004ShapeMethod(t, authorityType, expected.method, wantInputs)
			if !valid || method.Type.NumOut() < 1 {
				continue
			}
			wrapper := method.Type.Out(0)
			if wrapper.Kind() != reflect.Struct {
				t.Errorf("%s result=%v want struct %s", expected.method, wrapper, expected.wrapper)
				continue
			}
			if wrapper.Name() != expected.wrapper {
				t.Errorf("%s result=%s want=%s", expected.method, wrapper.Name(), expected.wrapper)
			}
			field, ok := t004ShapeField(t, wrapper, "AuthorityResult")
			if !ok {
				continue
			}
			if !field.Anonymous || field.Type.Name() != "AuthorityResult" {
				t.Errorf("%s AuthorityResult field anonymous/type=%v/%v", expected.method, field.Anonymous, field.Type)
				continue
			}
			if authorityResultType == nil {
				authorityResultType = field.Type
			} else if authorityResultType != field.Type {
				t.Errorf("%s embeds different AuthorityResult type", expected.method)
			}
		}
		if authorityResultType == nil {
			t.Error("AuthorityResult unreachable from final wrappers")
			return
		}

		t004AssertStructFields(t, authorityResultType, []string{"Applied", "Winner", "ArtifactSeq", "ClosedActionCount"})
		if field, ok := t004ShapeField(t, authorityResultType, "Applied"); ok && field.Type.Kind() != reflect.Bool {
			t.Errorf("AuthorityResult.Applied=%v want bool", field.Type)
		}
		if field, ok := t004ShapeField(t, authorityResultType, "ArtifactSeq"); ok && field.Type.Kind() != reflect.Int64 {
			t.Errorf("AuthorityResult.ArtifactSeq=%v want int64", field.Type)
		}
		if field, ok := t004ShapeField(t, authorityResultType, "ClosedActionCount"); ok && field.Type.Kind() != reflect.Int64 {
			t.Errorf("AuthorityResult.ClosedActionCount=%v want int64", field.Type)
		}

		winnerField, ok := t004ShapeField(t, authorityResultType, "Winner")
		if !ok {
			return
		}
		winnerType := winnerField.Type
		if winnerType.Kind() != reflect.Struct || winnerType.Name() != "AuthorityWinner" {
			t.Errorf("Winner type=%v want AuthorityWinner struct", winnerType)
			return
		}
		t004AssertStructFields(t, winnerType, []string{"Task", "Action", "Conflicts"})

		if taskField, ok := t004ShapeField(t, winnerType, "Task"); ok {
			taskType := taskField.Type
			if taskType.Kind() != reflect.Struct || taskType.Name() != "CanonicalTaskState" {
				t.Errorf("winner task type=%v want CanonicalTaskState", taskType)
			} else {
				t004AssertStructFields(t, taskType, []string{"TaskID", "Status", "CreatedAt", "Result", "Error", "DispatchedAt", "CancelRequestedAt", "CompletedAt"})
				if status, found := t004ShapeField(t, taskType, "Status"); found && status.Type.Name() != "TaskStatus" {
					t.Errorf("CanonicalTaskState.Status=%v want TaskStatus", status.Type)
				}
				if created, found := t004ShapeField(t, taskType, "CreatedAt"); found && created.Type != reflect.TypeOf(time.Time{}) {
					t.Errorf("CanonicalTaskState.CreatedAt=%v want time.Time", created.Type)
				}
			}
		}

		if actionField, ok := t004ShapeField(t, winnerType, "Action"); ok {
			if actionField.Type.Kind() != reflect.Pointer || actionField.Type.Elem().Kind() != reflect.Struct || actionField.Type.Elem().Name() != "CanonicalActionState" {
				t.Errorf("AuthorityWinner.Action=%v want *CanonicalActionState", actionField.Type)
			} else {
				actionType := actionField.Type.Elem()
				t004AssertStructFields(t, actionType, []string{"ActionID", "TaskID", "Status", "ProviderRequestID", "ConnectionGeneration", "RespondedAt", "ResolvedAt"})
				if status, found := t004ShapeField(t, actionType, "Status"); found && (status.Type.Name() != "ActionStatus" || status.Type.Kind() != reflect.String) {
					t.Errorf("CanonicalActionState.Status=%v want named ActionStatus string", status.Type)
				}
			}
		}

		if conflictsField, ok := t004ShapeField(t, winnerType, "Conflicts"); ok {
			if conflictsField.Type.Kind() != reflect.Slice || conflictsField.Type.Elem().Kind() != reflect.Struct || conflictsField.Type.Elem().Name() != "AuthorityConflict" {
				t.Errorf("AuthorityWinner.Conflicts=%v want []AuthorityConflict", conflictsField.Type)
			} else {
				conflictType := conflictsField.Type.Elem()
				t004AssertStructFields(t, conflictType, []string{"Kind", "Action"})
				if kind, found := t004ShapeField(t, conflictType, "Kind"); found && (kind.Type.Name() != "AuthorityConflictKind" || kind.Type.Kind() != reflect.String) {
					t.Errorf("AuthorityConflict.Kind=%v want named AuthorityConflictKind string", kind.Type)
				}
			}
		}
	})

	t.Run("command-dtos", func(t *testing.T) {
		t.Run("pending-action", func(t *testing.T) {
			method, valid := t004ShapeMethod(t, authorityType, "CommitInputRequired", 2)
			if !valid {
				return
			}
			requireType := method.Type.In(1)
			action, ok := t004ShapeField(t, requireType, "Action")
			if !ok {
				return
			}
			if action.Type.Kind() != reflect.Struct {
				t.Errorf("RequireInput.Action=%v want PendingAction struct", action.Type)
				return
			}
			pendingType := action.Type
			t004AssertStructFields(t, pendingType, []string{"ID", "TaskID", "Kind", "Status", "ProviderRequestID", "ConnectionGeneration", "RequestJSON", "ResponseJSON", "DeliveryJSON", "ExpiresAt", "CreatedAt", "RespondedAt", "ResolvedAt"})
			if status, found := t004ShapeField(t, pendingType, "Status"); found && (status.Type.Name() != "ActionStatus" || status.Type == reflect.TypeOf("")) {
				t.Errorf("PendingAction.Status=%v want named ActionStatus", status.Type)
			}
			if created, found := t004ShapeField(t, pendingType, "CreatedAt"); found && created.Type != reflect.TypeOf(time.Time{}) {
				t.Errorf("PendingAction.CreatedAt=%v want time.Time", created.Type)
			}
		})

		t.Run("dispatch", func(t *testing.T) {
			method, valid := t004ShapeMethod(t, authorityType, "CommitDispatched", 2)
			if !valid {
				return
			}
			dispatchType := method.Type.In(1)
			if dispatchType.Kind() != reflect.Struct || dispatchType.Name() != "DispatchTask" {
				t.Errorf("CommitDispatched input=%v want DispatchTask struct", dispatchType)
				return
			}
			t004AssertStructFields(t, dispatchType, []string{"TaskID", "ExpectedStatus", "DispatchedAt"})
		})

		for _, row := range []struct {
			name, method string
			fields       []string
		}{
			{"begin", "BeginActionResponse", []string{"TaskID", "ActionID", "ExpectedTaskStatus", "ExpectedActionStatus", "ResponseJSON", "ConnectionGeneration", "RespondedAt"}},
			{"resolve", "CommitActionResolution", []string{"TaskID", "ActionID", "ExpectedTaskStatus", "ExpectedActionStatus", "Resolution", "NextTaskStatus", "ConnectionGeneration", "DeliveryJSON", "ResolvedAt"}},
		} {
			row := row
			t.Run(row.name, func(t *testing.T) {
				method, valid := t004ShapeMethod(t, authorityType, row.method, 2)
				if !valid {
					return
				}
				t004AssertStructFields(t, method.Type.In(1), row.fields)
			})
		}
	})

	t.Run("stop-evidence", func(t *testing.T) {
		stopType := reflect.TypeOf(StopEvidence{})
		t004AssertStructFields(t, stopType, []string{"Kind", "ExecutionID", "Process", "ObservedAt"})
		process, ok := t004ShapeField(t, stopType, "Process")
		if !ok {
			return
		}
		if process.Type.Kind() != reflect.Pointer || process.Type.Elem().Kind() != reflect.Struct {
			t.Errorf("StopEvidence.Process=%v want struct pointer", process.Type)
			return
		}
		identity := process.Type.Elem()
		t004AssertStructFields(t, identity, []string{"PID", "StartFingerprint", "TreeID"})
		if treeID, found := t004ShapeField(t, identity, "TreeID"); found && treeID.Tag.Get("json") != "tree_id" {
			t.Errorf("StopProcessIdentity.TreeID json tag=%q", treeID.Tag.Get("json"))
		}
	})
}

func t004InputCommand(f *t004Fixture) t004Command {
	command := t004DefaultCommand("CommitInputRequired", f.id, f.action)
	command.ExpectedTaskStatus = t004Running
	return command
}

func t004BeginCommand(f *t004Fixture) t004Command {
	command := t004DefaultCommand("BeginActionResponse", f.id, f.action)
	command.ExpectedTaskStatus = t004InputRequired
	command.ExpectedActionStatus = "pending"
	command.At = t004AuthorityAt.Add(time.Second)
	return command
}

func t004ResolveCommand(f *t004Fixture, resolution string) t004Command {
	command := t004DefaultCommand("CommitActionResolution", f.id, f.action)
	command.ExpectedTaskStatus = t004InputRequired
	command.ExpectedActionStatus = "responding"
	command.Resolution = resolution
	command.NextTaskStatus = t004Running
	if resolution == "delivery_unknown" {
		command.NextTaskStatus = t004Failed
	}
	command.At = t004AuthorityAt.Add(2 * time.Second)
	return command
}

func t004CancelRequest(f *t004Fixture, at time.Time) t004Command {
	command := t004DefaultCommand("RequestCancel", f.id, f.action)
	command.At = at
	return command
}

func t004CommitCancelled(f *t004Fixture, kind string) t004Command {
	completedAt := t004AuthorityAt.Add(3 * time.Second)
	command := t004DefaultCommand("CommitCancelled", f.id, f.action)
	command.ExpectedTaskStatus = t004Cancelling
	command.At = completedAt
	command.Stop = t004StopSpec{
		Kind: kind, ExecutionID: "exec-" + f.id, PID: 4321,
		StartFingerprint: "start-abc", TreeID: "tree-owned",
		ObservedAt: completedAt.Add(-time.Second),
	}
	if kind == "native_acknowledged" {
		command.Stop.PID = 0
		command.Stop.StartFingerprint = ""
		command.Stop.TreeID = ""
	}
	return command
}

func t004SeedOpenActions(t *testing.T, fixture *t004Fixture) {
	t.Helper()
	t004SeedAction(t, fixture.store.db, fixture.id+"-pending", fixture.id, "open-pending-"+fixture.id, 101, "pending")
	t004SeedAction(t, fixture.store.db, fixture.id+"-responding", fixture.id, "open-responding-"+fixture.id, 102, "responding")
}

func t004AssertActionClosed(t *testing.T, db *sql.DB, id string, at time.Time) {
	t.Helper()
	var status string
	var resolved sql.NullTime
	if err := db.QueryRow(`SELECT status,resolved_at FROM pending_actions WHERE id=?`, id).Scan(&status, &resolved); err != nil {
		t.Errorf("read action %s: %v", id, err)
		return
	}
	if status != "task_closed" || !resolved.Valid || !resolved.Time.Equal(at.UTC()) {
		gotTime := "<NULL>"
		if resolved.Valid {
			gotTime = resolved.Time.Format(time.RFC3339Nano)
		}
		t.Errorf("action %s status/time=%s/%s want task_closed/%s", id, status, gotTime, at.UTC().Format(time.RFC3339Nano))
	}
}

func t004AssertWinnerTask(t *testing.T, result authorityObservedResult, id string, status TaskStatus) {
	t.Helper()
	if result.Winner.Task.TaskID != id || result.Winner.Task.Status != string(status) {
		t.Errorf("winner task=%#v want id=%s status=%s", result.Winner.Task, id, status)
	}
}

func TestTaskAuthority_ExactArtifactMatrix(t *testing.T) {
	type artifactCase struct {
		name, method, start, kind, event string
		setup                            func(*testing.T, *t004Fixture)
		command                          func(*t004Fixture) t004Command
		wantPayload                      func(*t004Fixture, t004Command) map[string]any
		wantStatus                       TaskStatus
		wantClosed                       int64
	}
	terminalSetup := func(t *testing.T, fixture *t004Fixture) { t004SeedOpenActions(t, fixture) }
	cases := []artifactCase{
		{
			name: "pending-cancel", method: "RequestCancel", start: "pending", kind: "terminal", event: "task.cancelled", wantStatus: t004Cancelled, wantClosed: 2,
			setup:   terminalSetup,
			command: func(f *t004Fixture) t004Command { return t004CancelRequest(f, t004AuthorityAt) },
			wantPayload: func(_ *t004Fixture, cmd t004Command) map[string]any {
				return map[string]any{"status": "cancelled", "cancel_requested_at": cmd.At.Format(time.RFC3339Nano), "requires_stop": false, "closed_action_count": float64(2)}
			},
		},
		{
			name: "active-cancel", method: "RequestCancel", start: "running", kind: "lifecycle", event: "task.cancel_requested", wantStatus: t004Cancelling, wantClosed: 2,
			setup:   terminalSetup,
			command: func(f *t004Fixture) t004Command { return t004CancelRequest(f, t004AuthorityAt) },
			wantPayload: func(_ *t004Fixture, cmd t004Command) map[string]any {
				return map[string]any{"status": "cancelling", "cancel_requested_at": cmd.At.Format(time.RFC3339Nano), "requires_stop": true, "closed_action_count": float64(2)}
			},
		},
		{
			name: "dispatch-pending", method: "CommitDispatched", start: "pending", kind: "lifecycle", event: "task.dispatched", wantStatus: t004Dispatched, wantClosed: 2,
			setup: terminalSetup,
			command: func(f *t004Fixture) t004Command {
				cmd := t004DefaultCommand("CommitDispatched", f.id, f.action)
				cmd.ExpectedTaskStatus = t004Pending
				return cmd
			},
			wantPayload: func(_ *t004Fixture, _ t004Command) map[string]any {
				return map[string]any{"status": "dispatched", "closed_action_count": float64(2)}
			},
		},
		{
			name: "completed", method: "CommitCompleted", start: "running", kind: "terminal", event: "task.completed", wantStatus: t004Completed, wantClosed: 2,
			setup: terminalSetup,
			command: func(f *t004Fixture) t004Command {
				cmd := t004DefaultCommand("CommitCompleted", f.id, f.action)
				cmd.ExpectedTaskStatus = t004Running
				return cmd
			},
			wantPayload: func(_ *t004Fixture, _ t004Command) map[string]any {
				return map[string]any{"status": "completed", "closed_action_count": float64(2)}
			},
		},
		{
			name: "failed", method: "CommitFailed", start: "running", kind: "terminal", event: "task.failed", wantStatus: t004Failed, wantClosed: 2,
			setup: terminalSetup,
			command: func(f *t004Fixture) t004Command {
				cmd := t004DefaultCommand("CommitFailed", f.id, f.action)
				cmd.ExpectedTaskStatus = t004Running
				return cmd
			},
			wantPayload: func(_ *t004Fixture, _ t004Command) map[string]any {
				return map[string]any{"status": "failed", "error_code": "task_failed", "closed_action_count": float64(2)}
			},
		},
		{
			name: "failed-crash", method: "CommitFailedCrash", start: "running", kind: "terminal", event: "task.failed_crash", wantStatus: t004FailedCrash, wantClosed: 2,
			setup: terminalSetup,
			command: func(f *t004Fixture) t004Command {
				cmd := t004DefaultCommand("CommitFailedCrash", f.id, f.action)
				cmd.ExpectedTaskStatus = t004Running
				return cmd
			},
			wantPayload: func(_ *t004Fixture, _ t004Command) map[string]any {
				return map[string]any{"status": "failed_crash", "error_code": "task_failed_crash", "closed_action_count": float64(2)}
			},
		},
		{
			name: "input-required", method: "CommitInputRequired", start: "running", kind: "lifecycle", event: "task.input_required", wantStatus: t004InputRequired,
			command: t004InputCommand,
			wantPayload: func(f *t004Fixture, _ t004Command) map[string]any {
				return map[string]any{"status": "input_required", "action_id": f.action}
			},
		},
		{
			name: "begin-response", method: "BeginActionResponse", start: "running", kind: "lifecycle", event: "task.input_response_started", wantStatus: t004InputRequired,
			setup:   func(t *testing.T, f *t004Fixture) { t004Run(t, f, t004InputCommand(f)) },
			command: t004BeginCommand,
			wantPayload: func(f *t004Fixture, cmd t004Command) map[string]any {
				return map[string]any{"status": "input_required", "action_id": f.action, "action_status": "responding", "connection_generation": float64(cmd.ConnectionGeneration)}
			},
		},
		{
			name: "resolve-answered", method: "CommitActionResolution", start: "running", kind: "lifecycle", event: "task.running", wantStatus: t004Running, wantClosed: 1,
			setup: func(t *testing.T, f *t004Fixture) {
				t004Run(t, f, t004InputCommand(f))
				t004Run(t, f, t004BeginCommand(f))
				t004SeedAction(t, f.store.db, f.id+"-sibling", f.id, "sibling-"+f.id, 8, "pending")
			},
			command: func(f *t004Fixture) t004Command { return t004ResolveCommand(f, "answered") },
			wantPayload: func(f *t004Fixture, cmd t004Command) map[string]any {
				return map[string]any{"status": "running", "action_id": f.action, "resolution": "answered", "closed_action_count": float64(1)}
			},
		},
		{
			name: "resolve-delivery-unknown", method: "CommitActionResolution", start: "running", kind: "terminal", event: "task.failed", wantStatus: t004Failed, wantClosed: 1,
			setup: func(t *testing.T, f *t004Fixture) {
				t004Run(t, f, t004InputCommand(f))
				t004Run(t, f, t004BeginCommand(f))
				t004SeedAction(t, f.store.db, f.id+"-sibling", f.id, "sibling-"+f.id, 8, "responding")
			},
			command: func(f *t004Fixture) t004Command { return t004ResolveCommand(f, "delivery_unknown") },
			wantPayload: func(f *t004Fixture, _ t004Command) map[string]any {
				return map[string]any{"status": "failed", "error_code": "action_delivery_unknown", "action_id": f.action, "action_status": "delivery_unknown", "closed_action_count": float64(1)}
			},
		},
	}
	for _, proofKind := range []string{"native_acknowledged", "process_tree_stopped", "process_absent"} {
		proofKind := proofKind
		cases = append(cases, artifactCase{
			name: "cancelled-" + proofKind, method: "CommitCancelled", start: "running", kind: "terminal", event: "task.cancelled", wantStatus: t004Cancelled, wantClosed: 1,
			setup: func(t *testing.T, f *t004Fixture) {
				t004Run(t, f, t004CancelRequest(f, t004AuthorityAt))
				t004SeedAction(t, f.store.db, f.id+"-late-open", f.id, "late-"+f.id, 77, "pending")
			},
			command: func(f *t004Fixture) t004Command { return t004CommitCancelled(f, proofKind) },
			wantPayload: func(f *t004Fixture, cmd t004Command) map[string]any {
				stop := map[string]any{"kind": proofKind, "execution_id": "exec-" + f.id, "observed_at": cmd.Stop.ObservedAt.UTC().Format(time.RFC3339Nano)}
				if proofKind != "native_acknowledged" {
					stop["process"] = map[string]any{"pid": float64(4321), "start_fingerprint": "start-abc", "tree_id": "tree-owned"}
				}
				return map[string]any{"status": "cancelled", "stop_evidence": stop, "closed_action_count": float64(1)}
			},
		})
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			fixture := t004NewFixture(t, "artifact-"+testCase.name, TaskStatus(testCase.start))
			if testCase.setup != nil {
				testCase.setup(t, fixture)
			}
			baseline := t004ArtifactBaseline(t, fixture.store.db)
			command := testCase.command(fixture)
			result, err := t004Invoke(context.Background(), fixture.target, command)
			t004RequireApplied(t, result, err)
			if result.ClosedActionCount != testCase.wantClosed {
				t.Errorf("closed count=%d want=%d", result.ClosedActionCount, testCase.wantClosed)
			}
			t004AssertWinnerTask(t, result, fixture.id, testCase.wantStatus)
			t004AssertOneArtifact(t, fixture.store.db, baseline, result, testCase.kind, testCase.event, testCase.wantPayload(fixture, command), command.At)

			if testCase.wantClosed == 2 {
				t004AssertActionClosed(t, fixture.store.db, fixture.id+"-pending", command.At)
				t004AssertActionClosed(t, fixture.store.db, fixture.id+"-responding", command.At)
			}
			if testCase.wantClosed == 1 {
				closedID := fixture.id + "-sibling"
				if strings.HasPrefix(testCase.name, "cancelled-") {
					closedID = fixture.id + "-late-open"
				}
				t004AssertActionClosed(t, fixture.store.db, closedID, command.At)
			}
			var status, taskResult, taskError string
			var completed sql.NullTime
			t004Must(t, fixture.store.db.QueryRow(`SELECT status,result,error,completed_at FROM tasks WHERE id=?`, fixture.id).Scan(&status, &taskResult, &taskError, &completed))
			if status != string(testCase.wantStatus) {
				t.Errorf("durable task status=%s want=%s", status, testCase.wantStatus)
			}
			switch testCase.method {
			case "RequestCancel":
				if testCase.wantStatus == t004Cancelled && result.RequiresStop {
					t.Errorf("pending cancellation RequiresStop=true")
				}
				if testCase.wantStatus == t004Cancelling && !result.RequiresStop {
					t.Errorf("active cancellation RequiresStop=false")
				}
				if result.Winner.Task.CancelRequestedAt == nil || !result.Winner.Task.CancelRequestedAt.Equal(command.At) {
					t.Errorf("winner cancel_requested_at=%v want=%s", result.Winner.Task.CancelRequestedAt, command.At)
				}
				if testCase.wantStatus == t004Cancelled && (result.Winner.Task.CompletedAt == nil || !result.Winner.Task.CompletedAt.Equal(command.At)) {
					t.Errorf("pending cancel completed_at=%v want=%s", result.Winner.Task.CompletedAt, command.At)
				}
			case "CommitDispatched":
				if result.Winner.Task.DispatchedAt == nil || !result.Winner.Task.DispatchedAt.Equal(command.At) {
					t.Errorf("dispatch winner timestamp=%v want=%s", result.Winner.Task.DispatchedAt, command.At)
				}
			case "CommitInputRequired":
				if result.Winner.Action == nil || result.Winner.Action.ActionID != fixture.action || result.Winner.Action.Status != "pending" {
					t.Errorf("input winner action=%#v", result.Winner.Action)
				}
			case "BeginActionResponse":
				if result.Winner.Action == nil || result.Winner.Action.Status != "responding" || result.Winner.Action.RespondedAt == nil || !result.Winner.Action.RespondedAt.Equal(command.At) {
					t.Errorf("begin winner action=%#v", result.Winner.Action)
				}
			case "CommitActionResolution":
				if result.Winner.Action == nil || result.Winner.Action.Status != command.Resolution || result.Winner.Action.ResolvedAt == nil || !result.Winner.Action.ResolvedAt.Equal(command.At) {
					t.Errorf("resolve winner action=%#v", result.Winner.Action)
				}
			default:
				if result.Winner.Action != nil {
					t.Errorf("task-only command winner action=%#v want nil", result.Winner.Action)
				}
			}
			if testCase.wantStatus == t004Cancelled && (taskResult != "" || taskError != "") {
				t.Errorf("cancelled task retained result/error=%q/%q", taskResult, taskError)
			}
			if testCase.wantStatus == t004Completed && (taskResult != "done" || taskError != "") {
				t.Errorf("completed normalization result/error=%q/%q", taskResult, taskError)
			}
			if (testCase.name == "failed" || testCase.name == "failed-crash") && (taskResult != "" || taskError != "sanitized failure") {
				t.Errorf("failure normalization result/error=%q/%q", taskResult, taskError)
			}
			if testCase.wantStatus == t004Failed && testCase.name == "resolve-delivery-unknown" && taskError != "action_delivery_unknown" {
				t.Errorf("delivery_unknown task error=%q want action_delivery_unknown", taskError)
			}
			if testCase.method == "CommitActionResolution" {
				var actionStatus, responseJSON, deliveryJSON string
				t004Must(t, fixture.store.db.QueryRow(`SELECT status,coalesce(response_json,''),coalesce(delivery_json,'') FROM pending_actions WHERE id=?`, fixture.action).Scan(&actionStatus, &responseJSON, &deliveryJSON))
				if actionStatus != command.Resolution || responseJSON == "" || deliveryJSON != command.DeliveryJSON {
					t.Errorf("resolved focal audit status/response/delivery=%q/%q/%q", actionStatus, responseJSON, deliveryJSON)
				}
			}
		})
	}
}

func TestTaskAuthority_ActiveCancellationSourceFence(t *testing.T) {
	for _, status := range []TaskStatus{t004Dispatched, t004Running, t004InputRequired, t004Retrying} {
		status := status
		t.Run(string(status), func(t *testing.T) {
			fixture := t004NewFixture(t, "cancel-source-"+string(status), status)
			t004SeedAction(t, fixture.store.db, fixture.action, fixture.id, "active-source-"+string(status), 7, "responding")
			baseline := t004ArtifactBaseline(t, fixture.store.db)
			command := t004CancelRequest(fixture, t004AuthorityAt)
			result, err := t004Invoke(context.Background(), fixture.target, command)
			t004RequireApplied(t, result, err)
			if !result.RequiresStop || result.ClosedActionCount != 1 {
				t.Errorf("source %s requires_stop/count=%v/%d want true/1", status, result.RequiresStop, result.ClosedActionCount)
			}
			t004AssertWinnerTask(t, result, fixture.id, t004Cancelling)
			t004AssertOneArtifact(t, fixture.store.db, baseline, result, "lifecycle", "task.cancel_requested", map[string]any{
				"status": "cancelling", "cancel_requested_at": command.At.Format(time.RFC3339Nano), "requires_stop": true, "closed_action_count": float64(1),
			}, command.At)
			t004AssertActionClosed(t, fixture.store.db, fixture.action, command.At)
		})
	}
}

func t004AssertStateEqual(t *testing.T, got, want t004State) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("database state changed\ngot=%#v\nwant=%#v", got, want)
	}
}

func t004InvokeLoser(t *testing.T, fixture *t004Fixture, command t004Command, wantKinds ...string) authorityObservedResult {
	t.Helper()
	before := t004ReadState(t, fixture.store.db)
	result, err := t004Invoke(context.Background(), fixture.target, command)
	if !t004IsConflict(err) {
		t.Errorf("error=%v want ErrAuthorityConflict", err)
	}
	t004AssertConflict(t, result, fixture.id, wantKinds...)
	t004AssertStateEqual(t, t004ReadState(t, fixture.store.db), before)
	return result
}

func TestTaskAuthority_StructuredConflictMatrix(t *testing.T) {
	t.Run("task-status", func(t *testing.T) {
		fixture := t004NewFixture(t, "conflict-task", t004Dispatched)
		command := t004DefaultCommand("CommitCompleted", fixture.id, fixture.action)
		command.ExpectedTaskStatus = t004Running
		result := t004InvokeLoser(t, fixture, command, "task_status")
		if result.Winner.Task.Status != "dispatched" || result.Winner.Action != nil {
			t.Errorf("task conflict winner=%#v", result.Winner)
		}
	})

	t.Run("action-missing", func(t *testing.T) {
		fixture := t004NewFixture(t, "conflict-missing", t004InputRequired)
		command := t004BeginCommand(fixture)
		result := t004InvokeLoser(t, fixture, command, "action_missing")
		if result.Winner.Action != nil {
			t.Errorf("missing action winner=%#v want nil", result.Winner.Action)
		}
	})

	t.Run("action-owner", func(t *testing.T) {
		fixture := t004NewFixture(t, "conflict-owner", t004InputRequired)
		other := fixture.id + "-other"
		t004SeedTask(t, fixture.store, other, t004InputRequired)
		t004SeedAction(t, fixture.store.db, fixture.action, other, "owner-correlation", 7, "pending")
		result := t004InvokeLoser(t, fixture, t004BeginCommand(fixture), "action_owner")
		if result.Winner.Action == nil || result.Winner.Action.TaskID != other {
			t.Errorf("owner winner action=%#v want owner=%s", result.Winner.Action, other)
		}
	})

	t.Run("action-source-status", func(t *testing.T) {
		fixture := t004NewFixture(t, "conflict-action-source", t004InputRequired)
		t004SeedAction(t, fixture.store.db, fixture.action, fixture.id, "action-source", 7, "responding")
		result := t004InvokeLoser(t, fixture, t004BeginCommand(fixture), "action_source_status")
		if result.Winner.Action == nil || result.Winner.Action.Status != "responding" {
			t.Errorf("source winner action=%#v", result.Winner.Action)
		}
	})

	t.Run("connection-generation", func(t *testing.T) {
		fixture := t004NewFixture(t, "conflict-generation", t004InputRequired)
		t004SeedAction(t, fixture.store.db, fixture.action, fixture.id, "generation", 7, "pending")
		command := t004BeginCommand(fixture)
		command.ConnectionGeneration = 8
		result := t004InvokeLoser(t, fixture, command, "connection_generation")
		if result.Winner.Action == nil || result.Winner.Action.ConnectionGeneration != 7 {
			t.Errorf("generation winner=%#v", result.Winner.Action)
		}
	})

	t.Run("dual-action-id-provider-correlation", func(t *testing.T) {
		fixture := t004NewFixture(t, "conflict-dual", t004Running)
		owner := fixture.id + "-owner"
		t004SeedTask(t, fixture.store, owner, t004InputRequired)
		t004SeedAction(t, fixture.store.db, fixture.action, owner, "other-provider", 44, "pending")
		t004SeedAction(t, fixture.store.db, fixture.id+"-correlation", fixture.id, "provider-"+fixture.action, 7, "pending")
		result := t004InvokeLoser(t, fixture, t004InputCommand(fixture), "action_id", "provider_correlation")
		if len(result.Winner.Conflicts) != 2 || result.Winner.Action == nil || result.Winner.Action.ActionID != fixture.action {
			t.Errorf("dual conflict winner=%#v", result.Winner)
		}
		if len(result.Winner.Conflicts) == 2 &&
			(result.Winner.Conflicts[0].Action == nil || result.Winner.Conflicts[0].Action.ActionID != fixture.action ||
				result.Winner.Conflicts[1].Action == nil || result.Winner.Conflicts[1].Action.ActionID != fixture.id+"-correlation") {
			t.Errorf("dual conflict precedence/actions=%#v", result.Winner.Conflicts)
		}
	})

	t.Run("provider-correlation", func(t *testing.T) {
		fixture := t004NewFixture(t, "conflict-correlation", t004Running)
		t004SeedAction(t, fixture.store.db, fixture.id+"-seed", fixture.id, "provider-"+fixture.action, 7, "pending")
		result := t004InvokeLoser(t, fixture, t004InputCommand(fixture), "provider_correlation")
		if result.Winner.Action == nil || result.Winner.Action.ActionID != fixture.id+"-seed" {
			t.Errorf("correlation winner=%#v", result.Winner.Action)
		}
	})

	t.Run("dispatch-before-created", func(t *testing.T) {
		fixture := t004NewFixture(t, "conflict-dispatch-created", t004Pending)
		command := t004DefaultCommand("CommitDispatched", fixture.id, fixture.action)
		command.ExpectedTaskStatus = t004Pending
		command.At = t004AuthorityAt.Add(-time.Hour - time.Nanosecond)
		result := t004InvokeLoser(t, fixture, command, "dispatch_time")
		if result.Winner.Action != nil {
			t.Errorf("dispatch time winner action=%#v want nil", result.Winner.Action)
		}
	})

	t.Run("dispatch-regresses-prior", func(t *testing.T) {
		fixture := t004NewFixture(t, "conflict-dispatch-prior", t004Retrying)
		prior := t004AuthorityAt.Add(time.Minute)
		_, err := fixture.store.db.Exec(`UPDATE tasks SET dispatched_at=? WHERE id=?`, prior, fixture.id)
		t004Must(t, err)
		command := t004DefaultCommand("CommitDispatched", fixture.id, fixture.action)
		command.ExpectedTaskStatus = t004Retrying
		command.At = prior.Add(-time.Nanosecond)
		result := t004InvokeLoser(t, fixture, command, "dispatch_time")
		if result.Winner.Task.DispatchedAt == nil || !result.Winner.Task.DispatchedAt.Equal(prior) {
			t.Errorf("dispatch winner prior=%v want=%s", result.Winner.Task.DispatchedAt, prior)
		}
	})

	t.Run("stop-evidence-time", func(t *testing.T) {
		fixture := t004NewFixture(t, "conflict-stop-time", t004Cancelling)
		requestedAt := t004AuthorityAt
		_, err := fixture.store.db.Exec(`UPDATE tasks SET cancel_requested_at=? WHERE id=?`, requestedAt, fixture.id)
		t004Must(t, err)
		command := t004CommitCancelled(fixture, "native_acknowledged")
		command.Stop.ObservedAt = requestedAt.Add(-time.Nanosecond)
		result := t004InvokeLoser(t, fixture, command, "stop_evidence_time")
		if result.Winner.Action != nil {
			t.Errorf("stop time winner action=%#v want nil", result.Winner.Action)
		}
	})
}

func TestTaskAuthority_ResolveWrongGenerationIsStructuredConflict(t *testing.T) {
	fixture := t004NewFixture(t, "resolve-wrong-generation", t004InputRequired)
	t004SeedAction(t, fixture.store.db, fixture.action, fixture.id, "resolve-generation", 7, "responding")
	command := t004ResolveCommand(fixture, "answered")
	command.ConnectionGeneration = 8
	result := t004InvokeLoser(t, fixture, command, "connection_generation")
	if result.Winner.Action == nil || result.Winner.Action.ConnectionGeneration != 7 || result.Winner.Action.Status != "responding" {
		t.Errorf("resolve generation winner=%#v", result.Winner.Action)
	}
}

func TestTaskAuthority_DispatchMonotonicPendingAndRetrying(t *testing.T) {
	for _, source := range []TaskStatus{t004Pending, t004Retrying} {
		source := source
		t.Run(string(source), func(t *testing.T) {
			fixture := t004NewFixture(t, "dispatch-monotonic-"+string(source), source)
			prior := t004AuthorityAt.Add(-time.Minute)
			if source == t004Retrying {
				_, err := fixture.store.db.Exec(`UPDATE tasks SET dispatched_at=? WHERE id=?`, prior, fixture.id)
				t004Must(t, err)
			}
			command := t004DefaultCommand("CommitDispatched", fixture.id, fixture.action)
			command.ExpectedTaskStatus = source
			command.At = prior.Add(time.Second)
			t004AssertBuiltTaskExpectedStatus(t, fixture.target, command)
			baseline := t004ArtifactBaseline(t, fixture.store.db)
			result, err := t004Invoke(context.Background(), fixture.target, command)
			t004RequireApplied(t, result, err)
			if result.Winner.Task.DispatchedAt == nil || !result.Winner.Task.DispatchedAt.Equal(command.At) || result.Winner.Action != nil {
				t.Errorf("dispatch winner=%#v", result.Winner)
			}
			t004AssertOneArtifact(t, fixture.store.db, baseline, result, "lifecycle", "task.dispatched", map[string]any{
				"status": "dispatched", "closed_action_count": float64(0),
			}, command.At)
		})
	}
}

func TestTaskAuthority_ValidationAndNotFoundReturnZero(t *testing.T) {
	type validationCase struct {
		name   string
		start  TaskStatus
		setup  func(*testing.T, *t004Fixture)
		mutate func(*t004Fixture) t004Command
	}
	cases := []validationCase{
		{"dispatch-illegal-caller", t004Running, nil, func(f *t004Fixture) t004Command {
			cmd := t004DefaultCommand("CommitDispatched", f.id, f.action)
			cmd.ExpectedTaskStatus = t004Running
			return cmd
		}},
		{"dispatch-zero-time", t004Pending, nil, func(f *t004Fixture) t004Command {
			cmd := t004DefaultCommand("CommitDispatched", f.id, f.action)
			cmd.ExpectedTaskStatus = t004Pending
			cmd.At = time.Time{}
			return cmd
		}},
		{"input-action-status", t004Running, nil, func(f *t004Fixture) t004Command {
			cmd := t004InputCommand(f)
			cmd.ActionStatus = "responding"
			return cmd
		}},
		{"begin-illegal-caller-action", t004InputRequired, func(t *testing.T, f *t004Fixture) {
			t004SeedAction(t, f.store.db, f.action, f.id, "begin-illegal", 7, "responding")
		}, func(f *t004Fixture) t004Command {
			cmd := t004BeginCommand(f)
			cmd.ExpectedActionStatus = "responding"
			return cmd
		}},
		{"resolve-illegal-caller-action", t004InputRequired, func(t *testing.T, f *t004Fixture) {
			t004SeedAction(t, f.store.db, f.action, f.id, "resolve-illegal", 7, "pending")
		}, func(f *t004Fixture) t004Command {
			cmd := t004ResolveCommand(f, "answered")
			cmd.ExpectedActionStatus = "pending"
			return cmd
		}},
		{"resolve-zero-generation", t004InputRequired, func(t *testing.T, f *t004Fixture) {
			t004SeedAction(t, f.store.db, f.action, f.id, "resolve-zero-generation", 7, "responding")
		}, func(f *t004Fixture) t004Command {
			cmd := t004ResolveCommand(f, "answered")
			cmd.ConnectionGeneration = 0
			return cmd
		}},
		{"cancelled-zero-completed-time", t004Cancelling, func(t *testing.T, f *t004Fixture) {
			_, err := f.store.db.Exec(`UPDATE tasks SET cancel_requested_at=? WHERE id=?`, t004AuthorityAt, f.id)
			t004Must(t, err)
		}, func(f *t004Fixture) t004Command {
			cmd := t004CommitCancelled(f, "native_acknowledged")
			cmd.At = time.Time{}
			return cmd
		}},
		{"cancelled-proof-after-completion", t004Cancelling, func(t *testing.T, f *t004Fixture) {
			_, err := f.store.db.Exec(`UPDATE tasks SET cancel_requested_at=? WHERE id=?`, t004AuthorityAt, f.id)
			t004Must(t, err)
		}, func(f *t004Fixture) t004Command {
			cmd := t004CommitCancelled(f, "native_acknowledged")
			cmd.Stop.ObservedAt = cmd.At.Add(time.Nanosecond)
			return cmd
		}},
		{"cancelled-unknown-kind", t004Cancelling, func(t *testing.T, f *t004Fixture) {
			_, err := f.store.db.Exec(`UPDATE tasks SET cancel_requested_at=? WHERE id=?`, t004AuthorityAt, f.id)
			t004Must(t, err)
		}, func(f *t004Fixture) t004Command {
			cmd := t004CommitCancelled(f, "unknown")
			return cmd
		}},
		{"cancelled-native-plus-process", t004Cancelling, func(t *testing.T, f *t004Fixture) {
			_, err := f.store.db.Exec(`UPDATE tasks SET cancel_requested_at=? WHERE id=?`, t004AuthorityAt, f.id)
			t004Must(t, err)
		}, func(f *t004Fixture) t004Command {
			cmd := t004CommitCancelled(f, "native_acknowledged")
			cmd.Stop.PID = 7
			cmd.Stop.StartFingerprint = "start"
			cmd.Stop.TreeID = "tree"
			return cmd
		}},
		{"cancelled-incomplete-process", t004Cancelling, func(t *testing.T, f *t004Fixture) {
			_, err := f.store.db.Exec(`UPDATE tasks SET cancel_requested_at=? WHERE id=?`, t004AuthorityAt, f.id)
			t004Must(t, err)
		}, func(f *t004Fixture) t004Command {
			cmd := t004CommitCancelled(f, "process_tree_stopped")
			cmd.Stop.TreeID = ""
			return cmd
		}},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			fixture := t004NewFixture(t, "validation-"+testCase.name, testCase.start)
			if testCase.setup != nil {
				testCase.setup(t, fixture)
			}
			before := t004ReadState(t, fixture.store.db)
			command := testCase.mutate(fixture)
			if testCase.name == "dispatch-illegal-caller" {
				t004AssertBuiltTaskExpectedStatus(t, fixture.target, command)
			}
			result, err := t004Invoke(context.Background(), fixture.target, command)
			if err == nil {
				t.Errorf("validation unexpectedly succeeded: %#v", result)
			}
			t004AssertZeroResult(t, result)
			t004AssertStateEqual(t, t004ReadState(t, fixture.store.db), before)
		})
	}

	t.Run("not-found", func(t *testing.T) {
		fixture := t004NewFixture(t, "not-found-owner", t004Running)
		command := t004DefaultCommand("CommitCompleted", "missing-task", "missing-action")
		result, err := t004Invoke(context.Background(), fixture.target, command)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not found") {
			t.Errorf("not-found error=%v", err)
		}
		t004AssertZeroResult(t, result)
	})
}

func TestTaskAuthority_BeginArtifactAbortRestoresAction(t *testing.T) {
	fixture := t004NewFixture(t, "begin-abort", t004Running)
	t004Run(t, fixture, t004InputCommand(fixture))
	before := t004ReadState(t, fixture.store.db)
	_, err := fixture.store.db.Exec(`CREATE TRIGGER t004_abort_begin BEFORE INSERT ON task_artifacts
		WHEN NEW.task_id='begin-abort' AND NEW.event_type='task.input_response_started'
		BEGIN SELECT RAISE(ABORT,'T004_BEGIN_ARTIFACT_ABORT'); END`)
	t004Must(t, err)
	result, invokeErr := t004Invoke(context.Background(), fixture.target, t004BeginCommand(fixture))
	if invokeErr == nil || !strings.Contains(invokeErr.Error(), "T004_BEGIN_ARTIFACT_ABORT") {
		t.Errorf("begin abort error=%v", invokeErr)
	}
	t004AssertZeroResult(t, result)
	after := t004ReadState(t, fixture.store.db)
	delete(before.artifacts, "")
	t004AssertStateEqual(t, after, before)
	row := after.actions[fixture.action]
	if row["status"] != "pending" || row["response_json"] != "<NULL>" || row["responded_at"] != "<NULL>" {
		t.Errorf("begin abort leaked action mutation: %#v", row)
	}
}

func TestTaskAuthority_WriteAbortReturnsWhollyZero(t *testing.T) {
	fixture := t004NewFixture(t, "terminal-abort", t004Running)
	before := t004ReadState(t, fixture.store.db)
	_, err := fixture.store.db.Exec(`CREATE TRIGGER t004_abort_terminal BEFORE INSERT ON task_artifacts
		WHEN NEW.task_id='terminal-abort' BEGIN SELECT RAISE(ABORT,'T004_TERMINAL_ABORT'); END`)
	t004Must(t, err)
	command := t004DefaultCommand("CommitCompleted", fixture.id, fixture.action)
	command.ExpectedTaskStatus = t004Running
	result, invokeErr := t004Invoke(context.Background(), fixture.target, command)
	if invokeErr == nil || !strings.Contains(invokeErr.Error(), "T004_TERMINAL_ABORT") {
		t.Errorf("terminal abort error=%v", invokeErr)
	}
	t004AssertZeroResult(t, result)
	t004AssertStateEqual(t, t004ReadState(t, fixture.store.db), before)
}

func TestTaskAuthority_LegalSourceMatrix(t *testing.T) {
	type sourceCase struct {
		method string
		legal  []TaskStatus
	}
	cases := []sourceCase{
		{"CommitDispatched", []TaskStatus{t004Pending, t004Retrying}},
		{"CommitCompleted", []TaskStatus{t004Running}},
		{"CommitFailed", []TaskStatus{t004Pending, t004Dispatched, t004Running, t004InputRequired, t004Retrying}},
		{"CommitFailedCrash", []TaskStatus{t004Dispatched, t004Running, t004InputRequired, t004Retrying, t004Cancelling}},
		{"CommitCancelled", []TaskStatus{t004Cancelling}},
		{"CommitInputRequired", []TaskStatus{t004Running}},
		{"BeginActionResponse", []TaskStatus{t004InputRequired}},
		{"CommitActionResolution", []TaskStatus{t004InputRequired}},
		{"RequestCancel", []TaskStatus{t004Pending, t004Dispatched, t004Running, t004InputRequired, t004Retrying}},
	}
	for _, testCase := range cases {
		for _, source := range testCase.legal {
			testCase, source := testCase, source
			t.Run(testCase.method+"-"+string(source), func(t *testing.T) {
				fixture := t004NewFixture(t, "source-"+testCase.method+"-"+string(source), source)
				command := t004DefaultCommand(testCase.method, fixture.id, fixture.action)
				command.ExpectedTaskStatus = source
				switch testCase.method {
				case "CommitDispatched":
					command.At = t004AuthorityAt
				case "CommitCancelled":
					requestedAt := t004AuthorityAt.Add(-2 * time.Second)
					_, err := fixture.store.db.Exec(`UPDATE tasks SET cancel_requested_at=? WHERE id=?`, requestedAt, fixture.id)
					t004Must(t, err)
					command = t004CommitCancelled(fixture, "native_acknowledged")
				case "CommitInputRequired":
					command = t004InputCommand(fixture)
				case "BeginActionResponse":
					t004SeedAction(t, fixture.store.db, fixture.action, fixture.id, "source-begin", 7, "pending")
					command = t004BeginCommand(fixture)
				case "CommitActionResolution":
					t004SeedAction(t, fixture.store.db, fixture.action, fixture.id, "source-resolve", 7, "responding")
					command = t004ResolveCommand(fixture, "answered")
				}
				result, err := t004Invoke(context.Background(), fixture.target, command)
				t004RequireApplied(t, result, err)
			})
		}
	}

	illegalExpected := []struct {
		method string
		from   TaskStatus
		bad    TaskStatus
	}{
		{"CommitDispatched", t004Pending, t004Running},
		{"CommitCompleted", t004Running, t004Dispatched},
		{"CommitFailed", t004Running, t004Cancelling},
		{"CommitFailedCrash", t004Running, t004Pending},
		{"CommitCancelled", t004Cancelling, t004Running},
		{"CommitInputRequired", t004Running, t004Dispatched},
	}
	for _, row := range illegalExpected {
		row := row
		t.Run("illegal-caller-"+row.method, func(t *testing.T) {
			fixture := t004NewFixture(t, "illegal-"+row.method, row.from)
			command := t004DefaultCommand(row.method, fixture.id, fixture.action)
			command.ExpectedTaskStatus = row.bad
			if row.method == "CommitCancelled" {
				command = t004CommitCancelled(fixture, "native_acknowledged")
				command.ExpectedTaskStatus = row.bad
			}
			before := t004ReadState(t, fixture.store.db)
			result, err := t004Invoke(context.Background(), fixture.target, command)
			if err == nil {
				t.Errorf("illegal caller source succeeded")
			}
			t004AssertZeroResult(t, result)
			t004AssertStateEqual(t, t004ReadState(t, fixture.store.db), before)
		})
	}
}

func TestTaskAuthority_DurableSourceClosureMatrix(t *testing.T) {
	all := []TaskStatus{t004Pending, t004Dispatched, t004Running, t004InputRequired, t004Retrying, t004Cancelling, t004Completed, t004Failed, t004FailedCrash, t004Cancelled}
	type durableCase struct {
		method   string
		expected TaskStatus
	}
	cases := []durableCase{
		{"CommitDispatched", t004Pending},
		{"CommitCompleted", t004Running},
		{"CommitFailed", t004Running},
		{"CommitFailedCrash", t004Running},
		{"CommitCancelled", t004Cancelling},
		{"CommitInputRequired", t004Running},
		{"BeginActionResponse", t004InputRequired},
		{"CommitActionResolution", t004InputRequired},
	}
	for _, testCase := range cases {
		for _, durable := range all {
			if durable == testCase.expected {
				continue
			}
			testCase, durable := testCase, durable
			t.Run(testCase.method+"-durable-"+string(durable), func(t *testing.T) {
				fixture := t004NewFixture(t, "durable-"+testCase.method+"-"+string(durable), durable)
				command := t004DefaultCommand(testCase.method, fixture.id, fixture.action)
				command.ExpectedTaskStatus = testCase.expected
				if testCase.method == "BeginActionResponse" {
					command = t004BeginCommand(fixture)
				}
				if testCase.method == "CommitActionResolution" {
					command = t004ResolveCommand(fixture, "answered")
				}
				if testCase.method == "CommitCancelled" {
					command = t004CommitCancelled(fixture, "native_acknowledged")
				}
				result := t004InvokeLoser(t, fixture, command, "task_status")
				if result.Winner.Task.Status != string(durable) || result.Winner.Action != nil {
					t.Errorf("durable source winner=%#v want status=%s action=nil", result.Winner, durable)
				}
			})
		}
	}

	for _, durable := range []TaskStatus{t004Cancelling, t004Completed, t004Failed, t004FailedCrash, t004Cancelled} {
		durable := durable
		t.Run("RequestCancel-durable-"+string(durable), func(t *testing.T) {
			fixture := t004NewFixture(t, "durable-request-cancel-"+string(durable), durable)
			result := t004InvokeLoser(t, fixture, t004CancelRequest(fixture, t004AuthorityAt), "task_status")
			if result.Winner.Task.Status != string(durable) || result.Winner.Action != nil {
				t.Errorf("request cancel winner=%#v want status=%s action=nil", result.Winner, durable)
			}
		})
	}
}

func TestTaskAuthority_ProviderCorrelationIsTaskScoped(t *testing.T) {
	store, _ := t004AuthorityPair(t)
	left := &t004Fixture{store: store, target: store, id: "provider-scope-left", action: "provider-scope-left-action"}
	right := &t004Fixture{store: store, target: store, id: "provider-scope-right", action: "provider-scope-right-action"}
	t004SeedTask(t, store, left.id, t004Running)
	t004SeedTask(t, store, right.id, t004Running)
	leftCommand, rightCommand := t004InputCommand(left), t004InputCommand(right)
	leftCommand.ProviderRequestID = "shared-provider-request"
	rightCommand.ProviderRequestID = "shared-provider-request"
	leftCommand.ConnectionGeneration = 55
	rightCommand.ConnectionGeneration = 55
	baseline := t004ArtifactBaseline(t, store.db)
	leftResult, leftErr := t004Invoke(context.Background(), left.target, leftCommand)
	rightResult, rightErr := t004Invoke(context.Background(), right.target, rightCommand)
	t004RequireApplied(t, leftResult, leftErr)
	t004RequireApplied(t, rightResult, rightErr)
	if artifacts := t004ReadNewArtifacts(t, store.db, baseline); len(artifacts) != 2 {
		t.Errorf("cross-task correlation artifacts=%d want=2", len(artifacts))
	}
	var count int
	t004Must(t, store.db.QueryRow(`SELECT count(*) FROM pending_actions WHERE provider_request_id=? AND connection_generation=?`, "shared-provider-request", 55).Scan(&count))
	if count != 2 {
		t.Errorf("cross-task provider rows=%d want=2", count)
	}
}

type t004RaceAttempt struct {
	id      string
	target  any
	command t004Command
}

type t004RaceOutcome struct {
	id     string
	result authorityObservedResult
	err    error
}

func t004RunRace(attempts ...t004RaceAttempt) map[string]t004RaceOutcome {
	start := make(chan struct{})
	ready := sync.WaitGroup{}
	ready.Add(len(attempts))
	output := make(chan t004RaceOutcome, len(attempts))
	for _, attempt := range attempts {
		attempt := attempt
		go func() {
			ready.Done()
			<-start
			result, err := t004Invoke(context.Background(), attempt.target, attempt.command)
			output <- t004RaceOutcome{id: attempt.id, result: result, err: err}
		}()
	}
	ready.Wait()
	close(start)
	results := map[string]t004RaceOutcome{}
	for range attempts {
		outcome := <-output
		results[outcome.id] = outcome
	}
	return results
}

func t004AssertRace(t *testing.T, db *sql.DB, baseline int64, outcomes map[string]t004RaceOutcome, wantApplied int) {
	t.Helper()
	applied := 0
	for id, outcome := range outcomes {
		lower := strings.ToLower(fmt.Sprint(outcome.err))
		if strings.Contains(lower, "sqlite_busy") || strings.Contains(lower, "sqlite_locked") || strings.Contains(lower, "database is locked") {
			t.Errorf("attempt %s leaked lock error: %v", id, outcome.err)
		}
		if outcome.result.Applied {
			applied++
			if outcome.err != nil {
				t.Errorf("applied attempt %s error=%v", id, outcome.err)
			}
		} else if outcome.err == nil || !t004IsConflict(outcome.err) {
			t.Errorf("loser %s error=%v result=%#v", id, outcome.err, outcome.result)
		}
	}
	if applied != wantApplied {
		t.Errorf("applied=%d want=%d outcomes=%#v", applied, wantApplied, outcomes)
	}
	artifacts := t004ReadNewArtifacts(t, db, baseline)
	if len(artifacts) != applied {
		t.Errorf("new artifacts=%d applied=%d", len(artifacts), applied)
	}
}

func TestTaskAuthority_MutuallyExclusiveRaces(t *testing.T) {
	type raceCase struct {
		name  string
		start TaskStatus
		setup func(*testing.T, *t004Fixture)
		left  func(*t004Fixture) t004Command
		right func(*t004Fixture) t004Command
	}
	cases := []raceCase{
		{
			name: "two-pending-cancels", start: t004Pending,
			left:  func(f *t004Fixture) t004Command { return t004CancelRequest(f, t004AuthorityAt) },
			right: func(f *t004Fixture) t004Command { return t004CancelRequest(f, t004AuthorityAt.Add(time.Nanosecond)) },
		},
		{
			name: "pending-cancel-fail", start: t004Pending,
			left: func(f *t004Fixture) t004Command { return t004CancelRequest(f, t004AuthorityAt) },
			right: func(f *t004Fixture) t004Command {
				cmd := t004DefaultCommand("CommitFailed", f.id, f.action)
				cmd.ExpectedTaskStatus = t004Pending
				return cmd
			},
		},
		{
			name: "active-cancel-complete", start: t004Running,
			left: func(f *t004Fixture) t004Command { return t004CancelRequest(f, t004AuthorityAt) },
			right: func(f *t004Fixture) t004Command {
				cmd := t004DefaultCommand("CommitCompleted", f.id, f.action)
				cmd.ExpectedTaskStatus = t004Running
				return cmd
			},
		},
		{
			name: "complete-crash", start: t004Running,
			left: func(f *t004Fixture) t004Command {
				cmd := t004DefaultCommand("CommitCompleted", f.id, f.action)
				cmd.ExpectedTaskStatus = t004Running
				return cmd
			},
			right: func(f *t004Fixture) t004Command {
				cmd := t004DefaultCommand("CommitFailedCrash", f.id, f.action)
				cmd.ExpectedTaskStatus = t004Running
				return cmd
			},
		},
		{
			name: "two-input", start: t004Running,
			left: t004InputCommand,
			right: func(f *t004Fixture) t004Command {
				cmd := t004InputCommand(f)
				cmd.ActionID += "-other"
				cmd.ProviderRequestID += "-other"
				return cmd
			},
		},
		{
			name: "two-resolutions", start: t004InputRequired,
			setup: func(t *testing.T, f *t004Fixture) {
				t004SeedAction(t, f.store.db, f.action, f.id, "race-resolution", 7, "responding")
			},
			left:  func(f *t004Fixture) t004Command { return t004ResolveCommand(f, "answered") },
			right: func(f *t004Fixture) t004Command { return t004ResolveCommand(f, "declined") },
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			leftStore, rightStore := t004AuthorityPair(t)
			t004SeedTask(t, leftStore, "race-"+testCase.name, testCase.start)
			leftFixture := &t004Fixture{store: leftStore, target: leftStore, id: "race-" + testCase.name, action: "race-" + testCase.name + "-action"}
			rightFixture := &t004Fixture{store: rightStore, target: rightStore, id: leftFixture.id, action: leftFixture.action}
			if testCase.setup != nil {
				testCase.setup(t, leftFixture)
			}
			baseline := t004ArtifactBaseline(t, leftStore.db)
			outcomes := t004RunRace(
				t004RaceAttempt{id: "attempt-left", target: leftStore, command: testCase.left(leftFixture)},
				t004RaceAttempt{id: "attempt-right", target: rightStore, command: testCase.right(rightFixture)},
			)
			if len(outcomes) != 2 {
				t.Fatalf("attempt map lost same-operation result: %#v", outcomes)
			}
			t004AssertRace(t, leftStore.db, baseline, outcomes, 1)
		})
	}
}

func t004CountApplied(results ...authorityObservedResult) int {
	count := 0
	for _, result := range results {
		if result.Applied {
			count++
		}
	}
	return count
}

func t004AssertAppliedEqualsFacts(t *testing.T, db *sql.DB, baseline int64, results ...authorityObservedResult) {
	t.Helper()
	applied := t004CountApplied(results...)
	if facts := len(t004ReadNewArtifacts(t, db, baseline)); facts != applied {
		t.Errorf("applied=%d facts=%d", applied, facts)
	}
}

func TestTaskAuthority_ComposableDispatchCancelOrders(t *testing.T) {
	orders := []struct {
		name          string
		start         TaskStatus
		dispatchFirst bool
	}{
		{"pending-cancel-first", t004Pending, false}, {"pending-dispatch-first", t004Pending, true},
		{"retry-cancel-first", t004Retrying, false}, {"retry-dispatch-first", t004Retrying, true},
	}
	for _, order := range orders {
		order := order
		t.Run(order.name, func(t *testing.T) {
			fixture := t004NewFixture(t, "order-"+order.name, order.start)
			dispatch := t004DefaultCommand("CommitDispatched", fixture.id, fixture.action)
			dispatch.ExpectedTaskStatus = order.start
			cancel := t004CancelRequest(fixture, t004AuthorityAt.Add(time.Second))
			baseline := t004ArtifactBaseline(t, fixture.store.db)
			var first, second authorityObservedResult
			var firstErr, secondErr error
			if order.dispatchFirst {
				first, firstErr = t004Invoke(context.Background(), fixture.target, dispatch)
				second, secondErr = t004Invoke(context.Background(), fixture.target, cancel)
				t004RequireApplied(t, first, firstErr)
				t004RequireApplied(t, second, secondErr)
				if !second.RequiresStop {
					t.Errorf("dispatch-first cancel RequiresStop=false")
				}
				t004AssertWinnerTask(t, second, fixture.id, t004Cancelling)
			} else {
				first, firstErr = t004Invoke(context.Background(), fixture.target, cancel)
				second, secondErr = t004Invoke(context.Background(), fixture.target, dispatch)
				t004RequireApplied(t, first, firstErr)
				if !t004IsConflict(secondErr) {
					t.Errorf("cancel-first dispatch error=%v", secondErr)
				}
				t004AssertConflict(t, second, fixture.id, "task_status")
				want := t004Cancelled
				if order.start == t004Retrying {
					want = t004Cancelling
					if !first.RequiresStop {
						t.Errorf("retry cancel RequiresStop=false")
					}
				}
				t004AssertWinnerTask(t, first, fixture.id, want)
			}
			t004AssertAppliedEqualsFacts(t, fixture.store.db, baseline, first, second)
		})
	}
}

func TestTaskAuthority_ComposableActionCancelOrders(t *testing.T) {
	t.Run("begin", func(t *testing.T) {
		for _, actionFirst := range []bool{false, true} {
			name := map[bool]string{false: "cancel-first", true: "begin-first"}[actionFirst]
			t.Run(name, func(t *testing.T) {
				fixture := t004NewFixture(t, "begin-order-"+name, t004Running)
				t004Run(t, fixture, t004InputCommand(fixture))
				begin, cancel := t004BeginCommand(fixture), t004CancelRequest(fixture, t004AuthorityAt.Add(2*time.Second))
				baseline := t004ArtifactBaseline(t, fixture.store.db)
				var first, second authorityObservedResult
				var firstErr, secondErr error
				if actionFirst {
					first, firstErr = t004Invoke(context.Background(), fixture.target, begin)
					second, secondErr = t004Invoke(context.Background(), fixture.target, cancel)
					t004RequireApplied(t, first, firstErr)
					t004RequireApplied(t, second, secondErr)
					t004AssertActionClosed(t, fixture.store.db, fixture.action, cancel.At)
				} else {
					first, firstErr = t004Invoke(context.Background(), fixture.target, cancel)
					second, secondErr = t004Invoke(context.Background(), fixture.target, begin)
					t004RequireApplied(t, first, firstErr)
					if !t004IsConflict(secondErr) {
						t.Errorf("cancel-first begin error=%v", secondErr)
					}
					t004AssertConflict(t, second, fixture.id, "task_status")
					if second.Winner.Action != nil {
						t.Errorf("task-status precedence returned action=%#v", second.Winner.Action)
					}
				}
				t004AssertAppliedEqualsFacts(t, fixture.store.db, baseline, first, second)
			})
		}
	})

	for _, resolution := range []string{"answered", "delivery_unknown"} {
		resolution := resolution
		t.Run("resolve-"+resolution, func(t *testing.T) {
			for _, actionFirst := range []bool{false, true} {
				name := map[bool]string{false: "cancel-first", true: "resolve-first"}[actionFirst]
				t.Run(name, func(t *testing.T) {
					fixture := t004NewFixture(t, "resolve-order-"+resolution+"-"+name, t004Running)
					t004Run(t, fixture, t004InputCommand(fixture))
					t004Run(t, fixture, t004BeginCommand(fixture))
					resolve, cancel := t004ResolveCommand(fixture, resolution), t004CancelRequest(fixture, t004AuthorityAt.Add(3*time.Second))
					baseline := t004ArtifactBaseline(t, fixture.store.db)
					var first, second authorityObservedResult
					var firstErr, secondErr error
					if actionFirst {
						first, firstErr = t004Invoke(context.Background(), fixture.target, resolve)
						second, secondErr = t004Invoke(context.Background(), fixture.target, cancel)
						t004RequireApplied(t, first, firstErr)
						if resolution == "answered" {
							t004RequireApplied(t, second, secondErr)
							var actionStatus string
							t004Must(t, fixture.store.db.QueryRow(`SELECT status FROM pending_actions WHERE id=?`, fixture.action).Scan(&actionStatus))
							if actionStatus != "answered" {
								t.Errorf("cancel rewrote truthful resolved action=%s", actionStatus)
							}
						} else {
							if !t004IsConflict(secondErr) {
								t.Errorf("delivery-first cancel error=%v", secondErr)
							}
							t004AssertConflict(t, second, fixture.id, "task_status")
						}
					} else {
						first, firstErr = t004Invoke(context.Background(), fixture.target, cancel)
						second, secondErr = t004Invoke(context.Background(), fixture.target, resolve)
						t004RequireApplied(t, first, firstErr)
						if !t004IsConflict(secondErr) {
							t.Errorf("cancel-first resolve error=%v", secondErr)
						}
						t004AssertConflict(t, second, fixture.id, "task_status")
						if second.Winner.Action != nil {
							t.Errorf("cancel-first resolve winner action=%#v", second.Winner.Action)
						}
					}
					t004AssertAppliedEqualsFacts(t, fixture.store.db, baseline, first, second)
				})
			}
		})
	}
}
