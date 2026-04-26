package workflow

// PrecommitSteps returns the 4-step pre-commit review pipeline workflow definition.
//
// The steps are:
//  1. diff_analysis — analyze the git diff for scope and intent.
//  2. review        — parallel multi-model review of the changes.
//  3. gate          — block if critical issues are found in the diff.
//  4. summary       — generate a commit-ready summary of the changes.
func PrecommitSteps() []WorkflowStep {
	return []WorkflowStep{
		{
			Name:   "diff_analysis",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "analyze",
				"prompt": "Analyze this git diff: describe the scope of changes, intent, and any concerns about correctness or safety:\n\n%s",
			},
		},
		{
			Name:   "review",
			Action: ActionDialogue,
			Config: map[string]any{
				"participants": []string{"codex", "claude"},
				"mode":         "parallel",
				"prompt":       "Review these code changes for correctness, style violations, security issues, and missing test coverage:\n\n%s",
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
			Name:   "summary",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "codereview",
				"prompt": "Generate a concise, commit-ready summary of these changes suitable for a commit message or PR description:\n\n%s",
			},
		},
	}
}
