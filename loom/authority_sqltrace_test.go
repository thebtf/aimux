package loom

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"modernc.org/sqlite"
)

type t004TraceEntry struct {
	ConnID int64
	Op     string
	SQL    string
}

type t004TraceDriver struct {
	base driver.Driver

	mu                sync.Mutex
	nextConnID        int64
	entries           []t004TraceEntry
	cancelAfterSelect context.CancelFunc
	failCommit        bool
	failRollback      bool
}

func (d *t004TraceDriver) Open(name string) (driver.Conn, error) {
	connection, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	if _, ok := connection.(driver.ExecerContext); !ok {
		_ = connection.Close()
		return nil, fmt.Errorf("T004_TRACE_DRIVER_CAPABILITY: %T lacks driver.ExecerContext", connection)
	}
	if _, ok := connection.(driver.QueryerContext); !ok {
		_ = connection.Close()
		return nil, fmt.Errorf("T004_TRACE_DRIVER_CAPABILITY: %T lacks driver.QueryerContext", connection)
	}
	d.mu.Lock()
	d.nextConnID++
	id := d.nextConnID
	d.mu.Unlock()
	return &t004TraceConn{Conn: connection, owner: d, id: id}, nil
}

func t004TraceOperation(query string) string {
	normalized := strings.ToUpper(strings.Join(strings.Fields(query), " "))
	for _, prefix := range []string{"PRAGMA", "BEGIN IMMEDIATE", "BEGIN", "SELECT", "UPDATE", "INSERT", "DELETE", "COMMIT", "ROLLBACK"} {
		if strings.HasPrefix(normalized, prefix) {
			return prefix
		}
	}
	return "OTHER"
}

func (d *t004TraceDriver) record(id int64, query string) {
	d.mu.Lock()
	d.entries = append(d.entries, t004TraceEntry{ConnID: id, Op: t004TraceOperation(query), SQL: strings.Join(strings.Fields(query), " ")})
	d.mu.Unlock()
}

func (d *t004TraceDriver) Reset() {
	d.mu.Lock()
	d.entries = nil
	d.cancelAfterSelect = nil
	d.failCommit = false
	d.failRollback = false
	d.mu.Unlock()
}

func (d *t004TraceDriver) Snapshot() []t004TraceEntry {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]t004TraceEntry(nil), d.entries...)
}

func (d *t004TraceDriver) ArmCancelAfterSelect(cancel context.CancelFunc) {
	d.mu.Lock()
	d.cancelAfterSelect = cancel
	d.mu.Unlock()
}

func (d *t004TraceDriver) cancelSnapshotConsumed() {
	d.mu.Lock()
	cancel := d.cancelAfterSelect
	d.cancelAfterSelect = nil
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (d *t004TraceDriver) ArmCommitFailure() {
	d.mu.Lock()
	d.failCommit = true
	d.mu.Unlock()
}

func (d *t004TraceDriver) ArmRollbackFailure() {
	d.mu.Lock()
	d.failRollback = true
	d.mu.Unlock()
}

func (d *t004TraceDriver) consumeFailure(op string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch op {
	case "COMMIT":
		if d.failCommit {
			d.failCommit = false
			return errorsT004TraceCommit
		}
	case "ROLLBACK":
		if d.failRollback {
			d.failRollback = false
			return errorsT004TraceRollback
		}
	}
	return nil
}

var (
	errorsT004TraceCommit   = fmt.Errorf("T004_INJECTED_COMMIT_FAILURE")
	errorsT004TraceRollback = fmt.Errorf("T004_INJECTED_ROLLBACK_FAILURE")
)

type t004TraceConn struct {
	driver.Conn
	owner *t004TraceDriver
	id    int64
}

func (c *t004TraceConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.owner.record(c.id, query)
	if err := c.owner.consumeFailure(t004TraceOperation(query)); err != nil {
		return nil, err
	}
	if executor, ok := c.Conn.(driver.ExecerContext); ok {
		return executor.ExecContext(ctx, query, args)
	}
	return nil, fmt.Errorf("T004_TRACE_DRIVER_CAPABILITY: %T lost driver.ExecerContext", c.Conn)
}

func (c *t004TraceConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.owner.record(c.id, query)
	querier, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, fmt.Errorf("T004_TRACE_DRIVER_CAPABILITY: %T lost driver.QueryerContext", c.Conn)
	}
	rows, err := querier.QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return &t004TraceRows{Rows: rows, owner: c.owner, selectQuery: t004TraceOperation(query) == "SELECT"}, nil
}

func (c *t004TraceConn) Begin() (driver.Tx, error) {
	c.owner.record(c.id, "BEGIN_TX_API")
	return c.Conn.Begin()
}

func (c *t004TraceConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.owner.record(c.id, "BEGIN_TX_API")
	if beginner, ok := c.Conn.(driver.ConnBeginTx); ok {
		return beginner.BeginTx(ctx, opts)
	}
	return nil, driver.ErrSkip
}

func (c *t004TraceConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (c *t004TraceConn) Ping(ctx context.Context) error {
	if pinger, ok := c.Conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (c *t004TraceConn) ResetSession(ctx context.Context) error {
	if resetter, ok := c.Conn.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (c *t004TraceConn) IsValid() bool {
	if validator, ok := c.Conn.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

type t004TraceRows struct {
	driver.Rows
	owner       *t004TraceDriver
	selectQuery bool
	once        sync.Once
}

func (r *t004TraceRows) Close() error {
	err := r.Rows.Close()
	if r.selectQuery {
		r.once.Do(r.owner.cancelSnapshotConsumed)
	}
	return err
}

var t004TraceDriverCounter atomic.Uint64

func t004OpenTraceStore(t *testing.T) (*TaskStore, *sql.DB, *t004TraceDriver) {
	t.Helper()
	trace := &t004TraceDriver{base: &sqlite.Driver{}}
	driverName := fmt.Sprintf("t004-sqlite-trace-%d", t004TraceDriverCounter.Add(1))
	sql.Register(driverName, trace)
	path := filepath.Join(t.TempDir(), "authority-trace.db")
	db, err := sql.Open(driverName, path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	t004Must(t, err)
	db.SetMaxOpenConns(4)
	if db.Stats().MaxOpenConnections < 3 {
		t.Fatalf("trace pool MaxOpenConnections=%d want>=3", db.Stats().MaxOpenConnections)
	}
	_, err = db.Exec(`PRAGMA foreign_keys=ON`)
	t004Must(t, err)
	store, err := NewTaskStore(db, "t004-trace")
	if err != nil {
		t.Fatalf("NewTaskStore(trace): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store, db, trace
}

func t004AssertSingleConnection(t *testing.T, entries []t004TraceEntry) {
	t.Helper()
	if len(entries) == 0 {
		t.Error("empty authority SQL trace")
		return
	}
	want := entries[0].ConnID
	for _, entry := range entries {
		if entry.ConnID != want {
			t.Errorf("authority SQL escaped pinned connection: %#v", entries)
			return
		}
	}
}

func t004TraceOps(entries []t004TraceEntry) []string {
	result := make([]string, len(entries))
	for index, entry := range entries {
		result[index] = entry.Op
	}
	return result
}

func t004AssertConflictTrace(t *testing.T, entries []t004TraceEntry) {
	t.Helper()
	t004AssertSingleConnection(t, entries)
	ops := t004TraceOps(entries)
	if len(ops) < 5 || ops[0] != "PRAGMA" || ops[1] != "PRAGMA" || ops[2] != "BEGIN IMMEDIATE" || ops[len(ops)-1] != "ROLLBACK" {
		t.Errorf("conflict trace ops=%v want foreign-key PRAGMA, busy-timeout PRAGMA, BEGIN IMMEDIATE, SELECT(s), ROLLBACK", ops)
	}
	seenSelect := false
	for index, op := range ops {
		if op == "SELECT" {
			seenSelect = true
		}
		if op == "UPDATE" || op == "INSERT" || op == "DELETE" {
			t.Errorf("conflict trace mutated before rollback at %d: %v", index, ops)
		}
		if op == "ROLLBACK" && index != len(ops)-1 {
			t.Errorf("post-rollback SQL at %d: %v", index, ops)
		}
	}
	if !seenSelect {
		t.Errorf("conflict trace has no pre-write SELECT: %v", ops)
	}
}

func TestTaskAuthority_TransactionTraceConflict(t *testing.T) {
	store, db, trace := t004OpenTraceStore(t)
	t004SeedTask(t, store, "trace-conflict", t004Dispatched)
	_, err := db.Exec(`CREATE TRIGGER t004_trace_abort BEFORE UPDATE OF status ON tasks
		WHEN OLD.id='trace-conflict' BEGIN SELECT RAISE(ABORT,'T004_TRACE_WRITE_FIRED'); END`)
	t004Must(t, err)
	fixture := &t004Fixture{store: store, target: store, id: "trace-conflict", action: "trace-conflict-action"}
	command := t004DefaultCommand("CommitCompleted", fixture.id, fixture.action)
	command.ExpectedTaskStatus = t004Running
	before := t004ReadState(t, db)
	trace.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	trace.ArmCancelAfterSelect(cancel)
	result, invokeErr := t004Invoke(ctx, store, command)
	entries := trace.Snapshot() // Snapshot before any verification query pollutes the trace.
	if !t004IsConflict(invokeErr) {
		t.Errorf("trace conflict error=%v", invokeErr)
	}
	t004AssertConflict(t, result, fixture.id, "task_status")
	t004AssertConflictTrace(t, entries)
	t004AssertStateEqual(t, t004ReadState(t, db), before)
}

func TestTaskAuthority_TransactionTraceActionConflictSkipsAbortTrigger(t *testing.T) {
	store, db, trace := t004OpenTraceStore(t)
	t004SeedTask(t, store, "trace-action-conflict", t004Running)
	t004SeedAction(t, db, "trace-seed-action", "trace-action-conflict", "provider-trace-attempt", 7, "pending")
	_, err := db.Exec(`CREATE TRIGGER t004_trace_action_abort BEFORE INSERT ON pending_actions
		WHEN NEW.task_id='trace-action-conflict' BEGIN SELECT RAISE(ABORT,'T004_TRACE_ACTION_WRITE_FIRED'); END`)
	t004Must(t, err)
	fixture := &t004Fixture{store: store, target: store, id: "trace-action-conflict", action: "trace-attempt"}
	command := t004InputCommand(fixture)
	command.ProviderRequestID = "provider-trace-attempt"
	trace.Reset()
	result, invokeErr := t004Invoke(context.Background(), store, command)
	entries := trace.Snapshot()
	if !t004IsConflict(invokeErr) {
		t.Errorf("action conflict error=%v", invokeErr)
	}
	t004AssertConflict(t, result, fixture.id, "provider_correlation")
	t004AssertConflictTrace(t, entries)
}

func TestTaskAuthority_TransactionTraceSuccess(t *testing.T) {
	store, db, trace := t004OpenTraceStore(t)
	t004SeedTask(t, store, "trace-success", t004Running)
	fixture := &t004Fixture{store: store, target: store, id: "trace-success", action: "trace-success-action"}
	command := t004DefaultCommand("CommitCompleted", fixture.id, fixture.action)
	command.ExpectedTaskStatus = t004Running
	trace.Reset()
	result, invokeErr := t004Invoke(context.Background(), store, command)
	entries := trace.Snapshot()
	t004RequireApplied(t, result, invokeErr)
	t004AssertSingleConnection(t, entries)
	ops := t004TraceOps(entries)
	if len(ops) < 7 || ops[0] != "PRAGMA" || ops[1] != "PRAGMA" || ops[2] != "BEGIN IMMEDIATE" || ops[len(ops)-1] != "COMMIT" {
		t.Errorf("success trace=%v want foreign-key PRAGMA, busy-timeout PRAGMA, BEGIN IMMEDIATE, SELECT(s), writes, COMMIT", ops)
	}
	seenSelect, seenUpdate, seenInsert := false, false, false
	for _, op := range ops {
		seenSelect = seenSelect || op == "SELECT"
		seenUpdate = seenUpdate || op == "UPDATE"
		seenInsert = seenInsert || op == "INSERT"
	}
	if !seenSelect || !seenUpdate || !seenInsert {
		t.Errorf("success trace missing select/update/insert: %v", ops)
	}
	_ = db
}

func TestTaskAuthority_TransactionTraceValidationAndNotFound(t *testing.T) {
	store, _, trace := t004OpenTraceStore(t)
	t004SeedTask(t, store, "trace-validation", t004Pending)
	fixture := &t004Fixture{store: store, target: store, id: "trace-validation", action: "trace-validation-action"}
	invalid := t004DefaultCommand("CommitDispatched", fixture.id, fixture.action)
	invalid.ExpectedTaskStatus = t004Running
	trace.Reset()
	result, invokeErr := t004Invoke(context.Background(), store, invalid)
	entries := trace.Snapshot()
	if invokeErr == nil {
		t.Errorf("invalid command succeeded")
	}
	t004AssertZeroResult(t, result)
	if len(entries) != 0 {
		t.Errorf("validation executed SQL: %#v", entries)
	}

	notFound := t004DefaultCommand("CommitCompleted", "trace-missing", "trace-missing-action")
	notFound.ExpectedTaskStatus = t004Running
	trace.Reset()
	result, invokeErr = t004Invoke(context.Background(), store, notFound)
	entries = trace.Snapshot()
	if invokeErr == nil || !strings.Contains(strings.ToLower(invokeErr.Error()), "not found") {
		t.Errorf("not-found error=%v", invokeErr)
	}
	t004AssertZeroResult(t, result)
	t004AssertConflictTrace(t, entries)
}

func TestTaskAuthority_CommitAndRollbackFailuresReturnZero(t *testing.T) {
	t.Run("commit", func(t *testing.T) {
		store, db, trace := t004OpenTraceStore(t)
		t004SeedTask(t, store, "trace-commit-failure", t004Running)
		fixture := &t004Fixture{store: store, target: store, id: "trace-commit-failure", action: "trace-commit-failure-action"}
		before := t004ReadState(t, db)
		command := t004DefaultCommand("CommitCompleted", fixture.id, fixture.action)
		command.ExpectedTaskStatus = t004Running
		trace.Reset()
		trace.ArmCommitFailure()
		result, invokeErr := t004Invoke(context.Background(), store, command)
		entries := trace.Snapshot()
		if invokeErr == nil || !strings.Contains(invokeErr.Error(), "T004_INJECTED_COMMIT_FAILURE") {
			t.Errorf("commit failure error=%v", invokeErr)
		}
		t004AssertZeroResult(t, result)
		if ops := t004TraceOps(entries); len(ops) < 2 || ops[len(ops)-2] != "COMMIT" || ops[len(ops)-1] != "ROLLBACK" {
			t.Errorf("commit failure trace=%v want COMMIT,ROLLBACK tail", ops)
		}
		t004AssertStateEqual(t, t004ReadState(t, db), before)
	})

	t.Run("rollback", func(t *testing.T) {
		store, _, trace := t004OpenTraceStore(t)
		t004SeedTask(t, store, "trace-rollback-failure", t004Dispatched)
		fixture := &t004Fixture{store: store, target: store, id: "trace-rollback-failure", action: "trace-rollback-failure-action"}
		command := t004DefaultCommand("CommitCompleted", fixture.id, fixture.action)
		command.ExpectedTaskStatus = t004Running
		trace.Reset()
		trace.ArmRollbackFailure()
		result, invokeErr := t004Invoke(context.Background(), store, command)
		entries := trace.Snapshot()
		if invokeErr == nil || !strings.Contains(invokeErr.Error(), "T004_INJECTED_ROLLBACK_FAILURE") {
			t.Errorf("rollback failure error=%v", invokeErr)
		}
		t004AssertZeroResult(t, result)
		if ops := t004TraceOps(entries); len(ops) == 0 || ops[len(ops)-1] != "ROLLBACK" {
			t.Errorf("rollback failure trace=%v", ops)
		}
	})
}

func TestTaskAuthority_InfrastructureFailureReturnsZero(t *testing.T) {
	store, db, trace := t004OpenTraceStore(t)
	t004SeedTask(t, store, "trace-infrastructure", t004Running)
	t004Must(t, db.Close())
	trace.Reset()
	command := t004DefaultCommand("CommitCompleted", "trace-infrastructure", "trace-infrastructure-action")
	command.ExpectedTaskStatus = t004Running
	result, invokeErr := t004Invoke(context.Background(), store, command)
	if invokeErr == nil {
		t.Errorf("closed-db command succeeded")
	}
	t004AssertZeroResult(t, result)
	if entries := trace.Snapshot(); len(entries) != 0 {
		t.Errorf("closed-db trace=%#v", entries)
	}
}

func TestT004TraceDriverContract(t *testing.T) {
	var _ driver.Driver = (*t004TraceDriver)(nil)
	var _ driver.ExecerContext = (*t004TraceConn)(nil)
	var _ driver.QueryerContext = (*t004TraceConn)(nil)
	var _ driver.ConnBeginTx = (*t004TraceConn)(nil)
	if reflect.TypeOf((&t004TraceDriver{}).Open).NumIn() != 1 {
		t.Fatal("trace driver Open wrapper shape changed")
	}
}
