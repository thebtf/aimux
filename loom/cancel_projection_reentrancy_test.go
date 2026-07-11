package loom

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom/deps"
	"modernc.org/sqlite"
)

type t014FixedIDGenerator struct {
	mu   sync.Mutex
	ids  []string
	next int
}

func (g *t014FixedIDGenerator) NewID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.next >= len(g.ids) {
		panic("t014 fixed ID generator exhausted")
	}
	id := g.ids[g.next]
	g.next++
	return id
}

var _ deps.IDGenerator = (*t014FixedIDGenerator)(nil)

type t014ReentrancyWorker struct {
	started map[string]*t013Gate
	release map[string]*t013Gate
}

func (w *t014ReentrancyWorker) Execute(_ context.Context, task *Task) (*WorkerResult, error) {
	w.started[task.ID].open()
	<-w.release[task.ID].ch
	return &WorkerResult{Content: "late result"}, nil
}

func (*t014ReentrancyWorker) Type() WorkerType { return WorkerTypeCLI }

type t014SignalSnapshotWorker struct {
	started          *t013Gate
	release          *t013Gate
	returned         *t013Gate
	contextSignalled *t013Gate
	contextSignals   atomic.Int32
}

func (w *t014SignalSnapshotWorker) Execute(ctx context.Context, _ *Task) (*WorkerResult, error) {
	go func() {
		<-ctx.Done()
		w.contextSignals.Add(1)
		w.contextSignalled.open()
	}()
	w.started.open()
	<-w.release.ch
	w.returned.open()
	return &WorkerResult{Content: "late result"}, nil
}

func (*t014SignalSnapshotWorker) Type() WorkerType { return WorkerTypeCLI }

type t014ReadFaultDriver struct {
	base            driver.Driver
	failNextTaskGet atomic.Bool
}

func (d *t014ReadFaultDriver) Open(name string) (driver.Conn, error) {
	connection, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	if _, ok := connection.(driver.ExecerContext); !ok {
		_ = connection.Close()
		return nil, fmt.Errorf("T014_READ_FAULT_DRIVER_CAPABILITY: %T lacks driver.ExecerContext", connection)
	}
	if _, ok := connection.(driver.QueryerContext); !ok {
		_ = connection.Close()
		return nil, fmt.Errorf("T014_READ_FAULT_DRIVER_CAPABILITY: %T lacks driver.QueryerContext", connection)
	}
	return &t014ReadFaultConn{Conn: connection, owner: d}, nil
}

type t014ReadFaultConn struct {
	driver.Conn
	owner *t014ReadFaultDriver
}

func (c *t014ReadFaultConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *t014ReadFaultConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	normalized := strings.ToUpper(strings.Join(strings.Fields(query), " "))
	if strings.Contains(normalized, "FROM TASKS WHERE ID = ?") && c.owner.failNextTaskGet.CompareAndSwap(true, false) {
		return nil, errors.New("T014_INJECTED_TASK_GET_FAILURE")
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *t014ReadFaultConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

var t014ReadFaultDriverCounter atomic.Uint64

func t014CollidingTaskIDs() (string, string) {
	probe := &LoomEngine{}
	seen := make(map[any]string, lifecycleOrderingStripes)
	for i := 0; i <= lifecycleOrderingStripes; i++ {
		id := fmt.Sprintf("t014-collision-%d", i)
		stripe := any(probe.lifecycleOrderingFor(id))
		if prior, ok := seen[stripe]; ok {
			return prior, id
		}
		seen[stripe] = id
	}
	panic("pigeonhole collision not found")
}

func t014PendingProjectionStateCount(engine *LoomEngine) int {
	stripes := reflect.ValueOf(engine).Elem().FieldByName("lifecycleOrdering")
	if !stripes.IsValid() || stripes.Kind() != reflect.Array || stripes.Len() == 0 {
		return -1
	}
	total := 0
	for i := 0; i < stripes.Len(); i++ {
		stripe := stripes.Index(i)
		if stripe.Kind() != reflect.Struct {
			return -1
		}
		pending := stripe.FieldByName("pending")
		if !pending.IsValid() || pending.Kind() != reflect.Map {
			return -1
		}
		total += pending.Len()
	}
	return total
}

func t014LogFieldValue(logger *recordingLogger, message, field string) (any, bool) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	for _, entry := range logger.entries {
		if entry.msg != message {
			continue
		}
		for index := 0; index+1 < len(entry.args); index += 2 {
			if key, ok := entry.args[index].(string); ok && key == field {
				return entry.args[index+1], true
			}
		}
	}
	return nil, false
}

func TestCancelProjectionReentrancyDoesNotHoldLifecycleStripe(t *testing.T) {
	type operationResult struct {
		count          int
		err            error
		conflictErr    error
		firstAccepted  bool
		secondAccepted bool
	}
	tests := []struct {
		name       string
		secondTask bool
		reenter    func(*LoomEngine, string) operationResult
		check      func(*testing.T, operationResult)
	}{
		{
			name: "same-task Cancel",
			reenter: func(engine *LoomEngine, taskID string) operationResult {
				return operationResult{err: engine.Cancel(taskID)}
			},
			check: func(t *testing.T, result operationResult) {
				if !errors.Is(result.err, ErrAuthorityConflict) {
					t.Fatalf("reentrant Cancel error=%v, want ErrAuthorityConflict", result.err)
				}
			},
		},
		{
			name: "same-task RecoverCrashed",
			reenter: func(engine *LoomEngine, taskID string) operationResult {
				task, getErr := engine.store.Get(taskID)
				if getErr != nil {
					return operationResult{err: getErr}
				}
				firstAccepted := engine.commitFailedCrashAt(task, TaskStatusCancelling, "first deferred failure", "first_deferred", t013At)
				secondAccepted := engine.commitFailedCrashAt(task, TaskStatusCancelling, "second deferred failure", "second_deferred", t013At.Add(time.Second))
				conflictErr := engine.Cancel(taskID)
				count, err := engine.RecoverCrashed()
				return operationResult{
					count: count, err: err, conflictErr: conflictErr,
					firstAccepted: firstAccepted, secondAccepted: secondAccepted,
				}
			},
			check: func(t *testing.T, result operationResult) {
				if result.err != nil || result.count != 0 || !errors.Is(result.conflictErr, ErrAuthorityConflict) || !result.firstAccepted || !result.secondAccepted {
					t.Fatalf("reentrant terminal contenders=%t/%t nested Cancel=%v RecoverCrashed=%d/%v, want handled/handled/conflict/0/nil while one terminal commit is deferred", result.firstAccepted, result.secondAccepted, result.conflictErr, result.count, result.err)
				}
			},
		},
		{
			name:       "same-stripe CancelAllForProject",
			secondTask: true,
			reenter: func(engine *LoomEngine, _ string) operationResult {
				count, err := engine.CancelAllForProject("collision-b")
				return operationResult{count: count, err: err}
			},
			check: func(t *testing.T, result operationResult) {
				if result.err != nil || result.count != 1 {
					t.Fatalf("same-stripe CancelAllForProject=%d/%v, want 1/nil", result.count, result.err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			firstID, secondID := t014CollidingTaskIDs()
			fixture := t013NewFixture(t)
			started := map[string]*t013Gate{firstID: t013NewGate(), secondID: t013NewGate()}
			release := map[string]*t013Gate{firstID: t013NewGate(), secondID: t013NewGate()}
			worker := &t014ReentrancyWorker{started: started, release: release}
			logger := &recordingLogger{}
			engine := fixture.engine(t, worker,
				WithIDGenerator(&t014FixedIDGenerator{ids: []string{firstID, secondID}}),
				WithLogger(logger),
			)
			t.Cleanup(release[firstID].open)
			t.Cleanup(release[secondID].open)

			if engine.lifecycleOrderingFor(firstID) != engine.lifecycleOrderingFor(secondID) {
				t.Fatal("test IDs do not share a lifecycle ordering stripe")
			}
			events := &t013Events{}
			engine.Events().Subscribe(events.record)
			projectionEntered := t013NewGate()
			type callbackResult struct {
				blocked bool
				result  operationResult
			}
			callbackDone := make(chan callbackResult, 1)
			engine.Events().Subscribe(func(event TaskEvent) {
				if event.Type != EventTaskCancelRequested || event.TaskID != firstID {
					return
				}
				projectionEntered.open()
				operationDone := make(chan operationResult, 1)
				go func() { operationDone <- tc.reenter(engine, firstID) }()
				select {
				case result := <-operationDone:
					callbackDone <- callbackResult{result: result}
				case <-time.After(t013Wait):
					callbackDone <- callbackResult{blocked: true}
				}
			})

			first, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "collision-a", Prompt: "first"})
			if err != nil || first != firstID {
				t.Fatalf("submit first=%q/%v, want %q/nil", first, err, firstID)
			}
			t013AwaitGate(t, "first worker", started[firstID])
			if tc.secondTask {
				second, submitErr := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "collision-b", Prompt: "second"})
				if submitErr != nil || second != secondID {
					t.Fatalf("submit second=%q/%v, want %q/nil", second, submitErr, secondID)
				}
				t013AwaitGate(t, "second worker", started[secondID])
			}

			cancelDone := make(chan error, 1)
			go func() { cancelDone <- engine.Cancel(firstID) }()
			t013AwaitGate(t, "cancel_requested subscriber", projectionEntered)
			var callback callbackResult
			select {
			case callback = <-callbackDone:
			case <-time.After(2 * t013Wait):
				t.Fatal("reentrant callback did not report a result")
			}
			select {
			case cancelErr := <-cancelDone:
				if cancelErr != nil {
					t.Fatalf("outer Cancel: %v", cancelErr)
				}
			case <-time.After(2 * t013Wait):
				t.Fatal("outer Cancel did not complete")
			}
			if callback.blocked {
				t.Fatal("reentrant lifecycle operation blocked behind a synchronous cancel_requested subscriber")
			}
			tc.check(t, callback.result)
			if pending := t014PendingProjectionStateCount(engine); pending != 0 {
				t.Fatalf("pending lifecycle projection states immediately after outer Cancel=%d, want 0", pending)
			}

			release[firstID].open()
			if tc.secondTask {
				release[secondID].open()
			}
			t013Close(t, engine)
			for _, taskID := range []string{firstID} {
				t013AssertTypes(t, t013EventTypes(events.snapshot(t), taskID),
					EventTaskCreated, EventTaskDispatched, EventTaskRunning, EventTaskCancelRequested, EventTaskFailedCrash,
				)
				if got := t013ArtifactCountByEvent(t, fixture.view, taskID, "task.failed_crash"); got != 1 {
					t.Fatalf("%s failed_crash facts=%d, want 1", taskID, got)
				}
			}
			if tc.name == "same-task RecoverCrashed" {
				final, getErr := fixture.view.Get(firstID)
				if getErr != nil || final.Error != "first deferred failure" || final.CompletedAt == nil || !final.CompletedAt.Equal(t013At) {
					t.Fatalf("first-wins deferred terminal=%#v err=%v, want first Error and CompletedAt=%s", final, getErr, t013At)
				}
				if errorCode, ok := t014LogFieldValue(logger, "task failed crash", "error_code"); !ok || errorCode != "first_deferred" {
					t.Fatalf("first-wins projected error_code=%v/%t, want first_deferred", errorCode, ok)
				}
			}
			if tc.secondTask {
				t013AssertTypes(t, t013EventTypes(events.snapshot(t), secondID),
					EventTaskCreated, EventTaskDispatched, EventTaskRunning, EventTaskCancelRequested, EventTaskFailedCrash,
				)
				if got := t013ArtifactCountByEvent(t, fixture.view, secondID, "task.failed_crash"); got != 1 {
					t.Fatalf("%s failed_crash facts=%d, want 1", secondID, got)
				}
			}
			if pending := t014PendingProjectionStateCount(engine); pending != 0 {
				t.Fatalf("pending lifecycle projection states=%d, want 0 after flush", pending)
			}
		})
	}
}

func TestCancelProjectionDeferredCommitFailureIsReturnedAndStateIsReleased(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(*LoomEngine, string) (int, error)
		count  int
	}{
		{
			name: "Cancel",
			cancel: func(engine *LoomEngine, taskID string) (int, error) {
				return 0, engine.Cancel(taskID)
			},
		},
		{
			name: "CancelAllForProject",
			cancel: func(engine *LoomEngine, _ string) (int, error) {
				return engine.CancelAllForProject("deferred-error")
			},
			count: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := t013NewFixture(t)
			started, release := t013NewGate(), t013NewGate()
			worker := &t014ReentrancyWorker{
				started: map[string]*t013Gate{"id-0": started},
				release: map[string]*t013Gate{"id-0": release},
			}
			engine := fixture.engine(t, worker)
			t.Cleanup(release.open)
			events := &t013Events{}
			engine.Events().Subscribe(events.record)
			type callbackResult struct {
				blocked  bool
				accepted bool
			}
			callbackDone := make(chan callbackResult, 1)
			engine.Events().Subscribe(func(event TaskEvent) {
				if event.Type != EventTaskCancelRequested || event.TaskID != "id-0" {
					return
				}
				task, getErr := fixture.view.Get(event.TaskID)
				if getErr != nil {
					callbackDone <- callbackResult{}
					return
				}
				commitDone := make(chan bool, 1)
				go func() {
					commitDone <- engine.commitFailedCrashAt(task, TaskStatusCancelling, "deferred terminal", "deferred_terminal", t013At)
				}()
				select {
				case accepted := <-commitDone:
					callbackDone <- callbackResult{accepted: accepted}
				case <-time.After(t013Wait):
					callbackDone <- callbackResult{blocked: true}
				}
			})

			id, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "deferred-error", Prompt: "deferred error"})
			if err != nil || id != "id-0" {
				t.Fatalf("Submit=%q/%v, want id-0/nil", id, err)
			}
			t013AwaitGate(t, "worker", started)
			if _, err := fixture.db.Exec(`CREATE TRIGGER t014_abort_deferred_terminal BEFORE INSERT ON task_artifacts
				WHEN NEW.task_id='id-0' AND NEW.event_type='task.failed_crash'
				BEGIN SELECT RAISE(ABORT,'T014_DEFERRED_TERMINAL_ABORT'); END`); err != nil {
				t.Fatal(err)
			}

			count, cancelErr := tc.cancel(engine, id)
			callback := <-callbackDone
			if callback.blocked || !callback.accepted {
				t.Fatalf("deferred terminal callback=%#v, want accepted without blocking", callback)
			}
			if count != tc.count || cancelErr == nil || !strings.Contains(cancelErr.Error(), "T014_DEFERRED_TERMINAL_ABORT") {
				t.Fatalf("cancel result=%d/%v, want %d/deferred commit error", count, cancelErr, tc.count)
			}
			current, getErr := fixture.view.Get(id)
			if getErr != nil || current.Status != TaskStatusCancelling {
				t.Fatalf("task after deferred commit abort=%#v err=%v, want cancelling", current, getErr)
			}
			if got := t013ArtifactCountByEvent(t, fixture.view, id, "task.failed_crash"); got != 0 {
				t.Fatalf("failed_crash facts after aborted deferred commit=%d, want 0", got)
			}
			if pending := t014PendingProjectionStateCount(engine); pending != 0 {
				t.Fatalf("pending lifecycle projection states=%d, want 0 after aborted flush", pending)
			}

			if _, err := fixture.db.Exec(`DROP TRIGGER t014_abort_deferred_terminal`); err != nil {
				t.Fatal(err)
			}
			if committed := engine.commitFailedCrashAt(current, TaskStatusCancelling, "retry after deferred abort", "retry_after_abort", t013At.Add(time.Second)); !committed {
				t.Fatal("failed_crash retry was not committed after deferred state cleanup")
			}
			release.open()
			t013Close(t, engine)
			t013AssertTypes(t, t013EventTypes(events.snapshot(t), id),
				EventTaskCreated, EventTaskDispatched, EventTaskRunning, EventTaskCancelRequested, EventTaskFailedCrash,
			)
			if got := t013ArtifactCountByEvent(t, fixture.view, id, "task.failed_crash"); got != 1 {
				t.Fatalf("failed_crash facts after retry=%d, want 1", got)
			}
		})
	}
}

func TestCancelProjectionTerminalWinnerConflictIsSatisfiedAndStateIsReleased(t *testing.T) {
	fixture := t013NewFixture(t)
	started, release := t013NewGate(), t013NewGate()
	worker := &t014ReentrancyWorker{
		started: map[string]*t013Gate{"id-0": started},
		release: map[string]*t013Gate{"id-0": release},
	}
	engine := fixture.engine(t, worker)
	t.Cleanup(release.open)
	events := &t013Events{}
	engine.Events().Subscribe(events.record)

	type callbackResult struct {
		handled bool
		winner  CommitResult
		err     error
	}
	callbackDone := make(chan callbackResult, 1)
	engine.Events().Subscribe(func(event TaskEvent) {
		if event.Type != EventTaskCancelRequested || event.TaskID != "id-0" {
			return
		}
		task, err := fixture.view.Get(event.TaskID)
		if err != nil {
			callbackDone <- callbackResult{err: err}
			return
		}
		deferred := engine.commitFailedCrashAt(
			task, TaskStatusCancelling, "deferred failed_crash loser", "deferred_loser", t013At,
		)
		winnerAt := t013At.Add(time.Second)
		winner, commitErr := fixture.store.CommitFailedCrash(context.Background(), FailCrashedTask{
			TaskID:         task.ID,
			ExpectedStatus: TaskStatusCancelling,
			Error:          "canonical terminal winner",
			CompletedAt:    winnerAt,
		})
		if commitErr == nil && winner.Applied {
			engine.emitTaskEvent(task, EventTaskFailedCrash, TaskStatusFailedCrash, winnerAt)
			engine.recordTaskFailed(task)
		}
		callbackDone <- callbackResult{handled: deferred, winner: winner, err: commitErr}
	})

	id, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI, ProjectID: "terminal-winner", Prompt: "terminal winner during projection",
	})
	if err != nil || id != "id-0" {
		t.Fatalf("Submit=%q/%v, want id-0/nil", id, err)
	}
	t013AwaitGate(t, "worker", started)
	if err := engine.Cancel(id); err != nil {
		t.Fatalf("Cancel with terminal canonical winner: %v", err)
	}
	callback := <-callbackDone
	if callback.err != nil || !callback.winner.Applied || !callback.handled {
		t.Fatalf("terminal-winner callback=%#v, want deferred handled and canonical winner applied", callback)
	}
	if pending := t014PendingProjectionStateCount(engine); pending != 0 {
		t.Fatalf("pending lifecycle projection states immediately after terminal-winner flush=%d, want 0", pending)
	}

	release.open()
	t013Close(t, engine)
	final, err := fixture.view.Get(id)
	if err != nil || final.Status != TaskStatusFailedCrash || final.Error != "canonical terminal winner" {
		t.Fatalf("terminal authority winner=%#v err=%v, want canonical failed_crash winner", final, err)
	}
	t013AssertTypes(t, t013EventTypes(events.snapshot(t), id),
		EventTaskCreated, EventTaskDispatched, EventTaskRunning, EventTaskCancelRequested, EventTaskFailedCrash,
	)
	if got := t013ArtifactCountByEvent(t, fixture.view, id, "task.failed_crash"); got != 1 {
		t.Fatalf("task.failed_crash facts=%d, want exactly 1 terminal winner", got)
	}
}

func TestCancelProjectionSignalSnapshotRaceStress(t *testing.T) {
	const iterations = 16
	for iteration := 0; iteration < iterations; iteration++ {
		t.Run(fmt.Sprintf("iteration-%02d", iteration), func(t *testing.T) {
			fixture := t013NewFixture(t)
			started, release := t013NewGate(), t013NewGate()
			returned, contextSignalled := t013NewGate(), t013NewGate()
			worker := &t014SignalSnapshotWorker{
				started: started, release: release, returned: returned, contextSignalled: contextSignalled,
			}
			engine := fixture.engine(t, worker)
			t.Cleanup(release.open)
			events := &t013Events{}
			engine.Events().Subscribe(events.record)

			id, err := engine.Submit(context.Background(), TaskRequest{
				WorkerType: WorkerTypeCLI, ProjectID: "snapshot-race", Prompt: "return during cancel signal snapshot",
			})
			if err != nil {
				t.Fatalf("Submit: %v", err)
			}
			t013AwaitGate(t, "worker", started)

			var explicitSignals atomic.Int32
			engine.mu.Lock()
			originalCancel := engine.cancels[id]
			if originalCancel == nil {
				engine.mu.Unlock()
				t.Fatalf("missing live cancel func for %s", id)
			}
			engine.cancels[id] = func() {
				explicitSignals.Add(1)
				originalCancel()
			}
			engine.mu.Unlock()

			type callbackResult struct {
				terminalHandled bool
				cancelErr       error
			}
			callbackDone := make(chan callbackResult, 1)
			engine.Events().Subscribe(func(event TaskEvent) {
				if event.Type != EventTaskCancelRequested || event.TaskID != id {
					return
				}
				task, getErr := fixture.view.Get(id)
				if getErr != nil {
					callbackDone <- callbackResult{cancelErr: getErr}
					return
				}
				terminalHandled := engine.commitFailedCrashAt(
					task, TaskStatusCancelling, "snapshot race terminal", "snapshot_race", t013At,
				)
				callbackDone <- callbackResult{
					terminalHandled: terminalHandled,
					cancelErr:       engine.Cancel(id),
				}
			})

			// Hold l.mu exclusively so Cancel owns the lifecycle stripe and is
			// forced to wait exactly at its signal-map snapshot. The worker then
			// returns and competes for that stripe before the subscriber reenters.
			engine.mu.Lock()
			cancelDone := make(chan error, 1)
			go func() { cancelDone <- engine.Cancel(id) }()
			deadline := time.Now().Add(t013Wait)
			reachedSnapshot := false
			for time.Now().Before(deadline) {
				current, getErr := fixture.view.Get(id)
				if getErr == nil && current.Status == TaskStatusCancelling {
					reachedSnapshot = true
					break
				}
				runtime.Gosched()
			}
			if !reachedSnapshot {
				engine.mu.Unlock()
				t.Fatal("Cancel did not reach durable cancelling state while signal snapshot was blocked")
			}
			release.open()
			select {
			case <-returned.ch:
			case <-time.After(t013Wait):
				engine.mu.Unlock()
				t.Fatal("worker did not return while Cancel signal snapshot was blocked")
			}
			engine.mu.Unlock()

			select {
			case cancelErr := <-cancelDone:
				if cancelErr != nil {
					t.Fatalf("outer Cancel: %v", cancelErr)
				}
			case <-time.After(t013Wait):
				t.Fatal("outer Cancel deadlocked after signal snapshot release")
			}
			select {
			case callback := <-callbackDone:
				if !callback.terminalHandled || !errors.Is(callback.cancelErr, ErrAuthorityConflict) {
					t.Fatalf("reentrant callback=%#v, want terminal handled and nested Cancel conflict", callback)
				}
			case <-time.After(t013Wait):
				t.Fatal("reentrant subscriber did not finish")
			}

			t013Close(t, engine)
			t013AwaitGate(t, "worker context signal", contextSignalled)
			if got := explicitSignals.Load(); got != 1 {
				t.Fatalf("explicit signal calls=%d, want exactly 1", got)
			}
			if got := worker.contextSignals.Load(); got != 1 {
				t.Fatalf("worker context signals=%d, want exactly 1", got)
			}
			engine.mu.RLock()
			_, cancelEntryPresent := engine.cancels[id]
			engine.mu.RUnlock()
			if cancelEntryPresent {
				t.Fatal("completed dispatch retained a cancels map entry")
			}
			if pending := t014PendingProjectionStateCount(engine); pending != 0 {
				t.Fatalf("pending lifecycle projection states=%d, want 0", pending)
			}
			if got := t013ArtifactCountByEvent(t, fixture.view, id, "task.failed_crash"); got != 1 {
				t.Fatalf("failed_crash facts=%d, want exactly 1", got)
			}
		})
	}
}

func TestDispatchPanicCommitsFromLocalStatusWhenCanonicalPreflightReadFails(t *testing.T) {
	readFault := &t014ReadFaultDriver{base: &sqlite.Driver{}}
	driverName := fmt.Sprintf("t014-read-fault-%d", t014ReadFaultDriverCounter.Add(1))
	sql.Register(driverName, readFault)

	path := filepath.ToSlash(filepath.Join(t.TempDir(), "panic-preflight.db"))
	dsn := "file:" + path + "?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000"
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	store, err := NewTaskStore(db, "t014-panic")
	if err != nil {
		t.Fatal(err)
	}
	observer, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	observer.SetMaxOpenConns(2)
	view := &TaskStore{db: observer, engineName: "t014-panic"}

	started, release := t013NewGate(), t013NewGate()
	worker := &t013ScriptedWorker{steps: []t013Step{{
		started: started, release: release, panicValue: "t014 panic after transient preflight read failure",
	}}}
	engine := New(store, WithClock(deps.NewFakeClock(t013At)), WithIDGenerator(deps.NewSequentialIDGenerator()))
	engine.RegisterWorker(WorkerTypeCLI, worker)
	events := &t013Events{}
	engine.Events().Subscribe(events.record)
	t.Cleanup(func() {
		release.open()
		ctx, cancel := context.WithTimeout(context.Background(), t013Wait)
		defer cancel()
		_ = engine.Close(ctx)
		_ = observer.Close()
		_ = db.Close()
	})

	id, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI, ProjectID: "panic-preflight", Prompt: "panic after read failure",
	})
	if err != nil || id != "id-0" {
		t.Fatalf("Submit=%q/%v, want id-0/nil", id, err)
	}
	t013AwaitGate(t, "panic worker", started)
	readFault.failNextTaskGet.Store(true)
	release.open()
	t013Close(t, engine)
	if readFault.failNextTaskGet.Load() {
		t.Fatal("panic recovery did not exercise the injected canonical preflight read failure")
	}

	final, err := view.Get(id)
	if err != nil || final.Status != TaskStatusFailedCrash || !strings.Contains(final.Error, "t014 panic") {
		t.Fatalf("task after panic/read failure=%#v err=%v, want durable failed_crash from local running status", final, err)
	}
	t013AssertTypes(t, t013EventTypes(events.snapshot(t), id),
		EventTaskCreated, EventTaskDispatched, EventTaskRunning, EventTaskFailedCrash,
	)
	if got := t013ArtifactCountByEvent(t, view, id, "task.failed_crash"); got != 1 {
		t.Fatalf("failed_crash facts=%d, want exactly 1", got)
	}
}
