package harness

type ThinkingRunTrace struct {
	SessionID         string             `json:"session_id"`
	Phase             Phase              `json:"phase"`
	Frame             TaskFrame          `json:"frame"`
	Ledger            KnowledgeLedger    `json:"ledger"`
	Moves             []MovePlan         `json:"moves,omitempty"`
	Observations      []Observation      `json:"observations,omitempty"`
	GateReports       []GateReport       `json:"gate_reports,omitempty"`
	Objections        []Objection        `json:"objections,omitempty"`
	ConfidenceFactors []ConfidenceFactor `json:"confidence_factors,omitempty"`
	StopDecision      *StopDecision      `json:"stop_decision,omitempty"`
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
		GateReports:       snapshot.GateReports,
		Objections:        snapshot.Objections,
		ConfidenceFactors: snapshot.ConfidenceFactors,
		StopDecision:      snapshot.StopDecision,
	}
}
