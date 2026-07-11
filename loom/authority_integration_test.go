package loom

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom/deps"
	_ "modernc.org/sqlite"
)

const t013Wait = 3 * time.Second

var t013At = time.Date(2026, 7, 11, 9, 30, 0, 123456000, time.UTC)

type t013Fixture struct {
	db       *sql.DB
	observer *sql.DB
	store    *TaskStore
	view     *TaskStore
	dsn      string
	name     string
}

func t013NewFixture(t *testing.T) *t013Fixture {
	t.Helper()
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "loom.db"))
	dsn := "file:" + path + "?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	store, err := NewTaskStore(db, "t013")
	if err != nil {
		t.Fatal(err)
	}
	observer, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	observer.SetMaxOpenConns(2)
	if err := observer.Ping(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = observer.Close()
		_ = db.Close()
	})
	return &t013Fixture{
		db: db, observer: observer, store: store,
		view: &TaskStore{db: observer, engineName: "t013"},
		dsn:  dsn, name: "t013",
	}
}

func (f *t013Fixture) engine(t *testing.T, worker Worker, opts ...Option) *LoomEngine {
	t.Helper()
	base := []Option{WithClock(deps.NewFakeClock(t013At)), WithIDGenerator(deps.NewSequentialIDGenerator())}
	engine := New(f.store, append(base, opts...)...)
	if worker != nil {
		engine.RegisterWorker(WorkerTypeCLI, worker)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), t013Wait)
		defer cancel()
		if err := engine.Close(ctx); err != nil {
			t.Errorf("bounded engine cleanup: %v", err)
		}
	})
	return engine
}

type t013Gate struct {
	ch   chan struct{}
	once sync.Once
}

func t013NewGate() *t013Gate { return &t013Gate{ch: make(chan struct{})} }
func (g *t013Gate) open()    { g.once.Do(func() { close(g.ch) }) }

type t013Step struct {
	started      *t013Gate
	release      *t013Gate
	ignoreCancel bool
	content      string
	err          error
	panicValue   any
	watch        *t013SignalObserver
}

type t013SignalObserver struct {
	view     *TaskStore
	recorder *t013ObservedRecorder
	ack      *t013Gate
	abort    *t013Gate
}

func (o *t013SignalObserver) start(ctx context.Context, taskID string) {
	if o == nil {
		return
	}
	go func() {
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			task, err := o.view.Get(taskID)
			var artifacts []TaskArtifact
			if err == nil {
				page, listErr := o.view.ListArtifacts(taskID, TaskArtifactListOptions{Limit: 100})
				if listErr != nil {
					err = listErr
				} else {
					artifacts = page.Items
				}
			}
			o.recorder.record(t013Observed{event: TaskEvent{TaskID: taskID}, task: task, artifacts: artifacts, err: err})
			o.ack.open()
		case <-o.abort.ch:
		case <-timer.C:
			o.recorder.record(t013Observed{event: TaskEvent{TaskID: taskID}, err: errors.New("t013 signal observer safety timeout")})
			o.ack.open()
		}
	}()
}

type t013ScriptedWorker struct {
	mu    sync.Mutex
	call  int
	steps []t013Step
}

func (w *t013ScriptedWorker) Execute(ctx context.Context, task *Task) (*WorkerResult, error) {
	w.mu.Lock()
	index := w.call
	w.call++
	if index >= len(w.steps) {
		w.mu.Unlock()
		return nil, fmt.Errorf("unexpected worker call %d", index)
	}
	step := w.steps[index]
	w.mu.Unlock()
	if step.started != nil {
		step.started.open()
	}
	if step.watch != nil {
		step.watch.start(ctx, task.ID)
	}
	if step.release != nil {
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		if step.ignoreCancel {
			select {
			case <-step.release.ch:
			case <-timer.C:
				return nil, errors.New("t013 scripted worker safety timeout")
			}
		} else {
			select {
			case <-step.release.ch:
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				return nil, errors.New("t013 scripted worker safety timeout")
			}
		}
	}
	if step.panicValue != nil {
		panic(step.panicValue)
	}
	if step.err != nil {
		return nil, step.err
	}
	return &WorkerResult{Content: step.content}, nil
}

func (*t013ScriptedWorker) Type() WorkerType { return WorkerTypeCLI }

func t013AwaitGate(t *testing.T, label string, gate *t013Gate) {
	t.Helper()
	timer := time.NewTimer(t013Wait)
	defer timer.Stop()
	select {
	case <-gate.ch:
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", label)
	}
}

func t013CloseOrStart(t *testing.T, engine *LoomEngine, started *t013Gate) (startedFirst bool, done <-chan error) {
	t.Helper()
	closeDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	go func() {
		defer cancel()
		closeDone <- engine.Close(ctx)
	}()
	timer := time.NewTimer(t013Wait)
	defer timer.Stop()
	select {
	case <-started.ch:
		return true, closeDone
	case err := <-closeDone:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
		return false, closeDone
	case <-timer.C:
		t.Fatal("neither worker start nor engine close completed")
		return false, closeDone
	}
}

func t013FinishClose(t *testing.T, done <-chan error) {
	t.Helper()
	timer := time.NewTimer(t013Wait)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-timer.C:
		t.Fatal("engine did not close after worker release")
	}
}

func t013Close(t *testing.T, engine *LoomEngine) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := engine.Close(ctx); err != nil {
		t.Fatalf("bounded Close: %v", err)
	}
}

type t013Events struct {
	mu       sync.Mutex
	events   []TaskEvent
	overflow bool
}

func (r *t013Events) record(event TaskEvent) {
	r.mu.Lock()
	if len(r.events) == 256 {
		r.overflow = true
	} else if !r.overflow {
		r.events = append(r.events, event)
	}
	r.mu.Unlock()
}

func (r *t013Events) snapshot(t *testing.T) []TaskEvent {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.overflow {
		t.Fatal("t013 event recorder overflow")
	}
	return append([]TaskEvent(nil), r.events...)
}

func (r *t013Events) count(taskID string, eventType EventType) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, event := range r.events {
		if event.TaskID == taskID && event.Type == eventType {
			count++
		}
	}
	return count
}

type t013ObservedRecorder struct {
	mu       sync.Mutex
	items    []t013Observed
	overflow bool
}

func (r *t013ObservedRecorder) record(observed t013Observed) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.items) == 64 {
		r.overflow = true
		return
	}
	r.items = append(r.items, observed)
}

func (r *t013ObservedRecorder) snapshot(t *testing.T) []t013Observed {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.overflow {
		t.Fatal("t013 observation recorder overflow")
	}
	return append([]t013Observed(nil), r.items...)
}

type t013SignalCheckpoint struct {
	taskID                string
	cancelRequestedEvents int
	ackTimedOut           bool
}

type t013SignalProbe struct {
	mu     sync.Mutex
	points []t013SignalCheckpoint
}

func (p *t013SignalProbe) record(point t013SignalCheckpoint) {
	p.mu.Lock()
	p.points = append(p.points, point)
	p.mu.Unlock()
}

func (p *t013SignalProbe) snapshot() []t013SignalCheckpoint {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]t013SignalCheckpoint(nil), p.points...)
}

func t013WrapCancel(t *testing.T, engine *LoomEngine, taskID string, events *t013Events, ack *t013Gate, probe *t013SignalProbe) {
	t.Helper()
	engine.mu.Lock()
	original, ok := engine.cancels[taskID]
	if !ok {
		engine.mu.Unlock()
		t.Fatalf("missing live cancel func for %s", taskID)
	}
	var restored atomic.Bool
	engine.cancels[taskID] = func() {
		point := t013SignalCheckpoint{taskID: taskID, cancelRequestedEvents: events.count(taskID, EventType("task.cancel_requested"))}
		original()
		timer := time.NewTimer(t013Wait)
		select {
		case <-ack.ch:
		case <-timer.C:
			point.ackTimedOut = true
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		probe.record(point)
	}
	engine.mu.Unlock()
	t.Cleanup(func() {
		if restored.CompareAndSwap(false, true) {
			engine.mu.Lock()
			if _, exists := engine.cancels[taskID]; exists {
				engine.cancels[taskID] = original
			}
			engine.mu.Unlock()
		}
	})
}

func t013Artifacts(t *testing.T, view *TaskStore, taskID string) []TaskArtifact {
	t.Helper()
	page, err := view.ListArtifacts(taskID, TaskArtifactListOptions{Limit: 100})
	if err != nil {
		t.Fatalf("ListArtifacts(%s): %v", taskID, err)
	}
	return page.Items
}

func t013ArtifactByEvent(t *testing.T, view *TaskStore, taskID, eventType string) TaskArtifact {
	t.Helper()
	var matches []TaskArtifact
	for _, artifact := range t013Artifacts(t, view, taskID) {
		if artifact.EventType == eventType {
			matches = append(matches, artifact)
		}
	}
	if len(matches) != 1 {
		t.Errorf("%s artifacts for %s=%d, want 1", eventType, taskID, len(matches))
		if len(matches) == 0 {
			return TaskArtifact{}
		}
	}
	return matches[0]
}

func t013ArtifactCountByEvent(t *testing.T, view *TaskStore, taskID, eventType string) int {
	t.Helper()
	count := 0
	for _, artifact := range t013Artifacts(t, view, taskID) {
		if artifact.EventType == eventType {
			count++
		}
	}
	return count
}

func t013CountEventInArtifacts(artifacts []TaskArtifact, eventType string) int {
	count := 0
	for _, artifact := range artifacts {
		if artifact.EventType == eventType {
			count++
		}
	}
	return count
}

func t013AssertExactArtifact(t *testing.T, artifact TaskArtifact, kind TaskArtifactKind, event string, payload map[string]any, at time.Time) {
	t.Helper()
	wantJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(artifact.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Kind != kind || artifact.EventType != event || artifact.Channel != "" || artifact.Summary != event {
		t.Errorf("artifact identity=%s/%s/%q/%q, want %s/%s/empty/%q", artifact.Kind, artifact.EventType, artifact.Channel, artifact.Summary, kind, event, event)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("%s payload=%s, want %s", event, gotJSON, wantJSON)
	}
	if artifact.ContentLength != int64(len(wantJSON)) || artifact.Redacted || artifact.Truncated {
		t.Errorf("%s length/redacted/truncated=%d/%v/%v, want %d/false/false", event, artifact.ContentLength, artifact.Redacted, artifact.Truncated, len(wantJSON))
	}
	if !artifact.CreatedAt.Equal(at.UTC()) {
		t.Errorf("%s created_at=%s, want %s", event, artifact.CreatedAt.Format(time.RFC3339Nano), at.UTC().Format(time.RFC3339Nano))
	}
}

func t013SeedAction(t *testing.T, db *sql.DB, id, taskID string) {
	t.Helper()
	if err := t013InsertAction(db, id, taskID); err != nil {
		t.Fatalf("seed action %s: %v", id, err)
	}
}

func t013InsertAction(db *sql.DB, id, taskID string) error {
	_, err := db.Exec(`INSERT INTO pending_actions
		(id,task_id,kind,status,provider_request_id,connection_generation,request_json,expires_at,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, id, taskID, "input", "pending", "provider-"+id, 1,
		`{"prompt":"seed"}`, t013At.Add(time.Hour), t013At.Add(-time.Minute))
	return err
}

func t013Action(t *testing.T, db *sql.DB, id string) (status string, resolved *time.Time) {
	t.Helper()
	var value sql.NullTime
	if err := db.QueryRow(`SELECT status,resolved_at FROM pending_actions WHERE id=?`, id).Scan(&status, &value); err != nil {
		t.Fatalf("read action %s: %v", id, err)
	}
	if value.Valid {
		at := value.Time.UTC()
		resolved = &at
	}
	return status, resolved
}

func t013EventTypes(events []TaskEvent, taskID string) []EventType {
	result := make([]EventType, 0, len(events))
	for _, event := range events {
		if event.TaskID == taskID {
			result = append(result, event.Type)
		}
	}
	return result
}

func t013AssertTypes(t *testing.T, got []EventType, want ...EventType) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("event types=%v, want %v", got, want)
	}
}

func t013AssertEventTimes(t *testing.T, events []TaskEvent, taskID string, want time.Time) {
	t.Helper()
	for _, event := range events {
		if event.TaskID == taskID && !event.Timestamp.Equal(want.UTC()) {
			t.Errorf("%s timestamp=%s, want %s", event.Type, event.Timestamp.Format(time.RFC3339Nano), want.UTC().Format(time.RFC3339Nano))
		}
	}
}

func t013AssertExactTaskEvent(t *testing.T, events []TaskEvent, want TaskEvent) {
	t.Helper()
	var matches []TaskEvent
	for _, event := range events {
		if event.TaskID == want.TaskID && event.Type == want.Type {
			matches = append(matches, event)
		}
	}
	if len(matches) != 1 {
		t.Errorf("%s/%s event cardinality=%d, want 1", want.TaskID, want.Type, len(matches))
		return
	}
	got := matches[0]
	if got.Type != want.Type || got.TaskID != want.TaskID || got.ProjectID != want.ProjectID || got.RequestID != want.RequestID || got.Status != want.Status || !got.Timestamp.Equal(want.Timestamp.UTC()) {
		t.Errorf("event=%#v, want exact compatibility fields %#v", got, want)
	}
}

func TestTaskAuthorityIntegration_CreateAndCreatedFactAreAtomicAcrossRestart(t *testing.T) {
	fixture := t013NewFixture(t)
	_, err := fixture.db.Exec(`CREATE TRIGGER t013_abort_created BEFORE INSERT ON task_artifacts
		WHEN NEW.task_id='id-0' AND NEW.event_type='task.created'
		BEGIN SELECT RAISE(ABORT,'T013_CREATED_ARTIFACT_ABORT'); END`)
	if err != nil {
		t.Fatal(err)
	}
	engine := fixture.engine(t, nil)
	recorded := &t013Events{}
	engine.Events().Subscribe(recorded.record)

	id, submitErr := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "atomic-create", Prompt: "create"})
	if submitErr == nil || !strings.Contains(submitErr.Error(), "T013_CREATED_ARTIFACT_ABORT") {
		t.Errorf("Submit error=%v, want created-artifact abort", submitErr)
	}
	if id != "" {
		t.Errorf("Submit task id=%q, want empty on atomic create abort", id)
	}
	if task, getErr := fixture.view.Get("id-0"); !isNoRows(getErr) || task != nil {
		t.Errorf("durable task after abort=%#v err=%v, want absent", task, getErr)
	}
	if artifacts := t013Artifacts(t, fixture.view, "id-0"); len(artifacts) != 0 {
		t.Errorf("artifacts after create abort=%d, want 0", len(artifacts))
	}
	if events := recorded.snapshot(t); len(events) != 0 {
		t.Errorf("events after create abort=%v, want none", t013EventTypes(events, "id-0"))
	}

	restartDB, openErr := sql.Open("sqlite", fixture.dsn)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer restartDB.Close()
	restarted, openErr := NewTaskStore(restartDB, fixture.name)
	if openErr != nil {
		t.Fatal(openErr)
	}
	if task, getErr := restarted.Get("id-0"); !isNoRows(getErr) || task != nil {
		t.Errorf("restarted store task=%#v err=%v, want absent", task, getErr)
	}
}

func TestTaskAuthorityIntegration_InitialDispatchArtifactAbortRollsBackAndEmitsNothing(t *testing.T) {
	fixture := t013NewFixture(t)
	_, err := fixture.db.Exec(`CREATE TRIGGER t013_abort_initial_dispatch BEFORE INSERT ON task_artifacts
		WHEN NEW.task_id='id-0' AND NEW.event_type='task.dispatched'
		BEGIN SELECT RAISE(ABORT,'T013_INITIAL_DISPATCH_ABORT'); END`)
	if err != nil {
		t.Fatal(err)
	}
	started, release := t013NewGate(), t013NewGate()
	worker := &t013ScriptedWorker{steps: []t013Step{{started: started, release: release, ignoreCancel: true, content: "ok"}}}
	engine := fixture.engine(t, worker)
	t.Cleanup(release.open)
	recorded := &t013Events{}
	engine.Events().Subscribe(recorded.record)

	id, submitErr := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "initial-abort", Prompt: "dispatch"})
	if submitErr == nil || !strings.Contains(submitErr.Error(), "T013_INITIAL_DISPATCH_ABORT") {
		t.Errorf("Submit error=%v, want dispatch-artifact abort", submitErr)
	}
	if id != "" {
		t.Errorf("Submit task id=%q, want empty on dispatch abort", id)
	}
	workerStarted, closeDone := t013CloseOrStart(t, engine, started)
	if workerStarted {
		t.Error("worker started even though durable dispatch fact aborted")
	}
	task, getErr := fixture.view.Get("id-0")
	if getErr != nil {
		t.Fatalf("Get(id-0): %v", getErr)
	}
	if task.Status != TaskStatusPending || task.DispatchedAt != nil {
		t.Errorf("task status/dispatched_at=%s/%v, want pending/nil", task.Status, task.DispatchedAt)
	}
	t013AssertExactArtifact(t, t013ArtifactByEvent(t, fixture.view, "id-0", "task.created"), TaskArtifactKindLifecycle, "task.created", map[string]any{
		"status": "pending", "closed_action_count": int64(0),
	}, t013At)
	if count := t013ArtifactCountByEvent(t, fixture.view, "id-0", "task.dispatched"); count != 0 {
		t.Errorf("dispatch artifacts survived abort: count=%d", count)
	}
	t013AssertTypes(t, t013EventTypes(recorded.snapshot(t), "id-0"), EventTaskCreated)
	if !workerStarted {
		// Close already completed and consumed its result.
		_ = closeDone
	} else {
		release.open()
		t013FinishClose(t, closeDone)
	}
}

func TestTaskAuthorityIntegration_RetryDispatchArtifactAbortRollsBackAndEmitsNothing(t *testing.T) {
	fixture := t013NewFixture(t)
	_, err := fixture.db.Exec(`CREATE TRIGGER t013_abort_retry_dispatch BEFORE INSERT ON task_artifacts
		WHEN NEW.task_id='id-0' AND NEW.event_type='task.dispatched'
		AND EXISTS(SELECT 1 FROM task_artifacts WHERE task_id=NEW.task_id AND event_type='task.dispatched')
		BEGIN SELECT RAISE(ABORT,'T013_RETRY_DISPATCH_ABORT'); END`)
	if err != nil {
		t.Fatal(err)
	}
	firstStarted, firstRelease := t013NewGate(), t013NewGate()
	secondStarted, secondRelease := t013NewGate(), t013NewGate()
	worker := &t013ScriptedWorker{steps: []t013Step{
		{started: firstStarted, release: firstRelease, ignoreCancel: true, content: ""},
		{started: secondStarted, release: secondRelease, ignoreCancel: true, content: "ok"},
	}}
	engine := fixture.engine(t, worker, WithMaxRetries(1))
	t.Cleanup(firstRelease.open)
	t.Cleanup(secondRelease.open)
	recorded := &t013Events{}
	engine.Events().Subscribe(recorded.record)

	id, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "retry-abort", Prompt: "retry"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	t013AwaitGate(t, "first worker", firstStarted)
	firstRelease.open()
	secondRan, closeDone := t013CloseOrStart(t, engine, secondStarted)
	if secondRan {
		t.Error("retry worker started even though retry dispatch fact aborted")
	}
	task, err := fixture.view.Get(id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	if task.Status != TaskStatusRetrying || task.Retries != 1 {
		t.Errorf("task status/retries=%s/%d, want retrying/1", task.Status, task.Retries)
	}
	if got := len(t013Artifacts(t, fixture.view, id)); got != 4 {
		t.Errorf("artifact count=%d, want created+initial dispatched+running+retrying", got)
	}
	t013AssertTypes(t, t013EventTypes(recorded.snapshot(t), id), EventTaskCreated, EventTaskDispatched, EventTaskRunning, EventTaskRetrying)
	if secondRan {
		secondRelease.open()
		t013FinishClose(t, closeDone)
	}
}

func TestTaskAuthorityIntegration_LiveDispatchCancelOrders(t *testing.T) {
	t.Run("pending/cancel-first", func(t *testing.T) {
		fixture := t013NewFixture(t)
		started, release := t013NewGate(), t013NewGate()
		meter := newRecordingMeter()
		engine := fixture.engine(t, &t013ScriptedWorker{steps: []t013Step{{started: started, release: release, ignoreCancel: true, content: "late"}}}, WithMeter(meter))
		t.Cleanup(release.open)
		events := &t013Events{}
		observed := &t013ObservedRecorder{}
		engine.Events().Subscribe(events.record)
		var callbackMu sync.Mutex
		var cancelErr error
		cancelCalls := 0
		engine.Events().Subscribe(func(event TaskEvent) {
			if event.Type == EventTaskCreated {
				err := engine.Cancel(event.TaskID)
				task, observeErr := fixture.view.Get(event.TaskID)
				page, listErr := fixture.view.ListArtifacts(event.TaskID, TaskArtifactListOptions{Limit: 100})
				if observeErr == nil {
					observeErr = listErr
				}
				observed.record(t013Observed{event: event, task: task, artifacts: page.Items, err: observeErr})
				callbackMu.Lock()
				cancelErr, cancelCalls = err, cancelCalls+1
				callbackMu.Unlock()
			}
		})
		_, _ = engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "pending-cancel-first", RequestID: "pending-cancel-first-request", Prompt: "race"})
		ran, done := t013CloseOrStart(t, engine, started)
		if ran {
			t.Error("worker started after cancel won pending→dispatched race")
			release.open()
			t013FinishClose(t, done)
		}
		callbackMu.Lock()
		gotCancelErr, gotCancelCalls := cancelErr, cancelCalls
		callbackMu.Unlock()
		if gotCancelCalls != 1 || gotCancelErr != nil {
			t.Errorf("Cancel callback calls/error=%d/%v, want 1/nil", gotCancelCalls, gotCancelErr)
		}
		snapshots := observed.snapshot(t)
		if len(snapshots) != 1 || snapshots[0].err != nil || snapshots[0].task == nil || snapshots[0].task.Status != TaskStatusCancelled || len(snapshots[0].artifacts) != 2 {
			t.Errorf("pending cancel observer=%#v, want one durable cancelled row with created+cancelled facts", snapshots)
		}
		task, err := fixture.view.Get("id-0")
		if err != nil || task.Status != TaskStatusCancelled {
			t.Errorf("task after cancel-first=%#v err=%v, want cancelled", task, err)
		}
		t013AssertExactArtifact(t, t013ArtifactByEvent(t, fixture.view, "id-0", "task.cancelled"), TaskArtifactKindTerminal, "task.cancelled", map[string]any{
			"status": "cancelled", "cancel_requested_at": t013At, "requires_stop": false, "closed_action_count": int64(0),
		}, t013At)
		if facts := len(t013Artifacts(t, fixture.view, "id-0")); facts != 2 {
			t.Errorf("pending cancel-first facts=%d, want created baseline + one cancelled fact", facts)
		}
		allEvents := events.snapshot(t)
		taskEvents := t013EventTypes(allEvents, "id-0")
		t013AssertTypes(t, taskEvents, EventTaskCreated, EventTaskCancelled)
		t013AssertExactTaskEvent(t, allEvents, TaskEvent{
			Type: EventTaskCancelled, TaskID: "id-0", ProjectID: "pending-cancel-first", RequestID: "pending-cancel-first-request", Status: TaskStatusCancelled, Timestamp: t013At,
		})
		t013AssertEventTimes(t, allEvents, "id-0", t013At)
		if got := meter.counterTotal("loom.tasks.cancelled"); got != 1 {
			t.Errorf("terminal pending cancellation metric delta=%d, want exactly 1", got)
		}
	})

	t.Run("pending/dispatch-first", func(t *testing.T) {
		fixture := t013NewFixture(t)
		started := t013NewGate()
		meter := newRecordingMeter()
		engine := fixture.engine(t, &t013ScriptedWorker{steps: []t013Step{{started: started, content: "late"}}}, WithMeter(meter))
		events := &t013Events{}
		observed := &t013ObservedRecorder{}
		engine.Events().Subscribe(events.record)
		var callbackMu sync.Mutex
		var cancelErr error
		cancelCalls := 0
		engine.Events().Subscribe(func(event TaskEvent) {
			if event.Type == EventTaskDispatched {
				err := engine.Cancel(event.TaskID)
				task, observeErr := fixture.view.Get(event.TaskID)
				page, listErr := fixture.view.ListArtifacts(event.TaskID, TaskArtifactListOptions{Limit: 100})
				if observeErr == nil {
					observeErr = listErr
				}
				observed.record(t013Observed{event: event, task: task, artifacts: page.Items, err: observeErr})
				callbackMu.Lock()
				cancelErr, cancelCalls = err, cancelCalls+1
				callbackMu.Unlock()
			}
		})
		id, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "pending-dispatch-first", RequestID: "pending-dispatch-first-request", Prompt: "race"})
		if err != nil {
			t.Errorf("Submit: %v", err)
		}
		t013Close(t, engine)
		callbackMu.Lock()
		gotCancelErr, gotCancelCalls := cancelErr, cancelCalls
		callbackMu.Unlock()
		if gotCancelCalls != 1 || gotCancelErr != nil {
			t.Errorf("Cancel callback calls/error=%d/%v, want 1/nil", gotCancelCalls, gotCancelErr)
		}
		snapshots := observed.snapshot(t)
		if len(snapshots) != 1 {
			t.Fatalf("dispatch-first observer cardinality=%d, want 1", len(snapshots))
		}
		observation := snapshots[0]
		if observation.task == nil || observation.task.Status != TaskStatusCancelling || observation.task.CancelRequestedAt == nil || !observation.task.CancelRequestedAt.Equal(t013At) {
			t.Errorf("composable dispatch/cancel snapshot=%#v, want cancelling at fixed time", observation.task)
		}
		cancelFacts := 0
		for _, artifact := range observation.artifacts {
			if artifact.EventType == "task.cancel_requested" {
				cancelFacts++
			}
		}
		if cancelFacts != 1 {
			t.Errorf("cancel-requested facts in dispatch/cancel snapshot=%d, want 1", cancelFacts)
		}
		if facts := len(observation.artifacts); facts != 3 {
			t.Errorf("pending dispatch-first facts=%d, want created baseline + dispatched + cancel-requested", facts)
		}
		if t013Signalled(started.ch) {
			t.Error("worker started after dispatched task durably entered cancelling")
		}
		task, getErr := fixture.view.Get(id)
		if getErr != nil || task.Status != TaskStatusFailedCrash {
			t.Errorf("task after unproven dispatch cancellation=%#v err=%v, want failed_crash", task, getErr)
		}
		allEvents := events.snapshot(t)
		if events.count(id, EventType("task.cancel_requested")) != 1 || events.count(id, EventTaskCancelled) != 0 {
			t.Errorf("dispatch-first cancel-requested/cancelled events=%d/%d, want 1/0", events.count(id, EventType("task.cancel_requested")), events.count(id, EventTaskCancelled))
		}
		t013AssertExactTaskEvent(t, allEvents, TaskEvent{
			Type: EventType("task.cancel_requested"), TaskID: id, ProjectID: "pending-dispatch-first", RequestID: "pending-dispatch-first-request", Status: TaskStatusCancelling, Timestamp: t013At,
		})
		t013AssertEventTimes(t, allEvents, id, t013At)
		if got := meter.counterTotal("loom.tasks.cancelled"); got != 0 {
			t.Errorf("active cancel intent incremented cancelled metric by %d", got)
		}
	})

	for _, tc := range []struct {
		name  string
		event EventType
	}{
		{name: "retry/cancel-first", event: EventTaskRetrying},
		{name: "retry/dispatch-first", event: EventType("task.dispatched")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := t013NewFixture(t)
			firstStarted, firstRelease := t013NewGate(), t013NewGate()
			secondStarted := t013NewGate()
			ack, abort := t013NewGate(), t013NewGate()
			observed := &t013ObservedRecorder{}
			worker := &t013ScriptedWorker{steps: []t013Step{
				{started: firstStarted, release: firstRelease, ignoreCancel: true, content: "", watch: &t013SignalObserver{view: fixture.view, recorder: observed, ack: ack, abort: abort}},
				{started: secondStarted, ignoreCancel: true, content: "late"},
			}}
			meter := newRecordingMeter()
			engine := fixture.engine(t, worker, WithMaxRetries(1), WithMeter(meter))
			t.Cleanup(firstRelease.open)
			t.Cleanup(abort.open)
			events := &t013Events{}
			engine.Events().Subscribe(events.record)
			var dispatchedCount atomic.Int32
			var callbackMu sync.Mutex
			var cancelErr error
			cancelCalls := 0
			callbackObserved := &t013ObservedRecorder{}
			engine.Events().Subscribe(func(event TaskEvent) {
				if event.Type == EventTaskDispatched {
					if dispatchedCount.Add(1) == 1 {
						return
					}
				}
				if event.Type == tc.event {
					err := engine.Cancel(event.TaskID)
					task, observeErr := fixture.view.Get(event.TaskID)
					page, listErr := fixture.view.ListArtifacts(event.TaskID, TaskArtifactListOptions{Limit: 100})
					if observeErr == nil {
						observeErr = listErr
					}
					callbackObserved.record(t013Observed{event: event, task: task, artifacts: page.Items, err: observeErr})
					callbackMu.Lock()
					cancelErr, cancelCalls = err, cancelCalls+1
					callbackMu.Unlock()
				}
			})
			requestID := strings.ReplaceAll(tc.name, "/", "-") + "-request"
			id, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: tc.name, RequestID: requestID, Prompt: "retry race"})
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			t013AwaitGate(t, "first retry worker", firstStarted)
			probe := &t013SignalProbe{}
			t013WrapCancel(t, engine, id, events, ack, probe)
			firstRelease.open()
			t013Close(t, engine)
			callbackMu.Lock()
			gotCancelErr, gotCancelCalls := cancelErr, cancelCalls
			callbackMu.Unlock()
			if gotCancelCalls != 1 || gotCancelErr != nil {
				t.Errorf("Cancel callback calls/error=%d/%v, want 1/nil at %s", gotCancelCalls, gotCancelErr, tc.event)
			}
			callbackSnapshots := callbackObserved.snapshot(t)
			if len(callbackSnapshots) != 1 {
				t.Fatalf("callback observations=%d, want 1", len(callbackSnapshots))
			}
			observation := callbackSnapshots[0]
			if observation.task == nil || observation.task.Status != TaskStatusCancelling || observation.task.CancelRequestedAt == nil || !observation.task.CancelRequestedAt.Equal(t013At) {
				t.Errorf("composable retry/cancel snapshot=%#v, want cancelling at fixed time", observation.task)
			}
			cancelFacts := 0
			for _, artifact := range observation.artifacts {
				if artifact.EventType == "task.cancel_requested" {
					cancelFacts++
				}
			}
			if cancelFacts != 1 {
				t.Errorf("cancel-requested facts in retry/cancel snapshot=%d, want 1", cancelFacts)
			}
			wantFacts := 5 // created, initial dispatched/running, retrying, cancel-requested
			if tc.event == EventTaskDispatched {
				wantFacts++ // post-retry CommitDispatched fact precedes cancel
			}
			if facts := len(observation.artifacts); facts != wantFacts {
				t.Errorf("%s facts=%d, want exact composable snapshot %d", tc.name, facts, wantFacts)
			}
			if t013Signalled(secondStarted.ch) {
				t.Error("second worker started after retry cancellation won")
			}
			signalSnapshots := observed.snapshot(t)
			if len(signalSnapshots) != 1 || signalSnapshots[0].err != nil || signalSnapshots[0].task == nil || signalSnapshots[0].task.Status != TaskStatusCancelling || t013CountEventInArtifacts(signalSnapshots[0].artifacts, "task.cancel_requested") != 1 {
				t.Errorf("signal-time durable observer=%#v, want cancelling + one committed cancel-requested fact", signalSnapshots)
			}
			points := probe.snapshot()
			if len(points) != 1 || points[0].taskID != id || points[0].cancelRequestedEvents != 1 || points[0].ackTimedOut {
				t.Errorf("signal checkpoints=%#v, want one projected event before signal and ACK", points)
			}
			task, getErr := fixture.view.Get(id)
			if getErr != nil || task.Status != TaskStatusFailedCrash {
				t.Errorf("retry race task=%#v err=%v, want failed_crash after unproven stop", task, getErr)
			}
			if events.count(id, EventType("task.cancel_requested")) != 1 || events.count(id, EventTaskCancelled) != 0 {
				t.Errorf("retry cancel-requested/cancelled events=%d/%d, want 1/0", events.count(id, EventType("task.cancel_requested")), events.count(id, EventTaskCancelled))
			}
			allEvents := events.snapshot(t)
			t013AssertExactTaskEvent(t, allEvents, TaskEvent{
				Type: EventType("task.cancel_requested"), TaskID: id, ProjectID: tc.name, RequestID: requestID, Status: TaskStatusCancelling, Timestamp: t013At,
			})
			t013AssertEventTimes(t, allEvents, id, t013At)
			if got := meter.counterTotal("loom.tasks.cancelled"); got != 0 {
				t.Errorf("active retry cancel intent incremented cancelled metric by %d", got)
			}
		})
	}
}

type t013CancelWorker struct {
	mu       sync.Mutex
	started  map[string]*t013Gate
	done     map[string]<-chan struct{}
	ack      map[string]*t013Gate
	release  *t013Gate
	view     *TaskStore
	observed *t013ObservedRecorder
}

func (w *t013CancelWorker) Execute(ctx context.Context, task *Task) (*WorkerResult, error) {
	w.mu.Lock()
	w.done[task.ID] = ctx.Done()
	started := w.started[task.ID]
	w.mu.Unlock()
	started.open()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		taskAtSignal, observeErr := w.view.Get(task.ID)
		var artifacts []TaskArtifact
		if observeErr == nil {
			page, listErr := w.view.ListArtifacts(task.ID, TaskArtifactListOptions{Limit: 100})
			if listErr != nil {
				observeErr = listErr
			} else {
				artifacts = page.Items
			}
		}
		w.observed.record(t013Observed{event: TaskEvent{TaskID: task.ID}, task: taskAtSignal, artifacts: artifacts, err: observeErr})
		w.ack[task.ID].open()
		select {
		case <-w.release.ch:
			return nil, ctx.Err()
		case <-timer.C:
			return nil, errors.New("t013 cancel worker release timeout")
		}
	case <-w.release.ch:
		return &WorkerResult{Content: "cleanup"}, nil
	case <-timer.C:
		return nil, errors.New("t013 cancel worker signal timeout")
	}
}

func (*t013CancelWorker) Type() WorkerType { return WorkerTypeCLI }

func (w *t013CancelWorker) doneFor(id string) <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.done[id]
}

func t013Signalled(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestTaskAuthorityIntegration_CancelAllPersistsEachIntentBeforeSignalAndNeverPublishesCancelledEarly(t *testing.T) {
	fixture := t013NewFixture(t)
	release := t013NewGate()
	observed := &t013ObservedRecorder{}
	started := map[string]*t013Gate{}
	acks := map[string]*t013Gate{}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("id-%d", i)
		started[id], acks[id] = t013NewGate(), t013NewGate()
	}
	worker := &t013CancelWorker{
		started: started, done: make(map[string]<-chan struct{}), ack: acks,
		release: release, view: fixture.view, observed: observed,
	}
	meter := newRecordingMeter()
	engine := fixture.engine(t, worker, WithMeter(meter))
	t.Cleanup(release.open)
	events := &t013Events{}
	engine.Events().Subscribe(events.record)
	for i := 0; i < 4; i++ {
		id, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "cancel-all", RequestID: fmt.Sprintf("request-%d", i), Prompt: fmt.Sprintf("task-%d", i)})
		if err != nil {
			t.Fatalf("Submit[%d]: %v", i, err)
		}
		t013AwaitGate(t, "worker "+id, worker.started[id])
	}
	controlID, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "cancel-other", RequestID: "request-4", Prompt: "control"})
	if err != nil {
		t.Fatalf("Submit control: %v", err)
	}
	t013AwaitGate(t, "worker "+controlID, worker.started[controlID])
	noLive := &Task{ID: "no-live", Status: TaskStatusRunning, WorkerType: WorkerTypeCLI, ProjectID: "cancel-all", Prompt: "no local cancel", CreatedAt: t013At}
	if err := fixture.store.Create(noLive); err != nil {
		t.Fatalf("seed no-live candidate: %v", err)
	}
	probe := &t013SignalProbe{}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("id-%d", i)
		t013WrapCancel(t, engine, id, events, acks[id], probe)
	}
	_, err = fixture.db.Exec(`CREATE TRIGGER t013_abort_cancel_id1 BEFORE INSERT ON task_artifacts
		WHEN NEW.task_id='id-1' AND NEW.event_type='task.cancel_requested'
		BEGIN SELECT RAISE(ABORT,'T013_CANCEL_FAIL_ID1'); END`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.db.Exec(`CREATE TRIGGER t013_abort_cancel_id3 BEFORE INSERT ON task_artifacts
		WHEN NEW.task_id='id-3' AND NEW.event_type='task.cancel_requested'
		BEGIN SELECT RAISE(ABORT,'T013_CANCEL_FAIL_ID3'); END`)
	if err != nil {
		t.Fatal(err)
	}

	count, cancelErr := engine.CancelAllForProject("cancel-all")
	if count != 2 || cancelErr == nil {
		t.Errorf("CancelAll count/error=%d/%v, want 2/joined failures", count, cancelErr)
	}
	if cancelErr != nil {
		message := cancelErr.Error()
		first, second := strings.Index(message, "T013_CANCEL_FAIL_ID1"), strings.Index(message, "T013_CANCEL_FAIL_ID3")
		if first < 0 || second < 0 || first >= second {
			t.Errorf("joined error order=%q, want id-1 marker before id-3 marker", message)
		}
	}
	observedAtSignal := make(map[string]t013Observed)
	for _, observation := range observed.snapshot(t) {
		if _, duplicate := observedAtSignal[observation.event.TaskID]; duplicate {
			t.Errorf("duplicate signal observation for %s", observation.event.TaskID)
		}
		observedAtSignal[observation.event.TaskID] = observation
	}
	if len(observedAtSignal) != 2 {
		t.Errorf("signal observation cardinality=%d, want exactly id-0/id-2", len(observedAtSignal))
	}
	for _, id := range []string{"id-0", "id-2"} {
		if !t013Signalled(worker.doneFor(id)) {
			t.Errorf("%s was not signalled", id)
		}
		observation, ok := observedAtSignal[id]
		if !ok || observation.err != nil || observation.task == nil || observation.task.Status != TaskStatusCancelling || observation.task.CancelRequestedAt == nil || !observation.task.CancelRequestedAt.Equal(t013At) {
			t.Errorf("%s state at signal=%#v err=%v, want already-durable cancelling at fixed time", id, observation.task, observation.err)
		}
		factsAtSignal := 0
		for _, artifact := range observation.artifacts {
			if artifact.EventType == "task.cancel_requested" {
				factsAtSignal++
			}
		}
		if factsAtSignal != 1 {
			t.Errorf("%s cancel-requested facts visible at signal=%d, want 1", id, factsAtSignal)
		}
		t013AssertExactArtifact(t, t013ArtifactByEvent(t, fixture.view, id, "task.cancel_requested"), TaskArtifactKindLifecycle, "task.cancel_requested", map[string]any{
			"status": "cancelling", "cancel_requested_at": t013At, "requires_stop": true, "closed_action_count": int64(0),
		}, t013At)
	}
	for _, id := range []string{"id-1", "id-3"} {
		if t013Signalled(worker.doneFor(id)) {
			t.Errorf("%s was signalled despite durable intent failure", id)
		}
		task, getErr := fixture.view.Get(id)
		if getErr != nil || task.Status != TaskStatusRunning || task.CancelRequestedAt != nil || t013ArtifactCountByEvent(t, fixture.view, id, "task.cancel_requested") != 0 {
			t.Errorf("failed intent %s=%#v err=%v, want untouched running/no fact", id, task, getErr)
		}
	}
	if t013Signalled(worker.doneFor("id-4")) {
		t.Error("project-B control was signalled by project-A CancelAll")
	}
	control, controlErr := fixture.view.Get("id-4")
	if controlErr != nil || control.Status != TaskStatusRunning || control.CancelRequestedAt != nil {
		t.Errorf("project-B control=%#v err=%v, want untouched running", control, controlErr)
	}
	noLiveState, noLiveErr := fixture.view.Get("no-live")
	if noLiveErr != nil || noLiveState.Status != TaskStatusRunning || noLiveState.CancelRequestedAt != nil {
		t.Errorf("no-live candidate=%#v err=%v, want excluded unchanged running", noLiveState, noLiveErr)
	}
	points := probe.snapshot()
	if len(points) != 2 || points[0].taskID != "id-0" || points[1].taskID != "id-2" {
		t.Errorf("signal order=%#v, want exact id-0,id-2", points)
	}
	for _, point := range points {
		if point.cancelRequestedEvents != 1 || point.ackTimedOut {
			t.Errorf("signal checkpoint=%#v, want projected event + observer ACK", point)
		}
	}
	for _, id := range []string{"id-0", "id-2"} {
		if events.count(id, EventType("task.cancel_requested")) != 1 {
			t.Errorf("%s cancel-requested events=%d, want 1", id, events.count(id, EventType("task.cancel_requested")))
		}
	}
	for _, id := range []string{"id-1", "id-3", "id-4", "no-live"} {
		if events.count(id, EventType("task.cancel_requested")) != 0 {
			t.Errorf("%s unexpectedly projected cancel-requested event", id)
		}
	}
	allEvents := events.snapshot(t)
	for _, event := range allEvents {
		if event.Type == EventTaskCancelled {
			t.Errorf("premature terminal cancellation event for %s", event.TaskID)
		}
	}
	for _, index := range []int{0, 2} {
		id := fmt.Sprintf("id-%d", index)
		t013AssertExactTaskEvent(t, allEvents, TaskEvent{
			Type: EventType("task.cancel_requested"), TaskID: id, ProjectID: "cancel-all", RequestID: fmt.Sprintf("request-%d", index), Status: TaskStatusCancelling, Timestamp: t013At,
		})
		t013AssertEventTimes(t, allEvents, id, t013At)
	}
	if got := meter.counterTotal("loom.tasks.cancelled"); got != 0 {
		t.Errorf("CancelAll intent incremented cancelled metric by %d", got)
	}
	release.open()
	t013Close(t, engine)
	for _, id := range []string{"id-0", "id-2"} {
		final, err := fixture.view.Get(id)
		if err != nil || final.Status != TaskStatusFailedCrash {
			t.Errorf("%s after legacy worker return=%#v err=%v, want failed_crash without StopEvidence", id, final, err)
		}
	}
}

func TestTaskAuthorityIntegration_LiveTerminalRacesRemainImmutable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		step   t013Step
		status TaskStatus
		event  string
	}{
		{name: "completion-before-cancel", step: t013Step{content: "done"}, status: TaskStatusCompleted, event: "task.completed"},
		{name: "failure-before-cancel", step: t013Step{err: errors.New("worker failed")}, status: TaskStatusFailed, event: "task.failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := t013NewFixture(t)
			meter := newRecordingMeter()
			engine := fixture.engine(t, &t013ScriptedWorker{steps: []t013Step{tc.step}}, WithMeter(meter))
			events := &t013Events{}
			observed := &t013ObservedRecorder{}
			engine.Events().Subscribe(events.record)
			engine.Events().Subscribe(func(event TaskEvent) {
				if event.Type != EventType(tc.event) {
					return
				}
				task, observeErr := fixture.view.Get(event.TaskID)
				var artifacts []TaskArtifact
				if observeErr == nil {
					page, listErr := fixture.view.ListArtifacts(event.TaskID, TaskArtifactListOptions{Limit: 100})
					if listErr != nil {
						observeErr = listErr
					} else {
						artifacts = page.Items
					}
				}
				observed.record(t013Observed{event: event, task: task, artifacts: artifacts, err: observeErr})
			})
			id, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: tc.name, Prompt: "terminal wins"})
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			t013Close(t, engine)
			snapshots := observed.snapshot(t)
			if len(snapshots) != 1 {
				t.Errorf("terminal observation cardinality=%d, want 1", len(snapshots))
			}
			if len(snapshots) == 1 {
				snapshot := snapshots[0]
				if snapshot.err != nil || snapshot.task == nil || snapshot.task.Status != tc.status || t013CountEventInArtifacts(snapshot.artifacts, tc.event) != 1 {
					t.Errorf("terminal event observed state/fact=%#v count=%d err=%v, want committed %s/1", snapshot.task, t013CountEventInArtifacts(snapshot.artifacts, tc.event), snapshot.err, tc.status)
				}
			}
			cancelErr := engine.Cancel(id)
			if !errors.Is(cancelErr, ErrAuthorityConflict) {
				t.Errorf("Cancel terminal error=%v, want ErrAuthorityConflict", cancelErr)
			}
			task, getErr := fixture.view.Get(id)
			if getErr != nil || task.Status != tc.status || task.CancelRequestedAt != nil {
				t.Errorf("terminal task=%#v err=%v, want immutable %s", task, getErr, tc.status)
			}
			artifact := t013ArtifactByEvent(t, fixture.view, id, tc.event)
			payload := map[string]any{"status": string(tc.status), "closed_action_count": int64(0)}
			if tc.status == TaskStatusFailed {
				payload["error_code"] = "task_failed"
			}
			t013AssertExactArtifact(t, artifact, TaskArtifactKindTerminal, tc.event, payload, t013At)
			if events.count(id, EventType(tc.event)) != 1 || events.count(id, EventType("task.cancel_requested")) != 0 || events.count(id, EventTaskCancelled) != 0 {
				t.Errorf("terminal event projection=%v, want one %s and no cancellation projection", t013EventTypes(events.snapshot(t), id), tc.event)
			}
			t013AssertEventTimes(t, events.snapshot(t), id, t013At)
			if got := meter.counterTotal("loom.tasks.cancelled"); got != 0 {
				t.Errorf("terminal conflict incremented cancelled metric by %d", got)
			}
		})
	}

	for _, tc := range []struct {
		name string
		step t013Step
	}{
		{name: "cancel-first-late-success", step: t013Step{content: "late success"}},
		{name: "cancel-first-late-error", step: t013Step{err: errors.New("late error")}},
		{name: "cancel-first-late-panic", step: t013Step{panicValue: "late panic"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := t013NewFixture(t)
			started, release := t013NewGate(), t013NewGate()
			ack, abort := t013NewGate(), t013NewGate()
			observed := &t013ObservedRecorder{}
			step := tc.step
			step.started, step.release, step.ignoreCancel = started, release, true
			step.watch = &t013SignalObserver{view: fixture.view, recorder: observed, ack: ack, abort: abort}
			meter := newRecordingMeter()
			engine := fixture.engine(t, &t013ScriptedWorker{steps: []t013Step{step}}, WithMeter(meter))
			t.Cleanup(release.open)
			t.Cleanup(abort.open)
			recorded := &t013Events{}
			engine.Events().Subscribe(recorded.record)
			requestID := tc.name + "-request"
			id, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: tc.name, RequestID: requestID, Prompt: "cancel wins"})
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			t013AwaitGate(t, "worker", started)
			probe := &t013SignalProbe{}
			t013WrapCancel(t, engine, id, recorded, ack, probe)
			if err := engine.Cancel(id); err != nil {
				t.Errorf("Cancel: %v", err)
			}
			observations := observed.snapshot(t)
			if len(observations) != 1 {
				t.Errorf("signal observation cardinality=%d, want 1", len(observations))
			}
			if len(observations) == 1 {
				intent := observations[0]
				if intent.err != nil || intent.task == nil || intent.task.Status != TaskStatusCancelling || intent.task.CancelRequestedAt == nil || !intent.task.CancelRequestedAt.Equal(t013At) || t013CountEventInArtifacts(intent.artifacts, "task.cancel_requested") != 1 {
					t.Errorf("task/fact at cancellation signal=%#v facts=%d err=%v, want durable cancelling/one fact", intent.task, t013CountEventInArtifacts(intent.artifacts, "task.cancel_requested"), intent.err)
				}
			}
			points := probe.snapshot()
			if len(points) != 1 || points[0].taskID != id || points[0].cancelRequestedEvents != 1 || points[0].ackTimedOut {
				t.Errorf("cancel signal checkpoint=%#v, want one projected intent before signal plus observer ACK", points)
			}
			if recorded.count(id, EventType("task.cancel_requested")) != 1 || recorded.count(id, EventTaskCancelled) != 0 {
				t.Errorf("cancel intent events=%v, want one cancel_requested and no cancelled", t013EventTypes(recorded.snapshot(t), id))
			}
			intentEvents := recorded.snapshot(t)
			t013AssertExactTaskEvent(t, intentEvents, TaskEvent{
				Type: EventType("task.cancel_requested"), TaskID: id, ProjectID: tc.name, RequestID: requestID, Status: TaskStatusCancelling, Timestamp: t013At,
			})
			t013AssertEventTimes(t, intentEvents, id, t013At)
			if got := meter.counterTotal("loom.tasks.cancelled"); got != 0 {
				t.Errorf("cancel intent incremented cancelled metric by %d", got)
			}
			release.open()
			t013Close(t, engine)
			final, getErr := fixture.view.Get(id)
			if getErr != nil || final.Status != TaskStatusFailedCrash {
				t.Errorf("task after unproven stop=%#v err=%v, want failed_crash", final, getErr)
			}
			t013AssertExactArtifact(t, t013ArtifactByEvent(t, fixture.view, id, "task.failed_crash"), TaskArtifactKindTerminal, "task.failed_crash", map[string]any{
				"status": "failed_crash", "error_code": "task_failed_crash", "closed_action_count": int64(0),
			}, t013At)
			terminalFacts := 0
			for _, artifact := range t013Artifacts(t, fixture.view, id) {
				switch artifact.EventType {
				case "task.completed", "task.failed", "task.failed_crash", "task.cancelled":
					terminalFacts++
				}
				if artifact.EventType == "task.completed" || artifact.EventType == "task.failed" || artifact.EventType == "task.cancelled" {
					t.Errorf("late outcome rewrote cancelled task with %s", artifact.EventType)
				}
			}
			if terminalFacts != 1 {
				t.Errorf("terminal fact count=%d, want exactly one failed_crash", terminalFacts)
			}
			terminalEvents := 0
			for _, event := range recorded.snapshot(t) {
				switch event.Type {
				case EventTaskCompleted, EventTaskFailed, EventTaskFailedCrash, EventTaskCancelled:
					terminalEvents++
					if event.Type != EventTaskFailedCrash {
						t.Errorf("late outcome emitted terminal %s", event.Type)
					}
				}
			}
			if terminalEvents != 1 {
				t.Errorf("terminal event count=%d, want exactly one failed_crash", terminalEvents)
			}
			t013AssertEventTimes(t, recorded.snapshot(t), id, t013At)
			if got := meter.counterTotal("loom.tasks.cancelled"); got != 0 {
				t.Errorf("unproven stop incremented cancelled metric by %d", got)
			}
		})
	}
}

func t013SeedRecoveryTask(t *testing.T, store *TaskStore, id string, status TaskStatus, created time.Time) {
	t.Helper()
	task := &Task{ID: id, Status: status, WorkerType: WorkerTypeCLI, ProjectID: "recovery", Prompt: "recover", CreatedAt: created.UTC()}
	if err := store.Create(task); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	if status != TaskStatusPending {
		_, err := store.db.Exec(`UPDATE tasks SET dispatched_at=? WHERE id=?`, created.Add(time.Second).UTC(), id)
		if err != nil {
			t.Fatal(err)
		}
	}
}

type t013Observed struct {
	event     TaskEvent
	task      *Task
	artifacts []TaskArtifact
	err       error
}

func TestTaskAuthorityIntegration_RecoverCrashedCommitsOneTerminalFactBeforeEvent(t *testing.T) {
	t.Run("all-active-statuses-and-idempotence", func(t *testing.T) {
		fixture := t013NewFixture(t)
		active := []TaskStatus{TaskStatusDispatched, TaskStatusRunning, TaskStatusInputRequired, TaskStatusRetrying, TaskStatusCancelling}
		ids := make([]string, len(active))
		for i, status := range active {
			ids[i] = fmt.Sprintf("active-%d", i)
			t013SeedRecoveryTask(t, fixture.store, ids[i], status, t013At.Add(time.Duration(i)*time.Second-time.Hour))
			t013SeedAction(t, fixture.db, "action-"+ids[i], ids[i])
		}
		t013SeedRecoveryTask(t, fixture.store, "pending", TaskStatusPending, t013At.Add(-2*time.Hour))
		t013SeedRecoveryTask(t, fixture.store, "terminal", TaskStatusCompleted, t013At.Add(-3*time.Hour))
		engine := fixture.engine(t, nil)
		events := &t013Events{}
		observed := &t013ObservedRecorder{}
		engine.Events().Subscribe(events.record)
		engine.Events().Subscribe(func(event TaskEvent) {
			if event.Type != EventTaskFailedCrash {
				return
			}
			task, err := fixture.view.Get(event.TaskID)
			var artifacts []TaskArtifact
			if err == nil {
				page, listErr := fixture.view.ListArtifacts(event.TaskID, TaskArtifactListOptions{Limit: 100})
				if listErr != nil {
					err = listErr
				} else {
					artifacts = page.Items
				}
			}
			observed.record(t013Observed{event: event, task: task, artifacts: artifacts, err: err})
		})

		count, err := engine.RecoverCrashed()
		if err != nil || count != len(active) {
			t.Errorf("RecoverCrashed count/error=%d/%v, want %d/nil", count, err, len(active))
		}
		observations := observed.snapshot(t)
		if len(observations) != len(active) {
			t.Errorf("recovery observations=%d, want %d synchronous post-commit events", len(observations), len(active))
		}
		for index, id := range ids {
			if index < len(observations) {
				got := observations[index]
				if got.err != nil || got.event.TaskID != id || got.task == nil || got.task.Status != TaskStatusFailedCrash {
					t.Errorf("recovery event[%d]=%#v, want durable failed_crash for %s", index, got, id)
				}
				if got.event.Timestamp != t013At {
					t.Errorf("recovery event timestamp=%s, want %s", got.event.Timestamp, t013At)
				}
				if len(got.artifacts) != 1 {
					t.Errorf("%s facts visible at event=%d, want 1", id, len(got.artifacts))
				}
			}
			task, getErr := fixture.view.Get(id)
			if getErr != nil || task.Status != TaskStatusFailedCrash || task.Result != "" || task.Error != "task interrupted by daemon restart" || task.CompletedAt == nil || !task.CompletedAt.Equal(t013At) {
				t.Errorf("recovered %s=%#v err=%v", id, task, getErr)
			}
			status, resolved := t013Action(t, fixture.observer, "action-"+id)
			if status != "task_closed" || resolved == nil || !resolved.Equal(t013At) {
				t.Errorf("closed action for %s=%s/%v, want task_closed/%s", id, status, resolved, t013At)
			}
			t013AssertExactArtifact(t, t013ArtifactByEvent(t, fixture.view, id, "task.failed_crash"), TaskArtifactKindTerminal, "task.failed_crash", map[string]any{
				"status": "failed_crash", "error_code": "task_failed_crash", "closed_action_count": int64(1),
			}, t013At)
		}
		for _, id := range []string{"pending", "terminal"} {
			if got := len(t013Artifacts(t, fixture.view, id)); got != 0 {
				t.Errorf("untouched %s artifacts=%d, want 0", id, got)
			}
		}
		baselineEvents := events.snapshot(t)
		baselineArtifacts := make(map[string][]TaskArtifact, len(ids)+2)
		for _, id := range append(append([]string(nil), ids...), "pending", "terminal") {
			baselineArtifacts[id] = t013Artifacts(t, fixture.view, id)
		}
		if second, secondErr := engine.RecoverCrashed(); secondErr != nil || second != 0 {
			t.Errorf("second RecoverCrashed=%d/%v, want 0/nil", second, secondErr)
		}
		if got := events.snapshot(t); !reflect.DeepEqual(got, baselineEvents) {
			t.Errorf("second recovery emitted events: before=%#v after=%#v", baselineEvents, got)
		}
		for id, before := range baselineArtifacts {
			if after := t013Artifacts(t, fixture.view, id); !reflect.DeepEqual(after, before) {
				t.Errorf("second recovery changed %s artifacts: before=%#v after=%#v", id, before, after)
			}
		}
	})

	t.Run("artifact-failure-is-isolated", func(t *testing.T) {
		fixture := t013NewFixture(t)
		for i, status := range []TaskStatus{TaskStatusDispatched, TaskStatusRunning, TaskStatusRetrying} {
			id := fmt.Sprintf("isolate-%d", i)
			t013SeedRecoveryTask(t, fixture.store, id, status, t013At.Add(time.Duration(i)*time.Second-time.Hour))
		}
		_, err := fixture.db.Exec(`CREATE TRIGGER t013_abort_recovery_middle BEFORE INSERT ON task_artifacts
			WHEN NEW.task_id='isolate-1' AND NEW.event_type='task.failed_crash'
			BEGIN SELECT RAISE(ABORT,'T013_RECOVERY_MIDDLE_ABORT'); END`)
		if err != nil {
			t.Fatal(err)
		}
		engine := fixture.engine(t, nil)
		events := &t013Events{}
		observed := &t013ObservedRecorder{}
		engine.Events().Subscribe(events.record)
		engine.Events().Subscribe(func(event TaskEvent) {
			if event.Type != EventTaskFailedCrash {
				return
			}
			task, observeErr := fixture.view.Get(event.TaskID)
			var artifacts []TaskArtifact
			if observeErr == nil {
				page, listErr := fixture.view.ListArtifacts(event.TaskID, TaskArtifactListOptions{Limit: 100})
				if listErr != nil {
					observeErr = listErr
				} else {
					artifacts = page.Items
				}
			}
			observed.record(t013Observed{event: event, task: task, artifacts: artifacts, err: observeErr})
		})
		count, recoverErr := engine.RecoverCrashed()
		if count != 2 || recoverErr == nil || !strings.Contains(recoverErr.Error(), "T013_RECOVERY_MIDDLE_ABORT") {
			t.Errorf("isolated recovery count/error=%d/%v, want 2/joined abort", count, recoverErr)
		}
		observations := observed.snapshot(t)
		if len(observations) != 2 || observations[0].event.TaskID != "isolate-0" || observations[1].event.TaskID != "isolate-2" {
			t.Errorf("isolated recovery observations=%#v, want exact isolate-0,isolate-2 order", observations)
		}
		for _, id := range []string{"isolate-0", "isolate-2"} {
			task, getErr := fixture.view.Get(id)
			if getErr != nil || task.Status != TaskStatusFailedCrash {
				t.Errorf("%s=%#v err=%v, want failed_crash", id, task, getErr)
			}
			if events.count(id, EventTaskFailedCrash) != 1 {
				t.Errorf("%s failed_crash events=%d, want 1", id, events.count(id, EventTaskFailedCrash))
			}
			t013AssertExactArtifact(t, t013ArtifactByEvent(t, fixture.view, id, "task.failed_crash"), TaskArtifactKindTerminal, "task.failed_crash", map[string]any{
				"status": "failed_crash", "error_code": "task_failed_crash", "closed_action_count": int64(0),
			}, t013At)
		}
		middle, getErr := fixture.view.Get("isolate-1")
		if getErr != nil || middle.Status != TaskStatusRunning {
			t.Errorf("isolate-1=%#v err=%v, want untouched running", middle, getErr)
		}
		if events.count("isolate-1", EventTaskFailedCrash) != 0 || len(t013Artifacts(t, fixture.view, "isolate-1")) != 0 {
			t.Error("failed middle recovery emitted an event or left a fact")
		}
		baselineEvents := events.snapshot(t)
		baselineArtifacts := map[string][]TaskArtifact{
			"isolate-0": t013Artifacts(t, fixture.view, "isolate-0"),
			"isolate-1": t013Artifacts(t, fixture.view, "isolate-1"),
			"isolate-2": t013Artifacts(t, fixture.view, "isolate-2"),
		}
		secondCount, secondErr := engine.RecoverCrashed()
		if secondCount != 0 || secondErr == nil || !strings.Contains(secondErr.Error(), "T013_RECOVERY_MIDDLE_ABORT") {
			t.Errorf("second isolated recovery=%d/%v, want 0/same abort", secondCount, secondErr)
		}
		if got := events.snapshot(t); !reflect.DeepEqual(got, baselineEvents) {
			t.Errorf("second isolated recovery emitted events: before=%#v after=%#v", baselineEvents, got)
		}
		for id, before := range baselineArtifacts {
			if after := t013Artifacts(t, fixture.view, id); !reflect.DeepEqual(after, before) {
				t.Errorf("second isolated recovery changed %s artifacts: before=%#v after=%#v", id, before, after)
			}
		}
	})
}
