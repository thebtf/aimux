package classifier

const (
	TaskClassCode     = "code"
	TaskClassReview   = "review"
	TaskClassResearch = "research"
	TaskClassSpec     = "spec"
	TaskClassPrompt   = "prompt"

	DefaultThreshold = 0.7
)

var taskClasses = []string{
	TaskClassCode,
	TaskClassReview,
	TaskClassResearch,
	TaskClassSpec,
	TaskClassPrompt,
}

type keyword struct {
	Term   string
	Weight float64
}

var keywordCorpus = map[string][]keyword{
	TaskClassCode: {
		{Term: "implement", Weight: 0.95},
		{Term: "fix", Weight: 0.85},
		{Term: "bug", Weight: 0.45},
		{Term: "code", Weight: 0.65},
		{Term: "refactor", Weight: 0.85},
		{Term: "test", Weight: 0.35},
		{Term: "failing test", Weight: 0.75},
		{Term: "build", Weight: 0.35},
		{Term: "compile", Weight: 0.55},
		{Term: "handler", Weight: 0.45},
		{Term: "function", Weight: 0.45},
		{Term: "package", Weight: 0.25},
		{Term: "endpoint", Weight: 0.35},
		{Term: "panic", Weight: 0.45},
	},
	TaskClassReview: {
		{Term: "review", Weight: 0.95},
		{Term: "code review", Weight: 1.00},
		{Term: "pr", Weight: 0.65},
		{Term: "pull request", Weight: 0.85},
		{Term: "diff", Weight: 0.65},
		{Term: "gate", Weight: 0.45},
		{Term: "find issues", Weight: 0.85},
		{Term: "issues", Weight: 0.35},
		{Term: "blocker", Weight: 0.45},
		{Term: "regression", Weight: 0.35},
		{Term: "approve", Weight: 0.30},
		{Term: "head", Weight: 0.25},
	},
	TaskClassResearch: {
		{Term: "research", Weight: 0.95},
		{Term: "compare", Weight: 0.60},
		{Term: "investigate", Weight: 0.55},
		{Term: "sources", Weight: 0.55},
		{Term: "evidence", Weight: 0.35},
		{Term: "official documentation", Weight: 0.65},
		{Term: "documentation", Weight: 0.35},
		{Term: "latest", Weight: 0.35},
		{Term: "papers", Weight: 0.50},
		{Term: "best practices", Weight: 0.55},
		{Term: "what is", Weight: 0.35},
		{Term: "why", Weight: 0.25},
	},
	TaskClassSpec: {
		{Term: "spec", Weight: 0.95},
		{Term: "specify", Weight: 0.85},
		{Term: "requirements", Weight: 0.75},
		{Term: "user story", Weight: 0.80},
		{Term: "acceptance criteria", Weight: 0.90},
		{Term: "tasks.md", Weight: 0.65},
		{Term: "plan.md", Weight: 0.55},
		{Term: "architecture", Weight: 0.40},
		{Term: "adr", Weight: 0.35},
		{Term: "feature spec", Weight: 1.00},
		{Term: "change request", Weight: 0.60},
	},
	TaskClassPrompt: {
		{Term: "prompt", Weight: 0.95},
		{Term: "improve prompt", Weight: 1.00},
		{Term: "brief", Weight: 0.70},
		{Term: "instructions", Weight: 0.55},
		{Term: "rewrite", Weight: 0.45},
		{Term: "template", Weight: 0.40},
		{Term: "delegate", Weight: 0.45},
		{Term: "delegation brief", Weight: 0.90},
		{Term: "system message", Weight: 0.75},
		{Term: "agent prompt", Weight: 0.90},
	},
}
