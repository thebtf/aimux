package policies

import (
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/guidance"
	inv "github.com/thebtf/aimux/pkg/investigate"
)

type InvestigatePolicy struct{}

func NewInvestigatePolicy() *InvestigatePolicy {
	return &InvestigatePolicy{}
}

func (p *InvestigatePolicy) ToolName() string {
	return "investigate"
}

func (p *InvestigatePolicy) BuildPlan(input guidance.PolicyInput) (guidance.NextActionPlan, error) {
	state, ok := input.StateSnapshot.(*inv.InvestigationState)
	if !ok || state == nil {
		return guidance.NextActionPlan{}, fmt.Errorf("investigate policy requires *investigate.InvestigationState")
	}

	action := strings.TrimSpace(input.Action)
	coverageGaps := uncheckedCoverageAreas(state)
	convergence := inv.ComputeConvergence(state)
	coverage := inv.ComputeCoverage(state)
	reportGaps := reportEvidenceGaps(action, state, coverageGaps, convergence, coverage)
	forceReport := reportForceRequested(input.RawResult)
	delegateStatus := investigateDelegateStatus(action, input.RawResult)
	delegateCLI := investigateDelegateCLI(input.RawResult)

	plan := guidance.NextActionPlan{
		State:            investigatePlanState(action, state, reportGaps, forceReport, delegateStatus),
		YouAreHere:       investigateYouAreHere(action, state, coverageGaps, reportGaps, convergence, coverage, forceReport, delegateStatus, delegateCLI),
		HowThisToolWorks: "This is a scratchpad for YOUR investigation. It does not research anything itself.",
		ChooseYourPath: map[string]guidance.PathBranch{
			guidance.BranchSelf: {
				When:     "Use this when you want to drive the investigation manually.",
				NextCall: nextInvestigateSelfCall(action, state, coverageGaps, reportGaps, delegateStatus),
				Example:  nextInvestigateSelfExample(action, state, coverageGaps, reportGaps, delegateStatus),
				Then:     nextInvestigateSelfThen(action, state, coverageGaps, reportGaps, forceReport, delegateStatus),
			},
		},
		Gaps:           investigatePlanGaps(action, coverageGaps, reportGaps),
		StopConditions: "convergence >= 1.0 AND coverage >= 80%",
		DoNot: []string{
			"Do not assume this tool researches in the background.",
			"Do not treat start as completion — you must add findings or delegate the investigation.",
		},
	}

	if action != "start" {
		plan.HowThisToolWorks = ""
	}

	return plan, nil
}

func uncheckedCoverageAreas(state *inv.InvestigationState) []string {
	if state == nil {
		return nil
	}

	gaps := make([]string, 0)
	for _, area := range state.CoverageAreas {
		if !state.CoverageChecked[area] {
			gaps = append(gaps, area)
		}
	}
	return gaps
}

func investigatePlanState(action string, state *inv.InvestigationState, reportGaps []string, forceReport bool, delegateStatus string) string {
	if action == "auto" {
		switch delegateStatus {
		case "running":
			return "delegate_running"
		case "completed":
			return "delegate_completed"
		case "failed":
			return "delegate_failed"
		case "cancelled":
			return "delegate_cancelled"
		default:
			return "delegate_ready"
		}
	}
	if action != "report" {
		return "notebook_ready"
	}
	if len(reportGaps) == 0 {
		return "report_ready"
	}
	if forceReport {
		return "report_incomplete_forced"
	}
	if len(state.Findings) == 0 {
		return "report_blocked"
	}
	return "report_preliminary"
}

func investigatePlanGaps(action string, coverageGaps []string, reportGaps []string) []string {
	if action == "report" {
		return reportGaps
	}
	return coverageGaps
}

func investigateYouAreHere(action string, state *inv.InvestigationState, coverageGaps []string, reportGaps []string, convergence float64, coverage float64, forceReport bool, delegateStatus, delegateCLI string) string {
	if action == "auto" {
		cliNote := "delegate"
		if delegateCLI != "" {
			cliNote = delegateCLI
		}
		switch delegateStatus {
		case "running":
			return fmt.Sprintf("Delegate job is running via %s. %d findings currently recorded.", cliNote, len(state.Findings))
		case "completed":
			return fmt.Sprintf("Delegate job completed via %s. %d findings are ready for report or assessment.", cliNote, len(state.Findings))
		case "failed":
			return fmt.Sprintf("Delegate job failed via %s. Resume from the existing session or redelegate with the same topic.", cliNote)
		case "cancelled":
			return fmt.Sprintf("Delegate job was cancelled via %s. You can resume from this session or redelegate.", cliNote)
		default:
			return fmt.Sprintf("Delegate path is prepared for %s. Start or resume the delegated investigation when ready.", cliNote)
		}
	}
	if action != "report" {
		return fmt.Sprintf("Iteration %d. %d findings. %d/%d areas covered.", state.Iteration, len(state.Findings), len(state.CoverageAreas)-len(coverageGaps), len(state.CoverageAreas))
	}

	prefix := "Report gate"
	if forceReport {
		prefix = "Report gate (force=true)"
	}

	if len(reportGaps) == 0 {
		return fmt.Sprintf("%s. Convergence %.2f, coverage %.0f%%. Evidence is strong for a final report.", prefix, convergence, coverage*100)
	}
	if len(state.Findings) == 0 {
		return fmt.Sprintf("%s. Convergence %.2f, coverage %.0f%%. No findings were recorded, so the report is incomplete.", prefix, convergence, coverage*100)
	}
	if forceReport {
		return fmt.Sprintf("%s. Convergence %.2f, coverage %.0f%%. Report was generated but is still incomplete (%d gap(s)).", prefix, convergence, coverage*100, len(reportGaps))
	}
	return fmt.Sprintf("%s. Convergence %.2f, coverage %.0f%%. Treat this as PRELIMINARY until %d gap(s) are closed.", prefix, convergence, coverage*100, len(reportGaps))
}

func reportEvidenceGaps(action string, state *inv.InvestigationState, coverageGaps []string, convergence float64, coverage float64) []string {
	if action != "report" {
		return nil
	}

	gaps := make([]string, 0, len(coverageGaps)+3)
	if len(state.Findings) == 0 {
		gaps = append(gaps, "no findings recorded")
	}
	if convergence < 1.0 {
		gaps = append(gaps, "convergence < 1.0")
	}
	if coverage < 0.8 {
		gaps = append(gaps, "coverage < 80%")
	}
	for _, gap := range coverageGaps {
		gaps = appendUnique(gaps, gap)
	}
	return gaps
}

func nextInvestigateSelfCall(action string, state *inv.InvestigationState, coverageGaps []string, reportGaps []string, delegateStatus string) string {
	if action == "auto" {
		switch delegateStatus {
		case "running":
			return `status(job_id="<job_id>")`
		case "completed":
			return `investigate(action="report", session_id="<session_id>")`
		case "failed", "cancelled":
			return `investigate(action="auto", topic="<topic>", session_id="<session_id>")`
		default:
			return `investigate(action="auto", topic="<topic>")`
		}
	}
	if action == "report" {
		if len(state.Findings) == 0 {
			return `investigate(action="finding", session_id="<session_id>", description="...", source="...", severity="P2")`
		}
		if len(reportGaps) > 0 {
			return `investigate(action="assess", session_id="<session_id>")`
		}
		return `investigate(action="list", cwd="<cwd>")`
	}
	if len(coverageGaps) == 0 {
		return `investigate(action="assess", session_id="<session_id>")`
	}
	return `investigate(action="finding", session_id="<session_id>", description="...", source="...", severity="P2")`
}

func nextInvestigateSelfExample(action string, state *inv.InvestigationState, coverageGaps []string, reportGaps []string, delegateStatus string) string {
	nextCall := nextInvestigateSelfCall(action, state, coverageGaps, reportGaps, delegateStatus)
	if action == "auto" || action == "report" {
		return nextCall
	}

	if len(coverageGaps) == 0 {
		return `investigate(action="assess", session_id="<session_id>")`
	}
	return `investigate(action="finding", session_id="<session_id>", description="Observed nil dereference in init()", source="main.go:42", severity="P0")`
}

func nextInvestigateSelfThen(action string, state *inv.InvestigationState, coverageGaps []string, reportGaps []string, forceReport bool, delegateStatus string) string {
	if action == "auto" {
		switch delegateStatus {
		case "running":
			return "Keep polling the job status. If the delegate stalls or the work is no longer needed, cancel it instead of adding manual findings."
		case "completed":
			return "Delegate work is finished. Review the session state, then assess or report instead of restarting the investigation manually."
		case "failed":
			return "The delegate failed. Resume from the existing session or redelegate with the same topic instead of starting a brand-new manual thread."
		case "cancelled":
			return "The delegate was cancelled. Resume this session or redelegate when you're ready to continue."
		default:
			return "Delegate the investigation, then monitor status rather than switching to manual finding entry immediately."
		}
	}
	if action == "report" {
		if len(state.Findings) == 0 {
			return "Add at least one finding before trying to generate a report."
		}
		if len(reportGaps) > 0 {
			if forceReport {
				return "Report was generated with force=true but it is still incomplete. Close the listed gaps, reassess, then regenerate a final report."
			}
			return "Treat this as PRELIMINARY guidance. Close the listed gaps, reassess, then regenerate a final report."
		}
		return "Evidence thresholds are met; you can treat this report as final."
	}
	if len(coverageGaps) == 0 {
		return "Assess convergence and, if it is strong enough, move to report."
	}
	return "Add more findings, then call assess to check convergence and coverage."
}

func investigateDelegateStatus(action string, raw any) string {
	if action != "auto" || raw == nil {
		return ""
	}
	payload, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	status, _ := payload["status"].(string)
	return strings.TrimSpace(status)
}

func investigateDelegateCLI(raw any) string {
	if raw == nil {
		return ""
	}
	payload, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	cli, _ := payload["cli"].(string)
	return strings.TrimSpace(cli)
}

func reportForceRequested(raw any) bool {
	if raw == nil {
		return false
	}
	payload, ok := raw.(map[string]any)
	if !ok {
		return false
	}

	if force, ok := asBool(payload["force"]); ok {
		return force
	}
	if metadata, ok := payload["metadata"].(map[string]any); ok {
		if force, ok := asBool(metadata["force"]); ok {
			return force
		}
	}
	return false
}

func asBool(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		normalized := strings.ToLower(strings.TrimSpace(v))
		switch normalized {
		case "true", "1", "yes", "y":
			return true, true
		case "false", "0", "no", "n":
			return false, true
		}
	case int:
		if v == 1 {
			return true, true
		}
		if v == 0 {
			return false, true
		}
	case int64:
		if v == 1 {
			return true, true
		}
		if v == 0 {
			return false, true
		}
	case float64:
		if v == 1 {
			return true, true
		}
		if v == 0 {
			return false, true
		}
	}
	return false, false
}

func appendUnique(values []string, candidate string) []string {
	for _, existing := range values {
		if existing == candidate {
			return values
		}
	}
	return append(values, candidate)
}
