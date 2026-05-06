package harness

import (
	"strings"
	"time"
)

type Phase string

const (
	PhaseFrame     Phase = "frame"
	PhaseInventory Phase = "inventory"
	PhaseMove      Phase = "move"
	PhaseObserve   Phase = "observe"
	PhaseTest      Phase = "test"
	PhaseIntegrate Phase = "integrate"
	PhaseFinalize  Phase = "finalize"
)

type MoveGroup string

const (
	MoveGroupFrame     MoveGroup = "frame"
	MoveGroupExplore   MoveGroup = "explore"
	MoveGroupTest      MoveGroup = "test"
	MoveGroupEvaluate  MoveGroup = "evaluate"
	MoveGroupCalibrate MoveGroup = "calibrate"
	MoveGroupFinalize  MoveGroup = "finalize"
)

type GateStatus string

const (
	GatePass    GateStatus = "pass"
	GateWarn    GateStatus = "warn"
	GateBlocked GateStatus = "blocked"
)

type ObjectionSeverity string

const (
	ObjectionCritical ObjectionSeverity = "critical"
	ObjectionMajor    ObjectionSeverity = "major"
	ObjectionMinor    ObjectionSeverity = "minor"
)

type StopAction string

const (
	StopContinue StopAction = "continue"
	StopRedirect StopAction = "redirect"
	StopCompress StopAction = "compress"
	StopFinalize StopAction = "finalize"
	// StopHalt serializes as "stop" because it is a caller-facing terminal action.
	StopHalt StopAction = "stop"
)

type TaskFrame struct {
	Task           string   `json:"task"`
	Goal           string   `json:"goal"`
	ContextSummary string   `json:"context_summary"`
	Constraints    []string `json:"constraints,omitempty"`
	SuccessSignal  string   `json:"success_signal"`
	RiskHints      []string `json:"risk_hints,omitempty"`
}

func NewTaskFrame(frame TaskFrame) (TaskFrame, error) {
	var err error
	if frame.Task, err = requiredTrimmedString(frame.Task, "task frame requires task", "Provide the task being reasoned about."); err != nil {
		return TaskFrame{}, err
	}
	if frame.Goal, err = requiredTrimmedString(frame.Goal, "task frame requires goal", "Describe the desired outcome."); err != nil {
		return TaskFrame{}, err
	}
	if frame.ContextSummary, err = requiredTrimmedString(frame.ContextSummary, "task frame requires context_summary", "Provide visible context for the run."); err != nil {
		return TaskFrame{}, err
	}
	if frame.SuccessSignal, err = requiredTrimmedString(frame.SuccessSignal, "task frame requires success_signal", "State how the caller will know the work is complete."); err != nil {
		return TaskFrame{}, err
	}
	return frame.clone(), nil
}

func requiredTrimmedString(value, message, nextStep string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", invalidInputError(message, nextStep)
	}
	return trimmed, nil
}

func (f TaskFrame) clone() TaskFrame {
	f.Constraints = cloneStrings(f.Constraints)
	f.RiskHints = cloneStrings(f.RiskHints)
	return f
}

type LedgerEntry struct {
	ID     string `json:"id,omitempty"`
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
	Status string `json:"status,omitempty"`
}

type KnowledgeLedger struct {
	Known       []LedgerEntry `json:"known,omitempty"`
	Unknown     []LedgerEntry `json:"unknown,omitempty"`
	Assumptions []LedgerEntry `json:"assumptions,omitempty"`
	Conflicts   []LedgerEntry `json:"conflicts,omitempty"`
	Checkable   []LedgerEntry `json:"checkable,omitempty"`
	Blocked     []LedgerEntry `json:"blocked,omitempty"`
}

func (l KnowledgeLedger) clone() KnowledgeLedger {
	return KnowledgeLedger{
		Known:       cloneEntries(l.Known),
		Unknown:     cloneEntries(l.Unknown),
		Assumptions: cloneEntries(l.Assumptions),
		Conflicts:   cloneEntries(l.Conflicts),
		Checkable:   cloneEntries(l.Checkable),
		Blocked:     cloneEntries(l.Blocked),
	}
}

func (l KnowledgeLedger) validate() error {
	for _, entries := range [][]LedgerEntry{l.Known, l.Unknown, l.Assumptions, l.Conflicts, l.Checkable, l.Blocked} {
		for _, entry := range entries {
			if _, err := requiredTrimmedString(entry.Text, "ledger entry requires text", "Report a visible knowledge entry before updating the ledger."); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l KnowledgeLedger) merge(adds KnowledgeLedger) (KnowledgeLedger, error) {
	if err := adds.validate(); err != nil {
		return KnowledgeLedger{}, err
	}
	merged := l.clone()
	merged.Known = append(merged.Known, cloneEntries(adds.Known)...)
	merged.Unknown = append(merged.Unknown, cloneEntries(adds.Unknown)...)
	merged.Assumptions = append(merged.Assumptions, cloneEntries(adds.Assumptions)...)
	merged.Conflicts = append(merged.Conflicts, cloneEntries(adds.Conflicts)...)
	merged.Checkable = append(merged.Checkable, cloneEntries(adds.Checkable)...)
	merged.Blocked = append(merged.Blocked, cloneEntries(adds.Blocked)...)
	return merged, nil
}

type MovePlan struct {
	Name                  string    `json:"name"`
	Group                 MoveGroup `json:"group"`
	Reason                string    `json:"reason"`
	ExpectedArtifactDelta string    `json:"expected_artifact_delta"`
	Execute               bool      `json:"execute"`
}

func (m MovePlan) validate() error {
	if _, err := requiredTrimmedString(m.Name, "move plan requires name", "Choose a named cognitive move."); err != nil {
		return err
	}
	if m.Group == "" {
		return invalidInputError("move plan requires group", "Choose a move group such as frame, explore, test, evaluate, calibrate, or finalize.")
	}
	if !validMoveGroup(m.Group) {
		return invalidInputError("move plan requires valid group", "Use frame, explore, test, evaluate, calibrate, or finalize.")
	}
	if _, err := requiredTrimmedString(m.Reason, "move plan requires reason", "Explain why this move fits the current gap."); err != nil {
		return err
	}
	if _, err := requiredTrimmedString(m.ExpectedArtifactDelta, "move plan requires expected_artifact_delta", "Name the artifact the move should change."); err != nil {
		return err
	}
	return nil
}

type EvidenceRef struct {
	Kind               string `json:"kind"`
	Ref                string `json:"ref"`
	Summary            string `json:"summary"`
	VerificationStatus string `json:"verification_status"`
}

func (e EvidenceRef) validate() error {
	for _, value := range []string{e.Kind, e.Ref, e.Summary} {
		if _, err := requiredTrimmedString(value, "evidence requires kind, ref, and summary", "Attach visible evidence before using it to advance the session."); err != nil {
			return err
		}
	}
	return nil
}

type Observation struct {
	MoveName         string        `json:"move_name"`
	WorkProduct      string        `json:"work_product"`
	Evidence         []EvidenceRef `json:"evidence,omitempty"`
	CallerConfidence float64       `json:"caller_confidence,omitempty"`
	Notes            []string      `json:"notes,omitempty"`
}

func (o Observation) validate() error {
	if _, err := requiredTrimmedString(o.MoveName, "observation requires move_name", "Name the move that produced this observation."); err != nil {
		return err
	}
	if _, err := requiredTrimmedString(o.WorkProduct, "observation requires work_product", "Report visible work product before marking a move observed."); err != nil {
		return err
	}
	if o.CallerConfidence < 0 || o.CallerConfidence > 1 {
		return invalidInputError("caller_confidence must be between 0 and 1", "Provide confidence as a normalized value.")
	}
	for _, evidence := range o.Evidence {
		if err := evidence.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (o Observation) clone() Observation {
	o.Evidence = cloneEvidence(o.Evidence)
	o.Notes = cloneStrings(o.Notes)
	return o
}

type GateReport struct {
	Status      GateStatus `json:"status"`
	Blockers    []string   `json:"blockers,omitempty"`
	Warnings    []string   `json:"warnings,omitempty"`
	MissingWork []string   `json:"missing_work,omitempty"`
}

func (g GateReport) validate() error {
	switch g.Status {
	case GatePass, GateWarn, GateBlocked:
		return nil
	default:
		return invalidInputError("gate report requires valid status", "Use pass, warn, or blocked.")
	}
}

func (g GateReport) clone() GateReport {
	g.Blockers = cloneStrings(g.Blockers)
	g.Warnings = cloneStrings(g.Warnings)
	g.MissingWork = cloneStrings(g.MissingWork)
	return g
}

type Objection struct {
	ID       string            `json:"id,omitempty"`
	Severity ObjectionSeverity `json:"severity"`
	Text     string            `json:"text"`
	Resolved bool              `json:"resolved,omitempty"`
}

func (o Objection) validate() error {
	if o.Severity == "" {
		return invalidInputError("objection requires severity", "Classify the objection before adding it.")
	}
	if !validObjectionSeverity(o.Severity) {
		return invalidInputError("objection requires valid severity", "Use critical, major, or minor.")
	}
	if _, err := requiredTrimmedString(o.Text, "objection requires text", "Describe the unresolved issue."); err != nil {
		return err
	}
	return nil
}

type ConfidenceFactor struct {
	Name   string  `json:"name"`
	Impact float64 `json:"impact"`
	Reason string  `json:"reason"`
}

func (f ConfidenceFactor) validate() error {
	if _, err := requiredTrimmedString(f.Name, "confidence factor requires name and reason", "Explain the evidence or objection changing confidence."); err != nil {
		return err
	}
	if _, err := requiredTrimmedString(f.Reason, "confidence factor requires name and reason", "Explain the evidence or objection changing confidence."); err != nil {
		return err
	}
	return nil
}

type StopDecision struct {
	Action               StopAction  `json:"action"`
	Reason               string      `json:"reason"`
	CanFinalize          bool        `json:"can_finalize"`
	UnresolvedObjections []Objection `json:"unresolved_objections,omitempty"`
	BudgetState          string      `json:"budget_state,omitempty"`
}

func (d StopDecision) validate() error {
	if d.Action == "" {
		return invalidInputError("stop decision requires action", "Choose continue, redirect, compress, finalize, or stop.")
	}
	if !validStopAction(d.Action) {
		return invalidInputError("stop decision requires valid action", "Use continue, redirect, compress, finalize, or stop.")
	}
	if _, err := requiredTrimmedString(d.Reason, "stop decision requires reason", "Explain why the session should continue, redirect, compress, finalize, or stop."); err != nil {
		return err
	}
	for _, objection := range d.UnresolvedObjections {
		if objection.Resolved {
			return invalidInputError("stop decision requires unresolved objections", "Remove resolved objections from unresolved_objections before stopping.")
		}
		if err := objection.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (d StopDecision) clone() StopDecision {
	d.UnresolvedObjections = cloneObjections(d.UnresolvedObjections)
	return d
}

type KnowledgePatch struct {
	Phase             Phase              `json:"phase,omitempty"`
	ProposedAnswer    string             `json:"proposed_answer,omitempty"`
	LedgerAdds        KnowledgeLedger    `json:"ledger_adds,omitempty"`
	Move              *MovePlan          `json:"move,omitempty"`
	Observation       *Observation       `json:"observation,omitempty"`
	GateReport        *GateReport        `json:"gate_report,omitempty"`
	Objections        []Objection        `json:"objections,omitempty"`
	ConfidenceFactors []ConfidenceFactor `json:"confidence_factors,omitempty"`
	StopDecision      *StopDecision      `json:"stop_decision,omitempty"`
}

type ThinkingSession struct {
	ID                string             `json:"id"`
	Phase             Phase              `json:"phase"`
	Frame             TaskFrame          `json:"frame"`
	StartedAt         time.Time          `json:"started_at,omitempty"`
	UpdatedAt         time.Time          `json:"updated_at,omitempty"`
	ProposedAnswer    string             `json:"proposed_answer,omitempty"`
	Ledger            KnowledgeLedger    `json:"ledger"`
	MoveHistory       []MovePlan         `json:"move_history,omitempty"`
	Observations      []Observation      `json:"observations,omitempty"`
	GateReports       []GateReport       `json:"gate_reports,omitempty"`
	Objections        []Objection        `json:"objections,omitempty"`
	ConfidenceFactors []ConfidenceFactor `json:"confidence_factors,omitempty"`
	StopDecision      *StopDecision      `json:"stop_decision,omitempty"`
}

func NewThinkingSession(id string, frame TaskFrame) ThinkingSession {
	now := time.Now().UTC()
	return ThinkingSession{
		ID:        id,
		Phase:     PhaseFrame,
		Frame:     frame.clone(),
		StartedAt: now,
		UpdatedAt: now,
		Ledger:    KnowledgeLedger{},
	}
}

func (s ThinkingSession) ApplyPatch(patch KnowledgePatch) (ThinkingSession, error) {
	next := s.clone()
	if patch.Phase != "" {
		if !validPhase(patch.Phase) {
			return ThinkingSession{}, invalidInputError("patch requires valid phase", "Use frame, inventory, move, observe, test, integrate, or finalize.")
		}
		next.Phase = patch.Phase
	}
	if patch.ProposedAnswer != "" {
		next.ProposedAnswer = patch.ProposedAnswer
	}

	ledger, err := next.Ledger.merge(patch.LedgerAdds)
	if err != nil {
		return ThinkingSession{}, err
	}
	next.Ledger = ledger

	if patch.Move != nil {
		if err := patch.Move.validate(); err != nil {
			return ThinkingSession{}, err
		}
		next.MoveHistory = append(next.MoveHistory, *patch.Move)
	}
	if patch.Observation != nil {
		if err := patch.Observation.validate(); err != nil {
			return ThinkingSession{}, err
		}
		next.Observations = append(next.Observations, patch.Observation.clone())
	}
	if patch.GateReport != nil {
		if err := patch.GateReport.validate(); err != nil {
			return ThinkingSession{}, err
		}
		next.GateReports = append(next.GateReports, patch.GateReport.clone())
	}
	for _, objection := range patch.Objections {
		if err := objection.validate(); err != nil {
			return ThinkingSession{}, err
		}
		next.Objections = append(next.Objections, objection)
	}
	for _, factor := range patch.ConfidenceFactors {
		if err := factor.validate(); err != nil {
			return ThinkingSession{}, err
		}
		next.ConfidenceFactors = append(next.ConfidenceFactors, factor)
	}
	if patch.StopDecision != nil {
		if err := patch.StopDecision.validate(); err != nil {
			return ThinkingSession{}, err
		}
		cloned := patch.StopDecision.clone()
		next.StopDecision = &cloned
	}
	next.UpdatedAt = time.Now().UTC()
	return next, nil
}

func (s ThinkingSession) clone() ThinkingSession {
	s.Frame = s.Frame.clone()
	s.Ledger = s.Ledger.clone()
	s.MoveHistory = cloneMoves(s.MoveHistory)
	s.Observations = cloneObservations(s.Observations)
	s.GateReports = cloneGates(s.GateReports)
	s.Objections = cloneObjections(s.Objections)
	s.ConfidenceFactors = cloneFactors(s.ConfidenceFactors)
	if s.StopDecision != nil {
		cloned := s.StopDecision.clone()
		s.StopDecision = &cloned
	}
	return s
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneEntries(in []LedgerEntry) []LedgerEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]LedgerEntry, len(in))
	copy(out, in)
	return out
}

func cloneEvidence(in []EvidenceRef) []EvidenceRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]EvidenceRef, len(in))
	copy(out, in)
	return out
}

func cloneMoves(in []MovePlan) []MovePlan {
	if len(in) == 0 {
		return nil
	}
	out := make([]MovePlan, len(in))
	copy(out, in)
	return out
}

func cloneObservations(in []Observation) []Observation {
	if len(in) == 0 {
		return nil
	}
	out := make([]Observation, len(in))
	for i, observation := range in {
		out[i] = observation.clone()
	}
	return out
}

func cloneGates(in []GateReport) []GateReport {
	if len(in) == 0 {
		return nil
	}
	out := make([]GateReport, len(in))
	for i, gate := range in {
		out[i] = gate.clone()
	}
	return out
}

func cloneObjections(in []Objection) []Objection {
	if len(in) == 0 {
		return nil
	}
	out := make([]Objection, len(in))
	copy(out, in)
	return out
}

func cloneFactors(in []ConfidenceFactor) []ConfidenceFactor {
	if len(in) == 0 {
		return nil
	}
	out := make([]ConfidenceFactor, len(in))
	copy(out, in)
	return out
}

func validPhase(phase Phase) bool {
	switch phase {
	case PhaseFrame, PhaseInventory, PhaseMove, PhaseObserve, PhaseTest, PhaseIntegrate, PhaseFinalize:
		return true
	default:
		return false
	}
}

func validMoveGroup(group MoveGroup) bool {
	switch group {
	case MoveGroupFrame, MoveGroupExplore, MoveGroupTest, MoveGroupEvaluate, MoveGroupCalibrate, MoveGroupFinalize:
		return true
	default:
		return false
	}
}

func validObjectionSeverity(severity ObjectionSeverity) bool {
	switch severity {
	case ObjectionCritical, ObjectionMajor, ObjectionMinor:
		return true
	default:
		return false
	}
}

func validStopAction(action StopAction) bool {
	switch action {
	case StopContinue, StopRedirect, StopCompress, StopFinalize, StopHalt:
		return true
	default:
		return false
	}
}
