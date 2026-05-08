package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Aggregator folds multi-pass review output into the gate-facing summary.
type Aggregator struct{}

// AggregatedFindings is the review pipeline result after deduplication.
type AggregatedFindings struct {
	Findings        []Finding  `json:"findings"`
	Summary         string     `json:"summary"`
	PassesCompleted []PassName `json:"passes_completed"`
	Severity        Severity   `json:"severity,omitempty"`
	Blocking        bool       `json:"blocking"`
}

type findingKey struct {
	file     string
	line     string
	severity Severity
	bodyHash string
}

// Aggregate deduplicates findings and escalates the final severity.
func (Aggregator) Aggregate(results []PassResult) AggregatedFindings {
	seen := make(map[findingKey]struct{})
	findings := make([]Finding, 0)
	passesCompleted := make([]PassName, 0, len(results))
	summaries := make([]string, 0, len(results))
	severity := Severity("")

	for _, result := range results {
		if result.Name != "" {
			passesCompleted = append(passesCompleted, result.Name)
		}
		if summary := strings.TrimSpace(result.Summary); summary != "" {
			summaries = append(summaries, fmt.Sprintf("%s: %s", result.Name, summary))
		}

		for _, finding := range result.Findings {
			if isHigherSeverity(finding.Severity, severity) {
				severity = finding.Severity
			}

			key := newFindingKey(finding)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			findings = append(findings, finding)
		}
	}

	return AggregatedFindings{
		Findings:        findings,
		Summary:         aggregateSummary(summaries, len(findings)),
		PassesCompleted: passesCompleted,
		Severity:        severity,
		Blocking:        severity == SeverityError,
	}
}

func newFindingKey(finding Finding) findingKey {
	return findingKey{
		file:     finding.File,
		line:     findingLineKey(finding.Line),
		severity: finding.Severity,
		bodyHash: hashFindingBody(finding.Body),
	}
}

func findingLineKey(line *int) string {
	if line == nil {
		return ""
	}
	return fmt.Sprintf("%d", *line)
}

func hashFindingBody(body string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(body)))
	return hex.EncodeToString(sum[:])
}

func isHigherSeverity(candidate Severity, current Severity) bool {
	return severityRank(candidate) > severityRank(current)
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityError:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

func aggregateSummary(summaries []string, findingCount int) string {
	if len(summaries) > 0 {
		return strings.Join(summaries, " | ")
	}
	if findingCount == 0 {
		return "No review findings."
	}
	return fmt.Sprintf("%d review finding(s).", findingCount)
}
