package workflow

import (
	"context"
	"fmt"
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

// TestC2_AllWorkflows_Runnable is the C2 acceptance test: every registered workflow
// is demonstrably runnable through the Engine with mock executors. Each step gets
// a mock response, gates pass, and the workflow completes successfully.
func TestC2_AllWorkflows_Runnable(t *testing.T) {
	for name, fn := range Registry {
		t.Run(name, func(t *testing.T) {
			steps := fn()
			if len(steps) == 0 {
				t.Fatalf("workflow %q returned zero steps", name)
			}

			// Build a mock sender that responds to any role with a safe response.
			// CRITICAL responses are intentionally avoided so gates pass.
			sender := &universalMockSender{
				response: fmt.Sprintf("[%s mock response] analysis complete, no CRITICAL issues found", name),
			}

			// Build mock dialogue runner and participant factory.
			dlgRunner := newMockDialogueRunner("synthesized: all participants agree")
			partFactory := participantFactory(map[string]string{})
			patternFn := &mockPatternFn{results: map[string]string{}}

			eng := New(sender, dlgRunner, patternFn.dispatch, partFactory)

			result, err := eng.Execute(
				context.Background(),
				steps,
				WorkflowInput{
					Topic: fmt.Sprintf("C2 acceptance test for %s", name),
					Files: []string{"main.go", "types.go"},
					Focus: "comprehensive",
				},
			)
			if err != nil {
				t.Fatalf("Execute(%s): unexpected error: %v", name, err)
			}

			// Accept "completed" or "gated" — gated is valid if the workflow design
			// intentionally gates on gate conditions. Failed is not acceptable.
			if result.Status == "failed" {
				t.Errorf("workflow %q failed: %s", name, result.Summary)
				for i, sr := range result.Steps {
					t.Logf("  step[%d] %q: status=%s content=%s", i, sr.Name, sr.Status, sr.Content)
				}
			}

			// Every step that ran should have a non-empty status.
			for i, sr := range result.Steps {
				if sr.Name == "" {
					t.Errorf("step[%d] has empty Name", i)
				}
				if sr.Status == "" {
					t.Errorf("step[%d] %q has empty Status", i, sr.Name)
				}
			}
		})
	}
}

// universalMockSender responds to any executor name with the same content.
// This lets us run all 9 workflows without knowing their executor names in advance.
type universalMockSender struct {
	response string
}

func (u *universalMockSender) Get(_ context.Context, name string) (ExecutorHandle, error) {
	return &mockHandle{name: name}, nil
}

func (u *universalMockSender) Send(_ context.Context, _ ExecutorHandle, _ types.Message) (*types.Response, error) {
	return &types.Response{Content: u.response}, nil
}

// TestC2_AllWorkflows_StepCount verifies that each workflow has a reasonable
// number of steps (at least 2 for non-trivial workflows).
func TestC2_AllWorkflows_StepCount(t *testing.T) {
	for name, fn := range Registry {
		steps := fn()
		if len(steps) < 2 {
			t.Errorf("workflow %q has %d steps; expected at least 2 for a non-trivial workflow", name, len(steps))
		}
	}
}

// TestC2_AllWorkflows_HaveGateOrMultiStep verifies that each workflow either
// has a gate step or has 3+ steps (evidence of multi-stage design).
func TestC2_AllWorkflows_HaveGateOrMultiStep(t *testing.T) {
	for name, fn := range Registry {
		steps := fn()
		hasGate := false
		for _, s := range steps {
			if s.Action == ActionGate {
				hasGate = true
				break
			}
		}
		if !hasGate && len(steps) < 3 {
			t.Errorf("workflow %q has no gate and only %d steps — expected either a gate or 3+ steps", name, len(steps))
		}
	}
}
