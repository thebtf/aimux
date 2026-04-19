package investigate

import (
	"fmt"
	"sync"
	"time"
)

const (
	// investigationTTL is the maximum lifetime of an inactive investigation.
	investigationTTL = 30 * time.Minute
	// maxFindingsPerInvestigation caps the total number of findings to prevent unbounded growth.
	maxFindingsPerInvestigation = 100
	// maxFindingDescriptionBytes caps finding description size.
	maxFindingDescriptionBytes = 2048
)

var (
	investigations sync.Map // map[string]*InvestigationState
)

// CreateInvestigation starts a new investigation session and registers a 30-minute
// cleanup timer that removes the session when it has been idle for the full TTL.
func CreateInvestigation(sessionID, topic, domainName string) *InvestigationState {
	domain := GetDomain(domainName)
	if domain == nil {
		domain = &GenericDomain
	}

	state := &InvestigationState{
		Topic:              topic,
		Domain:             domain.Name,
		Iteration:          0,
		Findings:           []Finding{},
		Corrections:        []Correction{},
		CoverageAreas:      append([]string{}, domain.CoverageAreas...),
		CoverageChecked:    make(map[string]bool),
		ConvergenceHistory: []float64{},
		CreatedAt:          time.Now(),
		LastActivityAt:     time.Now(),
	}
	investigations.Store(sessionID, state)

	// Cleanup timer: fires after TTL and removes the session if still idle.
	time.AfterFunc(investigationTTL, func() {
		val, ok := investigations.Load(sessionID)
		if !ok {
			return
		}
		s := val.(*InvestigationState)
		if time.Since(s.LastActivityAt) >= investigationTTL {
			investigations.Delete(sessionID)
		}
	})

	return state
}

// GetInvestigation returns the state for the given session, or nil.
func GetInvestigation(sessionID string) *InvestigationState {
	val, ok := investigations.Load(sessionID)
	if !ok {
		return nil
	}
	return val.(*InvestigationState)
}

// ListInvestigations returns summaries of all active investigations.
func ListInvestigations() []InvestigationSummary {
	var result []InvestigationSummary
	investigations.Range(func(key, value any) bool {
		id := key.(string)
		s := value.(*InvestigationState)
		result = append(result, InvestigationSummary{
			SessionID:     id,
			Topic:         s.Topic,
			Domain:        s.Domain,
			Iteration:     s.Iteration,
			FindingsCount: len(s.Findings),
		})
		return true
	})
	return result
}

// DeleteInvestigation removes an investigation from memory.
func DeleteInvestigation(sessionID string) {
	investigations.Delete(sessionID)
}

// AddFinding adds a finding to an investigation. Returns the finding, optional correction, and error.
// Deduplicates by description. Handles correction chains.
// Confidence defaults to VERIFIED if not specified.
func AddFinding(sessionID string, input FindingInput) (*Finding, *Correction, error) {
	val, ok := investigations.Load(sessionID)
	if !ok {
		return nil, nil, fmt.Errorf("investigation %q not found", sessionID)
	}
	state := val.(*InvestigationState)

	// Enforce size limits.
	if len(state.Findings) >= maxFindingsPerInvestigation {
		return nil, nil, fmt.Errorf("investigation %q has reached the maximum of %d findings", sessionID, maxFindingsPerInvestigation)
	}
	if len(input.Description) > maxFindingDescriptionBytes {
		return nil, nil, fmt.Errorf("finding description exceeds maximum length of %d bytes", maxFindingDescriptionBytes)
	}

	// Default confidence
	confidence := input.Confidence
	if confidence == "" {
		confidence = ConfidenceVerified
	}

	// Dedup: skip if exact same description already exists (active, not corrected)
	for i := range state.Findings {
		if state.Findings[i].Description == input.Description && state.Findings[i].CorrectedBy == "" {
			return &state.Findings[i], nil, nil
		}
	}

	findingID := fmt.Sprintf("F-%d-%d", state.Iteration, len(state.Findings)+1)

	finding := Finding{
		ID:           findingID,
		Severity:     input.Severity,
		Confidence:   confidence,
		Description:  input.Description,
		Source:       input.Source,
		Iteration:    state.Iteration,
		CoverageArea: input.CoverageArea,
	}

	// Build new state immutably
	newFindings := make([]Finding, len(state.Findings))
	copy(newFindings, state.Findings)

	var correction *Correction

	if input.Corrects != "" {
		// Find and mark the original finding
		found := false
		for i := range newFindings {
			if newFindings[i].ID == input.Corrects {
				newFindings[i] = Finding{
					ID:           newFindings[i].ID,
					Severity:     newFindings[i].Severity,
					Confidence:   newFindings[i].Confidence,
					Description:  newFindings[i].Description,
					Source:       newFindings[i].Source,
					Iteration:    newFindings[i].Iteration,
					CoverageArea: newFindings[i].CoverageArea,
					CorrectedBy:  findingID,
				}
				correction = &Correction{
					OriginalID:     input.Corrects,
					OriginalClaim:  newFindings[i].Description,
					CorrectedClaim: input.Description,
					Evidence:       input.Source,
					Iteration:      state.Iteration,
				}
				found = true
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("cannot correct %q — finding not found", input.Corrects)
		}
	}

	newFindings = append(newFindings, finding)

	// Build new corrections slice
	newCorrections := make([]Correction, len(state.Corrections))
	copy(newCorrections, state.Corrections)
	if correction != nil {
		newCorrections = append(newCorrections, *correction)
	}

	// Update coverage
	newCoverage := make(map[string]bool, len(state.CoverageChecked))
	for k, v := range state.CoverageChecked {
		newCoverage[k] = v
	}
	if input.CoverageArea != "" {
		newCoverage[input.CoverageArea] = true
	}

	// Store new state
	newState := &InvestigationState{
		Topic:              state.Topic,
		Domain:             state.Domain,
		Iteration:          state.Iteration,
		Findings:           newFindings,
		Corrections:        newCorrections,
		CoverageAreas:      state.CoverageAreas,
		CoverageChecked:    newCoverage,
		ConvergenceHistory: state.ConvergenceHistory,
		CreatedAt:          state.CreatedAt,
		LastActivityAt:     time.Now(),
	}
	investigations.Store(sessionID, newState)

	return &finding, correction, nil
}

// NextIteration advances the investigation to the next iteration.
func NextIteration(sessionID string) error {
	val, ok := investigations.Load(sessionID)
	if !ok {
		return fmt.Errorf("investigation %q not found", sessionID)
	}
	state := val.(*InvestigationState)

	convergence := ComputeConvergence(state)

	newHistory := make([]float64, len(state.ConvergenceHistory))
	copy(newHistory, state.ConvergenceHistory)
	newHistory = append(newHistory, convergence)

	newState := &InvestigationState{
		Topic:              state.Topic,
		Domain:             state.Domain,
		Iteration:          state.Iteration + 1,
		Findings:           state.Findings,
		Corrections:        state.Corrections,
		CoverageAreas:      state.CoverageAreas,
		CoverageChecked:    state.CoverageChecked,
		ConvergenceHistory: newHistory,
		CreatedAt:          state.CreatedAt,
		LastActivityAt:     time.Now(),
	}
	investigations.Store(sessionID, newState)
	return nil
}

// ComputeConvergence calculates convergence for the current iteration.
// Returns 1 - (corrections_this_iteration / findings_this_iteration).
func ComputeConvergence(state *InvestigationState) float64 {
	if state.Iteration <= 0 {
		return 0.5
	}

	currentCorrections := 0
	for _, c := range state.Corrections {
		if c.Iteration == state.Iteration {
			currentCorrections++
		}
	}

	currentFindings := 0
	for _, f := range state.Findings {
		if f.Iteration == state.Iteration {
			currentFindings++
		}
	}

	if currentFindings == 0 {
		return 1.0
	}
	return 1.0 - float64(currentCorrections)/float64(currentFindings)
}

// ComputeCoverage calculates what fraction of coverage areas have been checked.
func ComputeCoverage(state *InvestigationState) float64 {
	if len(state.CoverageAreas) == 0 {
		return 1.0
	}
	checked := 0
	for _, area := range state.CoverageAreas {
		if state.CoverageChecked[area] {
			checked++
		}
	}
	return float64(checked) / float64(len(state.CoverageAreas))
}

// ClearAllInvestigations removes all investigations (for testing).
func ClearAllInvestigations() {
	investigations.Range(func(key, _ any) bool {
		investigations.Delete(key)
		return true
	})
}
