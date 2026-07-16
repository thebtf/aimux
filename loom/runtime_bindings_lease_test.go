package loom

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func t003LeaseOpenStore(t *testing.T) (*TaskStore, *sql.DB, *time.Time) {
	t.Helper()
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "lease.db"))
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=25")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })

	store, err := NewTaskStore(db, "t003-lease")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2037, 3, 4, 5, 6, 7, 0, time.UTC)
	store.now = func() time.Time { return now }
	return store, db, &now
}

func t003LeaseSeed(t *testing.T, store *TaskStore, now time.Time) (ReserveWorkerRunBindingRequest, WorkerRunBindingAuthority) {
	t.Helper()
	task := makeTask("t003-lease-task", "t003-lease-project", TaskStatusRunning)
	task.TenantID = "t003-tenant"
	task.CreatedAt = now.Add(-time.Minute)
	if err := store.Create(task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	request := ReserveWorkerRunBindingRequest{
		BindingID:             "t003-lease-binding",
		TaskID:                task.ID,
		WorkerSessionID:       "t003-lease-session",
		TenantID:              "t003-tenant",
		ProjectID:             task.ProjectID,
		CanonicalWorktreeRoot: "D:/work/t003-lease",
		ProfileFingerprint:    "profile-t003",
		CapabilityFingerprint: "capability-t003",
		RequestedMode:         RuntimeBindingModeNew,
		ExecutorName:          "codex",
		SwarmScope:            "t003-scope",
		LeaseOwner:            "owner-a",
		LeaseTTL:              time.Minute,
	}
	if _, err := store.ReserveWorkerRunBinding(context.Background(), request); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	return request, WorkerRunBindingAuthority{BindingID: request.BindingID, LeaseOwner: request.LeaseOwner, LeaseGeneration: 1}
}

func t003LeaseBinding(t *testing.T, store *TaskStore, id, tenantID string) *WorkerRunBindingRecord {
	t.Helper()
	binding, err := store.GetWorkerRunBinding(context.Background(), id, tenantID)
	if err != nil {
		t.Fatalf("get binding %q: %v", id, err)
	}
	return binding
}

func t003LeaseSession(t *testing.T, store *TaskStore, id, tenantID string) *WorkerSessionRecord {
	t.Helper()
	session, err := store.GetWorkerSession(context.Background(), id, tenantID)
	if err != nil {
		t.Fatalf("get session %q: %v", id, err)
	}
	return session
}

func t003LeaseRequireActive(t *testing.T, binding *WorkerRunBindingRecord, owner string, generation int64, expiresAt time.Time) {
	t.Helper()
	if binding.State == WorkerRunBindingStateTerminal || binding.LeaseOwner == nil || *binding.LeaseOwner != owner || binding.LeaseGeneration != generation || binding.LeaseExpiresAt == nil || !binding.LeaseExpiresAt.Equal(expiresAt) {
		t.Fatalf("binding authority = state=%q owner=%v generation=%d expires=%v, want active owner=%q generation=%d expires=%s", binding.State, binding.LeaseOwner, binding.LeaseGeneration, binding.LeaseExpiresAt, owner, generation, expiresAt)
	}
}

func t003LeaseRequireSession(t *testing.T, session *WorkerSessionRecord, taskID, owner string, generation int64, expiresAt time.Time) {
	t.Helper()
	if session.State != WorkerSessionStateLeased || session.ActiveTaskID == nil || *session.ActiveTaskID != taskID || session.LeaseOwner == nil || *session.LeaseOwner != owner || session.LeaseGeneration != generation || session.LeaseExpiresAt == nil || !session.LeaseExpiresAt.Equal(expiresAt) {
		t.Fatalf("session authority = state=%q task=%v owner=%v generation=%d expires=%v, want leased task=%q owner=%q generation=%d expires=%s", session.State, session.ActiveTaskID, session.LeaseOwner, session.LeaseGeneration, session.LeaseExpiresAt, taskID, owner, generation, expiresAt)
	}
}

func TestTaskStore_RenewWorkerRunBindingLease_ExactUnexpiredAuthorityOnly(t *testing.T) {
	store, _, now := t003LeaseOpenStore(t)
	request, authority := t003LeaseSeed(t, store, *now)
	initialExpiry := now.Add(request.LeaseTTL)

	*now = now.Add(20 * time.Second)
	renewalTTL := 2 * time.Minute
	if _, err := store.RenewWorkerRunBindingLease(context.Background(), RenewWorkerRunBindingLeaseRequest{Authority: authority, LeaseTTL: renewalTTL}); err != nil {
		t.Fatalf("renew exact authority: %v", err)
	}
	expectedExpiry := now.Add(renewalTTL)
	t003LeaseRequireActive(t, t003LeaseBinding(t, store, request.BindingID, request.TenantID), authority.LeaseOwner, authority.LeaseGeneration, expectedExpiry)
	t003LeaseRequireSession(t, t003LeaseSession(t, store, request.WorkerSessionID, request.TenantID), request.TaskID, authority.LeaseOwner, authority.LeaseGeneration, expectedExpiry)

	for _, stale := range []WorkerRunBindingAuthority{
		{BindingID: authority.BindingID, LeaseOwner: "other-owner", LeaseGeneration: authority.LeaseGeneration},
		{BindingID: authority.BindingID, LeaseOwner: authority.LeaseOwner, LeaseGeneration: authority.LeaseGeneration + 1},
	} {
		if _, err := store.RenewWorkerRunBindingLease(context.Background(), RenewWorkerRunBindingLeaseRequest{Authority: stale, LeaseTTL: time.Hour}); err == nil {
			t.Fatalf("renew stale authority %+v succeeded", stale)
		}
		t003LeaseRequireActive(t, t003LeaseBinding(t, store, request.BindingID, request.TenantID), authority.LeaseOwner, authority.LeaseGeneration, expectedExpiry)
	}

	*now = expectedExpiry.Add(time.Nanosecond)
	if _, err := store.RenewWorkerRunBindingLease(context.Background(), RenewWorkerRunBindingLeaseRequest{Authority: authority, LeaseTTL: time.Hour}); err == nil {
		t.Fatal("renew expired authority succeeded")
	}
	t003LeaseRequireActive(t, t003LeaseBinding(t, store, request.BindingID, request.TenantID), authority.LeaseOwner, authority.LeaseGeneration, expectedExpiry)
	if initialExpiry.Equal(expectedExpiry) {
		t.Fatal("renewal did not extend the lease")
	}
}

func TestTaskStore_TakeoverWorkerRunBindingLease_ExpiryFencingAndSingleWinner(t *testing.T) {
	store, _, now := t003LeaseOpenStore(t)
	request, authority := t003LeaseSeed(t, store, *now)
	initialExpiry := now.Add(request.LeaseTTL)

	takeover := TakeoverWorkerRunBindingLeaseRequest{BindingID: request.BindingID, NewLeaseOwner: "owner-b", ExpectedLeaseGeneration: authority.LeaseGeneration, LeaseTTL: 2 * time.Minute}
	if _, err := store.TakeoverWorkerRunBindingLease(context.Background(), takeover); err == nil {
		t.Fatal("takeover before expiry succeeded")
	}
	t003LeaseRequireActive(t, t003LeaseBinding(t, store, request.BindingID, request.TenantID), authority.LeaseOwner, authority.LeaseGeneration, initialExpiry)

	*now = initialExpiry
	if _, err := store.TakeoverWorkerRunBindingLease(context.Background(), takeover); err != nil {
		t.Fatalf("takeover after expiry: %v", err)
	}
	newExpiry := now.Add(takeover.LeaseTTL)
	t003LeaseRequireActive(t, t003LeaseBinding(t, store, request.BindingID, request.TenantID), takeover.NewLeaseOwner, authority.LeaseGeneration+1, newExpiry)
	t003LeaseRequireSession(t, t003LeaseSession(t, store, request.WorkerSessionID, request.TenantID), request.TaskID, takeover.NewLeaseOwner, authority.LeaseGeneration+1, newExpiry)

	*now = newExpiry
	owners := []string{"owner-c", "owner-d"}
	results := make(chan error, len(owners))
	var started sync.WaitGroup
	started.Add(len(owners))
	for _, owner := range owners {
		go func(owner string) {
			started.Done()
			_, err := store.TakeoverWorkerRunBindingLease(context.Background(), TakeoverWorkerRunBindingLeaseRequest{BindingID: request.BindingID, NewLeaseOwner: owner, ExpectedLeaseGeneration: authority.LeaseGeneration + 1, LeaseTTL: time.Minute})
			results <- err
		}(owner)
	}
	started.Wait()
	var wins int
	for range owners {
		if <-results == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent takeovers won %d times, want 1", wins)
	}
	binding := t003LeaseBinding(t, store, request.BindingID, request.TenantID)
	if binding.LeaseOwner == nil || (*binding.LeaseOwner != owners[0] && *binding.LeaseOwner != owners[1]) || binding.LeaseGeneration != authority.LeaseGeneration+2 {
		t.Fatalf("concurrent winner binding = owner=%v generation=%d", binding.LeaseOwner, binding.LeaseGeneration)
	}
}

func TestTaskStore_WorkerRunBindingLease_TakeoverFencesOldAuthorityAndFinalizesOnce(t *testing.T) {
	store, _, now := t003LeaseOpenStore(t)
	request, oldAuthority := t003LeaseSeed(t, store, *now)
	*now = now.Add(request.LeaseTTL + time.Nanosecond)
	currentAuthority := WorkerRunBindingAuthority{BindingID: request.BindingID, LeaseOwner: "owner-b", LeaseGeneration: oldAuthority.LeaseGeneration + 1}
	if _, err := store.TakeoverWorkerRunBindingLease(context.Background(), TakeoverWorkerRunBindingLeaseRequest{BindingID: request.BindingID, NewLeaseOwner: currentAuthority.LeaseOwner, ExpectedLeaseGeneration: oldAuthority.LeaseGeneration, LeaseTTL: time.Minute}); err != nil {
		t.Fatalf("takeover: %v", err)
	}

	if _, err := store.RenewWorkerRunBindingLease(context.Background(), RenewWorkerRunBindingLeaseRequest{Authority: oldAuthority, LeaseTTL: time.Hour}); err == nil {
		t.Fatal("old authority renewed after takeover")
	}
	if _, err := store.FinalizeWorkerRunBinding(context.Background(), FinalizeWorkerRunBindingRequest{Authority: oldAuthority, TerminalReason: "stale"}); err == nil {
		t.Fatal("old authority finalized after takeover")
	}
	if _, err := store.FinalizeWorkerRunBinding(context.Background(), FinalizeWorkerRunBindingRequest{Authority: currentAuthority, TerminalReason: "completed"}); err != nil {
		t.Fatalf("current authority finalize: %v", err)
	}
	binding := t003LeaseBinding(t, store, request.BindingID, request.TenantID)
	if binding.State != WorkerRunBindingStateTerminal || binding.LeaseOwner != nil || binding.LeaseExpiresAt != nil || binding.TerminalReason == nil || *binding.TerminalReason != "completed" || binding.TerminalAt == nil || binding.TerminalAt.IsZero() || binding.TerminalAt.After(*now) {
		t.Fatalf("final binding = %+v", binding)
	}
	session := t003LeaseSession(t, store, request.WorkerSessionID, request.TenantID)
	if session.State != WorkerSessionStateAvailable || session.ActiveTaskID != nil || session.LeaseOwner != nil || session.LeaseExpiresAt != nil || session.LeaseGeneration != currentAuthority.LeaseGeneration {
		t.Fatalf("final session = %+v", session)
	}
}

func TestTaskStore_FinalizeWorkerRunBinding_StaleDuplicateAndBusyDoNotReplaceAuthority(t *testing.T) {
	store, db, now := t003LeaseOpenStore(t)
	request, authority := t003LeaseSeed(t, store, *now)
	initialExpiry := now.Add(request.LeaseTTL)

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("begin immediate: %v", err)
	}
	busyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := store.FinalizeWorkerRunBinding(busyCtx, FinalizeWorkerRunBindingRequest{Authority: authority, TerminalReason: "busy-winner"}); err == nil {
		t.Fatal("busy finalize succeeded")
	}
	if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatalf("rollback busy lock: %v", err)
	}
	t003LeaseRequireActive(t, t003LeaseBinding(t, store, request.BindingID, request.TenantID), authority.LeaseOwner, authority.LeaseGeneration, initialExpiry)

	if _, err := store.FinalizeWorkerRunBinding(context.Background(), FinalizeWorkerRunBindingRequest{Authority: authority, TerminalReason: "winner"}); err != nil {
		t.Fatalf("winner finalize: %v", err)
	}
	for _, stale := range []FinalizeWorkerRunBindingRequest{
		{Authority: authority, TerminalReason: "duplicate"},
		{Authority: WorkerRunBindingAuthority{BindingID: authority.BindingID, LeaseOwner: "other-owner", LeaseGeneration: authority.LeaseGeneration}, TerminalReason: "stale"},
	} {
		if _, err := store.FinalizeWorkerRunBinding(context.Background(), stale); err == nil {
			t.Fatalf("stale finalize %+v succeeded", stale)
		}
	}
	binding := t003LeaseBinding(t, store, request.BindingID, request.TenantID)
	if binding.State != WorkerRunBindingStateTerminal || binding.TerminalReason == nil || *binding.TerminalReason != "winner" || binding.LeaseOwner != nil || binding.LeaseExpiresAt != nil {
		t.Fatalf("terminal winner rewritten: %+v", binding)
	}
}

func TestTaskStore_WorkerRunBindingLease_GettersPersistExactAuthorityAndValidTimes(t *testing.T) {
	store, _, now := t003LeaseOpenStore(t)
	request, authority := t003LeaseSeed(t, store, *now)
	binding := t003LeaseBinding(t, store, request.BindingID, request.TenantID)
	session := t003LeaseSession(t, store, request.WorkerSessionID, request.TenantID)
	expiresAt := now.Add(request.LeaseTTL)
	t003LeaseRequireActive(t, binding, authority.LeaseOwner, authority.LeaseGeneration, expiresAt)
	t003LeaseRequireSession(t, session, request.TaskID, authority.LeaseOwner, authority.LeaseGeneration, expiresAt)
	for _, stamp := range []struct {
		name string
		at   *time.Time
	}{
		{"binding created", &binding.CreatedAt},
		{"binding updated", &binding.UpdatedAt},
		{"session created", &session.CreatedAt},
		{"session updated", &session.UpdatedAt},
	} {
		if stamp.at == nil || stamp.at.IsZero() || stamp.at.After(*now) {
			t.Fatalf("%s = %v, want non-zero and not future-dated", stamp.name, stamp.at)
		}
	}
	if binding.LeaseExpiresAt == nil || !binding.LeaseExpiresAt.Equal(expiresAt) || session.LeaseExpiresAt == nil || !session.LeaseExpiresAt.Equal(expiresAt) {
		t.Fatalf("persisted expiry mismatch: binding=%v session=%v want=%s", binding.LeaseExpiresAt, session.LeaseExpiresAt, expiresAt)
	}
	if binding.ID != request.BindingID || binding.TaskID != request.TaskID || binding.WorkerSessionID == nil || *binding.WorkerSessionID != request.WorkerSessionID || binding.LeaseOwner == nil || session.LeaseOwner == nil || *binding.LeaseOwner != *session.LeaseOwner {
		t.Fatalf("getter authority mismatch: binding=%+v session=%+v", binding, session)
	}
}

func TestTaskStore_FinalizeWorkerRunBinding_BlankSessionStateDefaultsAvailable(t *testing.T) {
	store, _, now := t003LeaseOpenStore(t)
	request, authority := t003LeaseSeed(t, store, *now)

	if _, err := store.FinalizeWorkerRunBinding(context.Background(), FinalizeWorkerRunBindingRequest{Authority: authority, TerminalReason: "complete"}); err != nil {
		t.Fatalf("finalize default state: %v", err)
	}
	session := t003LeaseSession(t, store, request.WorkerSessionID, request.TenantID)
	if session.State != WorkerSessionStateAvailable || session.ActiveTaskID != nil || session.LeaseOwner != nil || session.LeaseExpiresAt != nil || session.ClosedAt != nil {
		t.Fatalf("default finalized session = %#v", session)
	}
}

func TestTaskStore_FinalizeWorkerRunBinding_UnavailableSessionCannotResumeOrFork(t *testing.T) {
	store, _, now := t003LeaseOpenStore(t)
	request, authority := t003LeaseSeed(t, store, *now)

	if _, err := store.FinalizeWorkerRunBinding(context.Background(), FinalizeWorkerRunBindingRequest{
		Authority: authority, TerminalReason: "live-handle-closed", WorkerSessionState: WorkerSessionStateUnavailable,
	}); err != nil {
		t.Fatalf("finalize unavailable state: %v", err)
	}
	binding := t003LeaseBinding(t, store, request.BindingID, request.TenantID)
	if binding.State != WorkerRunBindingStateTerminal || binding.LeaseOwner != nil || binding.LeaseExpiresAt != nil || binding.TerminalReason == nil || *binding.TerminalReason != "live-handle-closed" {
		t.Fatalf("unavailable finalized binding = %#v", binding)
	}
	session := t003LeaseSession(t, store, request.WorkerSessionID, request.TenantID)
	if session.State != WorkerSessionStateUnavailable || session.ActiveTaskID != nil || session.LeaseOwner != nil || session.LeaseExpiresAt != nil || session.ClosedAt != nil {
		t.Fatalf("unavailable finalized session = %#v", session)
	}

	exact := request
	exact.BindingID = "unavailable-exact-resume"
	exact.RequestedMode = RuntimeBindingModeExactResume
	if _, err := store.ReserveWorkerRunBinding(context.Background(), exact); err == nil {
		t.Fatal("exact resume accepted unavailable session")
	}
	fork := request
	fork.BindingID = "unavailable-fork"
	fork.WorkerSessionID = "unavailable-fork-child"
	fork.ParentWorkerSessionID = request.WorkerSessionID
	fork.RequestedMode = RuntimeBindingModeFork
	if _, err := store.ReserveWorkerRunBinding(context.Background(), fork); err == nil {
		t.Fatal("fork accepted unavailable parent session")
	}
}

func TestTaskStore_FinalizeWorkerRunBinding_InvalidSessionStateIsZeroAndNonMutating(t *testing.T) {
	store, _, now := t003LeaseOpenStore(t)
	request, authority := t003LeaseSeed(t, store, *now)
	expiresAt := now.Add(request.LeaseTTL)

	got, err := store.FinalizeWorkerRunBinding(context.Background(), FinalizeWorkerRunBindingRequest{
		Authority: authority, TerminalReason: "invalid-state", WorkerSessionState: WorkerSessionStateClosed,
	})
	if err == nil {
		t.Fatal("finalize accepted closed session target")
	}
	if got != (WorkerRunBindingAuthority{}) {
		t.Fatalf("invalid finalization authority = %#v, want zero", got)
	}
	t003LeaseRequireActive(t, t003LeaseBinding(t, store, request.BindingID, request.TenantID), authority.LeaseOwner, authority.LeaseGeneration, expiresAt)
	t003LeaseRequireSession(t, t003LeaseSession(t, store, request.WorkerSessionID, request.TenantID), request.TaskID, authority.LeaseOwner, authority.LeaseGeneration, expiresAt)
}

func TestLoomEngine_GetWorkerSession_DelegatesToTaskStore(t *testing.T) {
	store, _, now := t003LeaseOpenStore(t)
	request, authority := t003LeaseSeed(t, store, *now)

	session, err := New(store).GetWorkerSession(context.Background(), request.WorkerSessionID, request.TenantID)
	if err != nil {
		t.Fatalf("engine get worker session: %v", err)
	}
	t003LeaseRequireSession(t, session, request.TaskID, authority.LeaseOwner, authority.LeaseGeneration, now.Add(request.LeaseTTL))
}
