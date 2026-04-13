package policies

import (
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/guidance"
)

// WorkflowPolicyInput carries context from the workflow handler to the policy.
type WorkflowPolicyInput struct {
	// Name is the workflow name.
	Name string
	// TotalSteps is the number of steps defined in the workflow.
	TotalSteps int
	// CompletedSteps is the number of steps that finished successfully.
	CompletedSteps int
	// FailedAtStep is the 1-based index of the step that failed (0 = no failure).
	FailedAtStep int
	// Status is the raw status string from the strategy result ("completed", "failed", "partial").
	Status string
}

// WorkflowPolicy computes next-step guidance for the workflow tool.
type WorkflowPolicy struct{}

// NewWorkflowPolicy creates a WorkflowPolicy.
func NewWorkflowPolicy() *WorkflowPolicy {
	return &WorkflowPolicy{}
}

// ToolName implements guidance.ToolPolicy.
func (p *WorkflowPolicy) ToolName() string { return "workflow" }

// BuildPlan implements guidance.ToolPolicy.
// StateSnapshot must be a *WorkflowPolicyInput. Falls back to a safe default on nil or wrong type.
func (p *WorkflowPolicy) BuildPlan(input guidance.PolicyInput) (guidance.NextActionPlan, error) {
	wpi := extractWorkflowInput(input.StateSnapshot)
	return buildWorkflowPlan(wpi), nil
}

func extractWorkflowInput(snapshot any) WorkflowPolicyInput {
	if snapshot == nil {
		return WorkflowPolicyInput{}
	}
	if wpi, ok := snapshot.(*WorkflowPolicyInput); ok && wpi != nil {
		return *wpi
	}
	if wpi, ok := snapshot.(WorkflowPolicyInput); ok {
		return wpi
	}
	return WorkflowPolicyInput{}
}

func buildWorkflowPlan(input WorkflowPolicyInput) guidance.NextActionPlan {
	state := workflowState(input)
	plan := guidance.NextActionPlan{
		State:          state,
		YouAreHere:     workflowYouAreHere(input, state),
		StopConditions: "status=completed and all steps show status=completed in the steps array",
		DoNot: []string{
			"Do not modify step definitions mid-execution — resubmit the full workflow with the corrected definition instead.",
			"Do not treat partial completion as success; check each step's status field individually.",
		},
	}

	if input.FailedAtStep > 0 {
		plan.ChooseYourPath = map[string]guidance.PathBranch{
			"retry": {
				When:     "The failed step is transient and can be retried without changes.",
				NextCall: `workflow(name="<name>", steps=<same_steps_json>)`,
				Example:  fmt.Sprintf(`workflow(name=%q, steps="[...]")`, input.Name),
				Then:     "Check the steps array to confirm the previously failed step now shows status=completed.",
			},
			"skip": {
				When:     "The failed step is non-critical and the pipeline can proceed without it.",
				NextCall: `workflow(name="<name>", steps=<steps_without_failed_step>)`,
				Example:  fmt.Sprintf(`workflow(name=%q, steps="[{...remaining steps...}]")`, input.Name),
				Then:     "Verify downstream steps do not depend on the skipped step's output.",
			},
			"cancel": {
				When:     "The failed step is critical and the workflow cannot produce a valid result.",
				NextCall: "Do not resubmit. Investigate the failure and fix the step definition first.",
				Example:  "Inspect the error field on the failed step in the result to understand the cause.",
				Then:     "Fix the underlying issue, then resubmit the full workflow.",
			},
		}
	}

	return plan
}

func workflowState(input WorkflowPolicyInput) string {
	status := strings.TrimSpace(strings.ToLower(input.Status))
	switch status {
	case "completed":
		return "complete"
	case "failed":
		if input.FailedAtStep > 0 {
			return fmt.Sprintf("failed_at_step_%d", input.FailedAtStep)
		}
		return "failed"
	default:
		if input.TotalSteps > 0 {
			return fmt.Sprintf("step_%d_of_%d", input.CompletedSteps, input.TotalSteps)
		}
		return "step_0_of_0"
	}
}

func workflowYouAreHere(input WorkflowPolicyInput, state string) string {
	nameNote := ""
	if input.Name != "" && input.Name != "workflow" {
		nameNote = fmt.Sprintf("Workflow %q. ", input.Name)
	}

	switch {
	case state == "complete":
		return fmt.Sprintf("%sAll %d step(s) completed successfully.", nameNote, input.TotalSteps)
	case strings.HasPrefix(state, "failed_at_step_"):
		return fmt.Sprintf("%sFailed at step %d of %d. %d step(s) completed before failure.",
			nameNote, input.FailedAtStep, input.TotalSteps, input.CompletedSteps)
	case state == "failed":
		return fmt.Sprintf("%sWorkflow failed. Check the steps array for individual step errors.", nameNote)
	default:
		return fmt.Sprintf("%sExecuting step %d of %d.", nameNote, input.CompletedSteps, input.TotalSteps)
	}
}
