package patterns

import "strconv"

// ReflectionDirective is an advisory hint returned to the agent when
// a pattern detects the agent may be rushing or missing evidence.
type ReflectionDirective struct {
	Directive string   `json:"directive"` // "STOP", "VERIFY", "CONTINUE"
	Checklist []string `json:"checklist"` // items to check before proceeding
	Reason    string   `json:"reason"`    // why the pause is needed
}

// NewStopDirective creates a STOP directive with the given reason and checklist.
func NewStopDirective(reason string, checklist []string) *ReflectionDirective {
	return &ReflectionDirective{
		Directive: "STOP",
		Checklist: checklist,
		Reason:    reason,
	}
}

// NewVerifyDirective creates a VERIFY directive with the given reason and checklist.
func NewVerifyDirective(reason string, checklist []string) *ReflectionDirective {
	return &ReflectionDirective{
		Directive: "VERIFY",
		Checklist: checklist,
		Reason:    reason,
	}
}

// ValidateEvidenceGate returns a STOP directive if findingsCount < requiredCount,
// indicating the agent should gather more evidence before proceeding.
// Returns nil when sufficient findings exist.
func ValidateEvidenceGate(findingsCount, requiredCount int) *ReflectionDirective {
	if findingsCount < requiredCount {
		return NewStopDirective(
			"Insufficient evidence to proceed — gather more findings first",
			[]string{
				"Document all observed symptoms",
				"Reproduce the issue in a controlled environment",
				"Collect at least " + strconv.Itoa(requiredCount) + " distinct findings before forming a hypothesis",
			},
		)
	}
	return nil
}

// ValidateConfidence returns a VERIFY directive if confidence > 0.8 with < 5 evidence items,
// which indicates overconfidence relative to the available evidence base.
// Returns nil when confidence is justified by the evidence.
func ValidateConfidence(confidence float64, evidenceCount int) *ReflectionDirective {
	if confidence > 0.8 && evidenceCount < 5 {
		return NewVerifyDirective(
			"High confidence with limited evidence — verify before committing",
			[]string{
				"List every piece of evidence supporting this hypothesis",
				"Identify at least one alternative explanation",
				"Consider what evidence would disprove this hypothesis",
				"Gather more data points before concluding",
			},
		)
	}
	return nil
}
