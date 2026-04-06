package investigate

import (
	"fmt"
	"strings"
)

// Assess computes convergence, coverage, gap analysis, and recommendation.
// Advances the investigation to the next iteration after assessment.
func Assess(sessionID string) (*AssessResult, error) {
	state := GetInvestigation(sessionID)
	if state == nil {
		return nil, fmt.Errorf("investigation %q not found", sessionID)
	}

	convergence := ComputeConvergence(state)
	coverage := ComputeCoverage(state)

	// Unchecked areas
	var unchecked []string
	for _, area := range state.CoverageAreas {
		if !state.CoverageChecked[area] {
			unchecked = append(unchecked, area)
		}
	}

	// Weak areas: only 1 finding (thin coverage)
	areaFindingCount := make(map[string]int)
	for _, f := range state.Findings {
		if f.CoverageArea != "" && f.CorrectedBy == "" {
			areaFindingCount[f.CoverageArea]++
		}
	}
	var weakAreas []string
	for area, count := range areaFindingCount {
		if count == 1 {
			weakAreas = append(weakAreas, area)
		}
	}

	// Conflicting areas: same area with different severity findings (P0 vs P2+)
	areaSeverities := make(map[string]map[Severity]bool)
	for _, f := range state.Findings {
		if f.CoverageArea != "" && f.CorrectedBy == "" {
			if areaSeverities[f.CoverageArea] == nil {
				areaSeverities[f.CoverageArea] = make(map[Severity]bool)
			}
			areaSeverities[f.CoverageArea][f.Severity] = true
		}
	}
	var conflictingAreas []string
	for area, sevs := range areaSeverities {
		hasCritical := sevs[SeverityP0] || sevs[SeverityP1]
		hasLow := sevs[SeverityP2] || sevs[SeverityP3]
		if hasCritical && hasLow {
			conflictingAreas = append(conflictingAreas, area)
		}
	}

	// Recommendation
	converged := convergence >= 1.0
	covered := coverage >= 0.8
	var recommendation string
	if converged && coverage >= 1.0 {
		recommendation = "COMPLETE"
	} else if converged && covered {
		recommendation = "MAY_STOP"
	} else {
		recommendation = "CONTINUE"
	}

	// Priority next: unchecked area with fewest findings
	priorityNext := ""
	if len(unchecked) > 0 {
		priorityNext = unchecked[0]
	}

	// Resolve domain for angles and anti-patterns
	domain := GetDomain(state.Domain)
	if domain == nil {
		domain = &GenericDomain
	}

	// Angle rotation
	angles := domain.Angles
	if len(angles) == 0 {
		angles = DefaultAngles
	}
	angleIdx := state.Iteration % len(angles)
	angle := angles[angleIdx]
	suggestedAngle := fmt.Sprintf("%s: %s", angle.Label, angle.Description)

	// Build think call suggestion
	thinkParams := make([]string, 0)
	for k, v := range angle.ThinkParams {
		resolved := strings.ReplaceAll(v, "{topic}", state.Topic)
		thinkParams = append(thinkParams, fmt.Sprintf(`%s: "%s"`, k, resolved))
	}
	suggestedThinkCall := fmt.Sprintf(`mcp__aimux__think({ pattern: "%s", %s })`,
		angle.ThinkPattern, strings.Join(thinkParams, ", "))

	// Anti-pattern warnings (rotate, 1-2 per assess)
	var antiPatternWarnings []string
	if len(domain.AntiPatterns) > 0 {
		apIdx := state.Iteration % len(domain.AntiPatterns)
		antiPatternWarnings = append(antiPatternWarnings, domain.AntiPatterns[apIdx])
		if state.Iteration < 3 && len(domain.AntiPatterns) > 1 {
			secondIdx := (apIdx + 1) % len(domain.AntiPatterns)
			antiPatternWarnings = append(antiPatternWarnings, domain.AntiPatterns[secondIdx])
		}
	}

	// Pattern hints: top 3 patterns not yet matched by findings
	var patternHints []PatternEntry
	if len(domain.Patterns) > 0 {
		findingDescs := make([]string, 0, len(state.Findings))
		for _, f := range state.Findings {
			findingDescs = append(findingDescs, strings.ToLower(f.Description))
		}
		for _, pattern := range domain.Patterns {
			if len(patternHints) >= 3 {
				break
			}
			words := strings.Fields(strings.ToLower(pattern.Indicator))
			significantWords := make([]string, 0)
			for _, w := range words {
				if len(w) > 3 {
					significantWords = append(significantWords, w)
				}
			}
			matched := false
			for _, desc := range findingDescs {
				matchCount := 0
				for _, w := range significantWords {
					if strings.Contains(desc, w) {
						matchCount++
					}
				}
				if len(significantWords) > 0 && matchCount >= (len(significantWords)+1)/2 {
					matched = true
					break
				}
			}
			if !matched {
				patternHints = append(patternHints, pattern)
			}
		}
	}

	// Adversarial prompt for P0 findings when MAY_STOP
	var adversarialPrompt string
	if recommendation == "MAY_STOP" || recommendation == "COMPLETE" {
		hasP0 := false
		for _, f := range state.Findings {
			if f.Severity == SeverityP0 && f.CorrectedBy == "" {
				hasP0 = true
				break
			}
		}
		if hasP0 {
			adversarialPrompt = "For each P0 finding, what evidence would CONTRADICT it? " +
				"Is the source reliable? Could there be an alternative explanation? " +
				"Are any findings circular (A proves B, B proves A)?"
		}
	}

	// Message
	correctionWarning := ""
	if len(state.Corrections) > 0 {
		correctionWarning = fmt.Sprintf(" You've been wrong %d time(s) in this investigation.", len(state.Corrections))
	}

	var message string
	switch {
	case recommendation == "MAY_STOP" || recommendation == "COMPLETE":
		if len(unchecked) > 0 {
			message = fmt.Sprintf("Convergence: %.0f%%, Coverage: %.0f%%. You MIGHT be done — but %d area(s) unchecked: %s.%s",
				convergence*100, coverage*100, len(unchecked), strings.Join(unchecked, ", "), correctionWarning)
		} else {
			message = fmt.Sprintf("All areas checked, convergence %.0f%%. You can stop.%s", convergence*100, correctionWarning)
		}
	case convergence < 1.0:
		currentCorrections := 0
		for _, c := range state.Corrections {
			if c.Iteration == state.Iteration {
				currentCorrections++
			}
		}
		if currentCorrections > 0 {
			message = fmt.Sprintf("Your model is SHIFTING — %d correction(s) this iteration.", currentCorrections)
		} else {
			message = "Early investigation — not enough iterations to assess convergence yet."
		}
		message += fmt.Sprintf(" Coverage: %.0f%%.%s", coverage*100, correctionWarning)
		if priorityNext != "" {
			method := domain.Methods[priorityNext]
			if method == "" {
				method = fmt.Sprintf("Investigate %q using the most appropriate tool.", priorityNext)
			}
			message += fmt.Sprintf(" NEXT: %s. METHOD: %s", priorityNext, method)
		}
	default:
		message = fmt.Sprintf("Convergence OK but only %.0f%% of areas checked. Unchecked: %s.",
			coverage*100, strings.Join(unchecked, ", "))
	}

	// Advance iteration
	NextIteration(sessionID)

	return &AssessResult{
		Iteration:           state.Iteration,
		ConvergenceScore:    convergence,
		CoverageScore:       coverage,
		FindingsCount:       len(state.Findings),
		CorrectionsCount:    len(state.Corrections),
		Recommendation:      recommendation,
		UncheckedAreas:      unchecked,
		WeakAreas:           weakAreas,
		ConflictingAreas:    conflictingAreas,
		PriorityNext:        priorityNext,
		SuggestedAngle:      suggestedAngle,
		SuggestedThinkCall:  suggestedThinkCall,
		AntiPatternWarnings: antiPatternWarnings,
		PatternHints:        patternHints,
		AdversarialPrompt:   adversarialPrompt,
		Message:             message,
	}, nil
}
