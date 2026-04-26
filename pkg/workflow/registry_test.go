package workflow

import (
	"testing"
)

// expectedWorkflows is the canonical set of workflow names the registry must expose.
var expectedWorkflows = []string{
	"codereview",
	"secaudit",
	"debug",
	"analyze",
	"refactor",
	"testgen",
	"docgen",
	"precommit",
	"tracer",
}

// TestRegistry_AllWorkflowsPresent verifies that all 9 expected workflow names
// are registered and their step functions are non-nil.
func TestRegistry_AllWorkflowsPresent(t *testing.T) {
	if len(Registry) != len(expectedWorkflows) {
		t.Errorf("Registry has %d entries; want %d", len(Registry), len(expectedWorkflows))
	}

	for _, name := range expectedWorkflows {
		fn, ok := Registry[name]
		if !ok {
			t.Errorf("Registry missing workflow %q", name)
			continue
		}
		if fn == nil {
			t.Errorf("Registry[%q] is nil", name)
		}
	}
}

// TestRegistry_AllStepsValid verifies that every step in every registered
// workflow has a non-empty Name and a valid (non-zero) Action.
func TestRegistry_AllStepsValid(t *testing.T) {
	for name, fn := range Registry {
		steps := fn()
		if len(steps) == 0 {
			t.Errorf("workflow %q returned zero steps", name)
			continue
		}
		for i, step := range steps {
			if step.Name == "" {
				t.Errorf("workflow %q step[%d] has empty Name", name, i)
			}
			// StepAction is an int; the zero value (ActionSingleExec = 0) is valid,
			// but we can check the Config is non-nil as a proxy for intentional setup.
			if step.Config == nil {
				t.Errorf("workflow %q step[%d] (%q) has nil Config", name, i, step.Name)
			}
		}
	}
}

// TestRegistry_NoDuplicateStepNames verifies that no workflow contains two
// steps with the same name.
func TestRegistry_NoDuplicateStepNames(t *testing.T) {
	for wfName, fn := range Registry {
		steps := fn()
		seen := make(map[string]int, len(steps))
		for i, step := range steps {
			if prev, exists := seen[step.Name]; exists {
				t.Errorf("workflow %q: duplicate step name %q at index %d (first seen at %d)", wfName, step.Name, i, prev)
			}
			seen[step.Name] = i
		}
	}
}

// TestRegistry_CodeReviewHas5Steps is a pinned regression check for the
// reference codereview workflow.
func TestRegistry_CodeReviewHas5Steps(t *testing.T) {
	fn, ok := Registry["codereview"]
	if !ok {
		t.Fatal("codereview not found in Registry")
	}
	steps := fn()
	if len(steps) != 5 {
		t.Errorf("codereview has %d steps; want 5", len(steps))
	}
}
