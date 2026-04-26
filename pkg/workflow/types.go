// Package workflow implements a generic multi-step workflow engine for aimux.
// Each workflow is a sequential list of WorkflowStep values. Steps are dispatched
// to the appropriate subsystem (executor, dialogue, think pattern) based on their
// Action type. The engine is fully interface-driven so it can be tested without
// real CLIs.
package workflow

import "time"

// StepAction is the type of work a step performs.
type StepAction int

const (
	// ActionSingleExec executes a single prompt against one executor.
	ActionSingleExec StepAction = iota

	// ActionDialogue runs a moderated multi-participant conversation.
	ActionDialogue

	// ActionThinkPattern invokes a local think pattern (no CLI required).
	ActionThinkPattern

	// ActionGate evaluates a quality check against prior step results.
	// If the check fails the engine returns a WorkflowResult with Status="gated".
	ActionGate

	// ActionParallel executes a prompt against multiple executors concurrently
	// and collects their responses.
	ActionParallel
)

// String returns a human-readable label for the StepAction.
func (a StepAction) String() string {
	switch a {
	case ActionSingleExec:
		return "single_exec"
	case ActionDialogue:
		return "dialogue"
	case ActionThinkPattern:
		return "think_pattern"
	case ActionGate:
		return "gate"
	case ActionParallel:
		return "parallel"
	default:
		return "unknown"
	}
}

// WorkflowStep defines one step in a workflow.
type WorkflowStep struct {
	// Name is the unique step identifier within the workflow.
	Name string

	// Action determines what kind of work this step performs.
	Action StepAction

	// Config holds action-specific configuration. Keys depend on Action:
	//
	//   ActionSingleExec:
	//     "cli"    string  — executor name (optional; "role" takes precedence)
	//     "role"   string  — role name resolved to a CLI by the engine
	//     "prompt" string  — Go format string (%s receives prior step summary)
	//
	//   ActionDialogue:
	//     "participants" []string — executor names
	//     "mode"         string   — "sequential", "parallel", "round_robin", "stance"
	//     "max_turns"    int      — turn limit (0 = unlimited)
	//     "prompt"       string   — Go format string (%s receives prior step summary)
	//
	//   ActionThinkPattern:
	//     "pattern"   string — registered think pattern name
	//     "input_key" string — key under which prior step summary is injected
	//
	//   ActionGate:
	//     "require" string — gate condition identifier; "no_critical_issues" is built-in
	//
	//   ActionParallel:
	//     "clis"   []string — executor names to run concurrently
	//     "role"   string   — role name (alternative to "clis"); resolves to first match
	//     "prompt" string   — Go format string (%s receives prior step summary)
	Config map[string]any

	// DependsOn lists step names that must complete before this step.
	// Reserved for future DAG execution; ignored by the current sequential engine.
	DependsOn []string

	// Timeout is the per-step execution timeout. Zero means use the engine default.
	Timeout time.Duration
}

// WorkflowInput is the input provided to a workflow execution.
type WorkflowInput struct {
	// Topic describes what the workflow is processing.
	Topic string

	// Files is the list of file paths relevant to the workflow (code workflows).
	Files []string

	// Focus narrows the workflow scope (e.g., "security", "performance").
	Focus string

	// Extra holds workflow-specific parameters not covered by the above fields.
	Extra map[string]any
}

// WorkflowResult is the output produced by a completed workflow execution.
type WorkflowResult struct {
	// Status is one of "completed", "failed", or "gated".
	Status string

	// Steps records the per-step outcomes in execution order.
	Steps []StepResult

	// Summary is the final synthesised output of the workflow.
	Summary string
}

// StepResult records the outcome of one workflow step.
type StepResult struct {
	// Name matches WorkflowStep.Name.
	Name string

	// Action matches WorkflowStep.Action.
	Action StepAction

	// Status is one of "completed", "failed", "skipped", or "gated".
	Status string

	// Content holds the textual output produced by the step.
	Content string

	// Duration is the wall-clock time spent on this step.
	Duration time.Duration
}
