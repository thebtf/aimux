package investigate

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// ReportEntry describes a saved investigation report on disk.
type ReportEntry struct {
	Filename string `json:"filename"`
	Topic    string `json:"topic"`
	Date     string `json:"date"`
	Size     int64  `json:"size"`
}

// ListReports scans .agent/reports/ for investigate-*.md files.
// Returns entries sorted by date descending (newest first).
func ListReports(cwd string) ([]ReportEntry, error) {
	dir := filepath.Join(cwd, ".agent", "reports")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read reports dir: %w", err)
	}

	var reports []ReportEntry
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "investigate-") || !strings.HasSuffix(name, ".md") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		topic, date := parseReportFilename(name)
		reports = append(reports, ReportEntry{
			Filename: name,
			Topic:    topic,
			Date:     date,
			Size:     info.Size(),
		})
	}

	sort.Slice(reports, func(i, j int) bool {
		return reports[i].Date > reports[j].Date
	})

	return reports, nil
}

// parseReportFilename extracts topic and date from investigate-{slug}-{date}.md.
func parseReportFilename(name string) (topic, date string) {
	// Remove prefix "investigate-" and suffix ".md"
	core := strings.TrimPrefix(name, "investigate-")
	core = strings.TrimSuffix(core, ".md")

	// Date is the last part after the last 'T' that looks like a timestamp
	// Format: {slug}-{YYYY-MM-DDT...}
	// Find the date portion: look for pattern YYYY-MM-DD
	for i := len(core) - 1; i >= 10; i-- {
		if core[i-10:i-6] == "-" || (i >= 19 && core[i-19] >= '2') {
			// Try to find date start: 4 digits, dash, 2 digits, dash, 2 digits
			candidate := findDateStart(core)
			if candidate >= 0 {
				topic = strings.TrimRight(core[:candidate], "-")
				date = core[candidate:]
				// Convert dashes back to colons for display
				topic = strings.ReplaceAll(topic, "-", " ")
				return
			}
		}
		break
	}

	topic = strings.ReplaceAll(core, "-", " ")
	return topic, ""
}

// findDateStart finds the index where a YYYY-MM-DD date pattern starts.
func findDateStart(s string) int {
	// Look for pattern: digit digit digit digit - digit digit - digit digit T
	for i := 0; i <= len(s)-10; i++ {
		if s[i] >= '2' && s[i] <= '9' && // year starts with 2-9
			isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) &&
			s[i+4] == '-' &&
			isDigit(s[i+5]) && isDigit(s[i+6]) &&
			s[i+7] == '-' &&
			isDigit(s[i+8]) && isDigit(s[i+9]) {
			// Verify it's preceded by a dash or is at start
			if i == 0 || s[i-1] == '-' {
				return i
			}
		}
	}
	return -1
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// RecallResult holds a matched report for the recall action.
type RecallResult struct {
	Filename string `json:"filename"`
	Topic    string `json:"topic"`
	Date     string `json:"date"`
	Content  string `json:"content"`
}

const contentSearchLines = 50 // Number of lines to scan during content-based recall

// RecallReport finds a report matching the topic query.
// First tries slug/topic substring match (case-insensitive), then falls back to
// content search (first 50 lines). Returns the newest match, or nil if no match.
func RecallReport(cwd, topicQuery string) (*RecallResult, error) {
	if topicQuery == "" {
		return nil, fmt.Errorf("topic query is required")
	}

	reports, err := ListReports(cwd)
	if err != nil {
		return nil, err
	}

	query := strings.ToLower(topicQuery)

	// Phase 1: topic/slug substring match (already sorted newest-first)
	for _, r := range reports {
		if strings.Contains(strings.ToLower(r.Topic), query) ||
			strings.Contains(strings.ToLower(r.Filename), query) {
			return readReportContent(cwd, r)
		}
	}

	// Phase 2: content search (first N lines of each report)
	dir := filepath.Join(cwd, ".agent", "reports")
	for _, r := range reports {
		if matchesContent(filepath.Join(dir, r.Filename), query) {
			return readReportContent(cwd, r)
		}
	}

	return nil, nil
}

// readReportContent reads the full content of a report file.
func readReportContent(cwd string, entry ReportEntry) (*RecallResult, error) {
	fpath := filepath.Join(cwd, ".agent", "reports", entry.Filename)
	content, err := os.ReadFile(fpath)
	if err != nil {
		return nil, fmt.Errorf("read report %s: %w", entry.Filename, err)
	}

	return &RecallResult{
		Filename: entry.Filename,
		Topic:    entry.Topic,
		Date:     entry.Date,
		Content:  string(content),
	}, nil
}

// matchesContent checks if a query appears in the first N lines of a file.
func matchesContent(fpath, query string) bool {
	f, err := os.Open(fpath)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineCount := 0
	for scanner.Scan() && lineCount < contentSearchLines {
		if strings.Contains(strings.ToLower(scanner.Text()), query) {
			return true
		}
		lineCount++
	}
	return false
}

// CleanupExpiredReports removes investigate reports older than maxAgeDays.
// Returns the count of deleted files.
func CleanupExpiredReports(cwd string, maxAgeDays int) (int, error) {
	if maxAgeDays <= 0 {
		maxAgeDays = 180
	}

	dir := filepath.Join(cwd, ".agent", "reports")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read reports dir: %w", err)
	}

	cutoff := time.Now().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
	deleted := 0

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "investigate-") || !strings.HasSuffix(name, ".md") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			fpath := filepath.Join(dir, name)
			if err := os.Remove(fpath); err == nil {
				deleted++
			}
		}
	}

	return deleted, nil
}
