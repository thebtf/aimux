package workflow

// DebugSteps returns the 6-step hypothesis-driven debugging workflow definition.
//
// The steps are:
//  1. symptom_capture — capture and structure the error symptom.
//  2. hypothesis_gen  — generate competing hypotheses via think pattern.
//  3. evidence_gather — parallel dialogue to gather evidence for/against each hypothesis.
//  4. root_cause      — determine most likely root cause via think pattern.
//  5. gate            — verify a root cause was identified before proceeding.
//  6. fix_plan        — generate a fix plan based on the confirmed root cause.
func DebugSteps() []WorkflowStep {
	return []WorkflowStep{
		{
			Name:   "symptom_capture",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "debug",
				"prompt": "Capture and structure the following error symptom. Describe what is observed, expected, and the context:\n\n%s",
			},
		},
		{
			Name:   "hypothesis_gen",
			Action: ActionThinkPattern,
			Config: map[string]any{
				"pattern":   "debugging_approach",
				"input_key": "issue",
			},
		},
		{
			Name:   "evidence_gather",
			Action: ActionDialogue,
			Config: map[string]any{
				"participants": []string{"codex", "gemini"},
				"mode":         "parallel",
				"prompt":       "Gather evidence for and against each debugging hypothesis. Examine the code and error context:\n\n%s",
			},
		},
		{
			Name:   "root_cause",
			Action: ActionThinkPattern,
			Config: map[string]any{
				"pattern":   "decision_framework",
				"input_key": "thought",
			},
		},
		{
			Name:   "gate",
			Action: ActionGate,
			Config: map[string]any{
				"require": "root_cause_identified",
			},
		},
		{
			Name:   "fix_plan",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "debug",
				"prompt": "Generate a concrete fix plan based on the identified root cause. Include steps to verify the fix:\n\n%s",
			},
		},
	}
}
