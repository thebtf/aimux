package loom

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

const (
	t005ProjectKey = "sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	t005Bearer     = "Bearer abcdefghijklmnopqrstuvwxyz0123456789"
)

var (
	_ AuthorityConflictKind = ConflictTaskStatus
	_ AuthorityConflictKind = ConflictActionID
	_ AuthorityConflictKind = ConflictProviderCorrelation
	_ AuthorityConflictKind = ConflictActionMissing
	_ AuthorityConflictKind = ConflictDispatchTime
	_ AuthorityConflictKind = ConflictStopEvidenceTime
	_ AuthorityConflictKind = ConflictActionOwner
	_ AuthorityConflictKind = ConflictActionSourceStatus
	_ AuthorityConflictKind = ConflictConnectionGeneration
)

func TestAuthorityConflictCanonicalNamesAndCompatibilityAliases(t *testing.T) {
	tests := []struct {
		canonical     AuthorityConflictKind
		compatibility AuthorityConflictKind
		value         AuthorityConflictKind
	}{
		{ConflictTaskStatus, AuthorityConflictTaskStatus, "task_status"},
		{ConflictActionID, AuthorityConflictActionID, "action_id"},
		{ConflictProviderCorrelation, AuthorityConflictProviderCorrelation, "provider_correlation"},
		{ConflictActionMissing, AuthorityConflictActionMissing, "action_missing"},
		{ConflictDispatchTime, AuthorityConflictDispatchTime, "dispatch_time"},
		{ConflictStopEvidenceTime, AuthorityConflictStopEvidenceTime, "stop_evidence_time"},
		{ConflictActionOwner, AuthorityConflictActionOwner, "action_owner"},
		{ConflictActionSourceStatus, AuthorityConflictActionSourceStatus, "action_source_status"},
		{ConflictConnectionGeneration, AuthorityConflictConnectionGeneration, "connection_generation"},
	}
	for _, testCase := range tests {
		if testCase.canonical != testCase.value {
			t.Errorf("canonical=%q want=%q", testCase.canonical, testCase.value)
		}
		if testCase.compatibility != testCase.canonical {
			t.Errorf("compatibility=%q canonical=%q", testCase.compatibility, testCase.canonical)
		}
	}
}

func t005AssertSecretAbsent(t *testing.T, values ...string) {
	t.Helper()
	for _, value := range values {
		for _, secret := range []string{t005ProjectKey, t005Bearer, "t005-structural-secret", "t005-private-reasoning"} {
			if strings.Contains(value, secret) {
				t.Fatalf("secret %q surfaced in %q", secret, value)
			}
		}
	}
}

func t005ArtifactPayloads(t *testing.T, store *TaskStore, taskID string) string {
	t.Helper()
	rows, err := store.db.Query(`SELECT payload_json FROM task_artifacts WHERE task_id=? ORDER BY seq`, taskID)
	t004Must(t, err)
	defer rows.Close()
	var payloads strings.Builder
	for rows.Next() {
		var payload string
		t004Must(t, rows.Scan(&payload))
		payloads.WriteString(payload)
	}
	t004Must(t, rows.Err())
	return payloads.String()
}

func TestTaskAuthority_RollbackFailureDiscardsPhysicalConnection(t *testing.T) {
	store, db, trace := t004OpenTraceStore(t)
	db.SetMaxOpenConns(1)
	t004SeedTask(t, store, "rollback-poisoned", TaskStatusDispatched)
	t004SeedTask(t, store, "rollback-fresh", TaskStatusRunning)

	trace.Reset()
	trace.ArmRollbackFailure()
	failed, err := store.CommitCompleted(context.Background(), CompleteTask{
		TaskID: "rollback-poisoned", ExpectedStatus: TaskStatusRunning,
		Result: "must-not-commit", CompletedAt: t004AuthorityAt,
	})
	if err == nil || !strings.Contains(err.Error(), "T004_INJECTED_ROLLBACK_FAILURE") {
		t.Fatalf("rollback failure error=%v", err)
	}
	if failed.Applied || failed.Winner.Task.TaskID != "" || failed.ArtifactSeq != 0 {
		t.Fatalf("rollback failure returned non-zero result: %#v", failed)
	}
	firstEntries := trace.Snapshot()
	firstConn := t005TraceConnForOperation(t, firstEntries, "ROLLBACK")

	fresh, err := store.CommitCompleted(context.Background(), CompleteTask{
		TaskID: "rollback-fresh", ExpectedStatus: TaskStatusRunning,
		Result: "healthy", CompletedAt: t004AuthorityAt.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("fresh authority call after rollback failure: %v", err)
	}
	if !fresh.Applied || fresh.Winner.Task.Status != TaskStatusCompleted {
		t.Fatalf("fresh authority result=%#v", fresh)
	}
	allEntries := trace.Snapshot()
	secondConn := t005TraceConnForOperation(t, allEntries[len(firstEntries):], "BEGIN IMMEDIATE")
	if secondConn == firstConn {
		t.Fatalf("rollback-failed physical connection reused: conn=%d", firstConn)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("pool unhealthy after discarded connection: %v", err)
	}
	if stats := db.Stats(); stats.InUse != 0 || stats.MaxOpenConnections != 1 {
		t.Fatalf("pool stats after recovery=%+v", stats)
	}
}

func t005TraceConnForOperation(t *testing.T, entries []t004TraceEntry, operation string) int64 {
	t.Helper()
	for _, entry := range entries {
		if entry.Op == operation {
			return entry.ConnID
		}
	}
	t.Fatalf("trace has no %s operation: %#v", operation, entries)
	return 0
}

func TestTaskAuthority_DurableIngressRedactsTaskErrors(t *testing.T) {
	store, _ := t004AuthorityPair(t)
	cases := []struct {
		name string
		id   string
		run  func() (CommitResult, error)
	}{
		{name: "failed", id: "redact-failed"},
		{name: "failed-crash", id: "redact-failed-crash"},
	}
	for _, tc := range cases {
		t004SeedTask(t, store, tc.id, TaskStatusRunning)
	}
	cases[0].run = func() (CommitResult, error) {
		return store.CommitFailed(context.Background(), FailTask{
			TaskID: cases[0].id, ExpectedStatus: TaskStatusRunning,
			Error: "provider rejected " + t005ProjectKey, CompletedAt: t004AuthorityAt,
		})
	}
	cases[1].run = func() (CommitResult, error) {
		return store.CommitFailedCrash(context.Background(), FailCrashedTask{
			TaskID: cases[1].id, ExpectedStatus: TaskStatusRunning,
			Error: "transport lost " + t005Bearer, CompletedAt: t004AuthorityAt,
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.run()
			if err != nil {
				t.Fatalf("commit terminal error: %v", err)
			}
			var persisted string
			t004Must(t, store.db.QueryRow(`SELECT error FROM tasks WHERE id=?`, tc.id).Scan(&persisted))
			surface, err := json.Marshal(result)
			t004Must(t, err)
			t005AssertSecretAbsent(t, persisted, result.Winner.Task.Error, string(surface), t005ArtifactPayloads(t, store, tc.id))
			if !strings.Contains(persisted, "[REDACTED]") || persisted != result.Winner.Task.Error {
				t.Fatalf("task error not consistently redacted: persisted=%q winner=%q", persisted, result.Winner.Task.Error)
			}
		})
	}
}

func TestTaskAuthority_DurableActionJSONIsStructuredAndSecretSafe(t *testing.T) {
	store, _ := t004AuthorityPair(t)
	t004SeedTask(t, store, "redact-action", TaskStatusRunning)
	requestJSON := `{"note":"` + t005ProjectKey + `","token":"t005-structural-secret","reasoning":"t005-private-reasoning"}`
	responseJSON := `{"message":"Authorization: ApiKey-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG","analysis":"t005-private-reasoning","ok":true}`
	deliveryJSON := `{"nested":{"access_token":"t005-structural-secret","scratchpad":"t005-private-reasoning"},"value":"` + t005Bearer + `"}`

	required, err := store.CommitInputRequired(context.Background(), RequireInput{
		TaskID: "redact-action", ExpectedStatus: TaskStatusRunning, OccurredAt: t004AuthorityAt,
		Action: PendingAction{
			ID: "redact-action-input", TaskID: "redact-action", Kind: "question",
			Status: ActionStatusPending, ProviderRequestID: "redact-provider", ConnectionGeneration: 7,
			RequestJSON: requestJSON, ExpiresAt: t004AuthorityAt.Add(time.Hour),
		},
	})
	if err != nil {
		t.Fatalf("CommitInputRequired: %v", err)
	}
	begun, err := store.BeginActionResponse(context.Background(), BeginResponse{
		TaskID: "redact-action", ActionID: "redact-action-input",
		ExpectedTaskStatus: TaskStatusInputRequired, ExpectedActionStatus: ActionStatusPending,
		ResponseJSON: responseJSON, ConnectionGeneration: 7, RespondedAt: t004AuthorityAt.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("BeginActionResponse: %v", err)
	}
	resolved, err := store.CommitActionResolution(context.Background(), ResolveAction{
		TaskID: "redact-action", ActionID: "redact-action-input",
		ExpectedTaskStatus: TaskStatusInputRequired, ExpectedActionStatus: ActionStatusResponding,
		Resolution: ActionStatusAnswered, NextTaskStatus: TaskStatusRunning,
		ConnectionGeneration: 7, DeliveryJSON: deliveryJSON, ResolvedAt: t004AuthorityAt.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("CommitActionResolution: %v", err)
	}

	var persistedRequest, persistedResponse, persistedDelivery string
	t004Must(t, store.db.QueryRow(`SELECT request_json,response_json,delivery_json FROM pending_actions WHERE id=?`, "redact-action-input").Scan(&persistedRequest, &persistedResponse, &persistedDelivery))
	for name, payload := range map[string]string{
		"request": persistedRequest, "response": persistedResponse, "delivery": persistedDelivery,
	} {
		var decoded any
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			t.Fatalf("%s payload no longer parses after redaction: %q: %v", name, payload, err)
		}
		if !strings.Contains(payload, "[REDACTED]") {
			t.Errorf("%s payload has no redaction marker: %q", name, payload)
		}
	}
	resultSurface, err := json.Marshal([]any{required, begun, resolved})
	t004Must(t, err)
	t005AssertSecretAbsent(t, persistedRequest, persistedResponse, persistedDelivery, string(resultSurface), t005ArtifactPayloads(t, store, "redact-action"))
}

func TestTaskAuthority_SafeActionJSONPreservesExactBytes(t *testing.T) {
	store, _ := t004AuthorityPair(t)
	t004SeedTask(t, store, "safe-json", TaskStatusRunning)
	safe := " {\n  \"prompt\" : \"continue?\", \"count\" : 1\n } "
	_, err := store.CommitInputRequired(context.Background(), RequireInput{
		TaskID: "safe-json", ExpectedStatus: TaskStatusRunning, OccurredAt: t004AuthorityAt,
		Action: PendingAction{
			ID: "safe-json-action", TaskID: "safe-json", Kind: "question", Status: ActionStatusPending,
			ProviderRequestID: "safe-json-provider", ConnectionGeneration: 8,
			RequestJSON: safe, ExpiresAt: t004AuthorityAt.Add(time.Hour),
		},
	})
	if err != nil {
		t.Fatalf("CommitInputRequired: %v", err)
	}
	var persisted string
	t004Must(t, store.db.QueryRow(`SELECT request_json FROM pending_actions WHERE id=?`, "safe-json-action").Scan(&persisted))
	if persisted != safe {
		t.Fatalf("safe JSON bytes changed: got=%q want=%q", persisted, safe)
	}
}

func TestSanitizeAuthorityJSON_RejectsDuplicateObjectKeys(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "ordinary", raw: `{"note":"first","note":"second"}`},
		{name: "sensitive earlier", raw: `{"note":"` + t005ProjectKey + `","note":"safe"}`},
		{name: "sensitive later", raw: `{"note":"safe","note":"` + t005ProjectKey + `"}`},
		{name: "nested object", raw: `{"outer":{"note":"first","note":"second"}}`},
		{name: "object in array", raw: `{"outer":[{"note":"first","note":"second"}]}`},
		{name: "escaped equivalent", raw: `{"note":"first","\u006eote":"second"}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := sanitizeAuthorityJSON(testCase.raw)
			if err == nil {
				t.Fatalf("sanitizeAuthorityJSON(%q)=%q, want duplicate-key error", testCase.raw, got)
			}
			if got != "" {
				t.Fatalf("sanitizeAuthorityJSON(%q) returned content on rejection: %q", testCase.raw, got)
			}
			if !strings.Contains(err.Error(), "duplicate object key") {
				t.Fatalf("sanitizeAuthorityJSON(%q) error=%v", testCase.raw, err)
			}
		})
	}
}

func TestSanitizeAuthorityJSON_MalformedAndTrailingSemanticsRemain(t *testing.T) {
	if _, err := sanitizeAuthorityJSON(`{"note":`); err == nil || !strings.Contains(err.Error(), "invalid JSON:") {
		t.Fatalf("malformed error=%v", err)
	}
	if _, err := sanitizeAuthorityJSON(`{"note":"safe"} {"next":"value"}`); err == nil || err.Error() != "invalid JSON: multiple values" {
		t.Fatalf("trailing-value error=%v", err)
	}
}

func TestTaskAuthority_ConflictWinnerRedactsLegacyErrorOnly(t *testing.T) {
	store, _ := t004AuthorityPair(t)
	t004SeedTask(t, store, "legacy-error-winner", TaskStatusRunning)
	const result = "caller-owned-result-semantics"
	rawError := "legacy failure " + t005ProjectKey
	_, err := store.db.Exec(
		`UPDATE tasks SET status=?,result=?,error=?,completed_at=? WHERE id=?`,
		TaskStatusCompleted,
		result,
		rawError,
		t004AuthorityAt,
		"legacy-error-winner",
	)
	t004Must(t, err)

	winner, err := store.CommitCompleted(context.Background(), CompleteTask{
		TaskID: "legacy-error-winner", ExpectedStatus: TaskStatusRunning,
		Result: "must-not-commit", CompletedAt: t004AuthorityAt,
	})
	if !errors.Is(err, ErrAuthorityConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	if winner.Winner.Task.Error != "legacy failure [REDACTED]" {
		t.Fatalf("winner error=%q", winner.Winner.Task.Error)
	}
	if winner.Winner.Task.Result != result {
		t.Fatalf("winner result changed: got=%q want=%q", winner.Winner.Task.Result, result)
	}
	surface, err := json.Marshal(winner)
	t004Must(t, err)
	t005AssertSecretAbsent(t, string(surface))
}

type t005PanicDriver struct {
	base driver.Driver

	panicNextAuthorityWrite atomic.Bool
	mu                      sync.Mutex
	connections             []driver.Conn
}

func (d *t005PanicDriver) Open(name string) (driver.Conn, error) {
	connection, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	if _, ok := connection.(driver.ExecerContext); !ok {
		_ = connection.Close()
		return nil, fmt.Errorf("T005_PANIC_DRIVER_CAPABILITY: %T lacks driver.ExecerContext", connection)
	}
	if _, ok := connection.(driver.QueryerContext); !ok {
		_ = connection.Close()
		return nil, fmt.Errorf("T005_PANIC_DRIVER_CAPABILITY: %T lacks driver.QueryerContext", connection)
	}
	d.mu.Lock()
	d.connections = append(d.connections, connection)
	d.mu.Unlock()
	return &t005PanicConn{Conn: connection, owner: d}, nil
}

func (d *t005PanicDriver) forceClose() {
	d.mu.Lock()
	connections := append([]driver.Conn(nil), d.connections...)
	d.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}

type t005PanicConn struct {
	driver.Conn
	owner *t005PanicDriver
}

func (c *t005PanicConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	normalized := strings.ToUpper(strings.TrimSpace(query))
	result, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
	if err != nil {
		return result, err
	}
	if strings.HasPrefix(normalized, "UPDATE TASKS SET STATUS=") && c.owner.panicNextAuthorityWrite.CompareAndSwap(true, false) {
		panic("T005_INJECTED_AUTHORITY_PANIC")
	}
	return result, nil
}

func (c *t005PanicConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *t005PanicConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

var t005PanicDriverCounter atomic.Uint64

func t005OpenPanicStore(t *testing.T) (*TaskStore, *sql.DB, *t005PanicDriver) {
	t.Helper()
	panicDriver := &t005PanicDriver{base: &sqlite.Driver{}}
	driverName := fmt.Sprintf("t005-sqlite-panic-%d", t005PanicDriverCounter.Add(1))
	sql.Register(driverName, panicDriver)
	path := filepath.Join(t.TempDir(), "authority-panic.db")
	db, err := sql.Open(driverName, path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	t004Must(t, err)
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA foreign_keys=ON`)
	t004Must(t, err)
	store, err := NewTaskStore(db, "t005-panic")
	if err != nil {
		t.Fatalf("NewTaskStore(panic): %v", err)
	}
	t.Cleanup(func() {
		panicDriver.forceClose()
		_ = db.Close()
	})
	return store, db, panicDriver
}

func TestTaskAuthority_PanicAfterBeginRollsBackAndReleasesConnection(t *testing.T) {
	store, db, panicDriver := t005OpenPanicStore(t)
	t004SeedTask(t, store, "panic-first", TaskStatusRunning)
	t004SeedTask(t, store, "panic-fresh", TaskStatusRunning)
	type durableTaskState struct {
		status      string
		result      string
		taskError   string
		completedAt sql.NullTime
	}
	readState := func(ctx context.Context) (durableTaskState, int, error) {
		var state durableTaskState
		err := db.QueryRowContext(ctx,
			`SELECT status,result,error,completed_at FROM tasks WHERE id=?`,
			"panic-first",
		).Scan(&state.status, &state.result, &state.taskError, &state.completedAt)
		if err != nil {
			return durableTaskState{}, 0, err
		}
		var artifacts int
		err = db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM task_artifacts WHERE task_id=?`,
			"panic-first",
		).Scan(&artifacts)
		return state, artifacts, err
	}
	before, beforeArtifacts, err := readState(context.Background())
	if err != nil {
		t.Fatalf("read pre-panic durable state: %v", err)
	}
	panicDriver.panicNextAuthorityWrite.Store(true)

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = store.CommitCompleted(context.Background(), CompleteTask{
			TaskID: "panic-first", ExpectedStatus: TaskStatusRunning,
			Result: "must-not-commit", CompletedAt: t004AuthorityAt,
		})
	}()
	if recovered != "T005_INJECTED_AUTHORITY_PANIC" {
		t.Fatalf("recovered panic=%v", recovered)
	}
	stateCtx, stateCancel := context.WithTimeout(context.Background(), time.Second)
	after, afterArtifacts, err := readState(stateCtx)
	stateCancel()
	if err != nil {
		t.Fatalf("read post-panic durable state: %v", err)
	}
	if after != before {
		t.Fatalf("panic transaction changed task state: before=%+v after=%+v", before, after)
	}
	if afterArtifacts != beforeArtifacts {
		t.Fatalf("panic transaction changed artifact count: before=%d after=%d", beforeArtifacts, afterArtifacts)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	fresh, err := store.CommitCompleted(ctx, CompleteTask{
		TaskID: "panic-fresh", ExpectedStatus: TaskStatusRunning,
		Result: "healthy", CompletedAt: t004AuthorityAt.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("fresh authority call after panic: %v", err)
	}
	if !fresh.Applied || fresh.Winner.Task.Status != TaskStatusCompleted {
		t.Fatalf("fresh authority result=%#v", fresh)
	}
	if stats := db.Stats(); stats.InUse != 0 {
		t.Fatalf("pool still has in-use connection after panic recovery: %+v", stats)
	}
}

func TestT005PanicDriverContract(t *testing.T) {
	var _ driver.Driver = (*t005PanicDriver)(nil)
	var _ driver.ExecerContext = (*t005PanicConn)(nil)
	var _ driver.QueryerContext = (*t005PanicConn)(nil)
	var _ driver.NamedValueChecker = (*t005PanicConn)(nil)
}
