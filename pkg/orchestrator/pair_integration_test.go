package orchestrator_test

import (
	"context"
	"testing"

	"github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/types"
)

// TestPairIntegration_EndToEnd simulates the full pair coding pipeline:
// exec → driver produces diff → reviewer validates → result returned
func TestPairIntegration_EndToEnd(t *testing.T) {
	diffOutput := `diff --git a/hello.go b/hello.go
--- a/hello.go
+++ b/hello.go
@@ -1,3 +1,7 @@
 package main

+import "fmt"
+
+func Hello() {
+	fmt.Println("hello")
+}
`
	reviewJSON := `[{"hunk_index": 0, "verdict": "approved", "comment": "clean implementation"}]`

	driver := &mockExecutor{runResult: &types.Result{Content: diffOutput, ExitCode: 0}}
	reviewer := &mockExecutor{runResult: &types.Result{Content: reviewJSON, ExitCode: 0}}

	pair := orchestrator.NewPairCoding(driver, reviewer)

	result, err := pair.Execute(context.Background(), types.StrategyParams{
		Prompt:  "Add Hello() function to hello.go",
		CWD:     "/tmp/test-project",
		CLIs:    []string{"codex", "claude"},
		Timeout: 300,
		Extra:   map[string]any{"max_rounds": 3},
	})

	if err != nil {
		t.Fatalf("pair execute: %v", err)
	}

	// Verify result
	if result.Status != "completed" {
		t.Errorf("status = %q, want completed", result.Status)
	}
	if result.ReviewReport == nil {
		t.Fatal("missing review report")
	}
	if result.ReviewReport.Approved != 1 {
		t.Errorf("approved = %d, want 1", result.ReviewReport.Approved)
	}
	if result.ReviewReport.Rejected != 0 {
		t.Errorf("rejected = %d, want 0", result.ReviewReport.Rejected)
	}
	if result.ReviewReport.DriverCLI != "codex" {
		t.Errorf("driver = %q, want codex", result.ReviewReport.DriverCLI)
	}
	if result.ReviewReport.ReviewerCLI != "claude" {
		t.Errorf("reviewer = %q, want claude", result.ReviewReport.ReviewerCLI)
	}
	if len(result.Participants) != 2 {
		t.Errorf("participants = %d, want 2", len(result.Participants))
	}
}

// TestPairIntegration_ComplexMode verifies complex mode returns structured data
func TestPairIntegration_ComplexMode(t *testing.T) {
	diffOutput := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1 +1,2 @@
 package main
+func init() {}
`
	reviewJSON := `[{"hunk_index": 0, "verdict": "approved"}]`

	driver := &mockExecutor{runResult: &types.Result{Content: diffOutput, ExitCode: 0}}
	reviewer := &mockExecutor{runResult: &types.Result{Content: reviewJSON, ExitCode: 0}}

	pair := orchestrator.NewPairCoding(driver, reviewer)

	result, err := pair.Execute(context.Background(), types.StrategyParams{
		Prompt: "Add init function",
		CLIs:   []string{"codex", "claude"},
		Extra:  map[string]any{"complex": true},
	})

	if err != nil {
		t.Fatalf("pair execute: %v", err)
	}

	if result.Extra == nil {
		t.Fatal("complex mode should populate Extra")
	}
	if _, ok := result.Extra["driver_diff"]; !ok {
		t.Error("complex mode should include driver_diff in Extra")
	}
}
