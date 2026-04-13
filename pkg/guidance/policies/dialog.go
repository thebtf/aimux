package policies

import (
	"fmt"

	"github.com/thebtf/aimux/pkg/guidance"
)

// DialogPolicyInput carries context from the dialog handler to the policy.
type DialogPolicyInput struct {
	// SessionID is the dialog session identifier (non-empty after first call).
	SessionID string
	// Turns is the number of back-and-forth turns completed.
	Turns int
	// Status is the raw status string from the strategy result ("completed", "running", etc.).
	Status string
	// Participants lists the CLIs that took part in this dialog.
	Participants []string
}

// DialogPolicy computes next-step guidance for the dialog tool.
type DialogPolicy struct{}

// NewDialogPolicy creates a DialogPolicy.
func NewDialogPolicy() *DialogPolicy {
	return &DialogPolicy{}
}

// ToolName implements guidance.ToolPolicy.
func (p *DialogPolicy) ToolName() string { return "dialog" }

// BuildPlan implements guidance.ToolPolicy.
// StateSnapshot must be a *DialogPolicyInput. Falls back to a safe default on nil or wrong type.
func (p *DialogPolicy) BuildPlan(input guidance.PolicyInput) (guidance.NextActionPlan, error) {
	dpi := extractDialogInput(input.StateSnapshot)
	return buildDialogPlan(dpi), nil
}

func extractDialogInput(snapshot any) DialogPolicyInput {
	return extractInput[DialogPolicyInput](snapshot)
}

func buildDialogPlan(input DialogPolicyInput) guidance.NextActionPlan {
	state := dialogState(input)
	plan := guidance.NextActionPlan{
		State:          state,
		YouAreHere:     dialogYouAreHere(input, state),
		StopConditions: "all turns completed and the full transcript is available in the result",
		DoNot: []string{
			"Do not pass session_id from a different tool — it must be the session_id returned by dialog.",
			"Do not start a new dialog session to continue an existing one; reuse session_id instead.",
		},
	}

	if state != "complete" && input.SessionID != "" {
		plan.ChooseYourPath = map[string]guidance.PathBranch{
			guidance.BranchSelf: {
				When:     "Continue the collaborative discussion with more turns.",
				NextCall: fmt.Sprintf(`dialog(prompt="<next_prompt>", session_id=%q, max_turns=<N>)`, input.SessionID),
				Example:  fmt.Sprintf(`dialog(prompt="Refine the previous proposal", session_id=%q, max_turns=4)`, input.SessionID),
				Then:     "Review the updated transcript and decide whether further turns are needed.",
			},
		}
	}

	return plan
}

func dialogState(input DialogPolicyInput) string {
	switch input.Status {
	case "error":
		return "error"
	case "cancelled":
		return "cancelled"
	}
	if input.Status == "completed" && input.Turns > 0 {
		return "complete"
	}
	if input.Turns > 0 {
		return fmt.Sprintf("turn_%d", input.Turns)
	}
	return "turn_0"
}

func dialogYouAreHere(input DialogPolicyInput, state string) string {
	participants := "CLIs"
	if len(input.Participants) > 0 {
		participants = fmt.Sprintf("%v", input.Participants)
	}

	switch state {
	case "complete":
		return fmt.Sprintf("Turn %d of dialog between %s — discussion complete. Full transcript is in the result.", input.Turns, participants)
	case "turn_0":
		return fmt.Sprintf("Dialog initiated between %s. No turns have been recorded yet.", participants)
	default:
		return fmt.Sprintf("Turn %d of dialog between %s. Session %s is active.", input.Turns, participants, input.SessionID)
	}
}
