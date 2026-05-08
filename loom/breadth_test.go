package loom

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom/clierror"
)

func TestLoomEngine_Submit_SubtaskBreadthDefaultAllows16AndRejects17th(t *testing.T) {
	engine, store, done := newBreadthTestEngine(t, Config{})

	rootID := submitBreadthRoot(t, engine)
	childIDs := make([]string, 0, DefaultMaxSubtaskBreadth)
	for i := 0; i < DefaultMaxSubtaskBreadth; i++ {
		childID, err := engine.Submit(context.Background(), TaskRequest{
			WorkerType:   WorkerTypeThinker,
			ParentTaskID: rootID,
			Prompt:       fmt.Sprintf("child %d", i),
		})
		if err != nil {
			t.Fatalf("Submit child %d within default breadth: %v", i, err)
		}
		childIDs = append(childIDs, childID)
	}
	for _, childID := range childIDs {
		waitForTaskStatus(t, store, childID, TaskStatusRunning)
	}

	rejectedID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType:   WorkerTypeThinker,
		ParentTaskID: rootID,
		Prompt:       "child over default breadth",
	})
	if err == nil {
		t.Fatal("Submit 17th child: want CapabilityMismatch, got nil")
	}
	if rejectedID != "" {
		t.Fatalf("rejected 17th child taskID = %q, want empty", rejectedID)
	}
	assertBreadthCapabilityMismatch(t, err)

	close(done)
	_ = engine.Close(context.Background())
}

func TestLoomEngine_Submit_SubtaskBreadthCompletionFreesSlot(t *testing.T) {
	engine, store, done := newBreadthTestEngine(t, Config{MaxSubtaskBreadth: 2})

	rootID := submitBreadthRoot(t, engine)
	childIDs := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		childID, err := engine.Submit(context.Background(), TaskRequest{
			WorkerType:   WorkerTypeThinker,
			ParentTaskID: rootID,
			Prompt:       fmt.Sprintf("bounded child %d", i),
		})
		if err != nil {
			t.Fatalf("Submit child %d within configured breadth: %v", i, err)
		}
		childIDs = append(childIDs, childID)
		waitForTaskStatus(t, store, childID, TaskStatusRunning)
	}

	rejectedID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType:   WorkerTypeThinker,
		ParentTaskID: rootID,
		Prompt:       "over configured breadth",
	})
	if err == nil {
		t.Fatal("Submit over configured breadth: want CapabilityMismatch, got nil")
	}
	if rejectedID != "" {
		t.Fatalf("rejected over-breadth taskID = %q, want empty", rejectedID)
	}
	assertBreadthCapabilityMismatch(t, err)

	done <- struct{}{}
	waitForAnyTaskStatus(t, store, childIDs, TaskStatusCompleted)

	replacementID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType:   WorkerTypeThinker,
		ParentTaskID: rootID,
		Prompt:       "replacement after completion",
	})
	if err != nil {
		t.Fatalf("Submit replacement after completion: %v", err)
	}
	if replacementID == "" {
		t.Fatal("replacement taskID is empty")
	}

	close(done)
	_ = engine.Close(context.Background())
}

func newBreadthTestEngine(t *testing.T, cfg Config) (*LoomEngine, *TaskStore, chan struct{}) {
	t.Helper()
	store := newTestStore(t)
	done := make(chan struct{})
	engine := New(store, WithConfig(cfg))
	engine.RegisterWorker(WorkerTypeCLI, &testWorker{wtype: WorkerTypeCLI, result: "root ok"})
	engine.RegisterWorker(WorkerTypeThinker, &blockingWorker{done: done})
	return engine, store, done
}

func submitBreadthRoot(t *testing.T, engine *LoomEngine) string {
	t.Helper()
	rootID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-breadth",
		Prompt:     "root",
	})
	if err != nil {
		t.Fatalf("Submit root: %v", err)
	}
	return rootID
}

func waitForAnyTaskStatus(t *testing.T, store *TaskStore, taskIDs []string, want TaskStatus) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var latest []*Task
	for time.Now().Before(deadline) {
		latest = latest[:0]
		for _, taskID := range taskIDs {
			task, err := store.Get(taskID)
			if err != nil {
				t.Fatalf("Get(%s): %v", taskID, err)
			}
			if task.Status == want {
				return taskID
			}
			latest = append(latest, task)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no task reached %s, latest=%#v", want, latest)
	return ""
}

func assertBreadthCapabilityMismatch(t *testing.T, err error) {
	t.Helper()

	var cliErr *clierror.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *clierror.CLIError: %v", err, err)
	}
	if cliErr.Code != clierror.CLIErrorCodeCapabilityMismatch {
		t.Fatalf("CLIError code = %v, want CapabilityMismatch", cliErr.Code)
	}
	if !strings.Contains(cliErr.Message, "root subtask budget exhausted") {
		t.Fatalf("CLIError message = %q, want root subtask budget exhausted", cliErr.Message)
	}
}
