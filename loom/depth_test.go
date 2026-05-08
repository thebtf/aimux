package loom

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/thebtf/aimux/loom/clierror"
)

func TestLoomEngine_Submit_SubtaskDepthDefaultAllowsDepth8AndRejectsDepth9(t *testing.T) {
	engine := newDepthTestEngine(t, Config{})

	parentID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-depth-default",
		Prompt:     "depth 0",
	})
	if err != nil {
		t.Fatalf("Submit root: %v", err)
	}

	for depth := 1; depth <= 8; depth++ {
		taskID, submitErr := engine.Submit(context.Background(), TaskRequest{
			WorkerType:   WorkerTypeCLI,
			ParentTaskID: parentID,
			Prompt:       fmt.Sprintf("depth %d", depth),
		})
		if submitErr != nil {
			t.Fatalf("Submit depth %d: %v", depth, submitErr)
		}
		parentID = taskID
	}

	rejectedID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType:   WorkerTypeCLI,
		ParentTaskID: parentID,
		Prompt:       "depth 9",
	})
	if err == nil {
		t.Fatal("Submit depth 9: want CapabilityMismatch, got nil")
	}
	if rejectedID != "" {
		t.Fatalf("rejected depth 9 taskID = %q, want empty", rejectedID)
	}
	assertDepthCapabilityMismatch(t, err, "subtask depth exceeded; max=8")
}

func TestLoomEngine_Submit_SubtaskDepthConfigurableLowerBound(t *testing.T) {
	engine := newDepthTestEngine(t, Config{MaxSubtaskDepth: 2})

	parentID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-depth-lower",
		Prompt:     "depth 0",
	})
	if err != nil {
		t.Fatalf("Submit root: %v", err)
	}

	for depth := 1; depth <= 2; depth++ {
		taskID, submitErr := engine.Submit(context.Background(), TaskRequest{
			WorkerType:   WorkerTypeCLI,
			ParentTaskID: parentID,
			Prompt:       fmt.Sprintf("depth %d", depth),
		})
		if submitErr != nil {
			t.Fatalf("Submit depth %d: %v", depth, submitErr)
		}
		parentID = taskID
	}

	rejectedID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType:   WorkerTypeCLI,
		ParentTaskID: parentID,
		Prompt:       "depth 3",
	})
	if err == nil {
		t.Fatal("Submit depth 3: want CapabilityMismatch, got nil")
	}
	if rejectedID != "" {
		t.Fatalf("rejected depth 3 taskID = %q, want empty", rejectedID)
	}
	assertDepthCapabilityMismatch(t, err, "subtask depth exceeded; max=2")
}

func newDepthTestEngine(t *testing.T, cfg Config) *LoomEngine {
	t.Helper()
	engine := New(newTestStore(t), WithConfig(cfg))
	engine.RegisterWorker(WorkerTypeCLI, &testWorker{wtype: WorkerTypeCLI, result: "ok"})
	return engine
}

func assertDepthCapabilityMismatch(t *testing.T, err error, wantMessage string) {
	t.Helper()

	var cliErr *clierror.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *clierror.CLIError: %v", err, err)
	}
	if cliErr.Code != clierror.CLIErrorCodeCapabilityMismatch {
		t.Fatalf("CLIError code = %v, want CapabilityMismatch", cliErr.Code)
	}
	if !strings.Contains(cliErr.Message, wantMessage) {
		t.Fatalf("CLIError message = %q, want containing %q", cliErr.Message, wantMessage)
	}
}
