package loom

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thebtf/aimux/loom/clierror"
)

func TestLoomEngine_Submit_AutoInheritsProjectIDFromParent(t *testing.T) {
	store := newTestStore(t)
	engine := newProjectIDInheritanceEngine(store)

	rootID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-parent",
		Prompt:     "root",
	})
	if err != nil {
		t.Fatalf("Submit root: %v", err)
	}

	childID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType:   WorkerTypeCLI,
		ParentTaskID: rootID,
		Prompt:       "child",
	})
	if err != nil {
		t.Fatalf("Submit child: %v", err)
	}

	child, err := engine.Get(childID)
	if err != nil {
		t.Fatalf("Get child: %v", err)
	}
	if child.ProjectID != "proj-parent" {
		t.Fatalf("child ProjectID = %q, want inherited proj-parent", child.ProjectID)
	}
}

func TestLoomEngine_Submit_MatchingParentProjectIDSucceeds(t *testing.T) {
	store := newTestStore(t)
	engine := newProjectIDInheritanceEngine(store)

	rootID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-shared",
		Prompt:     "root",
	})
	if err != nil {
		t.Fatalf("Submit root: %v", err)
	}

	childID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType:   WorkerTypeCLI,
		ProjectID:    "proj-shared",
		ParentTaskID: rootID,
		Prompt:       "child",
	})
	if err != nil {
		t.Fatalf("Submit child: %v", err)
	}

	child, err := engine.Get(childID)
	if err != nil {
		t.Fatalf("Get child: %v", err)
	}
	if child.ProjectID != "proj-shared" {
		t.Fatalf("child ProjectID = %q, want proj-shared", child.ProjectID)
	}
}

func TestLoomEngine_Submit_RejectsMismatchedParentProjectID(t *testing.T) {
	store := newTestStore(t)
	engine := newProjectIDInheritanceEngine(store)

	rootID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-parent",
		Prompt:     "root",
	})
	if err != nil {
		t.Fatalf("Submit root: %v", err)
	}

	childID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType:   WorkerTypeCLI,
		ProjectID:    "proj-other",
		ParentTaskID: rootID,
		Prompt:       "child",
	})
	if err == nil {
		t.Fatal("Submit child mismatch: want error, got nil")
	}
	if childID != "" {
		t.Fatalf("childID on mismatch = %q, want empty", childID)
	}

	var cliErr *clierror.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("mismatch error type = %T, want *clierror.CLIError: %v", err, err)
	}
	if cliErr.Code != clierror.CLIErrorCodeCapabilityMismatch {
		t.Fatalf("mismatch code = %v, want CapabilityMismatch", cliErr.Code)
	}
	if !strings.Contains(cliErr.Message, "subtask ProjectID must match parent ProjectID") {
		t.Fatalf("mismatch message = %q", cliErr.Message)
	}
}

func TestLoomEngine_Submit_RootAcceptsAnyProjectID(t *testing.T) {
	store := newTestStore(t)
	engine := newProjectIDInheritanceEngine(store)

	rootID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-free-root",
		Prompt:     "root",
	})
	if err != nil {
		t.Fatalf("Submit root: %v", err)
	}

	root, err := engine.Get(rootID)
	if err != nil {
		t.Fatalf("Get root: %v", err)
	}
	if root.ProjectID != "proj-free-root" {
		t.Fatalf("root ProjectID = %q, want proj-free-root", root.ProjectID)
	}
}

func newProjectIDInheritanceEngine(store *TaskStore) *LoomEngine {
	engine := New(store)
	engine.RegisterWorker(WorkerTypeCLI, &testWorker{wtype: WorkerTypeCLI, result: "ok"})
	return engine
}
