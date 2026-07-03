package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseYAML_ValidWorkflow(t *testing.T) {
	yaml := `
name: custom-review
description: A custom code review workflow
steps:
  - name: analyze
    action: single_exec
    config:
      role: analyzer
      prompt: "Analyze: %s"
  - name: quality-check
    action: gate
    config:
      require: no_critical_issues
      mode: blocking
  - name: synthesize
    action: single_exec
    config:
      role: synthesizer
      prompt: "Synthesize: %s"
    timeout: "30s"
`
	name, steps, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if name != "custom-review" {
		t.Errorf("name = %q, want custom-review", name)
	}
	if len(steps) != 3 {
		t.Fatalf("got %d steps, want 3", len(steps))
	}

	// Step 0: single_exec
	if steps[0].Name != "analyze" {
		t.Errorf("step[0].Name = %q", steps[0].Name)
	}
	if steps[0].Action != ActionSingleExec {
		t.Errorf("step[0].Action = %v, want ActionSingleExec", steps[0].Action)
	}

	// Step 1: gate
	if steps[1].Action != ActionGate {
		t.Errorf("step[1].Action = %v, want ActionGate", steps[1].Action)
	}

	// Step 2: timeout parsed
	if steps[2].Timeout.Seconds() != 30 {
		t.Errorf("step[2].Timeout = %v, want 30s", steps[2].Timeout)
	}
}

func TestParseYAML_AllActions(t *testing.T) {
	yaml := `
name: all-actions
steps:
  - name: s1
    action: single_exec
    config: {role: a}
  - name: s2
    action: dialogue
    config: {participants: [a, b], mode: sequential}
  - name: s3
    action: think_pattern
    config: {pattern: debugging_approach}
  - name: s4
    action: gate
    config: {require: no_critical_issues}
  - name: s5
    action: parallel
    config: {clis: [a, b]}
`
	_, steps, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if len(steps) != 5 {
		t.Fatalf("got %d steps, want 5", len(steps))
	}

	expected := []StepAction{
		ActionSingleExec,
		ActionDialogue,
		ActionThinkPattern,
		ActionGate,
		ActionParallel,
	}
	for i, want := range expected {
		if steps[i].Action != want {
			t.Errorf("step[%d] action = %v, want %v", i, steps[i].Action, want)
		}
	}
}

func TestParseYAML_Errors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"missing_name", "steps:\n  - name: s1\n    action: single_exec"},
		{"no_steps", "name: empty\nsteps: []"},
		{"empty_step_name", "name: w\nsteps:\n  - name: ''\n    action: gate"},
		{"unknown_action", "name: w\nsteps:\n  - name: s1\n    action: teleport"},
		{"invalid_timeout", "name: w\nsteps:\n  - name: s1\n    action: gate\n    timeout: 'not-a-duration'"},
		{"invalid_yaml", "name: w\nsteps:\n  broken:\n    - not valid workflow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ParseYAML([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestLoadYAML_FromFile(t *testing.T) {
	content := `
name: file-loaded
steps:
  - name: step1
    action: single_exec
    config:
      role: test
`
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	name, steps, err := LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if name != "file-loaded" {
		t.Errorf("name = %q, want file-loaded", name)
	}
	if len(steps) != 1 {
		t.Errorf("got %d steps, want 1", len(steps))
	}
}

func TestLoadYAML_FileNotFound(t *testing.T) {
	_, _, err := LoadYAML("/nonexistent/path/workflow.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestParseYAML_DependsOn(t *testing.T) {
	yaml := `
name: deps-test
steps:
  - name: s1
    action: single_exec
    config: {role: a}
  - name: s2
    action: single_exec
    config: {role: b}
    depends_on: [s1]
`
	_, steps, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if len(steps[1].DependsOn) != 1 || steps[1].DependsOn[0] != "s1" {
		t.Errorf("step[1].DependsOn = %v, want [s1]", steps[1].DependsOn)
	}
}
