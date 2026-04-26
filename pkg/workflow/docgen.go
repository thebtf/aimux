package workflow

// DocGenSteps returns the 3-step documentation generation workflow definition.
//
// The steps are:
//  1. discover  — discover undocumented code, exported symbols, and APIs.
//  2. generate  — generate documentation for all discovered targets.
//  3. review    — review documentation quality via think pattern.
func DocGenSteps() []WorkflowStep {
	return []WorkflowStep{
		{
			Name:   "discover",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "analyze",
				"prompt": "Discover all exported symbols, public APIs, and undocumented code in the following. List what needs documentation:\n\n%s",
			},
		},
		{
			Name:   "generate",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "docgen",
				"prompt": "Generate clear, accurate documentation for all identified symbols and APIs. Follow idiomatic conventions for the language:\n\n%s",
			},
		},
		{
			Name:   "review",
			Action: ActionThinkPattern,
			Config: map[string]any{
				"pattern":   "peer_review",
				"input_key": "artifact",
			},
		},
	}
}
