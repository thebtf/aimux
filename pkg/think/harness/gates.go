package harness

type FinalizationGateInput struct {
	Session        ThinkingSession
	ProposedAnswer string
	ForceFinalize  bool
	Confidence     ConfidenceReview
	Budget         BudgetReview
}

type FinalizationGateReview struct {
	CanFinalize          bool        `json:"can_finalize"`
	MissingGates         []string    `json:"missing_gates,omitempty"`
	Warnings             []string    `json:"warnings,omitempty"`
	UnresolvedObjections []Objection `json:"unresolved_objections,omitempty"`
}

func EvaluateFinalizationGates(input FinalizationGateInput) FinalizationGateReview {
	session := input.Session.clone()
	var missing []string
	var warnings []string

	if input.ProposedAnswer == "" {
		missing = append(missing, "proposed_answer")
	}
	if !hasFullLoop(session) {
		missing = append(missing, "full_loop")
	}
	if !hasEvidence(session) {
		missing = append(missing, "evidence")
	}
	if latestGateStatus(session) == GateBlocked {
		missing = append(missing, "gate_blocked")
	}
	if input.Confidence.Ceiling < 0.6 {
		missing = append(missing, "confidence_ceiling")
	}
	if input.Budget.Action == StopHalt {
		missing = append(missing, "budget_exhausted")
	}
	if input.Budget.Action == StopRedirect {
		warnings = append(warnings, input.Budget.BudgetState)
	}

	unresolved := unresolvedObjections(session.Objections)
	if hasCriticalObjection(unresolved) {
		missing = append(missing, "critical_objections")
	}
	if hasNonCriticalObjection(unresolved) && !input.ForceFinalize {
		missing = append(missing, "non_critical_objections")
	}
	if hasNonCriticalObjection(unresolved) && input.ForceFinalize {
		warnings = append(warnings, "forced_non_critical_objections_disclosed")
	}

	return FinalizationGateReview{
		CanFinalize:          len(missing) == 0,
		MissingGates:         missing,
		Warnings:             warnings,
		UnresolvedObjections: cloneObjections(unresolved),
	}
}

func hasFullLoop(session ThinkingSession) bool {
	return len(session.MoveHistory) > 0 && len(session.Observations) > 0 && len(session.GateReports) > 0
}

func hasEvidence(session ThinkingSession) bool {
	for _, observation := range session.Observations {
		if len(observation.Evidence) > 0 {
			return true
		}
	}
	return false
}

func latestGateStatus(session ThinkingSession) GateStatus {
	if len(session.GateReports) == 0 {
		return GateWarn
	}
	return session.GateReports[len(session.GateReports)-1].Status
}

func hasNonCriticalObjection(objections []Objection) bool {
	for _, objection := range objections {
		if objection.Severity != ObjectionCritical && !objection.Resolved {
			return true
		}
	}
	return false
}
