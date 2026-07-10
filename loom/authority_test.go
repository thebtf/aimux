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

var authorityAt = time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC)

type authorityOp string

const (
	opRequestCancel authorityOp = "RequestCancel"
	opCompleted     authorityOp = "CommitCompleted"
	opFailed        authorityOp = "CommitFailed"
	opFailedCrash   authorityOp = "CommitFailedCrash"
	opCancelled     authorityOp = "CommitCancelled"
	opInput         authorityOp = "CommitInputRequired"
	opBegin         authorityOp = "BeginActionResponse"
	opResolve       authorityOp = "CommitActionResolution"
)

type authorityCommand struct {
	op                         authorityOp
	taskID, actionID           string
	expectedTask               TaskStatus
	expectedAction, resolution string
	generation                 uint64
	next                       TaskStatus
	evidence                   StopEvidence
}

type authorityFact struct{ kind, event string }

type authorityCase struct {
	name    string
	start   TaskStatus
	setup   []authorityOp
	command authorityCommand
	wantErr error
	seed    string
}

type authorityFixture struct {
	store      *TaskStore
	authority  TaskAuthority
	id, action string
}

type authorityRow map[string]string
type authorityRows map[string]authorityRow
type authorityState struct{ tasks, actions, artifacts authorityRows }
type authorityPatch struct {
	id     string
	insert bool
	cells  map[string]any
}
type authorityDelta struct {
	taskID       string
	task, action authorityPatch
	fact         *authorityFact
}

func authorityTemplate(op authorityOp) authorityCommand {
	cmd := authorityCommand{op: op, generation: 7, evidence: StopEvidence{NativeAcknowledged: true, ObservedAt: authorityAt}}
	switch op {
	case opCompleted, opFailed, opInput:
		cmd.expectedTask = TaskStatusRunning
	case opFailedCrash:
		cmd.expectedTask = TaskStatusDispatched
	case opCancelled:
		cmd.expectedTask = TaskStatusCancelling
	case opBegin:
		cmd.expectedAction = "pending"
	case opResolve:
		cmd.expectedTask, cmd.expectedAction = TaskStatusInputRequired, "responding"
		cmd.resolution, cmd.next = "answered", TaskStatusRunning
	}
	return cmd
}

func authorityCommandFor(op authorityOp, task string, expectedTask TaskStatus, expectedAction string, generation uint64) authorityCommand {
	cmd := authorityTemplate(op)
	if task != "" {
		cmd.taskID = task
	}
	if expectedTask != "" {
		cmd.expectedTask = expectedTask
	}
	if expectedAction != "" {
		cmd.expectedAction = expectedAction
	}
	if generation != 0 {
		cmd.generation = generation
	}
	return cmd
}

func authorityResolution(resolution string, next TaskStatus) authorityCommand {
	cmd := authorityTemplate(opResolve)
	cmd.resolution, cmd.next = resolution, next
	return cmd
}

func bindAuthorityCommand(f *authorityFixture, cmd authorityCommand) authorityCommand {
	if cmd.taskID == "" {
		cmd.taskID = f.id
	}
	if cmd.actionID == "" {
		cmd.actionID = f.action
	}
	return cmd
}

var authoritySuccessCases = []authorityCase{
	{"request-cancel", TaskStatusRunning, nil, authorityTemplate(opRequestCancel), nil, ""},
	{"completed", TaskStatusRunning, nil, authorityTemplate(opCompleted), nil, ""},
	{"failed", TaskStatusRunning, nil, authorityTemplate(opFailed), nil, ""},
	{"failed-crash", TaskStatusDispatched, nil, authorityTemplate(opFailedCrash), nil, ""},
	{"cancelled", TaskStatusRunning, []authorityOp{opRequestCancel}, authorityTemplate(opCancelled), nil, ""},
	{"input-required", TaskStatusRunning, nil, authorityTemplate(opInput), nil, ""},
	{"begin-response", TaskStatusRunning, []authorityOp{opInput}, authorityTemplate(opBegin), nil, ""},
	{"resolve-answered", TaskStatusRunning, []authorityOp{opInput, opBegin}, authorityTemplate(opResolve), nil, ""},
	{"resolve-delivery-unknown", TaskStatusRunning, []authorityOp{opInput, opBegin}, authorityResolution("delivery_unknown", TaskStatusFailed), nil, ""},
}

var authorityRejectCases = []authorityCase{
	{"request-cancel-stale", TaskStatusCompleted, nil, authorityTemplate(opRequestCancel), ErrAuthorityConflict, ""},
	{"completed-stale", TaskStatusDispatched, nil, authorityTemplate(opCompleted), ErrAuthorityConflict, ""},
	{"failed-stale", TaskStatusDispatched, nil, authorityTemplate(opFailed), ErrAuthorityConflict, ""},
	{"failed-crash-stale", TaskStatusRunning, nil, authorityTemplate(opFailedCrash), ErrAuthorityConflict, ""},
	{"cancelled-stale", TaskStatusRunning, nil, authorityTemplate(opCancelled), ErrAuthorityConflict, ""},
	{"input-stale", TaskStatusDispatched, nil, authorityTemplate(opInput), ErrAuthorityConflict, ""},
	{"input-action-id", TaskStatusRunning, nil, authorityTemplate(opInput), ErrAuthorityConflict, "duplicate-id"},
	{"input-correlation", TaskStatusRunning, nil, authorityTemplate(opInput), ErrAuthorityConflict, "duplicate-correlation"},
	{"begin-action-state", TaskStatusRunning, []authorityOp{opInput}, authorityCommandFor(opBegin, "", "", "responding", 0), ErrAuthorityConflict, ""},
	{"begin-task-state", TaskStatusRunning, []authorityOp{opInput}, authorityTemplate(opBegin), ErrAuthorityConflict, "task-running"},
	{"begin-owner", TaskStatusRunning, []authorityOp{opInput}, authorityCommandFor(opBegin, "other", "", "", 0), ErrAuthorityConflict, "other-task"},
	{"begin-generation", TaskStatusRunning, []authorityOp{opInput}, authorityCommandFor(opBegin, "", "", "", 8), ErrAuthorityConflict, ""},
	{"resolve-action-state", TaskStatusRunning, []authorityOp{opInput, opBegin}, authorityCommandFor(opResolve, "", "", "pending", 0), ErrAuthorityConflict, ""},
	{"resolve-task-state", TaskStatusRunning, []authorityOp{opInput, opBegin}, authorityCommandFor(opResolve, "", TaskStatusRunning, "", 0), ErrAuthorityConflict, ""},
	{"resolve-owner", TaskStatusRunning, []authorityOp{opInput, opBegin}, authorityCommandFor(opResolve, "other", "", "", 0), ErrAuthorityConflict, "other-task"},
	{"not-found", TaskStatusRunning, nil, authorityCommandFor(opCompleted, "missing", "", "", 0), ErrTaskNotFound, ""},
	{"invalid-transition", TaskStatusPending, nil, authorityCommandFor(opFailedCrash, "", TaskStatusPending, "", 0), ErrAuthorityInvalidTransition, ""},
}

func authorityPair(t *testing.T) (*TaskStore, *TaskStore) {
	t.Helper()
	path := t.TempDir() + "/authority.db"
	return t004NewStore(t, t004OpenDB(t, path), "authority-a"), t004NewStore(t, t004OpenDB(t, path), "authority-b")
}

func seedAuthorityTask(t *testing.T, store *TaskStore, id string, status TaskStatus) {
	t.Helper()
	task := makeTask(id, "project-"+id, status)
	task.RequestID, task.Prompt, task.CreatedAt = "request-"+id, "prompt-"+id, authorityAt.Add(-time.Hour)
	t004Must(t, store.Create(task))
	_, err := store.db.Exec(`UPDATE tasks SET result='seed-result',error='seed-error' WHERE id=?`, id)
	t004Must(t, err)
}

func newAuthorityFixture(t *testing.T, id string, status TaskStatus) *authorityFixture {
	t.Helper()
	store, _ := authorityPair(t)
	seedAuthorityTask(t, store, id, status)
	canary := id + "-canary"
	seedAuthorityTask(t, store, canary, TaskStatusRunning)
	seedAuthorityAction(t, store, canary+"-action", canary, "canary-provider", 91, "answered")
	return &authorityFixture{store: store, authority: store, id: id, action: id + "-action"}
}

func seedAuthorityAction(t *testing.T, store *TaskStore, id, taskID, provider string, generation uint64, status string) {
	t.Helper()
	var response, delivery, responded, resolved any
	if status == "answered" {
		response, delivery = `{"canary":"response"}`, `{"canary":"delivery"}`
		responded, resolved = authorityAt.Add(-3*time.Minute), authorityAt.Add(-2*time.Minute)
	}
	_, err := store.db.Exec(`INSERT INTO pending_actions(id,task_id,kind,status,provider_request_id,connection_generation,request_json,response_json,delivery_json,expires_at,created_at,responded_at,resolved_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, taskID, "input", status, provider, generation, `{"canary":"request"}`, response, delivery, authorityAt.Add(time.Hour), authorityAt.Add(-time.Minute), responded, resolved)
	t004Must(t, err)
}

func authorityCell(value any) string {
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

func readAuthorityRows(t *testing.T, db *sql.DB, table, key string) authorityRows {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY ` + key)
	t004Must(t, err)
	defer rows.Close()
	columns, err := rows.Columns()
	t004Must(t, err)
	keyIndex := -1
	for i, column := range columns {
		if column == key {
			keyIndex = i
		}
	}
	if keyIndex < 0 {
		t.Fatalf("%s missing key column %s", table, key)
	}
	result := authorityRows{}
	for rows.Next() {
		values, targets := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		t004Must(t, rows.Scan(targets...))
		row := authorityRow{}
		for i, column := range columns {
			row[column] = authorityCell(values[i])
		}
		result[authorityCell(values[keyIndex])] = row
	}
	t004Must(t, rows.Err())
	return result
}

func readAuthorityState(t *testing.T, store *TaskStore, target string) authorityState {
	t.Helper()
	state := authorityState{
		tasks:     readAuthorityRows(t, store.db, "tasks", "id"),
		actions:   readAuthorityRows(t, store.db, "pending_actions", "id"),
		artifacts: readAuthorityRows(t, store.db, "task_artifacts", "seq"),
	}
	if _, ok := state.tasks[target]; !ok {
		t.Fatalf("authority state missing target %q", target)
	}
	return state
}

func runAuthorityCommand(f *authorityFixture, cmd authorityCommand) error {
	ctx := context.Background()
	switch cmd.op {
	case opRequestCancel:
		_, err := f.authority.RequestCancel(ctx, cmd.taskID, authorityAt)
		return err
	case opCompleted:
		_, err := f.authority.CommitCompleted(ctx, CompleteTask{TaskID: cmd.taskID, ExpectedStatus: cmd.expectedTask, Result: "done", CompletedAt: authorityAt})
		return err
	case opFailed:
		_, err := f.authority.CommitFailed(ctx, FailTask{TaskID: cmd.taskID, ExpectedStatus: cmd.expectedTask, Error: "failed", CompletedAt: authorityAt})
		return err
	case opFailedCrash:
		_, err := f.authority.CommitFailedCrash(ctx, FailCrashedTask{TaskID: cmd.taskID, ExpectedStatus: cmd.expectedTask, Error: "crash", CompletedAt: authorityAt})
		return err
	case opCancelled:
		_, err := f.authority.CommitCancelled(ctx, CancelTask{TaskID: cmd.taskID, ExpectedStatus: cmd.expectedTask, StopEvidence: cmd.evidence, CompletedAt: authorityAt.Add(time.Second)})
		return err
	case opInput:
		_, err := f.authority.CommitInputRequired(ctx, RequireInput{TaskID: cmd.taskID, ExpectedStatus: cmd.expectedTask, Action: PendingAction{ID: cmd.actionID, Kind: "input", ProviderRequestID: "provider-" + cmd.actionID, ConnectionGeneration: cmd.generation, RequestJSON: `{"prompt":"continue?"}`, ExpiresAt: authorityAt.Add(time.Hour)}, OccurredAt: authorityAt})
		return err
	case opBegin:
		_, err := f.authority.BeginActionResponse(ctx, BeginResponse{TaskID: cmd.taskID, ActionID: cmd.actionID, ExpectedStatus: cmd.expectedAction, ResponseJSON: `{"answer":"yes"}`, ConnectionGeneration: cmd.generation, RespondedAt: authorityAt.Add(time.Second)})
		return err
	case opResolve:
		_, err := f.authority.CommitActionResolution(ctx, ResolveAction{TaskID: cmd.taskID, ActionID: cmd.actionID, ExpectedActionStatus: cmd.expectedAction, ExpectedTaskStatus: cmd.expectedTask, Resolution: cmd.resolution, NextTaskStatus: cmd.next, DeliveryJSON: `{"resolution":"` + cmd.resolution + `"}`, ResolvedAt: authorityAt.Add(2 * time.Second)})
		return err
	default:
		return fmt.Errorf("unknown authority op %q", cmd.op)
	}
}

func runAuthorityOps(t *testing.T, f *authorityFixture, ops ...authorityOp) {
	t.Helper()
	for _, op := range ops {
		t004Must(t, runAuthorityCommand(f, bindAuthorityCommand(f, authorityTemplate(op))))
	}
}

func rejectAuthority(t *testing.T, f *authorityFixture, wantErr error, cmd authorityCommand) {
	t.Helper()
	before := readAuthorityState(t, f.store, f.id)
	err := runAuthorityCommand(f, cmd)
	if err == nil || wantErr != nil && !errors.Is(err, wantErr) {
		t.Fatalf("%s error=%v want=%v", cmd.op, err, wantErr)
	}
	assertAuthorityDelta(t, before, readAuthorityState(t, f.store, f.id), authorityDelta{})
}

func cloneAuthorityRows(rows authorityRows) authorityRows {
	clone := authorityRows{}
	for id, row := range rows {
		clone[id] = authorityRow{}
		for column, value := range row {
			clone[id][column] = value
		}
	}
	return clone
}

func applyAuthorityPatch(t *testing.T, rows authorityRows, patch authorityPatch) {
	t.Helper()
	if patch.id == "" {
		return
	}
	row, exists := rows[patch.id]
	if patch.insert {
		if exists {
			t.Fatalf("authority insert overwrote %q", patch.id)
		}
		row = authorityRow{}
		rows[patch.id] = row
	} else if !exists {
		t.Fatalf("authority update missing %q", patch.id)
	}
	for column, value := range patch.cells {
		if !patch.insert {
			if _, ok := row[column]; !ok {
				t.Fatalf("authority update missing column %s", column)
			}
		}
		row[column] = authorityCell(value)
	}
}

func expectedAuthorityDelta(t *testing.T, cmd authorityCommand, before, after authorityState) authorityDelta {
	t.Helper()
	delta := authorityDelta{taskID: cmd.taskID}
	task := func(cells map[string]any) { delta.task = authorityPatch{id: cmd.taskID, cells: cells} }
	action := func(cells map[string]any) { delta.action = authorityPatch{id: cmd.actionID, cells: cells} }
	fact := func(kind, event string) { delta.fact = &authorityFact{kind, event} }
	switch cmd.op {
	case opRequestCancel:
		task(map[string]any{"status": TaskStatusCancelling, "cancel_requested_at": authorityAt})
		fact("lifecycle", "task.cancel_requested")
	case opCompleted:
		task(map[string]any{"status": TaskStatusCompleted, "result": "done", "error": "", "completed_at": authorityAt})
		fact("terminal", string(EventTaskCompleted))
	case opFailed:
		task(map[string]any{"status": TaskStatusFailed, "result": "", "error": "failed", "completed_at": authorityAt})
		fact("terminal", string(EventTaskFailed))
	case opFailedCrash:
		task(map[string]any{"status": TaskStatusFailedCrash, "result": "", "error": "crash", "completed_at": authorityAt})
		fact("terminal", string(EventTaskFailedCrash))
		if _, owned := before.actions[cmd.actionID]; owned {
			status := after.actions[cmd.actionID]["status"]
			switch status {
			case "", "pending", "responding", "answered", "approved", "declined":
				t.Fatalf("crash left action actionable or invented resolution: %q", status)
			}
			action(map[string]any{"status": status})
		}
	case opCancelled:
		task(map[string]any{"status": TaskStatusCancelled, "completed_at": authorityAt.Add(time.Second)})
		fact("terminal", string(EventTaskCancelled))
	case opInput:
		task(map[string]any{"status": TaskStatusInputRequired})
		delta.action = authorityPatch{id: cmd.actionID, insert: true, cells: map[string]any{
			"id": cmd.actionID, "task_id": cmd.taskID, "kind": "input", "status": "pending",
			"provider_request_id": "provider-" + cmd.actionID, "connection_generation": cmd.generation,
			"request_json": `{"prompt":"continue?"}`, "response_json": nil, "delivery_json": nil,
			"expires_at": authorityAt.Add(time.Hour), "created_at": authorityAt, "responded_at": nil, "resolved_at": nil,
		}}
		fact("lifecycle", "task.input_required")
	case opBegin:
		action(map[string]any{"status": "responding", "response_json": `{"answer":"yes"}`, "responded_at": authorityAt.Add(time.Second)})
	case opResolve:
		task(map[string]any{"status": cmd.next})
		action(map[string]any{"status": cmd.resolution, "delivery_json": `{"resolution":"` + cmd.resolution + `"}`, "resolved_at": authorityAt.Add(2 * time.Second)})
		fact("lifecycle", map[TaskStatus]string{TaskStatusRunning: string(EventTaskRunning), TaskStatusFailed: string(EventTaskFailed)}[cmd.next])
	default:
		t.Fatalf("missing authority delta for %s", cmd.op)
	}
	return delta
}

func assertAuthorityDelta(t *testing.T, before, got authorityState, delta authorityDelta) {
	t.Helper()
	want := authorityState{cloneAuthorityRows(before.tasks), cloneAuthorityRows(before.actions), cloneAuthorityRows(before.artifacts)}
	applyAuthorityPatch(t, want.tasks, delta.task)
	applyAuthorityPatch(t, want.actions, delta.action)
	newArtifacts := authorityRows{}
	for seq, row := range got.artifacts {
		if prior, ok := before.artifacts[seq]; ok {
			if !reflect.DeepEqual(prior, row) {
				t.Fatalf("artifact %s was rewritten", seq)
			}
			continue
		}
		newArtifacts[seq] = row
	}
	if delta.fact == nil {
		if len(newArtifacts) != 0 {
			t.Fatalf("unexpected artifacts: %#v", newArtifacts)
		}
	} else {
		if len(newArtifacts) != 1 {
			t.Fatalf("artifact delta=%d want=1", len(newArtifacts))
		}
		for seq, row := range newArtifacts {
			if row["task_id"] != delta.taskID || row["kind"] != delta.fact.kind || row["event_type"] != delta.fact.event {
				t.Fatalf("artifact fact=%#v want=%#v", row, delta.fact)
			}
			want.artifacts[seq] = row
		}
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("authority delta escaped allowed mask")
	}
}

func TestTaskAuthority_CommandContractMatrix(t *testing.T) {
	for _, tc := range authoritySuccessCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthorityFixture(t, "contract-"+tc.name, tc.start)
			runAuthorityOps(t, f, tc.setup...)
			before := readAuthorityState(t, f.store, f.id)
			cmd := bindAuthorityCommand(f, tc.command)
			t004Must(t, runAuthorityCommand(f, cmd))
			after := readAuthorityState(t, f.store, f.id)
			assertAuthorityDelta(t, before, after, expectedAuthorityDelta(t, cmd, before, after))
			rejectAuthority(t, f, ErrAuthorityConflict, cmd)
		})
	}
}

func seedAuthorityConflict(t *testing.T, f *authorityFixture, seed string) {
	t.Helper()
	switch seed {
	case "":
	case "duplicate-id":
		seedAuthorityTask(t, f.store, f.id+"-duplicate-owner", TaskStatusInputRequired)
		seedAuthorityAction(t, f.store, f.action, f.id+"-duplicate-owner", "duplicate-id-provider", 88, "pending")
	case "duplicate-correlation":
		seedAuthorityTask(t, f.store, f.id+"-duplicate-owner", TaskStatusInputRequired)
		seedAuthorityAction(t, f.store, f.action+"-duplicate", f.id+"-duplicate-owner", "provider-"+f.action, 7, "pending")
	case "task-running":
		_, err := f.store.db.Exec(`UPDATE tasks SET status='running' WHERE id=?`, f.id)
		t004Must(t, err)
	case "other-task":
		seedAuthorityTask(t, f.store, "other", TaskStatusInputRequired)
	default:
		t.Fatalf("unknown conflict seed %q", seed)
	}
}

func TestTaskAuthority_ConflictMatrix(t *testing.T) {
	if len(authorityRejectCases) != 17 {
		t.Fatalf("conflict rows=%d want=17", len(authorityRejectCases))
	}
	for _, tc := range authorityRejectCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthorityFixture(t, "reject-"+tc.name, tc.start)
			runAuthorityOps(t, f, tc.setup...)
			seedAuthorityConflict(t, f, tc.seed)
			rejectAuthority(t, f, tc.wantErr, bindAuthorityCommand(f, tc.command))
		})
	}
}

func armAuthorityRollback(t *testing.T, f *authorityFixture, table, predicate string) {
	t.Helper()
	verb := "INSERT"
	if table == "pending_actions" {
		verb = "UPDATE"
	}
	_, err := f.store.db.Exec(fmt.Sprintf(`CREATE TRIGGER t004_abort AFTER %s ON %s WHEN NEW.task_id='%s' BEGIN SELECT CASE WHEN %s THEN RAISE(ABORT,'T004_ROLLBACK') ELSE RAISE(ABORT,'T004_PRECONDITION') END; END`, verb, table, f.id, predicate))
	t004Must(t, err)
}

func exerciseAuthorityRollback(t *testing.T, f *authorityFixture, table, predicate string, cmd authorityCommand) authorityState {
	t.Helper()
	before := readAuthorityState(t, f.store, f.id)
	armAuthorityRollback(t, f, table, predicate)
	err := runAuthorityCommand(f, cmd)
	if err == nil || strings.Count(err.Error(), "T004_ROLLBACK") != 1 {
		t.Fatalf("sentinel error=%v", err)
	}
	assertAuthorityDelta(t, before, readAuthorityState(t, f.store, f.id), authorityDelta{})
	_, err = f.store.db.Exec(`DROP TRIGGER t004_abort`)
	t004Must(t, err)
	t004Must(t, runAuthorityCommand(f, cmd))
	after := readAuthorityState(t, f.store, f.id)
	assertAuthorityDelta(t, before, after, expectedAuthorityDelta(t, cmd, before, after))
	return after
}

func TestTaskAuthority_AtomicRollbackMatrix(t *testing.T) {
	rows := []struct {
		name, table, predicate string
		setup                  []authorityOp
		command                authorityCommand
	}{
		{"completed", "task_artifacts", `(SELECT status FROM tasks WHERE id=NEW.task_id)='completed' AND (SELECT result FROM tasks WHERE id=NEW.task_id)='done' AND (SELECT completed_at FROM tasks WHERE id=NEW.task_id) IS NOT NULL`, nil, authorityTemplate(opCompleted)},
		{"begin", "pending_actions", `NEW.status='responding' AND NEW.response_json<>'' AND NEW.responded_at IS NOT NULL AND (SELECT status FROM tasks WHERE id=NEW.task_id)='input_required'`, []authorityOp{opInput}, authorityTemplate(opBegin)},
		{"resolve-answered", "task_artifacts", `(SELECT status FROM tasks WHERE id=NEW.task_id)='running' AND (SELECT status FROM pending_actions WHERE task_id=NEW.task_id)='answered'`, []authorityOp{opInput, opBegin}, authorityTemplate(opResolve)},
		{"resolve-delivery-unknown", "task_artifacts", `(SELECT status FROM tasks WHERE id=NEW.task_id)='failed' AND (SELECT status FROM pending_actions WHERE task_id=NEW.task_id)='delivery_unknown'`, []authorityOp{opInput, opBegin}, authorityResolution("delivery_unknown", TaskStatusFailed)},
		{"crash-input", "task_artifacts", `(SELECT status FROM tasks WHERE id=NEW.task_id)='failed_crash' AND (SELECT status FROM pending_actions WHERE task_id=NEW.task_id) NOT IN ('pending','responding')`, []authorityOp{opInput}, authorityCommandFor(opFailedCrash, "", TaskStatusInputRequired, "", 0)},
	}
	for _, row := range rows {
		row := row
		t.Run(row.name, func(t *testing.T) {
			f := newAuthorityFixture(t, "rollback-"+row.name, TaskStatusRunning)
			runAuthorityOps(t, f, row.setup...)
			exerciseAuthorityRollback(t, f, row.table, row.predicate, bindAuthorityCommand(f, row.command))
		})
	}
}

func TestTaskAuthority_InputRequiredTriggerRollback(t *testing.T) {
	f := newAuthorityFixture(t, "rollback-input", TaskStatusRunning)
	predicate := `(SELECT status FROM tasks WHERE id=NEW.task_id)='input_required' AND EXISTS(SELECT 1 FROM pending_actions WHERE id='` + f.action + `' AND task_id=NEW.task_id AND status='pending')`
	exerciseAuthorityRollback(t, f, "task_artifacts", predicate, bindAuthorityCommand(f, authorityTemplate(opInput)))
}

type authorityAttempt struct {
	op  authorityOp
	run func() error
}

type authorityRaceResult struct {
	op  authorityOp
	err error
}

func runAuthorityRace(attempts ...authorityAttempt) map[authorityOp]error {
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(len(attempts))
	out := make(chan authorityRaceResult, len(attempts))
	for _, attempt := range attempts {
		attempt := attempt
		go func() {
			ready.Done()
			<-start
			out <- authorityRaceResult{attempt.op, attempt.run()}
		}()
	}
	ready.Wait()
	close(start)
	results := map[authorityOp]error{}
	for range attempts {
		result := <-out
		results[result.op] = result.err
	}
	return results
}

func authorityRaceWinner(t *testing.T, results map[authorityOp]error) authorityOp {
	t.Helper()
	var winner authorityOp
	for op, err := range results {
		if err == nil {
			if winner != "" {
				t.Fatalf("multiple winners: %v", results)
			}
			winner = op
			continue
		}
		lower := strings.ToLower(err.Error())
		if !errors.Is(err, ErrAuthorityConflict) || strings.Contains(lower, "sqlite_busy") || strings.Contains(lower, "sqlite_locked") || strings.Contains(lower, "database is locked") {
			t.Fatalf("loser %s error=%v", op, err)
		}
	}
	if winner == "" {
		t.Fatalf("no winner: %v", results)
	}
	return winner
}

func TestTaskAuthority_LinearizationRaces(t *testing.T) {
	rows := []struct {
		name          string
		left, right   authorityOp
		rightExpected TaskStatus
	}{
		{"cancel-complete", opRequestCancel, opCompleted, ""},
		{"complete-crash", opCompleted, opFailedCrash, TaskStatusRunning},
		{"input-complete", opInput, opCompleted, ""},
	}
	for _, row := range rows {
		row := row
		t.Run(row.name, func(t *testing.T) {
			a, b := authorityPair(t)
			id := "race-" + row.name
			seedAuthorityTask(t, a, id, TaskStatusRunning)
			seedAuthorityTask(t, a, id+"-canary", TaskStatusRunning)
			seedAuthorityAction(t, a, id+"-canary-action", id+"-canary", "race-canary-"+id, 91, "answered")
			fa := &authorityFixture{authority: a, id: id, action: id + "-action"}
			fb := &authorityFixture{authority: b, id: id, action: id + "-action"}
			left, right := bindAuthorityCommand(fa, authorityTemplate(row.left)), bindAuthorityCommand(fb, authorityTemplate(row.right))
			if row.rightExpected != "" {
				right.expectedTask = row.rightExpected
			}
			before := readAuthorityState(t, a, id)
			winner := authorityRaceWinner(t, runAuthorityRace(
				authorityAttempt{row.left, func() error { return runAuthorityCommand(fa, left) }},
				authorityAttempt{row.right, func() error { return runAuthorityCommand(fb, right) }},
			))
			winnerCmd := map[authorityOp]authorityCommand{row.left: left, row.right: right}[winner]
			after := readAuthorityState(t, a, id)
			assertAuthorityDelta(t, before, after, expectedAuthorityDelta(t, winnerCmd, before, after))
			if winner == opRequestCancel {
				f := &authorityFixture{store: a, authority: a, id: id, action: id + "-action"}
				cmd := bindAuthorityCommand(f, authorityTemplate(opCancelled))
				t004Must(t, runAuthorityCommand(f, cmd))
				final := readAuthorityState(t, a, id)
				assertAuthorityDelta(t, after, final, expectedAuthorityDelta(t, cmd, after, final))
			}
		})
	}
}

func assertStructuredStopEvidence(t *testing.T, state authorityState) {
	t.Helper()
	raw := ""
	for _, artifact := range state.artifacts {
		if artifact["kind"] == "terminal" && artifact["event_type"] == string(EventTaskCancelled) {
			raw = artifact["payload_json"]
		}
	}
	var payload struct {
		StopEvidence struct {
			NativeAcknowledged bool      `json:"native_acknowledged"`
			ObservedAt         time.Time `json:"observed_at"`
		} `json:"stop_evidence"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || !payload.StopEvidence.NativeAcknowledged || !payload.StopEvidence.ObservedAt.Equal(authorityAt) {
		t.Fatalf("stop evidence=%s err=%v", raw, err)
	}
}

func TestTaskAuthority_ValidationEdges(t *testing.T) {
	f := newAuthorityFixture(t, "stop-evidence", TaskStatusRunning)
	runAuthorityOps(t, f, opRequestCancel)
	invalidEvidence := []struct {
		name  string
		value StopEvidence
	}{{"empty", StopEvidence{}}, {"ack-only", StopEvidence{NativeAcknowledged: true}}, {"observed-only", StopEvidence{ObservedAt: authorityAt}}}
	for _, invalid := range invalidEvidence {
		invalid := invalid
		t.Run("insufficient-"+invalid.name, func(t *testing.T) {
			cmd := bindAuthorityCommand(f, authorityTemplate(opCancelled))
			cmd.evidence = invalid.value
			rejectAuthority(t, f, nil, cmd)
		})
	}
	cmd := bindAuthorityCommand(f, authorityTemplate(opCancelled))
	before := readAuthorityState(t, f.store, f.id)
	t004Must(t, runAuthorityCommand(f, cmd))
	after := readAuthorityState(t, f.store, f.id)
	assertAuthorityDelta(t, before, after, expectedAuthorityDelta(t, cmd, before, after))
	assertStructuredStopEvidence(t, after)

	rows := []struct {
		status, start TaskStatus
		setup         []authorityOp
		allowed       bool
		action        bool
	}{
		{TaskStatusDispatched, TaskStatusDispatched, nil, true, false}, {TaskStatusRunning, TaskStatusRunning, nil, true, false},
		{TaskStatusRetrying, TaskStatusRetrying, nil, true, false}, {TaskStatusCancelling, TaskStatusRunning, []authorityOp{opRequestCancel}, true, false},
		{TaskStatusInputRequired, TaskStatusRunning, []authorityOp{opInput}, true, true},
		{TaskStatusPending, TaskStatusPending, nil, false, false}, {TaskStatusCompleted, TaskStatusCompleted, nil, false, false},
		{TaskStatusFailed, TaskStatusFailed, nil, false, false}, {TaskStatusFailedCrash, TaskStatusFailedCrash, nil, false, false},
		{TaskStatusCancelled, TaskStatusCancelled, nil, false, false},
	}
	for _, row := range rows {
		row := row
		t.Run("crash-"+string(row.status), func(t *testing.T) {
			f := newAuthorityFixture(t, "crash-"+string(row.status), row.start)
			runAuthorityOps(t, f, row.setup...)
			cmd := bindAuthorityCommand(f, authorityCommandFor(opFailedCrash, "", row.status, "", 0))
			if !row.allowed {
				rejectAuthority(t, f, nil, cmd)
				return
			}
			before := readAuthorityState(t, f.store, f.id)
			t004Must(t, runAuthorityCommand(f, cmd))
			after := readAuthorityState(t, f.store, f.id)
			assertAuthorityDelta(t, before, after, expectedAuthorityDelta(t, cmd, before, after))
		})
	}
}
