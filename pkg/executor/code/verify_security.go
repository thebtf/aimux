package code

import "strings"

// NewNoSecurityFindsCriterion creates the security success criterion.
func NewNoSecurityFindsCriterion(weight float64) Criterion {
	return Criterion{
		Name:        "NoSecurityFinds",
		Description: "no S2+ security findings are present in the diff",
		Weight:      weight,
		Verify:      VerifyNoSecurityFinds,
	}
}

// VerifyNoSecurityFinds fails when any security finding is S2+ or equivalent.
func VerifyNoSecurityFinds(_ Diff, project Project) (bool, string) {
	for _, finding := range project.SecurityFindings {
		if isBlockingSecuritySeverity(finding.Severity) {
			return false, securityEvidence(finding)
		}
	}
	return true, "no S2+ security findings"
}

func isBlockingSecuritySeverity(severity string) bool {
	switch strings.ToUpper(strings.TrimSpace(severity)) {
	case "S2", "S3", "S4", "MEDIUM", "HIGH", "CRITICAL", "ERROR", "BLOCKER":
		return true
	default:
		return false
	}
}

func securityEvidence(finding SecurityFinding) string {
	message := strings.TrimSpace(finding.Message)
	if message == "" {
		message = "security finding present"
	}
	severity := strings.TrimSpace(finding.Severity)
	if severity == "" {
		return message
	}
	return severity + ": " + message
}
