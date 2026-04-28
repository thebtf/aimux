package loom

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// fakeAuditEmitter records emitted events for test assertion.
type fakeAuditEmitter struct {
	events []AuditEvent
}

func (f *fakeAuditEmitter) Emit(e AuditEvent) {
	f.events = append(f.events, e)
}

// ---- T021: TestSubmit_ScopedToTenant ----

// TestSubmit_ScopedToTenant verifies that tasks submitted via TenantScopedLoomEngine
// are tagged with the tenant's tenant_id and are invisible to other tenants.
func TestSubmit_ScopedToTenant(t *testing.T) {
	db := newTestDB(t)
	engine, err := NewEngine(db, "test")
	if err != nil {
		t.Fatal(err)
	}

	worker := &testWorker{wtype: WorkerTypeThinker, result: "ok"}
	engine.RegisterWorker(WorkerTypeThinker, worker)

	tenantA := NewTenantScopedEngine(engine, "tenant-A", nil)
	tenantB := NewTenantScopedEngine(engine, "tenant-B", nil)

	// Submit from tenant-A.
	taskID, err := tenantA.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeThinker,
		ProjectID:  "proj-A",
		Prompt:     "tenant-A task",
	})
	if err != nil {
		t.Fatalf("tenantA.Submit: %v", err)
	}
	if taskID == "" {
		t.Fatal("Submit returned empty taskID")
	}

	// Wait for completion.
	waitForTerminal(t, engine, taskID, 3*time.Second)

	// tenant-A can get its own task.
	task, err := tenantA.Get(taskID)
	if err != nil {
		t.Fatalf("tenantA.Get own task: %v", err)
	}
	if task.TenantID != "tenant-A" {
		t.Errorf("task.TenantID = %q; want tenant-A", task.TenantID)
	}

	// tenant-B listing only shows its own tasks (none).
	listB, err := tenantB.List("proj-A")
	if err != nil {
		t.Fatalf("tenantB.List: %v", err)
	}
	if len(listB) != 0 {
		t.Errorf("tenantB.List: got %d tasks, want 0 (cross-tenant leak)", len(listB))
	}

	// tenant-A listing shows its own task.
	listA, err := tenantA.List("proj-A")
	if err != nil {
		t.Fatalf("tenantA.List: %v", err)
	}
	if len(listA) != 1 {
		t.Errorf("tenantA.List: got %d tasks, want 1", len(listA))
	}
}

// ---- T021: TestGet_CrossTenantReturns404 ----

// TestGet_CrossTenantReturns404 verifies that Get on a task belonging to another
// tenant returns ErrTaskNotFound (404 semantics), NOT ErrCrossTenantDenied (403).
// Defence-in-depth: we must not reveal that the task exists to a different tenant.
func TestGet_CrossTenantReturns404(t *testing.T) {
	db := newTestDB(t)
	engine, err := NewEngine(db, "test")
	if err != nil {
		t.Fatal(err)
	}

	worker := &testWorker{wtype: WorkerTypeThinker, result: "ok"}
	engine.RegisterWorker(WorkerTypeThinker, worker)

	tenantA := NewTenantScopedEngine(engine, "tenant-A", nil)
	tenantB := NewTenantScopedEngine(engine, "tenant-B", nil)

	// Submit from tenant-A.
	taskID, err := tenantA.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeThinker,
		ProjectID:  "proj-A",
		Prompt:     "secret task",
	})
	if err != nil {
		t.Fatalf("tenantA.Submit: %v", err)
	}
	waitForTerminal(t, engine, taskID, 3*time.Second)

	// tenant-B tries to Get tenant-A's task — must return ErrTaskNotFound, not 403.
	_, err = tenantB.Get(taskID)
	if err == nil {
		t.Fatal("tenantB.Get(tenant-A task): expected error, got nil")
	}
	if !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("tenantB.Get(tenant-A task): got %v; want ErrTaskNotFound (not 403)", err)
	}
}

// ---- T021: TestList_FiltersToTenant ----

// TestList_FiltersToTenant verifies that List only returns tasks scoped to the
// calling tenant, even when multiple tenants have tasks in the same project.
func TestList_FiltersToTenant(t *testing.T) {
	db := newTestDB(t)
	engine, err := NewEngine(db, "test")
	if err != nil {
		t.Fatal(err)
	}

	worker := &testWorker{wtype: WorkerTypeThinker, result: "ok", delay: 200 * time.Millisecond}
	engine.RegisterWorker(WorkerTypeThinker, worker)

	tenantA := NewTenantScopedEngine(engine, "tenant-A", nil)
	tenantB := NewTenantScopedEngine(engine, "tenant-B", nil)

	// Both tenants submit to the same project.
	for i := 0; i < 2; i++ {
		if _, err := tenantA.Submit(context.Background(), TaskRequest{
			WorkerType: WorkerTypeThinker,
			ProjectID:  "shared-proj",
			Prompt:     fmt.Sprintf("A task %d", i),
		}); err != nil {
			t.Fatalf("tenantA.Submit[%d]: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := tenantB.Submit(context.Background(), TaskRequest{
			WorkerType: WorkerTypeThinker,
			ProjectID:  "shared-proj",
			Prompt:     fmt.Sprintf("B task %d", i),
		}); err != nil {
			t.Fatalf("tenantB.Submit[%d]: %v", i, err)
		}
	}

	// Allow tasks to be persisted.
	time.Sleep(30 * time.Millisecond)

	listA, err := tenantA.List("shared-proj")
	if err != nil {
		t.Fatalf("tenantA.List: %v", err)
	}
	if len(listA) != 2 {
		t.Errorf("tenantA.List: got %d, want 2", len(listA))
	}
	for _, task := range listA {
		if task.TenantID != "tenant-A" {
			t.Errorf("tenantA.List: task %q has TenantID=%q; want tenant-A", task.ID, task.TenantID)
		}
	}

	listB, err := tenantB.List("shared-proj")
	if err != nil {
		t.Fatalf("tenantB.List: %v", err)
	}
	if len(listB) != 3 {
		t.Errorf("tenantB.List: got %d, want 3", len(listB))
	}
	for _, task := range listB {
		if task.TenantID != "tenant-B" {
			t.Errorf("tenantB.List: task %q has TenantID=%q; want tenant-B", task.ID, task.TenantID)
		}
	}
}

// ---- T021: TestCancel_CrossTenantReturns404 ----

// TestCancel_CrossTenantReturns404 verifies that Cancel on a task belonging to
// another tenant returns ErrTaskNotFound (not ErrCrossTenantDenied / 403).
func TestCancel_CrossTenantReturns404(t *testing.T) {
	db := newTestDB(t)
	engine, err := NewEngine(db, "test")
	if err != nil {
		t.Fatal(err)
	}

	doneAll := make(chan struct{})
	worker := &blockingWorker{done: doneAll}
	engine.RegisterWorker(WorkerTypeThinker, worker)

	tenantA := NewTenantScopedEngine(engine, "tenant-A", nil)
	tenantB := NewTenantScopedEngine(engine, "tenant-B", nil)

	taskID, err := tenantA.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeThinker,
		ProjectID:  "proj",
		Prompt:     "blocking",
	})
	if err != nil {
		t.Fatalf("tenantA.Submit: %v", err)
	}

	// Wait until running.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, _ := engine.Get(taskID)
		if task != nil && task.Status == TaskStatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// tenant-B tries to Cancel tenant-A's task — must return ErrTaskNotFound.
	err = tenantB.Cancel(taskID)
	if err == nil {
		t.Fatal("tenantB.Cancel(tenant-A task): expected error, got nil")
	}
	if !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("tenantB.Cancel(tenant-A task): got %v; want ErrTaskNotFound", err)
	}

	// Unblock all goroutines so test can clean up.
	for i := 0; i < 5; i++ {
		select {
		case doneAll <- struct{}{}:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ---- T023: TestLegacyDefault_PreservedBehavior ----

// TestLegacyDefault_PreservedBehavior verifies that when tenantID == LegacyTenantID,
// existing single-tenant behaviour is fully preserved (T023 gate).
func TestLegacyDefault_PreservedBehavior(t *testing.T) {
	db := newTestDB(t)
	engine, err := NewEngine(db, "test")
	if err != nil {
		t.Fatal(err)
	}

	worker := &testWorker{wtype: WorkerTypeThinker, result: "legacy ok"}
	engine.RegisterWorker(WorkerTypeThinker, worker)

	legacy := NewTenantScopedEngine(engine, LegacyTenantID, nil)

	taskID, err := legacy.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeThinker,
		ProjectID:  "proj",
		Prompt:     "legacy task",
	})
	if err != nil {
		t.Fatalf("legacy.Submit: %v", err)
	}

	waitForTerminal(t, engine, taskID, 3*time.Second)

	task, err := legacy.Get(taskID)
	if err != nil {
		t.Fatalf("legacy.Get: %v", err)
	}
	if task.TenantID != LegacyTenantID {
		t.Errorf("task.TenantID = %q; want %q", task.TenantID, LegacyTenantID)
	}
	if task.Status != TaskStatusCompleted {
		t.Errorf("task.Status = %q; want completed", task.Status)
	}
}

// ---- T060: TestLoomSubmit_QuotaEnforced ----

// TestLoomSubmit_QuotaEnforced verifies the FR-17 quota enforcement:
// when a tenant's in-flight task count (pending+dispatched+running) reaches
// MaxLoomTasksQueued, further Submits from that tenant return ErrLoomQuotaExceeded.
// Tenant-B with a higher quota must still succeed during tenant-A's exhaustion.
func TestLoomSubmit_QuotaEnforced(t *testing.T) {
	db := newTestDB(t)
	engine, err := NewEngine(db, "test")
	if err != nil {
		t.Fatal(err)
	}

	// Blocking worker so tasks stay in-flight for counting.
	doneAll := make(chan struct{})
	worker := &blockingWorker{done: doneAll}
	engine.RegisterWorker(WorkerTypeThinker, worker)

	// Tenant-A: quota = 2
	tenantAStore := engine.store
	tenantA := NewTenantScopedEngine(engine, "quota-tenant-A", &TenantQuotaConfig{
		MaxLoomTasksQueued: 2,
		AuditEmitter:       &fakeAuditEmitter{},
	})

	// Tenant-B: quota = 10
	tenantB := NewTenantScopedEngine(engine, "quota-tenant-B", &TenantQuotaConfig{
		MaxLoomTasksQueued: 10,
		AuditEmitter:       &fakeAuditEmitter{},
	})

	_ = tenantAStore // suppress unused var warning; used for engine store access

	// Fill tenant-A to quota limit (2 tasks).
	for i := 0; i < 2; i++ {
		if _, err := tenantA.Submit(context.Background(), TaskRequest{
			WorkerType: WorkerTypeThinker,
			ProjectID:  "proj",
			Prompt:     fmt.Sprintf("A task %d", i),
		}); err != nil {
			t.Fatalf("tenantA.Submit[%d] (within quota): %v", i, err)
		}
	}

	// Wait for tasks to be at least pending so they count.
	time.Sleep(30 * time.Millisecond)

	// Third submit for tenant-A must fail with ErrLoomQuotaExceeded.
	_, err = tenantA.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeThinker,
		ProjectID:  "proj",
		Prompt:     "A over quota",
	})
	if err == nil {
		t.Fatal("tenantA.Submit over quota: expected ErrLoomQuotaExceeded, got nil")
	}
	if !errors.Is(err, ErrLoomQuotaExceeded) {
		t.Errorf("tenantA.Submit over quota: got %v; want ErrLoomQuotaExceeded", err)
	}

	// Tenant-B must still succeed (different quota, different tenant).
	taskIDsB := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		id, err := tenantB.Submit(context.Background(), TaskRequest{
			WorkerType: WorkerTypeThinker,
			ProjectID:  "proj",
			Prompt:     fmt.Sprintf("B task %d", i),
		})
		if err != nil {
			t.Fatalf("tenantB.Submit[%d]: unexpected quota error: %v", i, err)
		}
		taskIDsB = append(taskIDsB, id)
	}
	if len(taskIDsB) != 5 {
		t.Errorf("tenantB submitted %d tasks; want 5", len(taskIDsB))
	}

	// Unblock all goroutines.
	for i := 0; i < 20; i++ {
		select {
		case doneAll <- struct{}{}:
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// ---- T060: TestLoomSubmit_QuotaAuditEvent ----

// TestLoomSubmit_QuotaAuditEvent verifies that when a submit is rejected for quota,
// an AuditEvent of type "loom_submit_rejected" is emitted with correct fields.
func TestLoomSubmit_QuotaAuditEvent(t *testing.T) {
	db := newTestDB(t)
	engine, err := NewEngine(db, "test")
	if err != nil {
		t.Fatal(err)
	}

	// Blocking worker so tasks stay in-flight.
	doneAll := make(chan struct{})
	worker := &blockingWorker{done: doneAll}
	engine.RegisterWorker(WorkerTypeThinker, worker)

	auditor := &fakeAuditEmitter{}
	tenantA := NewTenantScopedEngine(engine, "audit-tenant-A", &TenantQuotaConfig{
		MaxLoomTasksQueued: 1,
		AuditEmitter:       auditor,
	})

	// Fill quota.
	if _, err := tenantA.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeThinker,
		ProjectID:  "proj",
		Prompt:     "fill",
	}); err != nil {
		t.Fatalf("first Submit: %v", err)
	}

	time.Sleep(30 * time.Millisecond)

	// Trigger quota rejection.
	_, err = tenantA.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeThinker,
		ProjectID:  "proj",
		Prompt:     "over limit",
	})
	if !errors.Is(err, ErrLoomQuotaExceeded) {
		t.Fatalf("expected ErrLoomQuotaExceeded, got %v", err)
	}

	// Verify audit event emitted.
	if len(auditor.events) == 0 {
		t.Fatal("no audit events emitted on quota rejection")
	}
	evt := auditor.events[len(auditor.events)-1]
	if evt.Type != "loom_submit_rejected" {
		t.Errorf("audit event type = %q; want loom_submit_rejected", evt.Type)
	}
	if evt.TenantID != "audit-tenant-A" {
		t.Errorf("audit event TenantID = %q; want audit-tenant-A", evt.TenantID)
	}
	if evt.CurrentDepth < 1 {
		t.Errorf("audit event CurrentDepth = %d; want >= 1", evt.CurrentDepth)
	}
	if evt.Limit != 1 {
		t.Errorf("audit event Limit = %d; want 1", evt.Limit)
	}

	// Unblock.
	for i := 0; i < 5; i++ {
		select {
		case doneAll <- struct{}{}:
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// ---- helpers ----

func waitForTerminal(t *testing.T, engine *LoomEngine, taskID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := engine.Get(taskID)
		if err == nil && task.Status.IsTerminal() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := engine.Get(taskID)
	if task != nil {
		t.Fatalf("task %s did not reach terminal state within %v; current status: %s", taskID, timeout, task.Status)
	} else {
		t.Fatalf("task %s did not reach terminal state within %v", taskID, timeout)
	}
}
