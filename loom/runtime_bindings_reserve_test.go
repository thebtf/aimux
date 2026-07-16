package loom

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

var t003ReserveNow = time.Date(2026, 7, 15, 12, 0, 0, 123456000, time.UTC)

type t003ReserveFixture struct {
	db       *sql.DB
	observer *sql.DB
	store    *TaskStore
	now      time.Time
}

func t003NewReserveFixture(t *testing.T) *t003ReserveFixture {
	t.Helper()

	path := filepath.ToSlash(filepath.Join(t.TempDir(), "runtime-bindings.db"))
	dsn := "file:" + path + "?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=0"
	open := func(name string) *sql.DB {
		t.Helper()
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatalf("open %s database: %v", name, err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
			_ = db.Close()
			t.Fatalf("enable %s foreign keys: %v", name, err)
		}
		return db
	}

	db := open("store")
	store, err := NewTaskStore(db, "reserve-engine")
	if err != nil {
		_ = db.Close()
		t.Fatalf("NewTaskStore: %v", err)
	}
	store.now = func() time.Time { return t003ReserveNow }
	observer := open("observer")
	t.Cleanup(func() {
		_ = observer.Close()
		_ = db.Close()
	})

	fixture := &t003ReserveFixture{db: db, observer: observer, store: store, now: t003ReserveNow}
	fixture.seedTask(t, "reserve-task")
	return fixture
}

func (f *t003ReserveFixture) seedTask(t *testing.T, id string) {
	t.Helper()
	if err := f.store.Create(&Task{
		ID:         id,
		Status:     TaskStatusPending,
		WorkerType: WorkerTypeCLI,
		ProjectID:  "reserve-project",
		TenantID:   "reserve-tenant",
		Prompt:     "reserve runtime binding",
		CreatedAt:  f.now,
	}); err != nil {
		t.Fatalf("seed task %q: %v", id, err)
	}
}

func t003ReserveRequest(mode RuntimeBindingMode, bindingID, sessionID string) ReserveWorkerRunBindingRequest {
	return ReserveWorkerRunBindingRequest{
		BindingID:             bindingID,
		TaskID:                "reserve-task",
		WorkerSessionID:       sessionID,
		TenantID:              "reserve-tenant",
		ProjectID:             "reserve-project",
		CanonicalWorktreeRoot: "D:/worktrees/aimux-loom",
		ProfileFingerprint:    "profile-v1",
		CapabilityFingerprint: "capability-v1",
		RequestedMode:         mode,
		ExecutorName:          "codex",
		SwarmScope:            "reserve-swarm",
		LeaseOwner:            "reserve-owner",
		LeaseTTL:              2 * time.Minute,
		ParentWorkerSessionID: "",
	}
}

func t003SeedAvailableSession(t *testing.T, f *t003ReserveFixture, id string, req ReserveWorkerRunBindingRequest) {
	t.Helper()
	var providerName, providerSessionID any
	var providerGeneration any
	if expected := req.ExpectedParentProviderSession; expected != nil {
		providerName = expected.ProviderName
		providerSessionID = expected.SessionID
		providerGeneration = expected.Generation
	}
	_, err := f.db.Exec(`
		INSERT INTO worker_sessions (
			id, tenant_id, engine_name, project_id, canonical_worktree_root,
			profile_fingerprint, capability_fingerprint, requested_mode, state,
			provider_name, provider_session_id, provider_session_generation,
			lease_generation, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, req.TenantID, "reserve-engine", req.ProjectID, req.CanonicalWorktreeRoot,
		req.ProfileFingerprint, req.CapabilityFingerprint, RuntimeBindingModeNew,
		WorkerSessionStateAvailable, providerName, providerSessionID, providerGeneration,
		0, f.now.Format(time.RFC3339Nano), f.now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("seed available worker session %q: %v", id, err)
	}
}

func t003MutateResumeSession(t *testing.T, f *t003ReserveFixture, column, value, id string) {
	t.Helper()
	if column != "tenant_id" && column != "engine_name" && column != "project_id" && column != "canonical_worktree_root" && column != "profile_fingerprint" && column != "capability_fingerprint" {
		t.Fatalf("unsupported exact-resume key column %q", column)
	}
	statement := "UPDATE worker_sessions SET " + column + " = ? WHERE id = ?"
	if _, err := f.db.Exec(statement, value, id); err != nil {
		t.Fatalf("mutate exact-resume session %s: %v", column, err)
	}
}

func t003Counts(t *testing.T, db *sql.DB) (sessions, bindings int) {
	t.Helper()
	if err := db.QueryRow(`SELECT COUNT(*) FROM worker_sessions`).Scan(&sessions); err != nil {
		t.Fatalf("count worker sessions: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM worker_run_bindings`).Scan(&bindings); err != nil {
		t.Fatalf("count worker run bindings: %v", err)
	}
	return sessions, bindings
}

func t003RequireCounts(t *testing.T, db *sql.DB, wantSessions, wantBindings int) {
	t.Helper()
	if sessions, bindings := t003Counts(t, db); sessions != wantSessions || bindings != wantBindings {
		t.Fatalf("worker session/run binding counts = %d/%d, want %d/%d", sessions, bindings, wantSessions, wantBindings)
	}
}

func t003RequireForeignKeyCheck(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var fkIndex int
		if err := rows.Scan(&table, &rowID, &parent, &fkIndex); err != nil {
			t.Fatalf("scan foreign_key_check violation: %v", err)
		}
		t.Fatalf("foreign_key_check violation: table=%s rowid=%v parent=%s fk=%d", table, rowID, parent, fkIndex)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign_key_check: %v", err)
	}
}

func TestTaskStore_ReserveWorkerRunBinding_NewAtomicallyCreatesLeasedSession(t *testing.T) {
	f := t003NewReserveFixture(t)
	req := t003ReserveRequest(RuntimeBindingModeNew, "binding-new", "session-new")

	authority, err := f.store.ReserveWorkerRunBinding(context.Background(), req)
	if err != nil {
		t.Fatalf("reserve new binding: %v", err)
	}
	if authority.BindingID != req.BindingID || authority.LeaseOwner != req.LeaseOwner || authority.LeaseGeneration != 1 {
		t.Fatalf("reserve authority = %#v, want binding=%q owner=%q generation=1", authority, req.BindingID, req.LeaseOwner)
	}

	binding, err := f.store.GetWorkerRunBinding(context.Background(), req.BindingID, req.TenantID)
	if err != nil {
		t.Fatalf("get reserved binding: %v", err)
	}
	session, err := f.store.GetWorkerSession(context.Background(), req.WorkerSessionID, req.TenantID)
	if err != nil {
		t.Fatalf("get leased session: %v", err)
	}
	wantExpiry := f.now.Add(req.LeaseTTL)
	if binding.TaskID != req.TaskID || binding.WorkerSessionID == nil || *binding.WorkerSessionID != req.WorkerSessionID || binding.TenantID != req.TenantID || binding.EngineName != "reserve-engine" || binding.ProjectID != req.ProjectID || binding.RequestedMode != RuntimeBindingModeNew || binding.ExecutorName != req.ExecutorName || binding.State != WorkerRunBindingStateReserved || binding.LeaseOwner == nil || *binding.LeaseOwner != req.LeaseOwner || binding.LeaseGeneration != 1 || binding.LeaseExpiresAt == nil || !binding.LeaseExpiresAt.Equal(wantExpiry) {
		t.Fatalf("reserved binding = %#v", binding)
	}
	if binding.ProviderSession != nil || binding.ProviderConnectionGeneration != nil || binding.LiveHandle != nil || binding.ExecutionID != nil || binding.Process != nil {
		t.Fatalf("reserved binding retained provider/live/process identity: %#v", binding)
	}
	if session.TenantID != req.TenantID || session.EngineName != "reserve-engine" || session.ProjectID != req.ProjectID || session.CanonicalWorktreeRoot != req.CanonicalWorktreeRoot || session.ProfileFingerprint != req.ProfileFingerprint || session.CapabilityFingerprint != req.CapabilityFingerprint || session.RequestedMode != RuntimeBindingModeNew || session.State != WorkerSessionStateLeased || session.ActiveTaskID == nil || *session.ActiveTaskID != req.TaskID || session.LeaseOwner == nil || *session.LeaseOwner != req.LeaseOwner || session.LeaseGeneration != 1 || session.LeaseExpiresAt == nil || !session.LeaseExpiresAt.Equal(wantExpiry) || session.ParentWorkerSessionID != nil || session.ProviderSession != nil {
		t.Fatalf("leased session = %#v", session)
	}
	t003RequireCounts(t, f.observer, 1, 1)
	t003RequireForeignKeyCheck(t, f.observer)
}

func TestTaskStore_ReserveWorkerRunBinding_StatelessCreatesBindingOnly(t *testing.T) {
	f := t003NewReserveFixture(t)
	req := t003ReserveRequest(RuntimeBindingModeStateless, "binding-stateless", "")

	authority, err := f.store.ReserveWorkerRunBinding(context.Background(), req)
	if err != nil {
		t.Fatalf("reserve stateless binding: %v", err)
	}
	if authority.BindingID != req.BindingID || authority.LeaseOwner != req.LeaseOwner || authority.LeaseGeneration != 1 {
		t.Fatalf("stateless authority = %#v", authority)
	}
	binding, err := f.store.GetWorkerRunBinding(context.Background(), req.BindingID, req.TenantID)
	if err != nil {
		t.Fatalf("get stateless binding: %v", err)
	}
	if binding.WorkerSessionID != nil || binding.RequestedMode != RuntimeBindingModeStateless || binding.State != WorkerRunBindingStateReserved || binding.LeaseGeneration != 1 {
		t.Fatalf("stateless binding = %#v", binding)
	}
	t003RequireCounts(t, f.observer, 0, 1)
	t003RequireForeignKeyCheck(t, f.observer)
}

func TestTaskStore_ReserveWorkerRunBinding_TypesTaskLookupAndOwnershipErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ReserveWorkerRunBindingRequest)
		wantErr error
	}{
		{
			name: "missing task",
			mutate: func(req *ReserveWorkerRunBindingRequest) {
				req.TaskID = "missing-reserve-task"
			},
			wantErr: ErrTaskNotFound,
		},
		{
			name: "foreign task owner",
			mutate: func(req *ReserveWorkerRunBindingRequest) {
				req.TenantID = "other-tenant"
			},
			wantErr: ErrAuthorityConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := t003NewReserveFixture(t)
			req := t003ReserveRequest(RuntimeBindingModeStateless, "binding-task-error", "")
			test.mutate(&req)

			_, err := f.store.ReserveWorkerRunBinding(context.Background(), req)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("reserve error = %v, want errors.Is(_, %v)", err, test.wantErr)
			}
			t003RequireCounts(t, f.observer, 0, 0)
		})
	}
}

// TestTaskStore_ReserveWorkerRunBinding_StatelessBlankProjectMatchesBlankTaskAndPersistsBlank
// proves a stateless reserve whose ProjectID is blank matches a durable task
// row that itself carries a blank project (no-project/single-tenant
// callers) and persists that blank value honestly — never a sentinel that
// would mismatch the owning task row.
func TestTaskStore_ReserveWorkerRunBinding_StatelessBlankProjectMatchesBlankTaskAndPersistsBlank(t *testing.T) {
	f := t003NewReserveFixture(t)
	if err := f.store.Create(&Task{
		ID:         "reserve-task-blank-project",
		Status:     TaskStatusPending,
		WorkerType: WorkerTypeCLI,
		ProjectID:  "",
		TenantID:   "reserve-tenant",
		Prompt:     "reserve runtime binding blank project",
		CreatedAt:  f.now,
	}); err != nil {
		t.Fatalf("seed blank-project task: %v", err)
	}
	req := t003ReserveRequest(RuntimeBindingModeStateless, "binding-stateless-blank-project", "")
	req.TaskID = "reserve-task-blank-project"
	req.ProjectID = ""

	authority, err := f.store.ReserveWorkerRunBinding(context.Background(), req)
	if err != nil {
		t.Fatalf("reserve stateless blank-project binding: %v", err)
	}
	if authority.BindingID != req.BindingID || authority.LeaseOwner != req.LeaseOwner || authority.LeaseGeneration != 1 {
		t.Fatalf("stateless blank-project authority = %#v", authority)
	}
	binding, err := f.store.GetWorkerRunBinding(context.Background(), req.BindingID, req.TenantID)
	if err != nil {
		t.Fatalf("get stateless blank-project binding: %v", err)
	}
	if binding.ProjectID != "" {
		t.Fatalf("stored project ID = %q, want blank to honestly match the owning blank-project task row", binding.ProjectID)
	}
	if binding.WorkerSessionID != nil || binding.RequestedMode != RuntimeBindingModeStateless || binding.State != WorkerRunBindingStateReserved {
		t.Fatalf("stateless blank-project binding = %#v", binding)
	}
	t003RequireCounts(t, f.observer, 0, 1)
	t003RequireForeignKeyCheck(t, f.observer)
}

// TestTaskStore_ReserveWorkerRunBinding_SessionBackedModesRejectBlankProjectBeforeWrite
// proves new/exact_resume/fork reserves still require a nonblank project —
// only stateless may compatibly carry a blank one — and that the rejection
// happens before any row is written.
func TestTaskStore_ReserveWorkerRunBinding_SessionBackedModesRejectBlankProjectBeforeWrite(t *testing.T) {
	for _, mode := range []RuntimeBindingMode{RuntimeBindingModeNew, RuntimeBindingModeExactResume, RuntimeBindingModeFork} {
		t.Run(string(mode), func(t *testing.T) {
			f := t003NewReserveFixture(t)
			taskID := "reserve-task-blank-project-" + string(mode)
			if err := f.store.Create(&Task{
				ID:         taskID,
				Status:     TaskStatusPending,
				WorkerType: WorkerTypeCLI,
				ProjectID:  "",
				TenantID:   "reserve-tenant",
				Prompt:     "reserve runtime binding blank project",
				CreatedAt:  f.now,
			}); err != nil {
				t.Fatalf("seed blank-project task: %v", err)
			}
			req := t003ReserveRequest(mode, "binding-blank-project-"+string(mode), "session-blank-project-"+string(mode))
			req.TaskID = taskID
			req.ProjectID = ""
			if mode == RuntimeBindingModeFork {
				req.ParentWorkerSessionID = "parent-blank-project-" + string(mode)
			}
			if _, err := f.store.ReserveWorkerRunBinding(context.Background(), req); err == nil {
				t.Fatalf("%s reserve with blank project error = nil, want validation failure", mode)
			}
			t003RequireCounts(t, f.observer, 0, 0)
		})
	}
}

func TestTaskStore_ReserveWorkerRunBinding_ExactResumeRequiresExactAvailableGKey(t *testing.T) {
	t.Run("selects exact available session", func(t *testing.T) {
		f := t003NewReserveFixture(t)
		req := t003ReserveRequest(RuntimeBindingModeExactResume, "binding-resume", "session-resume")
		t003SeedAvailableSession(t, f, req.WorkerSessionID, req)

		authority, err := f.store.ReserveWorkerRunBinding(context.Background(), req)
		if err != nil {
			t.Fatalf("reserve exact resume: %v", err)
		}
		if authority.BindingID != req.BindingID || authority.LeaseGeneration != 1 {
			t.Fatalf("exact-resume authority = %#v", authority)
		}
		session, err := f.store.GetWorkerSession(context.Background(), req.WorkerSessionID, req.TenantID)
		if err != nil {
			t.Fatalf("get exact-resume session: %v", err)
		}
		if session.State != WorkerSessionStateLeased || session.LeaseGeneration != 1 || session.ActiveTaskID == nil || *session.ActiveTaskID != req.TaskID {
			t.Fatalf("exact-resume session = %#v", session)
		}
		t003RequireCounts(t, f.observer, 1, 1)
	})

	mutations := []struct {
		name          string
		mutateSession func(*testing.T, *t003ReserveFixture, string)
		mutateRequest func(*ReserveWorkerRunBindingRequest)
	}{
		{"tenant", func(t *testing.T, f *t003ReserveFixture, id string) {
			t003MutateResumeSession(t, f, `tenant_id`, "other-tenant", id)
		}, nil},
		{"engine", func(t *testing.T, f *t003ReserveFixture, id string) {
			t003MutateResumeSession(t, f, `engine_name`, "other-engine", id)
		}, nil},
		{"project", func(t *testing.T, f *t003ReserveFixture, id string) {
			t003MutateResumeSession(t, f, `project_id`, "other-project", id)
		}, nil},
		{"canonical-worktree", func(t *testing.T, f *t003ReserveFixture, id string) {
			t003MutateResumeSession(t, f, `canonical_worktree_root`, "D:/worktrees/other", id)
		}, nil},
		{"profile", func(t *testing.T, f *t003ReserveFixture, id string) {
			t003MutateResumeSession(t, f, `profile_fingerprint`, "profile-v2", id)
		}, nil},
		{"capability", func(t *testing.T, f *t003ReserveFixture, id string) {
			t003MutateResumeSession(t, f, `capability_fingerprint`, "capability-v2", id)
		}, nil},
		{"relative-worktree", nil, func(r *ReserveWorkerRunBindingRequest) { r.CanonicalWorktreeRoot = "relative/worktree" }},
		{"non-clean-worktree", nil, func(r *ReserveWorkerRunBindingRequest) {
			r.CanonicalWorktreeRoot = "D:/worktrees/aimux-loom/../aimux-loom"
		}},
	}
	for _, mutation := range mutations {
		t.Run("rejects "+mutation.name+" mismatch", func(t *testing.T) {
			f := t003NewReserveFixture(t)
			base := t003ReserveRequest(RuntimeBindingModeExactResume, "binding-"+mutation.name, "session-resume")
			t003SeedAvailableSession(t, f, base.WorkerSessionID, base)
			if mutation.mutateSession != nil {
				mutation.mutateSession(t, f, base.WorkerSessionID)
			}
			if mutation.mutateRequest != nil {
				mutation.mutateRequest(&base)
			}

			if _, err := f.store.ReserveWorkerRunBinding(context.Background(), base); err == nil {
				t.Fatalf("exact resume accepted %s mutation", mutation.name)
			}
			t003RequireCounts(t, f.observer, 1, 0)
		})
	}
}

func TestTaskStore_ReserveWorkerRunBinding_ForkRequiresExactDistinctParent(t *testing.T) {
	t.Run("creates child from exact parent", func(t *testing.T) {
		f := t003NewReserveFixture(t)
		req := t003ReserveRequest(RuntimeBindingModeFork, "binding-fork", "session-child")
		req.ParentWorkerSessionID = "session-parent"
		req.ExpectedParentProviderSession = &ProviderSessionIdentity{ProviderName: "codex", SessionID: "parent-provider", Generation: 3}
		t003SeedAvailableSession(t, f, req.ParentWorkerSessionID, req)

		if _, err := f.store.ReserveWorkerRunBinding(context.Background(), req); err != nil {
			t.Fatalf("reserve fork: %v", err)
		}
		child, err := f.store.GetWorkerSession(context.Background(), req.WorkerSessionID, req.TenantID)
		if err != nil {
			t.Fatalf("get fork child: %v", err)
		}
		if child.RequestedMode != RuntimeBindingModeFork || child.ParentWorkerSessionID == nil || *child.ParentWorkerSessionID != req.ParentWorkerSessionID || child.ID == req.ParentWorkerSessionID {
			t.Fatalf("fork child = %#v", child)
		}
		t003RequireCounts(t, f.observer, 2, 1)
	})

	for _, tc := range []struct {
		name   string
		mutate func(*ReserveWorkerRunBindingRequest)
	}{
		{"missing-parent", func(r *ReserveWorkerRunBindingRequest) { r.ParentWorkerSessionID = "missing-parent" }},
		{"same-child-and-parent", func(r *ReserveWorkerRunBindingRequest) { r.WorkerSessionID = r.ParentWorkerSessionID }},
		{"parent-g-key-mismatch", func(r *ReserveWorkerRunBindingRequest) { r.ProfileFingerprint = "other-profile" }},
		{"parent-provider-mismatch", func(r *ReserveWorkerRunBindingRequest) {
			r.ExpectedParentProviderSession = &ProviderSessionIdentity{ProviderName: "codex", SessionID: "other-parent-provider", Generation: 3}
		}},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			f := t003NewReserveFixture(t)
			req := t003ReserveRequest(RuntimeBindingModeFork, "binding-fork-"+tc.name, "session-child")
			req.ParentWorkerSessionID = "session-parent"
			req.ExpectedParentProviderSession = &ProviderSessionIdentity{ProviderName: "codex", SessionID: "parent-provider", Generation: 3}
			t003SeedAvailableSession(t, f, req.ParentWorkerSessionID, req)
			tc.mutate(&req)

			if _, err := f.store.ReserveWorkerRunBinding(context.Background(), req); err == nil {
				t.Fatalf("fork accepted %s", tc.name)
			}
			t003RequireCounts(t, f.observer, 1, 0)
		})
	}
}

func TestTaskStore_ReserveWorkerRunBinding_ConcurrentSameSessionHasOneWinner(t *testing.T) {
	f := t003NewReserveFixture(t)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := t003ReserveRequest(RuntimeBindingModeNew, fmt.Sprintf("binding-race-%d", i), "session-race")
			_, err := f.store.ReserveWorkerRunBinding(context.Background(), req)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	conflicts := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAuthorityConflict):
			conflicts++
		default:
			t.Fatalf("concurrent reserve returned non-conflict error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent reserve successes/conflicts = %d/%d, want 1/1", successes, conflicts)
	}
	t003RequireCounts(t, f.observer, 1, 1)
	var activeBindings int
	if err := f.observer.QueryRow(`SELECT COUNT(*) FROM worker_run_bindings WHERE state IN ('reserved', 'running', 'returned', 'cancelling')`).Scan(&activeBindings); err != nil {
		t.Fatalf("count active race bindings: %v", err)
	}
	if activeBindings != 1 {
		t.Fatalf("active race bindings = %d, want 1", activeBindings)
	}
	session, err := f.store.GetWorkerSession(context.Background(), "session-race", "reserve-tenant")
	if err != nil {
		t.Fatalf("get race session: %v", err)
	}
	if session.State != WorkerSessionStateLeased || session.LeaseGeneration != 1 || session.LeaseOwner == nil || *session.LeaseOwner != "reserve-owner" {
		t.Fatalf("race session = %#v", session)
	}
	t003RequireForeignKeyCheck(t, f.observer)
}

func TestTaskStore_ReserveWorkerRunBinding_BusyRollbackLeavesNoHalfCommittedAuthority(t *testing.T) {
	f := t003NewReserveFixture(t)
	req := t003ReserveRequest(RuntimeBindingModeNew, "binding-busy", "session-busy")
	if _, err := f.observer.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("hold observer write transaction: %v", err)
	}
	rollback := true
	defer func() {
		if rollback {
			_, _ = f.observer.Exec(`ROLLBACK`)
		}
	}()

	t003RequireCounts(t, f.observer, 0, 0)
	if _, err := f.store.ReserveWorkerRunBinding(context.Background(), req); err == nil {
		t.Fatal("reserve succeeded while observer held BEGIN IMMEDIATE")
	}
	t003RequireCounts(t, f.observer, 0, 0)
	if _, err := f.observer.Exec(`ROLLBACK`); err != nil {
		t.Fatalf("rollback observer write transaction: %v", err)
	}
	rollback = false
	t003RequireCounts(t, f.observer, 0, 0)

	if _, err := f.store.ReserveWorkerRunBinding(context.Background(), req); err != nil {
		t.Fatalf("reserve after observer rollback: %v", err)
	}
	t003RequireCounts(t, f.observer, 1, 1)
	t003RequireForeignKeyCheck(t, f.observer)
}

func TestBeginAuthorityTransaction_EnablesForeignKeysOnPinnedConnection(t *testing.T) {
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "authority-foreign-keys.db"))
	db, err := sql.Open("sqlite", "file:"+path+"?_journal_mode=WAL&_busy_timeout=0")
	if err != nil {
		t.Fatalf("open fresh database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	tx, err := beginAuthorityTransaction(context.Background(), db)
	if err != nil {
		t.Fatalf("begin authority transaction: %v", err)
	}
	defer func() {
		if err := tx.rollback(); err != nil {
			t.Errorf("rollback authority transaction: %v", err)
		}
	}()

	var foreignKeys int
	if err := tx.conn.QueryRowContext(tx.ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("read pinned foreign_keys pragma: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("pinned connection PRAGMA foreign_keys = %d, want 1", foreignKeys)
	}
}
