//go:build !short

package critical_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	_ "modernc.org/sqlite"
)

// criticalDB opens a fresh per-test SQLite database for loom critical tests.
// Each test gets a private file under t.TempDir() so cross-test interference
// is impossible.
func criticalDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "loom_critical.db")
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	// Single connection mirrors production single-writer pattern and avoids
	// SQLITE_BUSY surprises during the short-lived test runs.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// fastWorker is a minimal Worker implementation that completes immediately.
// It is used by tests that only need a successful task lifecycle to exercise
// tenant scoping.
type fastWorker struct{}

func (fastWorker) Execute(_ context.Context, _ *loom.Task) (*loom.WorkerResult, error) {
	return &loom.WorkerResult{Content: "ok"}, nil
}

func (fastWorker) Type() loom.WorkerType { return loom.WorkerTypeThinker }

// gatedWorker blocks Execute until release is signalled or the test ctx
// expires. It allows tests to count in-flight tasks against a tenant quota
// before any of them complete.
type gatedWorker struct {
	mu      sync.Mutex
	release chan struct{}
}

func newGatedWorker() *gatedWorker {
	return &gatedWorker{release: make(chan struct{})}
}

func (g *gatedWorker) Execute(ctx context.Context, _ *loom.Task) (*loom.WorkerResult, error) {
	select {
	case <-g.release:
		return &loom.WorkerResult{Content: "released"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *gatedWorker) Type() loom.WorkerType { return loom.WorkerTypeThinker }

func (g *gatedWorker) Release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.release:
		// already closed
	default:
		close(g.release)
	}
}

// waitForLoomTerminal polls engine.Get until the task reaches a terminal
// state or timeout expires. Returns the final task or fails the test.
func waitForLoomTerminal(t *testing.T, engine *loom.LoomEngine, taskID string, timeout time.Duration) *loom.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := engine.Get(taskID)
		if err == nil && task != nil && task.Status.IsTerminal() {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach terminal state within %v", taskID, timeout)
	return nil
}

// TestCritical_LoomIsolation_CrossTenantGetReturnsNotFound verifies the
// CHK079 contract: a tenant calling Get on a foreign tenant's task ID MUST
// receive ErrTaskNotFound (404 semantics). Returning ErrCrossTenantDenied
// (403) would disclose the existence of the foreign task.
//
// @critical — release blocker per rule #10
func TestCritical_LoomIsolation_CrossTenantGetReturnsNotFound(t *testing.T) {
	db := criticalDB(t)
	engine, err := loom.NewEngine(db, "critical-test")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close(context.Background()) })

	engine.RegisterWorker(loom.WorkerTypeThinker, fastWorker{})

	tenantA := loom.NewTenantScopedEngine(engine, "tenantA", nil)
	tenantB := loom.NewTenantScopedEngine(engine, "tenantB", nil)

	taskID, err := tenantA.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeThinker,
		ProjectID:  "proj-A",
		Prompt:     "tenantA secret task",
	})
	if err != nil {
		t.Fatalf("tenantA.Submit: %v", err)
	}
	if taskID == "" {
		t.Fatal("Submit returned empty taskID")
	}

	waitForLoomTerminal(t, engine, taskID, 3*time.Second)

	// Boundary 1: cross-tenant Get → ErrTaskNotFound, never the foreign task.
	got, err := tenantB.Get(taskID)
	if err == nil {
		t.Fatalf("CRITICAL: tenantB.Get(taskA) returned task %+v — must be ErrTaskNotFound", got)
	}
	if !errors.Is(err, loom.ErrTaskNotFound) {
		t.Fatalf("CRITICAL: cross-tenant Get returned %v — must be ErrTaskNotFound (not 403/denied)", err)
	}

	// Boundary 2: cross-tenant Cancel — same 404 contract.
	if err := tenantB.Cancel(taskID); !errors.Is(err, loom.ErrTaskNotFound) {
		t.Fatalf("CRITICAL: cross-tenant Cancel returned %v — must be ErrTaskNotFound", err)
	}

	// Boundary 3: List for the same project must hide the row from tenantB.
	listB, err := tenantB.List("proj-A")
	if err != nil {
		t.Fatalf("tenantB.List: %v", err)
	}
	if len(listB) != 0 {
		t.Fatalf("CRITICAL: tenantB.List leaked %d task(s) from tenantA", len(listB))
	}

	// Sanity: tenantA still owns its task.
	taskA, err := tenantA.Get(taskID)
	if err != nil {
		t.Fatalf("tenantA.Get(own): %v", err)
	}
	if taskA == nil || taskA.TenantID != "tenantA" {
		t.Fatalf("tenantA lost ownership of task: %+v", taskA)
	}
}

// TestCritical_LoomIsolation_QuotaIsPerTenant verifies the FR-17 quota
// contract: when tenantA reaches MaxLoomTasksQueued, further Submit calls
// from tenantA fail with ErrLoomQuotaExceeded — but tenantB's quota is
// completely independent and continues to admit submissions.
//
// @critical — release blocker per rule #10
func TestCritical_LoomIsolation_QuotaIsPerTenant(t *testing.T) {
	db := criticalDB(t)
	engine, err := loom.NewEngine(db, "critical-test")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close(context.Background()) })

	gw := newGatedWorker()
	engine.RegisterWorker(loom.WorkerTypeThinker, gw)
	t.Cleanup(gw.Release)

	tenantA := loom.NewTenantScopedEngine(engine, "tenantA-quota", &loom.TenantQuotaConfig{
		MaxLoomTasksQueued: 2,
	})
	tenantB := loom.NewTenantScopedEngine(engine, "tenantB-quota", &loom.TenantQuotaConfig{
		MaxLoomTasksQueued: 10,
	})

	// Saturate tenantA up to the cap. The gatedWorker keeps each task
	// in-flight, so the COUNT in TenantScopedLoomEngine.Submit's quota
	// check sees them all.
	for i := 0; i < 2; i++ {
		if _, err := tenantA.Submit(context.Background(), loom.TaskRequest{
			WorkerType: loom.WorkerTypeThinker,
			ProjectID:  "proj",
			Prompt:     "fill",
		}); err != nil {
			t.Fatalf("tenantA.Submit[%d]: %v (within quota)", i, err)
		}
	}
	// Allow Submit→dispatched persistence to settle before counting.
	time.Sleep(50 * time.Millisecond)

	// Boundary 1: tenantA over its quota → ErrLoomQuotaExceeded.
	if _, err := tenantA.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeThinker,
		ProjectID:  "proj",
		Prompt:     "over quota",
	}); !errors.Is(err, loom.ErrLoomQuotaExceeded) {
		t.Fatalf("CRITICAL: tenantA over-quota submit returned %v — must be ErrLoomQuotaExceeded", err)
	}

	// Boundary 2: tenantB is unaffected — quota independence.
	for i := 0; i < 3; i++ {
		if _, err := tenantB.Submit(context.Background(), loom.TaskRequest{
			WorkerType: loom.WorkerTypeThinker,
			ProjectID:  "proj",
			Prompt:     "tenantB free",
		}); err != nil {
			t.Fatalf("CRITICAL: tenantB.Submit[%d] blocked by tenantA quota: %v", i, err)
		}
	}
}
