package workflow

// SecurityAuditSteps returns the 6-phase security audit workflow definition (OWASP-aligned).
//
// The steps are:
//  1. attack_surface  — map entry points, auth boundaries, and data flows.
//  2. auth_analysis   — parallel dialogue to analyze authentication and authorization.
//  3. injection_test  — check injection vulnerabilities (SQL, XSS, command).
//  4. owasp_check     — OWASP Top 10 assessment via think pattern.
//  5. dependency_scan — dependency and secrets scan.
//  6. remediation     — generate remediation plan with severity ranking.
func SecurityAuditSteps() []WorkflowStep {
	return []WorkflowStep{
		{
			Name:   "attack_surface",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "secaudit",
				"prompt": "Map the attack surface of the following code: entry points, authentication boundaries, and data flows.\n\n%s",
			},
		},
		{
			Name:   "auth_analysis",
			Action: ActionDialogue,
			Config: map[string]any{
				"participants": []string{"codex", "claude"},
				"mode":         "parallel",
				"prompt":       "Analyze the authentication and authorization mechanisms in this code. Identify weaknesses and gaps:\n\n%s",
			},
		},
		{
			Name:   "injection_test",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "secaudit",
				"prompt": "Check for injection vulnerabilities (SQL injection, XSS, command injection, path traversal) in this code:\n\n%s",
			},
		},
		{
			Name:   "owasp_check",
			Action: ActionThinkPattern,
			Config: map[string]any{
				"pattern":   "architecture_analysis",
				"input_key": "components",
			},
		},
		{
			Name:   "dependency_scan",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "secaudit",
				"prompt": "Scan for insecure dependencies, hardcoded secrets, and sensitive data exposure in:\n\n%s",
			},
		},
		{
			Name:   "remediation",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "secaudit",
				"prompt": "Generate a remediation plan with severity ranking (CRITICAL/HIGH/MEDIUM/LOW) based on the security findings:\n\n%s",
			},
		},
	}
}
