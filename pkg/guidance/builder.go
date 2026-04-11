package guidance

// ResponseBuilder builds the guidance envelope from a policy plan and raw handler output.
type ResponseBuilder struct{}

// NewResponseBuilder creates a response builder.
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
