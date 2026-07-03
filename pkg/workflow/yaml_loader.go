package workflow

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// YAMLWorkflow is the on-disk schema for a workflow definition file.
// Files are loaded by LoadYAML and converted to []WorkflowStep.
type YAMLWorkflow struct {
	// Name is the workflow identifier (e.g., "codereview").
	Name string `yaml:"name"`

	// Description is an optional human-readable description.
	Description string `yaml:"description,omitempty"`

	// Steps defines the ordered step list.
	Steps []YAMLStep `yaml:"steps"`
}

// YAMLStep is the YAML-serializable representation of a single workflow step.
type YAMLStep struct {
	Name      string            `yaml:"name"`
	Action    string            `yaml:"action"` // single_exec, dialogue, think_pattern, gate, parallel
	Config    map[string]any    `yaml:"config,omitempty"`
	DependsOn []string          `yaml:"depends_on,omitempty"`
	Timeout   string            `yaml:"timeout,omitempty"` // Go duration string, e.g. "30s"
}

// LoadYAML reads a YAML workflow file and returns the parsed workflow steps.
// Returns an error if the file is unreadable, unparseable, or has invalid steps.
func LoadYAML(path string) (string, []WorkflowStep, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("workflow yaml: read %s: %w", path, err)
	}
	return ParseYAML(data)
}

// ParseYAML parses YAML bytes into a workflow name and step slice.
func ParseYAML(data []byte) (string, []WorkflowStep, error) {
	var yw YAMLWorkflow
	if err := yaml.Unmarshal(data, &yw); err != nil {
		return "", nil, fmt.Errorf("workflow yaml: unmarshal: %w", err)
	}

	if yw.Name == "" {
		return "", nil, fmt.Errorf("workflow yaml: missing 'name' field")
	}
	if len(yw.Steps) == 0 {
		return "", nil, fmt.Errorf("workflow yaml: %q has no steps", yw.Name)
	}

	steps := make([]WorkflowStep, 0, len(yw.Steps))
	for i, ys := range yw.Steps {
		if ys.Name == "" {
			return "", nil, fmt.Errorf("workflow yaml: step[%d] has empty name", i)
		}

		action, err := parseStepAction(ys.Action)
		if err != nil {
			return "", nil, fmt.Errorf("workflow yaml: step %q: %w", ys.Name, err)
		}

		var timeout time.Duration
		if ys.Timeout != "" {
			timeout, err = time.ParseDuration(ys.Timeout)
			if err != nil {
				return "", nil, fmt.Errorf("workflow yaml: step %q: invalid timeout %q: %w", ys.Name, ys.Timeout, err)
			}
		}

		steps = append(steps, WorkflowStep{
			Name:      ys.Name,
			Action:    action,
			Config:    ys.Config,
			DependsOn: ys.DependsOn,
			Timeout:   timeout,
		})
	}

	return yw.Name, steps, nil
}

// parseStepAction converts a YAML action string to a StepAction constant.
func parseStepAction(s string) (StepAction, error) {
	switch s {
	case "single_exec":
		return ActionSingleExec, nil
	case "dialogue":
		return ActionDialogue, nil
	case "think_pattern":
		return ActionThinkPattern, nil
	case "gate":
		return ActionGate, nil
	case "parallel":
		return ActionParallel, nil
	default:
		return 0, fmt.Errorf("unknown action %q (valid: single_exec, dialogue, think_pattern, gate, parallel)", s)
	}
}
