package guidance

// Branch names used in choose_your_path guidance.
const (
	BranchSelf     = "self"
	BranchDelegate = "delegate"
	BranchHybrid   = "hybrid"
)

// PathBranch describes a concrete next-step branch for the caller.
type PathBranch struct {
	When     string `json:"when,omitempty"`
	NextCall string `json:"next_call,omitempty"`
	Example  string `json:"example,omitempty"`
	Then     string `json:"then,omitempty"`
}

// GuidanceFields is the single source of truth for shared guidance output fields.
type GuidanceFields struct {
	State            string                `json:"state,omitempty"`
	YouAreHere       string                `json:"you_are_here,omitempty"`
	HowThisToolWorks string                `json:"how_this_tool_works,omitempty"`
	ChooseYourPath   map[string]PathBranch `json:"choose_your_path,omitempty"`
	Gaps             []string              `json:"gaps,omitempty"`
	StopConditions   string                `json:"stop_conditions,omitempty"`
	DoNot            []string              `json:"do_not,omitempty"`
}

// NextActionPlan contains policy-produced guidance fields.
// It aliases GuidanceFields so policy output and envelope shape cannot drift.
type NextActionPlan = GuidanceFields

// HandlerResult is the internal contract between tool handlers and guidance.
// Result must contain the raw handler payload that was previously returned flat.
type HandlerResult struct {
	Tool     string `json:"tool,omitempty"`
	Action   string `json:"action,omitempty"`
	State    any    `json:"state,omitempty"`
	Result   any    `json:"result"`
	Metadata any    `json:"metadata,omitempty"`
}

// PolicyInput is the shared policy input contract used by tool policies.
type PolicyInput struct {
	Action        string
	StateSnapshot any
	RawResult     any
}

// ToolPolicy computes next-step guidance for a stateful tool.
// Implementations are pure functions over the provided snapshot.
type ToolPolicy interface {
	ToolName() string
	BuildPlan(input PolicyInput) (NextActionPlan, error)
}

// ResponseEnvelope is the outer guidance payload returned to MCP callers.
type ResponseEnvelope struct {
	GuidanceFields
	Result any `json:"result"`
}
