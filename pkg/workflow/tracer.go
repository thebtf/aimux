package workflow

// TracerSteps returns the 4-step call path analysis workflow definition.
//
// The steps are:
//  1. entry_point  — identify entry points for the function or feature.
//  2. trace_paths  — parallel dialogue to trace all execution paths.
//  3. diagram      — generate a call graph diagram via think pattern.
//  4. summary      — summarize discovered paths and dependencies.
func TracerSteps() []WorkflowStep {
	return []WorkflowStep{
		{
			Name:   "entry_point",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "analyze",
				"prompt": "Identify all entry points, callers, and triggering conditions for the following function or feature:\n\n%s",
			},
		},
		{
			Name:   "trace_paths",
			Action: ActionDialogue,
			Config: map[string]any{
				"participants": []string{"codex", "gemini"},
				"mode":         "parallel",
				"prompt":       "Trace all execution paths through this code. Map the full call graph including branches, loops, and error paths:\n\n%s",
			},
		},
		{
			Name:   "diagram",
			Action: ActionThinkPattern,
			Config: map[string]any{
				"pattern":   "visual_reasoning",
				"input_key": "description",
			},
		},
		{
			Name:   "summary",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "analyze",
				"prompt": "Summarize the discovered execution paths and dependencies. Highlight critical paths, bottlenecks, and hidden coupling:\n\n%s",
			},
		},
	}
}
