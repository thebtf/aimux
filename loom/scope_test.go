package loom

import (
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
