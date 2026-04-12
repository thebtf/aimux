package guidance

import "reflect"

// Branch names used in choose_your_path guidance.
const (
	BranchSelf     = "self"
	BranchDelegate = "delegate"
	BranchHybrid   = "hybrid"
)

const (
	// StateGuidanceNotImplemented marks a production fallback when a tool policy
	// is not yet implemented.
	StateGuidanceNotImplemented = "guidance_not_implemented"
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

// UnwrapResult returns the nested payload only for known guidance wrapper shapes.
// Unknown payloads are returned unchanged to avoid dropping sibling fields from
// legitimate domain objects that happen to contain a result key.
func UnwrapResult(response any) any {
	if response == nil {
		return nil
	}

	switch payload := response.(type) {
	case ResponseEnvelope:
		return payload.Result
	case *ResponseEnvelope:
		if payload == nil {
			return nil
		}
		return payload.Result
	case HandlerResult:
		return payload.Result
	case *HandlerResult:
		if payload == nil {
			return nil
		}
		return payload.Result
	case map[string]any:
		if isGuidanceEnvelopeMap(payload) || hasOnlyResultKey(payload) {
			return payload["result"]
		}
	}

	if nested, ok := unwrapSingleResultMap(response); ok {
		return nested
	}

	return response
}

func isGuidanceEnvelopeMap(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	if _, ok := payload["result"]; !ok {
		return false
	}

	for _, key := range []string{"state", "you_are_here", "how_this_tool_works", "choose_your_path", "gaps", "stop_conditions", "do_not"} {
		if _, ok := payload[key]; ok {
			return true
		}
	}
	return false
}

func hasOnlyResultKey(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	if len(payload) != 1 {
		return false
	}
	_, ok := payload["result"]
	return ok
}

func unwrapSingleResultMap(response any) (any, bool) {
	value := reflect.ValueOf(response)
	if value.Kind() != reflect.Map || value.Len() != 1 {
		return nil, false
	}

	for _, key := range value.MapKeys() {
		if key.Kind() != reflect.String || key.String() != "result" {
			return nil, false
		}
		nested := value.MapIndex(key)
		if !nested.IsValid() {
			return nil, true
		}
		return nested.Interface(), true
	}

	return nil, false
}

// NewMissingPolicyEnvelope creates the production fallback payload used when a
// tool does not have a registered guidance policy yet.
func NewMissingPolicyEnvelope(rawResponse any) ResponseEnvelope {
	return ResponseEnvelope{
		GuidanceFields: GuidanceFields{State: StateGuidanceNotImplemented},
		Result:         UnwrapResult(rawResponse),
	}
}
