package workflow

// CodeReviewSteps returns the 5-step code review workflow definition.
//
// The steps are:
//  1. analyze  — single executor performs initial code analysis.
//  2. review   — dialogue between two reviewers identifies specific issues.
//  3. assess   — decision_framework think pattern structures the findings.
//  4. gate     — blocks progression if any CRITICAL issues are present.
//  5. validate — single executor produces the final review assessment.
func CodeReviewSteps() []WorkflowStep {
	return []WorkflowStep{
		{
			Name:   "analyze",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "analyze",
				"prompt": "Analyze the following code for quality, patterns, and potential issues:\n\n%s",
			},
		},
		{
			Name:   "review",
			Action: ActionDialogue,
			Config: map[string]any{
				"participants": []string{"codex", "claude"},
				"mode":         "parallel",
				"max_turns":    4,
				"prompt":       "Review this code analysis and identify specific issues:\n\n%s",
			},
		},
		{
			Name:   "assess",
			Action: ActionThinkPattern,
			Config: map[string]any{
				"pattern":   "decision_framework",
				"input_key": "findings",
			},
		},
		{
			Name:   "gate",
			Action: ActionGate,
			Config: map[string]any{
				"require": "no_critical_issues",
			},
		},
		{
			Name:   "validate",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "codereview",
				"prompt": "Validate these review findings and produce a final assessment:\n\n%s",
			},
		},
	}
}
