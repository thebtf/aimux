package workflow

// Registry maps workflow names to their step definition functions.
// Callers can look up a workflow by name and invoke the function to obtain
// the step slice for that workflow.
var Registry = map[string]func() []WorkflowStep{
	"codereview": CodeReviewSteps,
	"secaudit":   SecurityAuditSteps,
	"debug":      DebugSteps,
	"analyze":    AnalyzeSteps,
	"refactor":   RefactorSteps,
	"testgen":    TestGenSteps,
	"docgen":     DocGenSteps,
	"precommit":  PrecommitSteps,
	"tracer":     TracerSteps,
}
