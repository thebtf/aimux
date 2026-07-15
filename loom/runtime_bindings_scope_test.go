package loom

import (
	"context"
	"errors"
	"testing"
)

func TestTaskStore_RuntimeBindingGettersAreTenantAndEngineScoped(t *testing.T) {
	f := t003NewReserveFixture(t)
	request := t003ReserveRequest(RuntimeBindingModeNew, "binding-scoped-get", "session-scoped-get")
	if _, err := f.store.ReserveWorkerRunBinding(context.Background(), request); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	if _, err := f.store.GetWorkerSession(context.Background(), request.WorkerSessionID, request.TenantID); err != nil {
		t.Fatalf("get owned worker session: %v", err)
	}
	if _, err := f.store.GetWorkerRunBinding(context.Background(), request.BindingID, request.TenantID); err != nil {
		t.Fatalf("get owned run binding: %v", err)
	}

	for _, lookup := range []struct {
		name string
		get  func(*TaskStore) error
	}{
		{
			name: "foreign tenant session",
			get: func(store *TaskStore) error {
				_, err := store.GetWorkerSession(context.Background(), request.WorkerSessionID, "foreign-tenant")
				return err
			},
		},
		{
			name: "foreign tenant binding",
			get: func(store *TaskStore) error {
				_, err := store.GetWorkerRunBinding(context.Background(), request.BindingID, "foreign-tenant")
				return err
			},
		},
	} {
		t.Run(lookup.name, func(t *testing.T) {
			if err := lookup.get(f.store); !errors.Is(err, ErrTaskNotFound) {
				t.Fatalf("error = %v, want ErrTaskNotFound", err)
			}
		})
	}

	foreignEngine, err := NewTaskStore(f.db, "foreign-engine")
	if err != nil {
		t.Fatalf("open foreign engine store: %v", err)
	}
	if _, err := foreignEngine.GetWorkerSession(context.Background(), request.WorkerSessionID, request.TenantID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("foreign engine session error = %v, want ErrTaskNotFound", err)
	}
	if _, err := foreignEngine.GetWorkerRunBinding(context.Background(), request.BindingID, request.TenantID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("foreign engine binding error = %v, want ErrTaskNotFound", err)
	}
}

func TestRuntimeBindingWriteErrorOnlyClassifiesUniquenessAsConflict(t *testing.T) {
	f := t003NewReserveFixture(t)
	if _, err := f.db.Exec(`
		CREATE TABLE runtime_binding_error_classification (
			id INTEGER PRIMARY KEY,
			state TEXT NOT NULL CHECK (state = 'valid')
		)
	`); err != nil {
		t.Fatalf("create classification table: %v", err)
	}
	if _, err := f.db.Exec(`INSERT INTO runtime_binding_error_classification (id, state) VALUES (1, 'valid')`); err != nil {
		t.Fatalf("seed classification table: %v", err)
	}

	uniqueTx, err := beginAuthorityTransaction(context.Background(), f.db)
	if err != nil {
		t.Fatalf("begin unique transaction: %v", err)
	}
	_, writeErr := uniqueTx.conn.ExecContext(uniqueTx.ctx, `INSERT INTO runtime_binding_error_classification (id, state) VALUES (1, 'valid')`)
	if writeErr == nil {
		t.Fatal("duplicate primary key unexpectedly succeeded")
	}
	if got := runtimeBindingWriteError(uniqueTx, "duplicate", writeErr); !errors.Is(got, ErrAuthorityConflict) {
		t.Fatalf("duplicate error = %v, want ErrAuthorityConflict", got)
	}

	checkTx, err := beginAuthorityTransaction(context.Background(), f.db)
	if err != nil {
		t.Fatalf("begin check transaction: %v", err)
	}
	_, writeErr = checkTx.conn.ExecContext(checkTx.ctx, `INSERT INTO runtime_binding_error_classification (id, state) VALUES (2, 'invalid')`)
	if writeErr == nil {
		t.Fatal("CHECK violation unexpectedly succeeded")
	}
	if got := runtimeBindingWriteError(checkTx, "check", writeErr); errors.Is(got, ErrAuthorityConflict) {
		t.Fatalf("CHECK violation misclassified as ErrAuthorityConflict: %v", got)
	}
}
