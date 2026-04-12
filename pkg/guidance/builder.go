package guidance

// ResponseBuilder builds the guidance envelope from a policy plan and raw handler output.
type ResponseBuilder struct{}

// NewResponseBuilder creates a new response builder.
func NewResponseBuilder() *ResponseBuilder {
	return &ResponseBuilder{}
}

// Build composes the final response envelope. Guidance fields are emitted only when populated,
// while Result is always present and contains untouched handler output.
func (b *ResponseBuilder) Build(plan NextActionPlan, handlerResult HandlerResult) ResponseEnvelope {
	return ResponseEnvelope{
		GuidanceFields: cloneGuidanceFields(plan),
		Result:         handlerResult.Result,
	}
}

// BuildPayload composes the payload returned from stateful handlers before marshalToolResult().
// Guidance fields are added as top-level keys while raw handler payload is preserved under
// `result`.
func (b *ResponseBuilder) BuildPayload(plan NextActionPlan, handlerResult HandlerResult) map[string]any {
	payload := make(map[string]any)
	payload["result"] = handlerResult.Result

	fields := cloneGuidanceFields(plan)
	if fields.State != "" {
		payload["state"] = fields.State
	}
	if fields.YouAreHere != "" {
		payload["you_are_here"] = fields.YouAreHere
	}
	if fields.HowThisToolWorks != "" {
		payload["how_this_tool_works"] = fields.HowThisToolWorks
	}
	if len(fields.ChooseYourPath) > 0 {
		payload["choose_your_path"] = fields.ChooseYourPath
	}
	if len(fields.Gaps) > 0 {
		payload["gaps"] = fields.Gaps
	}
	if fields.StopConditions != "" {
		payload["stop_conditions"] = fields.StopConditions
	}
	if len(fields.DoNot) > 0 {
		payload["do_not"] = fields.DoNot
	}

	return payload
}

func cloneGuidanceFields(in GuidanceFields) GuidanceFields {
	out := in
	if len(in.ChooseYourPath) > 0 {
		out.ChooseYourPath = cloneBranches(in.ChooseYourPath)
	}
	if len(in.Gaps) > 0 {
		out.Gaps = cloneStrings(in.Gaps)
	}
	if len(in.DoNot) > 0 {
		out.DoNot = cloneStrings(in.DoNot)
	}
	return out
}

func cloneBranches(in map[string]PathBranch) map[string]PathBranch {
	out := make(map[string]PathBranch, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}
