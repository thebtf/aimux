package workflow

// RefactorSteps returns the 6-step safe refactoring pipeline workflow definition.
//
// The steps are:
//  1. impact_analysis — map what will be affected by the refactoring.
//  2. plan            — decompose refactoring into safe steps via think pattern.
//  3. review_plan     — adversarial stance dialogue to validate the plan.
//  4. gate            — block if critical issues are found in the plan.
//  5. execute         — execute the refactoring.
//  6. verify          — verify the refactoring preserves existing behavior.
func RefactorSteps() []WorkflowStep {
	return []WorkflowStep{
		{
			Name:   "impact_analysis",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "analyze",
				"prompt": "Map everything that will be affected by this refactoring: callers, dependents, interfaces, tests, and data contracts:\n\n%s",
			},
		},
		{
			Name:   "plan",
			Action: ActionThinkPattern,
			Config: map[string]any{
				"pattern":   "problem_decomposition",
				"input_key": "problem",
			},
		},
		{
			Name:   "review_plan",
			Action: ActionDialogue,
			Config: map[string]any{
				"participants": []string{"codex", "claude"},
				"mode":         "stance",
				"stances": map[string]string{
					"codex":  "advocate",
					"claude": "critic",
				},
				"prompt": "Review the following refactoring plan. Codex advocates for the plan; Claude stress-tests it for risks:\n\n%s",
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
			Name:   "execute",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "refactor",
				"prompt": "Execute the approved refactoring plan. Apply all changes described:\n\n%s",
			},
		},
		{
			Name:   "verify",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "codereview",
				"prompt": "Verify that the refactoring preserves all observable behavior. Check for regressions, missing edge cases, and interface compatibility:\n\n%s",
			},
		},
	}
}
