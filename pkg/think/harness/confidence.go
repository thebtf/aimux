package harness

import "strings"

type ConfidenceInput struct {
	CallerConfidence float64
	Evidence         []EvidenceRef
	GateReport       GateReport
	Objections       []Objection
	Ledger           KnowledgeLedger
}

type ConfidenceReview struct {
	CallerConfidence float64            `json:"caller_confidence"`
	Ceiling          float64            `json:"ceiling"`
	Tier             string             `json:"tier"`
	Factors          []ConfidenceFactor `json:"factors,omitempty"`
}

func EvaluateConfidence(input ConfidenceInput) ConfidenceReview {
	caller := normalizeConfidence(input.CallerConfidence)
	ceiling := 0.9
	factors := []ConfidenceFactor{{
		Name:   "caller_confidence",
		Impact: caller,
		Reason: "caller supplied normalized confidence",
	}}

	evidenceCap, evidenceFactors := evidenceConfidenceCap(input.Evidence)
	ceiling = minFloat(ceiling, evidenceCap)
	factors = append(factors, evidenceFactors...)

	switch input.GateReport.Status {
	case GateBlocked:
		ceiling = minFloat(ceiling, 0.35)
		factors = append(factors, ConfidenceFactor{Name: "gate_blocked", Impact: -0.5, Reason: "the latest gate report blocks progress"})
	case GateWarn:
		if containsExact(input.GateReport.MissingWork, "evidence") {
			ceiling = minFloat(ceiling, 0.45)
		} else {
			ceiling = minFloat(ceiling, 0.65)
		}
		factors = append(factors, ConfidenceFactor{Name: "gate_warning", Impact: -0.2, Reason: "the latest gate report still has warnings or missing work"})
	}

	if hasLedgerConflicts(input.Ledger) {
		ceiling = minFloat(ceiling, 0.5)
		factors = append(factors, ConfidenceFactor{Name: "source_conflict", Impact: -0.3, Reason: "unresolved ledger conflicts constrain confidence"})
	}

	unresolved := unresolvedObjections(input.Objections)
	if hasCriticalObjection(unresolved) {
		ceiling = minFloat(ceiling, 0.25)
		factors = append(factors, ConfidenceFactor{Name: "critical_objection", Impact: -0.6, Reason: "an unresolved critical objection blocks high confidence"})
	} else if len(unresolved) > 0 {
		ceiling = minFloat(ceiling, 0.65)
		factors = append(factors, ConfidenceFactor{Name: "unresolved_objections", Impact: -0.2, Reason: "unresolved non-critical objections require disclosure or resolution"})
	}

	if caller > 0 {
		ceiling = minFloat(ceiling, caller)
	}
	return ConfidenceReview{
		CallerConfidence: caller,
		Ceiling:          ceiling,
		Tier:             confidenceTier(ceiling),
		Factors:          factors,
	}
}

func evidenceConfidenceCap(evidence []EvidenceRef) (float64, []ConfidenceFactor) {
	if len(evidence) == 0 {
		return 0.45, []ConfidenceFactor{{
			Name:   "missing_evidence",
			Impact: -0.35,
			Reason: "no visible evidence references were attached",
		}}
	}

	cap := 0.85
	verified := 0
	for _, item := range evidence {
		switch strings.ToLower(item.VerificationStatus) {
		case "verified":
			verified++
		case "inferred":
			cap = minFloat(cap, 0.7)
		case "stale", "blocked", "unknown", "":
			cap = minFloat(cap, 0.55)
		default:
			cap = minFloat(cap, 0.6)
		}
	}
	if verified == len(evidence) {
		cap = 0.9
	}
	return cap, []ConfidenceFactor{{
		Name:   "visible_evidence",
		Impact: 0.2,
		Reason: "visible evidence references constrain and support the answer",
	}}
}

func normalizeConfidence(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func confidenceTier(value float64) string {
	switch {
	case value >= 0.75:
		return "high"
	case value >= 0.5:
		return "medium"
	default:
		return "low"
	}
}

func hasLedgerConflicts(ledger KnowledgeLedger) bool {
	for _, conflict := range ledger.Conflicts {
		if conflict.Status != "resolved" {
			return true
		}
	}
	return false
}

func hasCriticalObjection(objections []Objection) bool {
	for _, objection := range objections {
		if objection.Severity == ObjectionCritical && !objection.Resolved {
			return true
		}
	}
	return false
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func containsExact(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
