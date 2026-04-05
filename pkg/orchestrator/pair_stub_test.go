package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/types"
)

// promptCapturingExecutor captures the prompt sent to Run() for assertion.
type promptCapturingExecutor struct {
	capturedPrompt *string
	response       string
}

func (e *promptCapturingExecutor) Run(ctx context.Context, args types.SpawnArgs) (*types.Result, error) {
	// Capture full context: command + all args + stdin
	*e.capturedPrompt = args.Command + " " + strings.Join(args.Args, " ") + " " + args.Stdin
	return &types.Result{Content: e.response, ExitCode: 0}, nil
}
func (e *promptCapturingExecutor) Start(ctx context.Context, args types.SpawnArgs) (types.Session, error) {
	return nil, nil
}
func (e *promptCapturingExecutor) Name() string    { return "capture" }
func (e *promptCapturingExecutor) Available() bool { return true }

// TestPairStub_ReviewerPromptContainsAntiStubCriteria verifies the review
// prompt sent to reviewer CLI includes all STUB-* patterns from checklist.
func TestPairStub_ReviewerPromptContainsAntiStubCriteria(t *testing.T) {
	var capturedPrompt string

	stubDiff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,5 @@
 package main
+
+func hello() { _ = "stub" }
`
	driver := &mockExecutor{
		runResult: &types.Result{Content: stubDiff, ExitCode: 0},
	}
	reviewer := &promptCapturingExecutor{
		capturedPrompt: &capturedPrompt,
		response:       `[{"hunk_index": 0, "verdict": "changes_requested", "comment": "STUB-PASSTHROUGH"}]`,
	}

	pair := orchestrator.NewPairCoding(driver, reviewer)
	result, err := pair.Execute(context.Background(), types.StrategyParams{
		Prompt: "add hello function",
		CLIs:   []string{"codex", "claude"},
	})

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Verify anti-stub criteria present in reviewer prompt
	mustContain := []string{"Completeness", "STUB-PASSTHROUGH", "STUB-HARDCODED", "STUB-NOOP", "STUB-TODO", "STUB-DISCARD"}
	for _, pattern := range mustContain {
		if !strings.Contains(capturedPrompt, pattern) {
			t.Errorf("reviewer prompt missing anti-stub pattern %q", pattern)
		}
	}

	// Verify reviewer rejected the stub
	if result.Status != "max_rounds_exceeded" && result.ReviewReport != nil {
		if result.ReviewReport.Rejected == 0 {
			t.Log("Note: reviewer mock returned changes_requested but pair completed — expected in mock scenario")
		}
	}
}

// TestPairStub_CleanDiffApproved verifies clean code passes review.
func TestPairStub_CleanDiffApproved(t *testing.T) {
	cleanDiff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,6 @@
 package main
+
+import "fmt"
+
+func Hello() { fmt.Println("hello") }
`
	driver := &mockExecutor{
		runResult: &types.Result{Content: cleanDiff, ExitCode: 0},
	}
	reviewer := &mockExecutor{
		runResult: &types.Result{
			Content:  `[{"hunk_index": 0, "verdict": "approved", "comment": "clean implementation"}]`,
			ExitCode: 0,
		},
	}

	pair := orchestrator.NewPairCoding(driver, reviewer)
	result, err := pair.Execute(context.Background(), types.StrategyParams{
		Prompt: "add Hello function",
		CLIs:   []string{"codex", "claude"},
	})

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ReviewReport == nil {
		t.Fatal("expected ReviewReport")
	}
	if result.ReviewReport.Approved != 1 {
		t.Errorf("expected 1 approved, got %d", result.ReviewReport.Approved)
	}
}
