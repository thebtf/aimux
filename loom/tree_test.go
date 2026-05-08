package loom

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestLoomEngine_GetTree_PreOrder(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	base := time.Now().UTC().Truncate(time.Second)

	tasks := []*Task{
		treeTask("root", "", base),
		treeTask("child-a", "root", base.Add(time.Second)),
		treeTask("grandchild-a", "child-a", base.Add(2*time.Second)),
		treeTask("child-b", "root", base.Add(3*time.Second)),
	}
	for _, task := range tasks {
		if err := store.Create(task); err != nil {
			t.Fatalf("Create %s: %v", task.ID, err)
		}
	}

	nodes, err := engine.GetTree("root", 2)
	if err != nil {
		t.Fatalf("GetTree: %v", err)
	}
	gotIDs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		gotIDs = append(gotIDs, node.ID)
	}
	wantIDs := []string{"root", "child-a", "grandchild-a", "child-b"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("pre-order IDs = %v, want %v", gotIDs, wantIDs)
	}
	if !reflect.DeepEqual(nodes[0].SubtaskIDs, []string{"child-a", "child-b"}) {
		t.Fatalf("root SubtaskIDs = %v, want [child-a child-b]", nodes[0].SubtaskIDs)
	}
	if nodes[2].Depth != 2 {
		t.Fatalf("grandchild depth = %d, want 2", nodes[2].Depth)
	}
	if nodes[2].ParentTaskID != "child-a" {
		t.Fatalf("grandchild parent = %q, want child-a", nodes[2].ParentTaskID)
	}
}

func TestLoomEngine_GetTree_DepthExceeded(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	base := time.Now().UTC().Truncate(time.Second)

	for _, task := range []*Task{
		treeTask("root", "", base),
		treeTask("child", "root", base.Add(time.Second)),
		treeTask("grandchild", "child", base.Add(2*time.Second)),
	} {
		if err := store.Create(task); err != nil {
			t.Fatalf("Create %s: %v", task.ID, err)
		}
	}

	nodes, err := engine.GetTree("root", 1)
	if !errors.Is(err, ErrSubtaskDepthExceeded) {
		t.Fatalf("GetTree error = %v, want ErrSubtaskDepthExceeded", err)
	}
	if nodes != nil {
		t.Fatalf("nodes on depth error = %v, want nil", nodes)
	}
}

func treeTask(id, parentID string, createdAt time.Time) *Task {
	return &Task{
		ID:           id,
		ParentTaskID: parentID,
		Status:       TaskStatusPending,
		WorkerType:   WorkerTypeCLI,
		ProjectID:    "proj-tree",
		Prompt:       id,
		CreatedAt:    createdAt,
	}
}
