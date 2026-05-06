package harness

type ThinkingRunTrace struct {
	SessionID         string             `json:"session_id"`
	Phase             Phase              `json:"phase"`
	Frame             TaskFrame          `json:"frame"`
	Ledger            KnowledgeLedger    `json:"ledger"`
	Moves             []MovePlan         `json:"moves,omitempty"`
	Observations      []Observation      `json:"observations,omitempty"`
	EvidenceCount     int                `json:"evidence_count"`
	GateReports       []GateReport       `json:"gate_reports,omitempty"`
	Objections        []Objection        `json:"objections,omitempty"`
	ConfidenceFactors []ConfidenceFactor `json:"confidence_factors,omitempty"`
	StopDecision      *StopDecision      `json:"stop_decision,omitempty"`
}

type TraceSummary struct {
	SessionID        string     `json:"session_id"`
	Phase            Phase      `json:"phase"`
	MoveCount        int        `json:"move_count"`
	ObservationCount int        `json:"observation_count"`
	EvidenceCount    int        `json:"evidence_count"`
	GateStatus       GateStatus `json:"gate_status,omitempty"`
	ConfidenceTier   string     `json:"confidence_tier,omitempty"`
	StopAction       StopAction `json:"stop_action,omitempty"`
	StopReason       string     `json:"stop_reason,omitempty"`
	CanFinalize      bool       `json:"can_finalize"`
	MissingGates     []string   `json:"missing_gates,omitempty"`
}

func NewThinkingRunTrace(session ThinkingSession) ThinkingRunTrace {
	snapshot := session.clone()
	return ThinkingRunTrace{
		SessionID:         snapshot.ID,
		Phase:             snapshot.Phase,
		Frame:             snapshot.Frame,
		Ledger:            snapshot.Ledger,
		Moves:             snapshot.MoveHistory,
		Observations:      snapshot.Observations,
		EvidenceCount:     evidenceCount(snapshot),
		GateReports:       snapshot.GateReports,
		Objections:        snapshot.Objections,
		ConfidenceFactors: snapshot.ConfidenceFactors,
		StopDecision:      snapshot.StopDecision,
	}
}

func NewTraceSummary(session ThinkingSession, confidenceTier string, stopReason string, missingGates []string) TraceSummary {
	snapshot := session.clone()
	summary := TraceSummary{
		SessionID:        snapshot.ID,
		Phase:            snapshot.Phase,
		MoveCount:        len(snapshot.MoveHistory),
		ObservationCount: len(snapshot.Observations),
		EvidenceCount:    evidenceCount(snapshot),
		ConfidenceTier:   confidenceTier,
		StopReason:       stopReason,
		MissingGates:     cloneStrings(missingGates),
	}
	if len(snapshot.GateReports) > 0 {
		summary.GateStatus = snapshot.GateReports[len(snapshot.GateReports)-1].Status
	}
	if snapshot.StopDecision != nil {
		summary.StopAction = snapshot.StopDecision.Action
		summary.CanFinalize = snapshot.StopDecision.CanFinalize
	}
	return summary
}

func evidenceCount(session ThinkingSession) int {
	total := 0
	for _, observation := range session.Observations {
		total += len(observation.Evidence)
	}
	return total
}
