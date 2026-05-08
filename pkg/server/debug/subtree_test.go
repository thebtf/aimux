package debug

import (
	"errors"
	"strings"
	"testing"

	"github.com/thebtf/aimux/loom"
)

func TestFormatSubtreeRendersMultiLevelTree(t *testing.T) {
	getter := &fakeTreeGetter{nodes: []loom.TaskNode{
		{ID: "root", ProjectID: "proj", WorkerType: "code", Status: loom.TaskStatusCompleted, Depth: 0, SubtaskIDs: []string{"driver", "navigator"}},
		{ID: "driver", ParentTaskID: "root", ProjectID: "proj", WorkerType: "code_driver", Status: loom.TaskStatusCompleted, Depth: 1},
		{ID: "navigator", ParentTaskID: "root", ProjectID: "proj", WorkerType: "code_navigator", Status: loom.TaskStatusFailed, Depth: 1},
	}}

	text, err := FormatSubtree(getter, "root", 3)
	if err != nil {
		t.Fatalf("FormatSubtree returned error: %v", err)
	}
	assertContains(t, text, "- root depth=0 worker=code status=completed project=proj children=2")
	assertContains(t, text, "  - driver depth=1 worker=code_driver status=completed project=proj parent=root children=0")
	assertContains(t, text, "  - navigator depth=1 worker=code_navigator status=failed project=proj parent=root children=0")
	if strings.TrimSpace(text) == "" {
		t.Fatal("formatted subtree is empty")
	}
}

func TestFormatSubtreePassesDepthLimitToLoom(t *testing.T) {
	getter := &fakeTreeGetter{nodes: []loom.TaskNode{{ID: "root", Depth: 0}}}

	if _, err := FormatSubtree(getter, "root", 1); err != nil {
		t.Fatalf("FormatSubtree returned error: %v", err)
	}
	if getter.gotTaskID != "root" {
		t.Fatalf("taskID = %q, want root", getter.gotTaskID)
	}
	if getter.gotMaxDepth != 1 {
		t.Fatalf("maxDepth = %d, want 1", getter.gotMaxDepth)
	}
}

func TestFormatSubtreeReturnsDepthError(t *testing.T) {
	getter := &fakeTreeGetter{err: loom.ErrSubtaskDepthExceeded}

	_, err := FormatSubtree(getter, "root", 1)
	if !errors.Is(err, loom.ErrSubtaskDepthExceeded) {
		t.Fatalf("error = %v, want ErrSubtaskDepthExceeded", err)
	}
}

func TestFormatSubtreeDetectsDuplicateNodeCycle(t *testing.T) {
	getter := &fakeTreeGetter{nodes: []loom.TaskNode{
		{ID: "root", Depth: 0},
		{ID: "child", ParentTaskID: "root", Depth: 1},
		{ID: "root", ParentTaskID: "child", Depth: 2},
	}}

	_, err := FormatSubtree(getter, "root", 4)
	if !errors.Is(err, ErrSubtreeCycle) {
		t.Fatalf("error = %v, want ErrSubtreeCycle", err)
	}
}

type fakeTreeGetter struct {
	nodes       []loom.TaskNode
	err         error
	gotTaskID   string
	gotMaxDepth int
}

func (f *fakeTreeGetter) GetTree(taskID string, maxDepth int) ([]loom.TaskNode, error) {
	f.gotTaskID = taskID
	f.gotMaxDepth = maxDepth
	if f.err != nil {
		return nil, f.err
	}
	return f.nodes, nil
}

func assertContains(t *testing.T, text string, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("formatted subtree missing %q:\n%s", want, text)
	}
}
