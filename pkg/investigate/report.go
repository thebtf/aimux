package investigate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GenerateReport creates a markdown investigation report with all enhanced sections.
func GenerateReport(state *InvestigationState) string {
	var sb strings.Builder

	// Header with metadata
	sb.WriteString(fmt.Sprintf("# Investigation Report: %s\n\n", state.Topic))
	sb.WriteString(fmt.Sprintf("**Generated:** %s\n", time.Now().UTC().Format(time.RFC3339)))
	model := os.Getenv("CLAUDE_MODEL")
	if model == "" {
		model = "unknown"
	}
	sessionID := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if sessionID == "" {
		sessionID = "unknown"
	}
	sb.WriteString(fmt.Sprintf("**Model:** %s\n", model))
	sb.WriteString(fmt.Sprintf("**Session:** %s\n", sessionID))

	coverage := ComputeCoverage(state)
	sb.WriteString(fmt.Sprintf("**Coverage:** %.0f%%\n", coverage*100))

	// Confidence aggregate
	confidenceCounts := map[Confidence]int{}
	for _, f := range state.Findings {
		if f.CorrectedBy == "" {
			confidenceCounts[f.Confidence]++
		}
	}
	var confParts []string
	for _, c := range []Confidence{ConfidenceVerified, ConfidenceInferred, ConfidenceStale, ConfidenceBlocked, ConfidenceUnknown} {
		if n := confidenceCounts[c]; n > 0 {
			confParts = append(confParts, fmt.Sprintf("%d %s", n, c))
		}
	}
	if len(confParts) > 0 {
		sb.WriteString(fmt.Sprintf("**Confidence:** %s\n", strings.Join(confParts, ", ")))
	}

	sb.WriteString(fmt.Sprintf("**Iterations:** %d\n", state.Iteration))
	sb.WriteString(fmt.Sprintf("**Findings:** %d\n", len(state.Findings)))
	sb.WriteString(fmt.Sprintf("**Corrections:** %d\n", len(state.Corrections)))
	sb.WriteString("\n")

	// Completeness gate warning
	if coverage < 0.8 {
		checked := 0
		for _, a := range state.CoverageAreas {
			if state.CoverageChecked[a] {
				checked++
			}
		}
		var unchecked []string
		for _, a := range state.CoverageAreas {
			if !state.CoverageChecked[a] {
				unchecked = append(unchecked, a)
			}
		}
		sb.WriteString(fmt.Sprintf("> **WARNING:** Coverage %.0f%% (%d/%d areas checked). Report may be incomplete.\n",
			coverage*100, checked, len(state.CoverageAreas)))
		sb.WriteString(fmt.Sprintf("> Unchecked: %s\n", strings.Join(unchecked, ", ")))
		sb.WriteString("> Consider continuing investigation before acting on conclusions.\n\n")
	}

	// Findings table
	sb.WriteString("## Findings\n\n")
	sb.WriteString("| ID | Severity | Confidence | Description | Source | Iter | Status |\n")
	sb.WriteString("|----|----------|------------|-------------|--------|------|--------|\n")
	for _, f := range state.Findings {
		status := "active"
		if f.CorrectedBy != "" {
			status = fmt.Sprintf("~~corrected by %s~~", f.CorrectedBy)
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %d | %s |\n",
			f.ID, f.Severity, f.Confidence, f.Description, f.Source, f.Iteration, status))
	}
	sb.WriteString("\n")

	// Corrections section
	if len(state.Corrections) > 0 {
		sb.WriteString("## Corrections (evolution of understanding)\n\n")
		for _, c := range state.Corrections {
			sb.WriteString(fmt.Sprintf("### %s → corrected in iteration %d\n", c.OriginalID, c.Iteration))
			sb.WriteString(fmt.Sprintf("- **Was:** %s\n", c.OriginalClaim))
			sb.WriteString(fmt.Sprintf("- **Now:** %s\n", c.CorrectedClaim))
			sb.WriteString(fmt.Sprintf("- **Evidence:** %s\n\n", c.Evidence))
		}
	}

	// What to Be Skeptical Of (NEW — auto-generated)
	sb.WriteString("## What to Be Skeptical Of\n\n")
	hasInferred := false
	for _, f := range state.Findings {
		if f.CorrectedBy == "" && f.Confidence == ConfidenceInferred {
			if !hasInferred {
				sb.WriteString("**Findings based on INFERRED evidence (not directly verified):**\n")
				hasInferred = true
			}
			sb.WriteString(fmt.Sprintf("- %s: %s\n", f.ID, f.Description))
		}
	}
	if hasInferred {
		sb.WriteString("\n")
	}

	// Single-point-of-failure conclusions
	for _, f := range state.Findings {
		if f.Severity == SeverityP0 && f.CorrectedBy == "" {
			sb.WriteString(fmt.Sprintf("- **If %s is wrong**, conclusions depending on it collapse\n", f.ID))
		}
	}

	// Low-coverage areas
	var lowCoverageAreas []string
	for _, a := range state.CoverageAreas {
		if !state.CoverageChecked[a] {
			lowCoverageAreas = append(lowCoverageAreas, a)
		}
	}
	if len(lowCoverageAreas) > 0 {
		sb.WriteString(fmt.Sprintf("- Areas not fully investigated: %s\n", strings.Join(lowCoverageAreas, ", ")))
	}
	sb.WriteString("\n")

	// Coverage map
	sb.WriteString("## Coverage Map\n\n")
	sb.WriteString("| Area | Status |\n")
	sb.WriteString("|------|--------|\n")
	for _, area := range state.CoverageAreas {
		status := "✗ unchecked"
		if state.CoverageChecked[area] {
			status = "✓ checked"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s |\n", area, status))
	}
	sb.WriteString("\n")

	// Convergence history
	sb.WriteString("## Convergence History\n\n")
	if len(state.ConvergenceHistory) > 0 {
		for i, score := range state.ConvergenceHistory {
			sb.WriteString(fmt.Sprintf("- Iteration %d: %.0f%%\n", i+1, score*100))
		}
	} else {
		sb.WriteString("(no convergence data — investigation ended early)\n")
	}
	sb.WriteString("\n")

	// Key Takeaways (NEW — auto-generated from findings)
	sb.WriteString("## Key Takeaways\n\n")

	// 1. Most important root cause: highest severity active finding
	var rootCause *Finding
	for i := range state.Findings {
		f := &state.Findings[i]
		if f.CorrectedBy == "" {
			if rootCause == nil || f.Severity < rootCause.Severity {
				rootCause = f
			}
		}
	}
	if rootCause != nil {
		sb.WriteString(fmt.Sprintf("1. **Root cause:** %s (%s)\n", rootCause.Description, rootCause.Severity))
	} else {
		sb.WriteString("1. **Root cause:** No findings recorded\n")
	}

	// 2. Most impactful recommendation: from P0/P1 findings
	var recommendation *Finding
	for i := range state.Findings {
		f := &state.Findings[i]
		if f.CorrectedBy == "" && (f.Severity == SeverityP0 || f.Severity == SeverityP1) {
			if recommendation == nil {
				recommendation = f
			}
		}
	}
	if recommendation != nil {
		sb.WriteString(fmt.Sprintf("2. **Key recommendation:** Address %s — %s\n", recommendation.ID, recommendation.Description))
	} else {
		sb.WriteString("2. **Key recommendation:** No critical findings\n")
	}

	// 3. Most dangerous assumption: inferred or blocked findings
	var dangerousAssumption *Finding
	for i := range state.Findings {
		f := &state.Findings[i]
		if f.CorrectedBy == "" && (f.Confidence == ConfidenceInferred || f.Confidence == ConfidenceBlocked) {
			dangerousAssumption = f
			break
		}
	}
	if dangerousAssumption != nil {
		sb.WriteString(fmt.Sprintf("3. **Watch out:** %s (confidence: %s) — verify before acting\n",
			dangerousAssumption.Description, dangerousAssumption.Confidence))
	} else if len(lowCoverageAreas) > 0 {
		sb.WriteString(fmt.Sprintf("3. **Watch out:** %d unchecked areas may contain unknown issues\n", len(lowCoverageAreas)))
	} else {
		sb.WriteString("3. **Watch out:** All findings verified — low assumption risk\n")
	}

	return sb.String()
}

// SaveReport writes the report to .agent/reports/ and returns the file path.
func SaveReport(cwd, topic, report string) (string, error) {
	dir := filepath.Join(cwd, ".agent", "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create reports dir: %w", err)
	}

	slug := strings.ToLower(topic)
	slug = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, slug)
	if len(slug) > 40 {
		slug = slug[:40]
	}

	date := time.Now().UTC().Format("2006-01-02T15-04-05")
	filename := fmt.Sprintf("investigate-%s-%s.md", slug, date)
	fpath := filepath.Join(dir, filename)

	if err := os.WriteFile(fpath, []byte(report), 0o644); err != nil {
		return "", fmt.Errorf("write report: %w", err)
	}

	return fpath, nil
}
