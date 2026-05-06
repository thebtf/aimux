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

	factors := confidenceFactors(req.Evidence, gate, objections, req.CallerConfidence)
	if execErr == nil {
		factors = append(factors, exec.ConfidenceFactors...)
	}
	ceiling := confidenceCeiling(req.CallerConfidence, req.Evidence, gate, objections)
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
		ConfidenceCeiling:    ceiling,
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

func confidenceFactors(evidence []EvidenceRef, gate GateReport, objections []Objection, caller float64) []ConfidenceFactor {
	factors := []ConfidenceFactor{{
		Name:   "caller_confidence",
		Impact: caller,
		Reason: "caller supplied normalized confidence",
	}}
	if len(evidence) == 0 {
		factors = append(factors, ConfidenceFactor{Name: "missing_evidence", Impact: -0.25, Reason: "no evidence references were attached"})
	} else {
		factors = append(factors, ConfidenceFactor{Name: "visible_evidence", Impact: 0.15, Reason: "one or more evidence references were attached"})
	}
	if gate.Status != GatePass {
		factors = append(factors, ConfidenceFactor{Name: "gate_not_passed", Impact: -0.2, Reason: "gate produced warnings or blockers"})
	}
	if len(objections) > 0 {
		factors = append(factors, ConfidenceFactor{Name: "unresolved_objections", Impact: -0.2, Reason: "unresolved objections constrain confidence"})
	}
	return factors
}

func confidenceCeiling(caller float64, evidence []EvidenceRef, gate GateReport, objections []Objection) float64 {
	cap := 0.85
	if len(evidence) == 0 {
		cap = 0.45
	}
	if gate.Status == GateWarn && cap > 0.65 {
		cap = 0.65
	}
	if gate.Status == GateBlocked && cap > 0.35 {
		cap = 0.35
	}
	if len(objections) > 0 && cap > 0.55 {
		cap = 0.55
	}
	if caller <= 0 {
		return cap
	}
	if caller < cap {
		return caller
	}
	return cap
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
