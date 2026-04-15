package orchestrator

// NewWorkflowStrategyForTest creates a WorkflowStrategy for testing interpolation.
func NewWorkflowStrategyForTest() *WorkflowStrategy {
	return &WorkflowStrategy{}
}

// InterpolateForTest exposes the interpolate method for package-external tests.
func (w *WorkflowStrategy) InterpolateForTest(s, input string, results map[string]*StepResult) string {
	return w.interpolate(s, input, results)
}
