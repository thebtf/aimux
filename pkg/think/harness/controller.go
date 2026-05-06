package harness

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

type StartRequest struct {
	Task           string `json:"task"`
	Goal           string `json:"goal,omitempty"`
	ContextSummary string `json:"context_summary"`
	SuccessSignal  string `json:"success_signal,omitempty"`
}

type StartResponse struct {
	SessionID         string               `json:"session_id"`
	Phase             Phase                `json:"phase"`
	AllowedMoveGroups []MoveGroup          `json:"allowed_move_groups"`
	RecommendedMoves  []MoveRecommendation `json:"recommended_moves"`
	MissingInputs     []string             `json:"missing_inputs"`
	NextPrompt        string               `json:"next_prompt"`
	KnowledgeState    KnowledgeLedger      `json:"knowledge_state"`
}

type StepRequest struct {
	SessionID        string        `json:"session_id"`
	ChosenMove       string        `json:"chosen_move"`
	WorkProduct      string        `json:"work_product,omitempty"`
	Evidence         []EvidenceRef `json:"evidence,omitempty"`
	CallerConfidence float64       `json:"confidence,omitempty"`
	Execute          *bool         `json:"execute,omitempty"`
}

type StepResponse struct {
	SessionID             string               `json:"session_id"`
	Phase                 Phase                `json:"phase"`
	Executed              bool                 `json:"executed"`
	ChosenMove            string               `json:"chosen_move"`
	Observation           *Observation         `json:"observation,omitempty"`
	LedgerPatch           KnowledgeLedger      `json:"ledger_patch,omitempty"`
	GateReport            GateReport           `json:"gate_report"`
	ConfidenceCeiling     float64              `json:"confidence_ceiling"`
	ConfidenceFactors     []ConfidenceFactor   `json:"confidence_factors,omitempty"`
	UnresolvedObjections  []Objection          `json:"unresolved_objections"`
	AllowedMoveGroups     []MoveGroup          `json:"allowed_move_groups"`
	RecommendedMoves      []MoveRecommendation `json:"recommended_moves"`
	RequiredReportBack    []string             `json:"required_report_back,omitempty"`
	NextPrompt            string               `json:"next_prompt"`
	PatternExecutionError string               `json:"pattern_execution_error,omitempty"`
}

type FinalizeRequest struct {
	SessionID      string `json:"session_id"`
	ProposedAnswer string `json:"proposed_answer"`
	ForceFinalize  bool   `json:"force_finalize,omitempty"`
}

type FinalizeResponse struct {
	SessionID            string             `json:"session_id"`
	CanFinalize          bool               `json:"can_finalize"`
	Accepted             bool               `json:"accepted"`
	MissingGates         []string           `json:"missing_gates,omitempty"`
	GateReport           GateReport         `json:"gate_report"`
	ConfidenceCeiling    float64            `json:"confidence_ceiling"`
	ConfidenceTier       string             `json:"confidence_tier"`
	ConfidenceFactors    []ConfidenceFactor `json:"confidence_factors,omitempty"`
	UnresolvedObjections []Objection        `json:"unresolved_objections,omitempty"`
	StopDecision         StopDecision       `json:"stop_decision"`
	TraceSummary         TraceSummary       `json:"trace_summary"`
}

type Controller struct {
	store       Store
	catalog     MoveCatalog
	idGenerator func() string
	adapter     PatternAdapter
}

type ControllerOption func(*Controller)

func WithIDGenerator(fn func() string) ControllerOption {
	return func(c *Controller) {
		if fn != nil {
			c.idGenerator = fn
		}
	}
}

func NewController(store Store, opts ...ControllerOption) *Controller {
	if store == nil {
		store = NewInMemoryStore()
	}
	c := &Controller{
		store:       store,
		catalog:     NewDefaultMoveCatalog(),
		idGenerator: defaultSessionID,
		adapter:     PatternAdapter{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Controller) Start(ctx context.Context, req StartRequest) (StartResponse, error) {
	if req.Task == "" {
		return StartResponse{}, invalidInputError("start requires task", "Provide the task the caller wants to reason about.")
	}
	if req.ContextSummary == "" {
		return StartResponse{}, invalidInputError("start requires context_summary", "Summarize the visible context for this thinking run.")
	}

	goal := req.Goal
	if goal == "" {
		goal = "Produce a supported caller-owned answer without premature finalization."
	}
	success := req.SuccessSignal
	if success == "" {
		success = "The caller can finalize with visible evidence, calibrated confidence, and no unresolved critical objections."
	}
	frame, err := NewTaskFrame(TaskFrame{
		Task:           req.Task,
		Goal:           goal,
		ContextSummary: req.ContextSummary,
		SuccessSignal:  success,
	})
	if err != nil {
		return StartResponse{}, err
	}

	session := NewThinkingSession(c.idGenerator(), frame)
	session.Ledger = KnowledgeLedger{
		Known: []LedgerEntry{{
			ID:     "context_summary",
			Text:   req.ContextSummary,
			Source: "caller",
			Status: "visible",
		}},
		Unknown: []LedgerEntry{{
			ID:     "work_product",
			Text:   "No caller work product has been observed yet.",
			Status: "open",
		}},
		Checkable: []LedgerEntry{{
			ID:     "finalization_support",
			Text:   "Finalization must be checked against evidence, gates, objections, and budget.",
			Status: "pending",
		}},
	}

	created, err := c.store.Create(ctx, session)
	if err != nil {
		return StartResponse{}, err
	}

	return StartResponse{
		SessionID:         created.ID,
		Phase:             created.Phase,
		AllowedMoveGroups: c.catalog.AllowedGroups(created.Phase),
		RecommendedMoves:  c.catalog.Recommend(created),
		MissingInputs:     []string{"chosen_move", "work_product", "evidence", "confidence"},
		NextPrompt:        "Inventory what is known, unknown, assumed, conflicting, checkable, or blocked, then choose the next cognitive move and report its visible work product.",
		KnowledgeState:    created.Ledger.clone(),
	}, nil
}

func (c *Controller) Session(ctx context.Context, sessionID string) (ThinkingSession, error) {
	return c.store.Get(ctx, sessionID)
}

func (c *Controller) Step(ctx context.Context, req StepRequest) (StepResponse, error) {
	if req.SessionID == "" {
		return StepResponse{}, invalidInputError("step requires session_id", "Pass the session_id returned by think(action=start).")
	}
	if req.ChosenMove == "" {
		return StepResponse{}, invalidInputError("step requires chosen_move", "Choose one cognitive move from the allowed moves.")
	}

	session, err := c.store.Get(ctx, req.SessionID)
	if err != nil {
		return StepResponse{}, err
	}
	move, ok := c.catalog.Find(req.ChosenMove)
	if !ok {
		return StepResponse{}, moveMismatchError(req.ChosenMove)
	}

	execute := true
	if req.Execute != nil {
		execute = *req.Execute
	}
	if !execute {
		return StepResponse{
			SessionID:          session.ID,
			Phase:              session.Phase,
			Executed:           false,
			ChosenMove:         move.Name,
			GateReport:         GateReport{Status: GateWarn, MissingWork: []string{"work_product", "evidence", "confidence"}},
			AllowedMoveGroups:  c.catalog.AllowedGroups(session.Phase),
			RecommendedMoves:   c.catalog.Recommend(session),
			RequiredReportBack: []string{"work_product", "evidence", "confidence"},
			NextPrompt:         fmt.Sprintf("Use %s as guidance, then report the visible work_product, evidence, and confidence in a later think(action=step) call.", move.Name),
		}, nil
	}

	if req.WorkProduct == "" {
		return StepResponse{}, invalidInputError("step requires work_product when execute is true", "Provide visible work_product before marking the move executed.")
	}
	for _, evidence := range req.Evidence {
		if err := evidence.validate(); err != nil {
			return StepResponse{}, err
		}
	}

	observation := Observation{
		MoveName:         move.Name,
		WorkProduct:      req.WorkProduct,
		Evidence:         req.Evidence,
		CallerConfidence: req.CallerConfidence,
	}
	if err := observation.validate(); err != nil {
		return StepResponse{}, err
	}

	exec, execErr := c.adapter.Execute(ctx, move, req.WorkProduct, req.SessionID)
	ledgerAdds := KnowledgeLedger{
		Known: []LedgerEntry{{
			ID:     fmt.Sprintf("work_product_%d", len(session.Observations)+1),
			Text:   req.WorkProduct,
			Source: move.Name,
			Status: "observed",
		}},
	}
	for i, evidence := range req.Evidence {
		ledgerAdds.Checkable = append(ledgerAdds.Checkable, LedgerEntry{
			ID:     fmt.Sprintf("evidence_%d", i+1),
			Text:   evidence.Summary,
			Source: evidence.Ref,
			Status: evidence.VerificationStatus,
		})
	}
	if execErr == nil {
		var mergeErr error
		ledgerAdds, mergeErr = ledgerAdds.merge(exec.LedgerAdds)
		if mergeErr != nil {
			return StepResponse{}, mergeErr
		}
	}

	gate := GateReport{Status: GatePass}
	var objections []Objection
	if len(req.Evidence) == 0 {
		gate = GateReport{Status: GateWarn, MissingWork: []string{"evidence"}}
		if req.CallerConfidence > 0.6 {
			objections = append(objections, Objection{
				ID:       "evidence_gap",
				Severity: ObjectionMajor,
				Text:     "caller confidence is high relative to missing evidence",
			})
		}
	}
	if execErr != nil {
		gate = GateReport{Status: GateBlocked, Blockers: []string{"selected cognitive move could not execute"}}
		objections = append(objections, Objection{
			ID:       "move_execution_failed",
			Severity: ObjectionMajor,
			Text:     execErr.Error(),
		})
	}

	activeObjections := append(unresolvedObjections(session.Objections), objections...)
	confidence := EvaluateConfidence(ConfidenceInput{
		CallerConfidence: req.CallerConfidence,
		Evidence:         req.Evidence,
		GateReport:       gate,
		Objections:       activeObjections,
		Ledger:           ledgerAdds,
	})
	factors := confidence.Factors
	if execErr == nil {
		factors = append(factors, exec.ConfidenceFactors...)
	}
	patch := KnowledgePatch{
		Phase:             PhaseIntegrate,
		LedgerAdds:        ledgerAdds,
		Move:              &MovePlan{Name: move.Name, Group: move.Group, Reason: move.Purpose, ExpectedArtifactDelta: "session ledger, observation, gate report, and confidence factors update", Execute: true},
		Observation:       &observation,
		GateReport:        &gate,
		Objections:        objections,
		ConfidenceFactors: factors,
		StopDecision: &StopDecision{
			Action:      StopContinue,
			Reason:      "move completed; integrate the new artifacts before finalization",
			CanFinalize: false,
		},
	}

	updated, err := c.store.Update(ctx, req.SessionID, func(current ThinkingSession) (ThinkingSession, error) {
		return current.ApplyPatch(patch)
	})
	if err != nil {
		return StepResponse{}, err
	}

	resp := StepResponse{
		SessionID:            updated.ID,
		Phase:                updated.Phase,
		Executed:             true,
		ChosenMove:           move.Name,
		Observation:          &observation,
		LedgerPatch:          ledgerAdds,
		GateReport:           gate,
		ConfidenceCeiling:    confidence.Ceiling,
		ConfidenceFactors:    factors,
		UnresolvedObjections: unresolvedObjections(updated.Objections),
		AllowedMoveGroups:    c.catalog.AllowedGroups(updated.Phase),
		RecommendedMoves:     c.catalog.Recommend(updated),
		NextPrompt:           "Integrate the updated knowledge state, resolve blockers or objections, then choose the next cognitive move or request finalization when support is sufficient.",
	}
	if execErr != nil {
		resp.PatternExecutionError = execErr.Error()
	}
	return resp, nil
}

func (c *Controller) Finalize(ctx context.Context, req FinalizeRequest) (FinalizeResponse, error) {
	if req.SessionID == "" {
		return FinalizeResponse{}, invalidInputError("finalize requires session_id", "Pass the session_id returned by think(action=start).")
	}
	if req.ProposedAnswer == "" {
		return FinalizeResponse{}, invalidInputError("finalize requires proposed_answer", "Provide the caller-owned proposed final answer for gate evaluation.")
	}

	session, err := c.store.Get(ctx, req.SessionID)
	if err != nil {
		return FinalizeResponse{}, err
	}

	evidence := collectEvidence(session)
	latestGate := latestGateReport(session)
	confidence := EvaluateConfidence(ConfidenceInput{
		CallerConfidence: latestCallerConfidence(session),
		Evidence:         evidence,
		GateReport:       latestGate,
		Objections:       unresolvedObjections(session.Objections),
		Ledger:           session.Ledger,
	})
	budget := ReviewBudget(BalancedBudgetProfile(), BudgetState{
		StartedAt:          session.StartedAt,
		Now:                time.Now().UTC(),
		StepCount:          len(session.MoveHistory),
		ConsecutiveNoDelta: consecutiveNoDelta(session),
		MeaningfulDelta:    hasMeaningfulDelta(session),
		LastDelta:          lastDelta(session),
		ExpectedNextDelta:  "resolved gate, new evidence, objection update, or final integration",
	})
	gates := EvaluateFinalizationGates(FinalizationGateInput{
		Session:        session,
		ProposedAnswer: req.ProposedAnswer,
		ForceFinalize:  req.ForceFinalize,
		Confidence:     confidence,
		Budget:         budget,
	})

	gateReport := GateReport{Status: GatePass}
	stopAction := StopFinalize
	stopReason := "finalized"
	if !gates.CanFinalize {
		gateReport = GateReport{
			Status:      GateBlocked,
			Blockers:    cloneStrings(gates.MissingGates),
			MissingWork: cloneStrings(gates.MissingGates),
		}
		stopAction = StopContinue
		stopReason = "missing_gates"
		if containsExact(gates.MissingGates, "budget_exhausted") {
			stopAction = StopHalt
			stopReason = "budget_exhausted"
		}
	} else if len(gates.Warnings) > 0 {
		gateReport = GateReport{Status: GateWarn, Warnings: cloneStrings(gates.Warnings)}
	}

	stop := StopDecision{
		Action:               stopAction,
		Reason:               finalizationReason(gates, confidence, budget, stopReason),
		CanFinalize:          gates.CanFinalize,
		UnresolvedObjections: gates.UnresolvedObjections,
		BudgetState:          budget.BudgetState,
	}

	updated, err := c.store.Update(ctx, req.SessionID, func(current ThinkingSession) (ThinkingSession, error) {
		return current.ApplyPatch(KnowledgePatch{
			Phase:             PhaseFinalize,
			GateReport:        &gateReport,
			ConfidenceFactors: confidence.Factors,
			StopDecision:      &stop,
		})
	})
	if err != nil {
		return FinalizeResponse{}, err
	}

	return FinalizeResponse{
		SessionID:            updated.ID,
		CanFinalize:          gates.CanFinalize,
		Accepted:             gates.CanFinalize,
		MissingGates:         cloneStrings(gates.MissingGates),
		GateReport:           gateReport,
		ConfidenceCeiling:    confidence.Ceiling,
		ConfidenceTier:       confidence.Tier,
		ConfidenceFactors:    confidence.Factors,
		UnresolvedObjections: gates.UnresolvedObjections,
		StopDecision:         stop,
		TraceSummary:         NewTraceSummary(updated, confidence.Tier, stopReason, gates.MissingGates),
	}, nil
}

func collectEvidence(session ThinkingSession) []EvidenceRef {
	var evidence []EvidenceRef
	for _, observation := range session.Observations {
		evidence = append(evidence, cloneEvidence(observation.Evidence)...)
	}
	return evidence
}

func latestGateReport(session ThinkingSession) GateReport {
	if len(session.GateReports) == 0 {
		return GateReport{Status: GateWarn, MissingWork: []string{"full_loop", "evidence"}}
	}
	return session.GateReports[len(session.GateReports)-1].clone()
}

func latestCallerConfidence(session ThinkingSession) float64 {
	if len(session.Observations) == 0 {
		return 0
	}
	return session.Observations[len(session.Observations)-1].CallerConfidence
}

func consecutiveNoDelta(session ThinkingSession) int {
	if len(session.Observations) == 0 {
		return 0
	}
	last := session.Observations[len(session.Observations)-1]
	if len(last.Evidence) > 0 {
		return 0
	}
	count := 1
	for i := len(session.Observations) - 1; i > 0; i-- {
		current := session.Observations[i]
		previous := session.Observations[i-1]
		if current.WorkProduct == previous.WorkProduct && len(previous.Evidence) == 0 {
			count++
			continue
		}
		break
	}
	return count
}

func hasMeaningfulDelta(session ThinkingSession) bool {
	if len(session.Observations) == 0 {
		return false
	}
	last := session.Observations[len(session.Observations)-1]
	if len(last.Evidence) > 0 {
		return true
	}
	if len(session.Observations) == 1 {
		return last.WorkProduct != ""
	}
	previous := session.Observations[len(session.Observations)-2]
	return last.WorkProduct != "" && last.WorkProduct != previous.WorkProduct
}

func lastDelta(session ThinkingSession) string {
	if len(session.Observations) == 0 {
		return ""
	}
	last := session.Observations[len(session.Observations)-1]
	if last.WorkProduct != "" {
		return last.WorkProduct
	}
	if len(last.Evidence) > 0 {
		return last.Evidence[len(last.Evidence)-1].Summary
	}
	return ""
}

func finalizationReason(gates FinalizationGateReview, confidence ConfidenceReview, budget BudgetReview, stopReason string) string {
	if gates.CanFinalize {
		if len(gates.UnresolvedObjections) > 0 {
			return "finalization accepted with disclosed non-critical objections"
		}
		return "finalization accepted with visible evidence and calibrated confidence"
	}
	if stopReason == "budget_exhausted" {
		return budget.ResumableSummary
	}
	return fmt.Sprintf("finalization blocked by missing gates %v with confidence tier %s", gates.MissingGates, confidence.Tier)
}

func unresolvedObjections(objections []Objection) []Objection {
	var unresolved []Objection
	for _, objection := range objections {
		if !objection.Resolved {
			unresolved = append(unresolved, objection)
		}
	}
	return cloneObjections(unresolved)
}

var defaultSessionCounter atomic.Uint64

func defaultSessionID() string {
	n := defaultSessionCounter.Add(1)
	return fmt.Sprintf("think-%d-%d", time.Now().UTC().UnixNano(), n)
}
