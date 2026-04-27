package loom

import (
	"fmt"
	"testing"
)

// TestTaskStore_List_ScopedByEngineName verifies that TaskStore.List returns
// only tasks belonging to the store's own engine_name (AIMUX-10 FR-4).
// Two stores with distinct engineNames share a single in-memory SQLite database;
// each must see only the tasks it created.
func TestTaskStore_List_ScopedByEngineName(t *testing.T) {
	// Use a shared in-memory DB — both stores must see the same table.
	db := newTestDB(t)

	storeA, err := NewTaskStore(db, "engineA")
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewTaskStore(db, "engineB")
	if err != nil {
		t.Fatal(err)
	}

	// Submit one task per store, both in the same project.
	taskA := makeTask("task-a", "proj-x", TaskStatusPending)
	if err := storeA.Create(taskA); err != nil {
		t.Fatal(err)
	}
	taskB := makeTask("task-b", "proj-x", TaskStatusPending)
	if err := storeB.Create(taskB); err != nil {
		t.Fatal(err)
	}

	t.Run("storeA sees only its own task", func(t *testing.T) {
		list, err := storeA.List("proj-x")
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 {
			t.Fatalf("storeA.List: want 1 task, got %d", len(list))
		}
		if list[0].ID != "task-a" {
			t.Fatalf("storeA.List: want task-a, got %q", list[0].ID)
		}
		if list[0].EngineName != "engineA" {
			t.Fatalf("storeA.List: EngineName want engineA, got %q", list[0].EngineName)
		}
	})

	t.Run("storeB sees only its own task", func(t *testing.T) {
		list, err := storeB.List("proj-x")
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 {
			t.Fatalf("storeB.List: want 1 task, got %d", len(list))
		}
		if list[0].ID != "task-b" {
			t.Fatalf("storeB.List: want task-b, got %q", list[0].ID)
		}
		if list[0].EngineName != "engineB" {
			t.Fatalf("storeB.List: EngineName want engineB, got %q", list[0].EngineName)
		}
	})

	t.Run("status-filtered list also scoped", func(t *testing.T) {
		listA, err := storeA.List("proj-x", TaskStatusPending)
		if err != nil {
			t.Fatal(err)
		}
		if len(listA) != 1 || listA[0].ID != "task-a" {
			t.Fatalf("storeA.List(queued): want [task-a], got %v", listA)
		}

		listB, err := storeB.List("proj-x", TaskStatusPending)
		if err != nil {
			t.Fatal(err)
		}
		if len(listB) != 1 || listB[0].ID != "task-b" {
			t.Fatalf("storeB.List(queued): want [task-b], got %v", listB)
		}
	})
}

// TestTaskStore_ListAll_CrossEngine verifies that ListAll returns tasks from both
// engines sharing the same database (AIMUX-10 FR-5).
func TestTaskStore_ListAll_CrossEngine(t *testing.T) {
	db := newTestDB(t)

	storeA, err := NewTaskStore(db, "aimux")
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewTaskStore(db, "aimux-dev")
	if err != nil {
		t.Fatal(err)
	}

	taskA := makeTask("all-a", "proj-x", TaskStatusPending)
	if err := storeA.Create(taskA); err != nil {
		t.Fatal(err)
	}
	taskB := makeTask("all-b", "proj-x", TaskStatusPending)
	if err := storeB.Create(taskB); err != nil {
		t.Fatal(err)
	}

	t.Run("ListAll from storeA returns both", func(t *testing.T) {
		all, err := storeA.ListAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 2 {
			t.Fatalf("ListAll: want 2 tasks, got %d", len(all))
		}
	})

	t.Run("ListAll from storeB returns both", func(t *testing.T) {
		all, err := storeB.ListAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 2 {
			t.Fatalf("ListAll: want 2 tasks, got %d", len(all))
		}
	})

	t.Run("EngineName populated on cross-engine rows", func(t *testing.T) {
		all, err := storeA.ListAll()
		if err != nil {
			t.Fatal(err)
		}
		engines := make(map[string]bool)
		for _, task := range all {
			engines[task.EngineName] = true
		}
		if !engines["aimux"] || !engines["aimux-dev"] {
			t.Fatalf("ListAll: expected both engine names, got %v", engines)
		}
	})
}

// TestTaskStore_Count_ScopedByEngineName verifies that Count is scoped by engine_name
// and CountAll returns the union (AIMUX-10 FR-4, FR-5).
func TestTaskStore_Count_ScopedByEngineName(t *testing.T) {
	db := newTestDB(t)

	storeProd, err := NewTaskStore(db, "aimux")
	if err != nil {
		t.Fatal(err)
	}
	storeDev, err := NewTaskStore(db, "aimux-dev")
	if err != nil {
		t.Fatal(err)
	}

	// 2 prod tasks, 1 dev task
	for i := 0; i < 2; i++ {
		if err := storeProd.Create(makeTask(fmt.Sprintf("prod-%d", i), "proj-x", TaskStatusPending)); err != nil {
			t.Fatalf("prod Create %d: %v", i, err)
		}
	}
	if err := storeDev.Create(makeTask("dev-0", "proj-x", TaskStatusPending)); err != nil {
		t.Fatalf("dev Create: %v", err)
	}

	t.Run("Count scoped to prod engine", func(t *testing.T) {
		got, err := storeProd.Count(TaskFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if got != 2 {
			t.Errorf("prod Count = %d, want 2", got)
		}
	})

	t.Run("Count scoped to dev engine", func(t *testing.T) {
		got, err := storeDev.Count(TaskFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if got != 1 {
			t.Errorf("dev Count = %d, want 1", got)
		}
	})

	t.Run("CountAll returns union", func(t *testing.T) {
		got, err := storeProd.CountAll()
		if err != nil {
			t.Fatal(err)
		}
		if got != 3 {
			t.Errorf("CountAll = %d, want 3", got)
		}
	})
}
