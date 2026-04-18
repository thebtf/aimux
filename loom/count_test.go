package loom

import (
	"testing"
)

func TestTaskStore_CountAll(t *testing.T) {
	store := newTestStore(t)

	if err := store.Create(makeTask("t1", "proj-a", TaskStatusPending)); err != nil {
		t.Fatalf("Create t1: %v", err)
	}
	if err := store.Create(makeTask("t2", "proj-a", TaskStatusPending)); err != nil {
		t.Fatalf("Create t2: %v", err)
	}
	if err := store.Create(makeTask("t3", "proj-b", TaskStatusPending)); err != nil {
		t.Fatalf("Create t3: %v", err)
	}

	got, err := store.Count(TaskFilter{})
	if err != nil {
		t.Fatalf("Count all: %v", err)
	}
	if got != 3 {
		t.Fatalf("Count() = %d, want 3", got)
	}
}

func TestTaskStore_CountByProject(t *testing.T) {
	store := newTestStore(t)
	if err := store.Create(makeTask("t1", "proj-a", TaskStatusPending)); err != nil {
		t.Fatalf("Create t1: %v", err)
	}
	if err := store.Create(makeTask("t2", "proj-a", TaskStatusPending)); err != nil {
		t.Fatalf("Create t2: %v", err)
	}
	if err := store.Create(makeTask("t3", "proj-b", TaskStatusPending)); err != nil {
		t.Fatalf("Create t3: %v", err)
	}

	got, err := store.Count(TaskFilter{ProjectID: "proj-a"})
	if err != nil {
		t.Fatalf("Count proj-a: %v", err)
	}
	if got != 2 {
		t.Fatalf("proj-a count = %d, want 2", got)
	}
}

func TestTaskStore_CountByProjectAndStatuses(t *testing.T) {
	store := newTestStore(t)
	if err := store.Create(makeTask("t1", "proj-a", TaskStatusPending)); err != nil {
		t.Fatalf("Create t1: %v", err)
	}
	if err := store.Create(makeTask("t2", "proj-a", TaskStatusPending)); err != nil {
		t.Fatalf("Create t2: %v", err)
	}
	if err := store.Create(makeTask("t3", "proj-a", TaskStatusRunning)); err != nil {
		t.Fatalf("Create t3: %v", err)
	}

	got, err := store.Count(TaskFilter{
		ProjectID: "proj-a",
		Statuses:  []TaskStatus{TaskStatusRunning},
	})
	if err != nil {
		t.Fatalf("Count proj-a/running: %v", err)
	}
	if got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
}

func TestLoomEngine_CountDelegatesToStore(t *testing.T) {
	engine := New(newTestStore(t))
	_ = engine
	// newTestStore is not exported outside this package file scope, but it is
	// available here because this test is in package loom.
	store := engine.store

	if err := store.Create(makeTask("t1", "proj-x", TaskStatusPending)); err != nil {
		t.Fatalf("Create t1: %v", err)
	}

	got, err := engine.Count(TaskFilter{ProjectID: "proj-x"})
	if err != nil {
		t.Fatalf("engine.Count: %v", err)
	}
	if got != 1 {
		t.Fatalf("engine.Count = %d, want 1", got)
	}
}
