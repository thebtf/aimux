package loom

import (
	"context"
	"testing"
)

func t003RequireLifecycleAuthority(t *testing.T, got, want WorkerRunBindingAuthority) {
	t.Helper()
	if got != want {
		t.Fatalf("authority = %#v, want %#v", got, want)
	}
}

func t003RequireStartedBinding(t *testing.T, binding *WorkerRunBindingRecord, authority WorkerRunBindingAuthority, provider ProviderSessionIdentity, connectionGeneration int64, live LiveHandleIdentity, executionID string) {
	t.Helper()
	if binding.State != WorkerRunBindingStateRunning || binding.ProviderSession == nil || *binding.ProviderSession != provider || binding.ProviderConnectionGeneration == nil || *binding.ProviderConnectionGeneration != connectionGeneration || binding.LiveHandle == nil || *binding.LiveHandle != live || binding.ExecutionID == nil || *binding.ExecutionID != executionID || binding.StartedAt == nil || binding.StartedAt.IsZero() {
		t.Fatalf("started binding = %#v", binding)
	}
	t003LeaseRequireActive(t, binding, authority.LeaseOwner, authority.LeaseGeneration, *binding.LeaseExpiresAt)
}

func t003RequireUnchangedTask(t *testing.T, store *TaskStore, taskID string, wantStatus TaskStatus, wantResult string) {
	t.Helper()
	task, err := store.Get(taskID)
	if err != nil {
		t.Fatalf("get task %q: %v", taskID, err)
	}
	if task.Status != wantStatus || task.Result != wantResult {
		t.Fatalf("task lifecycle mutation = status=%q result=%q, want status=%q result=%q", task.Status, task.Result, wantStatus, wantResult)
	}
}

func TestTaskStore_WorkerRunBindingLifecycle_StartAndReturnPreserveLeaseAndWinner(t *testing.T) {
	f := t003NewReserveFixture(t)
	req := t003ReserveRequest(RuntimeBindingModeNew, "binding-lifecycle", "session-lifecycle")
	authority, err := f.store.ReserveWorkerRunBinding(context.Background(), req)
	if err != nil {
		t.Fatalf("reserve binding: %v", err)
	}
	before, err := f.store.Get(req.TaskID)
	if err != nil {
		t.Fatalf("get task before lifecycle: %v", err)
	}

	provider := ProviderSessionIdentity{ProviderName: "codex", SessionID: "provider-session", Generation: 7}
	connectionGeneration := int64(11)
	live := LiveHandleIdentity{Scope: "lifecycle-scope", HandleID: "live-handle", HandleGeneration: 13, RegistryGeneration: 17}
	start := StartWorkerRunBindingRequest{
		Authority:                    authority,
		ProviderSession:              &provider,
		ProviderConnectionGeneration: &connectionGeneration,
		LiveHandle:                   live,
		ExecutionID:                  "execution-lifecycle",
	}
	startedAuthority, err := f.store.StartWorkerRunBinding(context.Background(), start)
	if err != nil {
		t.Fatalf("start binding: %v", err)
	}
	t003RequireLifecycleAuthority(t, startedAuthority, authority)

	started := t003LeaseBinding(t, f.store, req.BindingID)
	t003RequireStartedBinding(t, started, authority, provider, connectionGeneration, live, start.ExecutionID)
	if !started.StartedAt.Equal(f.now) {
		t.Fatalf("started_at = %s, want %s", started.StartedAt, f.now)
	}
	expiresAt := *started.LeaseExpiresAt
	session := t003LeaseSession(t, f.store, req.WorkerSessionID)
	if session.ProviderSession == nil || *session.ProviderSession != provider {
		t.Fatalf("started session provider = %#v, want %#v", session.ProviderSession, provider)
	}
	t003LeaseRequireSession(t, session, req.TaskID, authority.LeaseOwner, authority.LeaseGeneration, expiresAt)

	duplicateStart := start
	duplicateStart.ExecutionID = "duplicate-execution"
	staleOwnerStart := start
	staleOwnerStart.Authority.LeaseOwner = "stale-owner"
	staleGenerationStart := start
	staleGenerationStart.Authority.LeaseGeneration++
	for _, attempt := range []StartWorkerRunBindingRequest{duplicateStart, staleOwnerStart, staleGenerationStart} {
		if _, err := f.store.StartWorkerRunBinding(context.Background(), attempt); err == nil {
			t.Fatalf("non-winning start succeeded: %#v", attempt.Authority)
		}
		t003RequireStartedBinding(t, t003LeaseBinding(t, f.store, req.BindingID), authority, provider, connectionGeneration, live, start.ExecutionID)
	}

	process := ProcessIdentity{PID: 23, StartFingerprint: "process-start", TreeID: "process-tree"}
	returnedAuthority, err := f.store.RecordWorkerRunBindingReturned(context.Background(), ReturnWorkerRunBindingRequest{Authority: authority, Process: &process})
	if err != nil {
		t.Fatalf("record native return: %v", err)
	}
	t003RequireLifecycleAuthority(t, returnedAuthority, authority)
	returned := t003LeaseBinding(t, f.store, req.BindingID)
	if returned.State != WorkerRunBindingStateReturned || returned.Process == nil || *returned.Process != process || returned.ReturnedAt == nil || !returned.ReturnedAt.Equal(f.now) || returned.StartedAt == nil || !returned.StartedAt.Equal(f.now) {
		t.Fatalf("returned binding = %#v", returned)
	}
	t003LeaseRequireActive(t, returned, authority.LeaseOwner, authority.LeaseGeneration, expiresAt)
	t003LeaseRequireSession(t, t003LeaseSession(t, f.store, req.WorkerSessionID), req.TaskID, authority.LeaseOwner, authority.LeaseGeneration, expiresAt)

	duplicateReturn := ReturnWorkerRunBindingRequest{Authority: authority, Process: &ProcessIdentity{PID: 29, StartFingerprint: "duplicate-start", TreeID: "duplicate-tree"}}
	staleOwnerReturn := duplicateReturn
	staleOwnerReturn.Authority.LeaseOwner = "stale-owner"
	staleGenerationReturn := duplicateReturn
	staleGenerationReturn.Authority.LeaseGeneration++
	for _, attempt := range []ReturnWorkerRunBindingRequest{duplicateReturn, staleOwnerReturn, staleGenerationReturn} {
		if _, err := f.store.RecordWorkerRunBindingReturned(context.Background(), attempt); err == nil {
			t.Fatalf("non-winning return succeeded: %#v", attempt.Authority)
		}
		winner := t003LeaseBinding(t, f.store, req.BindingID)
		if winner.State != WorkerRunBindingStateReturned || winner.Process == nil || *winner.Process != process {
			t.Fatalf("returned winner rewritten: %#v", winner)
		}
		t003LeaseRequireActive(t, winner, authority.LeaseOwner, authority.LeaseGeneration, expiresAt)
	}

	t003RequireUnchangedTask(t, f.store, req.TaskID, before.Status, before.Result)
	t003RequireForeignKeyCheck(t, f.observer)
}

func TestTaskStore_WorkerRunBindingLifecycle_ExactResumeAdvancesGenerationAndFencesOldAuthority(t *testing.T) {
	f := t003NewReserveFixture(t)
	first := t003ReserveRequest(RuntimeBindingModeNew, "binding-generation-one", "session-generation")
	firstAuthority, err := f.store.ReserveWorkerRunBinding(context.Background(), first)
	if err != nil {
		t.Fatalf("reserve generation one: %v", err)
	}
	before, err := f.store.Get(first.TaskID)
	if err != nil {
		t.Fatalf("get task before generation reuse: %v", err)
	}
	if _, err := f.store.FinalizeWorkerRunBinding(context.Background(), FinalizeWorkerRunBindingRequest{Authority: firstAuthority, TerminalReason: "generation-one-complete"}); err != nil {
		t.Fatalf("finalize generation one: %v", err)
	}

	second := t003ReserveRequest(RuntimeBindingModeExactResume, "binding-generation-two", first.WorkerSessionID)
	secondAuthority, err := f.store.ReserveWorkerRunBinding(context.Background(), second)
	if err != nil {
		t.Fatalf("reserve exact resume generation two: %v", err)
	}
	if secondAuthority.BindingID != second.BindingID || secondAuthority.LeaseOwner != second.LeaseOwner || secondAuthority.LeaseGeneration != 2 {
		t.Fatalf("generation-two authority = %#v", secondAuthority)
	}
	secondBinding := t003LeaseBinding(t, f.store, second.BindingID)
	if secondBinding.State != WorkerRunBindingStateReserved || secondBinding.LeaseGeneration != 2 {
		t.Fatalf("generation-two binding = %#v", secondBinding)
	}
	t003LeaseRequireSession(t, t003LeaseSession(t, f.store, second.WorkerSessionID), second.TaskID, secondAuthority.LeaseOwner, secondAuthority.LeaseGeneration, *secondBinding.LeaseExpiresAt)

	staleForSecond := firstAuthority
	staleForSecond.BindingID = second.BindingID
	live := LiveHandleIdentity{Scope: "generation-scope", HandleID: "generation-handle", HandleGeneration: 3, RegistryGeneration: 5}
	for _, action := range []struct {
		name string
		call func() error
	}{
		{"finalize", func() error {
			_, err := f.store.FinalizeWorkerRunBinding(context.Background(), FinalizeWorkerRunBindingRequest{Authority: staleForSecond, TerminalReason: "stale"})
			return err
		}},
		{"start", func() error {
			_, err := f.store.StartWorkerRunBinding(context.Background(), StartWorkerRunBindingRequest{Authority: staleForSecond, LiveHandle: live, ExecutionID: "stale-execution"})
			return err
		}},
		{"return", func() error {
			_, err := f.store.RecordWorkerRunBindingReturned(context.Background(), ReturnWorkerRunBindingRequest{Authority: staleForSecond})
			return err
		}},
	} {
		if err := action.call(); err == nil {
			t.Fatalf("old authority %s succeeded against generation two", action.name)
		}
		winner := t003LeaseBinding(t, f.store, second.BindingID)
		if winner.State != WorkerRunBindingStateReserved || winner.LeaseGeneration != secondAuthority.LeaseGeneration {
			t.Fatalf("generation-two winner rewritten after stale %s: %#v", action.name, winner)
		}
	}

	var activeBindings int
	if err := f.observer.QueryRow(`SELECT COUNT(*) FROM worker_run_bindings WHERE state IN ('reserved', 'running', 'returned', 'cancelling')`).Scan(&activeBindings); err != nil {
		t.Fatalf("count active bindings: %v", err)
	}
	if activeBindings != 1 {
		t.Fatalf("active bindings = %d, want 1", activeBindings)
	}
	t003RequireUnchangedTask(t, f.store, second.TaskID, before.Status, before.Result)
	t003RequireForeignKeyCheck(t, f.observer)
}
