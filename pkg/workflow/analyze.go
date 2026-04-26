package workflow

// AnalyzeSteps returns the 4-step code analysis workflow definition.
//
// The steps are:
//  1. overview    — high-level architecture and code analysis.
//  2. deep_review — sequential multi-turn dialogue for detailed review.
//  3. assessment  — architecture quality assessment via think pattern.
//  4. summary     — synthesize findings into an actionable report.
func AnalyzeSteps() []WorkflowStep {
	return []WorkflowStep{
		{
			Name:   "overview",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "analyze",
				"prompt": "Provide a high-level analysis of the following code: architecture patterns, responsibilities, dependencies, and areas of concern:\n\n%s",
			},
		},
		{
			Name:   "deep_review",
			Action: ActionDialogue,
			Config: map[string]any{
				"participants": []string{"codex", "claude"},
				"mode":         "sequential",
				"max_turns":    4,
				"prompt":       "Conduct a detailed review of this code. Identify specific issues, smells, and improvement opportunities:\n\n%s",
			},
		},
		{
			Name:   "assessment",
			Action: ActionThinkPattern,
			Config: map[string]any{
				"pattern":   "architecture_analysis",
				"input_key": "components",
			},
		},
		{
			Name:   "summary",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "analyze",
				"prompt": "Synthesize all findings into an actionable report with prioritized recommendations:\n\n%s",
			},
		},
	}
}
