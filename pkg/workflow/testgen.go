package workflow

// TestGenSteps returns the 4-step test generation pipeline workflow definition.
//
// The steps are:
//  1. analyze  — analyze code to understand what needs testing.
//  2. generate — parallel dialogue to generate tests from multiple perspectives.
//  3. assess   — review test quality via think pattern.
//  4. validate — validate that generated tests compile and run.
func TestGenSteps() []WorkflowStep {
	return []WorkflowStep{
		{
			Name:   "analyze",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "analyze",
				"prompt": "Analyze this code to identify: public API surface, edge cases, error paths, and integration points that require test coverage:\n\n%s",
			},
		},
		{
			Name:   "generate",
			Action: ActionDialogue,
			Config: map[string]any{
				"participants": []string{"codex", "gemini"},
				"mode":         "parallel",
				"prompt":       "Generate comprehensive tests for this code. Cover unit tests, edge cases, and error conditions:\n\n%s",
			},
		},
		{
			Name:   "assess",
			Action: ActionThinkPattern,
			Config: map[string]any{
				"pattern":   "peer_review",
				"input_key": "artifact",
			},
		},
		{
			Name:   "validate",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "testgen",
				"prompt": "Validate that the generated tests are correct, compile cleanly, and provide meaningful coverage. Fix any issues found:\n\n%s",
			},
		},
	}
}
