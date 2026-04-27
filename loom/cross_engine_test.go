package loom

import (
	"testing"
	"time"
)

// TestCrossEngineIsolation is a regression test for AIMUX-10 NFR-4:
// two daemons sharing the same SQLite database must not cross-contaminate
// each other's task lifecycles. Each subtest covers a distinct isolation
// boundary defined in the specification.
func TestCrossEngineIsolation(t *testing.T) {
	// Shared in-memory database — both stores see the same schema and rows.
	db := newTestDB(t)

	storeProd, err := NewTaskStore(db, "prod")
	if err != nil {
		t.Fatal(err)
	}
	storeDev, err := NewTaskStore(db, "dev")
	if err != nil {
		t.Fatal(err)
	}

	// Seed: one prod task in dispatched state, one dev task in running state.
	// These are the two states that MarkCrashed targets.
	prodTask := &Task{
		ID:         "prod-task-1",
		Status:     TaskStatusPending,
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-shared",
		Prompt:     "prod prompt",
		CreatedAt:  time.Now().UTC(),
	}
	devTask := &Task{
		ID:         "dev-task-1",
		Status:     TaskStatusPending,
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-shared",
		Prompt:     "dev prompt",
		CreatedAt:  time.Now().UTC(),
	}

	if err := storeProd.Create(prodTask); err != nil {
		t.Fatalf("prod Create: %v", err)
	}
	if err := storeDev.Create(devTask); err != nil {
		t.Fatalf("dev Create: %v", err)
	}

	// Forcibly set prod task to dispatched and dev task to running via raw SQL
	// to bypass the state machine (test setup shortcut used throughout loom_test.go).
	if _, err := db.Exec("UPDATE tasks SET status='dispatched' WHERE id='prod-task-1'"); err != nil {
		t.Fatalf("SQL setup dispatched: %v", err)
	}
	if _, err := db.Exec("UPDATE tasks SET status='running' WHERE id='dev-task-1'"); err != nil {
		t.Fatalf("SQL setup running: %v", err)
	}

	// (a) MarkCrashed only changes its own rows.
	t.Run("a: MarkCrashed scoped to engine", func(t *testing.T) {
		n, err := storeProd.MarkCrashed()
		if err != nil {
			t.Fatalf("storeProd.MarkCrashed: %v", err)
		}
		if n != 1 {
			t.Errorf("prod MarkCrashed: want 1 row affected, got %d", n)
		}

		// prod task must be failed_crash; dev task must still be running.
		prodRow, err := storeProd.Get("prod-task-1")
		if err != nil {
			t.Fatalf("Get prod-task-1: %v", err)
		}
		if prodRow.Status != TaskStatusFailedCrash {
			t.Errorf("prod-task-1 status: want failed_crash, got %q", prodRow.Status)
		}

		devRow, err := storeDev.Get("dev-task-1")
		if err != nil {
			t.Fatalf("Get dev-task-1: %v", err)
		}
		if devRow.Status != TaskStatusRunning {
			t.Errorf("dev-task-1 status: want running (untouched by prod MarkCrashed), got %q", devRow.Status)
		}
	})

	// (b) Default List returns only the calling store's own rows.
	t.Run("b: List scoped to engine", func(t *testing.T) {
		prodList, err := storeProd.List("proj-shared")
		if err != nil {
			t.Fatalf("storeProd.List: %v", err)
		}
		for _, task := range prodList {
			if task.EngineName != "prod" {
				t.Errorf("storeProd.List returned task with engine_name=%q (want prod)", task.EngineName)
			}
		}

		devList, err := storeDev.List("proj-shared")
		if err != nil {
			t.Fatalf("storeDev.List: %v", err)
		}
		for _, task := range devList {
			if task.EngineName != "dev" {
				t.Errorf("storeDev.List returned task with engine_name=%q (want dev)", task.EngineName)
			}
		}

		if len(prodList) != 1 {
			t.Errorf("storeProd.List: want 1 task, got %d", len(prodList))
		}
		if len(devList) != 1 {
			t.Errorf("storeDev.List: want 1 task, got %d", len(devList))
		}
	})

	// (c) Get(uuid) from store-A succeeds for a task created by store-B.
	// Get is intentionally cross-engine (US3 / NFR-2) — tasks must be reachable
	// by UUID from any daemon for status queries and result retrieval.
	t.Run("c: Get is cross-engine", func(t *testing.T) {
		// store-A (prod) can retrieve a task stamped with engine_name=dev.
		devTaskFromProdStore, err := storeProd.Get("dev-task-1")
		if err != nil {
			t.Fatalf("storeProd.Get(dev-task-1): %v", err)
		}
		if devTaskFromProdStore.ID != "dev-task-1" {
			t.Errorf("Get cross-engine: want ID dev-task-1, got %q", devTaskFromProdStore.ID)
		}
		if devTaskFromProdStore.EngineName != "dev" {
			t.Errorf("Get cross-engine: EngineName want dev, got %q", devTaskFromProdStore.EngineName)
		}

		// store-B (dev) can retrieve a task stamped with engine_name=prod.
		prodTaskFromDevStore, err := storeDev.Get("prod-task-1")
		if err != nil {
			t.Fatalf("storeDev.Get(prod-task-1): %v", err)
		}
		if prodTaskFromDevStore.EngineName != "prod" {
			t.Errorf("Get cross-engine reverse: EngineName want prod, got %q", prodTaskFromDevStore.EngineName)
		}
	})

	// (d) ListAll from any store returns rows from both engines.
	t.Run("d: ListAll returns cross-engine rows", func(t *testing.T) {
		all, err := storeProd.ListAll()
		if err != nil {
			t.Fatalf("storeProd.ListAll: %v", err)
		}
		if len(all) < 2 {
			t.Fatalf("ListAll: want >= 2 rows (prod + dev), got %d", len(all))
		}

		seen := make(map[string]bool)
		for _, task := range all {
			seen[task.EngineName] = true
		}
		if !seen["prod"] {
			t.Error("ListAll: engine_name=prod not found in results")
		}
		if !seen["dev"] {
			t.Error("ListAll: engine_name=dev not found in results")
		}

		// Both store instances must return the same cross-engine view.
		allFromDev, err := storeDev.ListAll()
		if err != nil {
			t.Fatalf("storeDev.ListAll: %v", err)
		}
		if len(allFromDev) != len(all) {
			t.Errorf("ListAll count mismatch: prod store=%d dev store=%d", len(all), len(allFromDev))
		}
	})

	// (e) Pre-migration backfill rows (engine_name='') are invisible to default
	// List of either store, but visible to ListAll.
	// This validates that legacy rows from pre-AIMUX-10 databases do not
	// pollute either daemon's scoped view, and that operators can still
	// see them via the global escape hatch.
	t.Run("e: pre-migration rows invisible to List, visible to ListAll", func(t *testing.T) {
		// Insert a legacy row with engine_name='' directly — bypasses NewTaskStore
		// validation to simulate a pre-migration database row.
		_, err := db.Exec(`
			INSERT INTO tasks (id, status, worker_type, project_id, request_id, prompt,
			                   cwd, env, cli, role, model, effort, timeout, metadata,
			                   result, error, retries, created_at, engine_name)
			VALUES ('legacy-task-1', 'pending', 'cli', 'proj-shared', '', 'legacy prompt',
			        '', '{}', '', '', '', '', 0, '{}',
			        '', '', 0, ?, '')`,
			time.Now().UTC(),
		)
		if err != nil {
			t.Fatalf("INSERT legacy row: %v", err)
		}

		// Default List of prod store must NOT include the legacy row.
		prodList, err := storeProd.List("proj-shared")
		if err != nil {
			t.Fatalf("storeProd.List after legacy insert: %v", err)
		}
		for _, task := range prodList {
			if task.ID == "legacy-task-1" {
				t.Error("storeProd.List: legacy row (engine_name='') should be invisible to scoped List")
			}
		}

		// Default List of dev store must NOT include the legacy row.
		devList, err := storeDev.List("proj-shared")
		if err != nil {
			t.Fatalf("storeDev.List after legacy insert: %v", err)
		}
		for _, task := range devList {
			if task.ID == "legacy-task-1" {
				t.Error("storeDev.List: legacy row (engine_name='') should be invisible to scoped List")
			}
		}

		// ListAll MUST include the legacy row.
		all, err := storeProd.ListAll()
		if err != nil {
			t.Fatalf("storeProd.ListAll after legacy insert: %v", err)
		}
		found := false
		for _, task := range all {
			if task.ID == "legacy-task-1" {
				found = true
				break
			}
		}
		if !found {
			t.Error("ListAll: legacy row (engine_name='') must be visible in global cross-engine view")
		}
	})
}
