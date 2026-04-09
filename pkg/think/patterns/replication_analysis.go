package patterns

import (
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

type replicationAnalysisPattern struct{}

// NewReplicationAnalysisPattern returns the "replication_analysis" pattern handler.
func NewReplicationAnalysisPattern() think.PatternHandler { return &replicationAnalysisPattern{} }

func (p *replicationAnalysisPattern) Name() string { return "replication_analysis" }

func (p *replicationAnalysisPattern) Description() string {
	return "Plan replication of a claim, experiment, or benchmark — identify requirements and risks"
}

func (p *replicationAnalysisPattern) Validate(input map[string]any) (map[string]any, error) {
	claimRaw, ok := input["claim"]
	if !ok {
		return nil, fmt.Errorf("missing required field: claim")
	}
	claim, ok := claimRaw.(string)
	if !ok || claim == "" {
		return nil, fmt.Errorf("field 'claim' must be a non-empty string")
	}

	out := map[string]any{"claim": claim}

	if v, ok := input["originalMethod"].(string); ok && v != "" {
		out["originalMethod"] = v
	}
	if v, ok := input["resources"].([]any); ok {
		out["resources"] = v
	}
	if v, ok := input["constraints"].([]any); ok {
		out["constraints"] = v
	}

	return out, nil
}

func (p *replicationAnalysisPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	claim := validInput["claim"].(string)
	originalMethod, _ := validInput["originalMethod"].(string)
	resources, _ := validInput["resources"].([]any)
	constraints, _ := validInput["constraints"].([]any)

	requirements := buildRequirements(claim, originalMethod)
	risks := buildRisks(originalMethod, constraints)
	criticalAssumptions := buildAssumptions(originalMethod)
	reqAny := make([]any, len(requirements))
	for i, r := range requirements {
		reqAny[i] = r
	}
	riskAny := make([]any, len(risks))
	for i, r := range risks {
		riskAny[i] = r
	}
	feasibility, effort := assessFeasibility(reqAny, riskAny, resources, constraints)

	data := map[string]any{
		"claim":                  claim,
		"replicationFeasibility": feasibility,
		"requirements":           requirements,
		"risks":                  risks,
		"estimatedEffort":        effort,
		"criticalAssumptions":    criticalAssumptions,
		"guidance":               BuildGuidance("replication_analysis", "full", []string{"originalMethod", "resources", "constraints"}),
	}

	// Tier 2A: text analysis
	primaryText := validInput["claim"].(string)
	if analysis := AnalyzeText(primaryText); analysis != nil {
		domain := MatchDomainTemplate(primaryText)
		if domain != nil {
			analysis.Gaps = DetectGaps(analysis.Entities, domain)
		}
		data["textAnalysis"] = analysis
	}

	return think.MakeThinkResult("replication_analysis", data, sessionID, nil, "", nil), nil
}

func buildRequirements(claim, method string) []string {
	reqs := []string{
		fmt.Sprintf("Access to original data or equivalent dataset for: '%s'", claim),
		"Documented evaluation protocol",
		"Baseline measurement environment",
	}
	if method != "" {
		reqs = append(reqs, fmt.Sprintf("Reproduce method: %s", method))
	} else {
		reqs = append(reqs, "Reconstruct methodology from available description")
	}
	return reqs
}

func buildRisks(method string, constraints []any) []string {
	risks := []string{
		"Original data may be unavailable or under NDA",
		"Undocumented hyperparameters or implementation details",
		"Hardware or environment differences affecting reproducibility",
	}
	if method == "" {
		risks = append(risks, "Methodology ambiguity — replication may diverge from original")
	}
	for _, c := range constraints {
		if cs, ok := c.(string); ok {
			risks = append(risks, fmt.Sprintf("Constraint limits scope: %s", cs))
		}
	}
	return risks
}

func buildAssumptions(method string) []string {
	assumptions := []string{
		"Claim is stated precisely enough to define success criteria",
		"Evaluation metric is identical to original",
	}
	if method != "" {
		assumptions = append(assumptions, fmt.Sprintf("Method '%s' is fully specified and deterministic", method))
	}
	return assumptions
}

func assessFeasibility(_ []any, risks, resources, constraints []any) (string, string) {
	blockers := 0
	for _, r := range risks {
		if rs, ok := r.(string); ok && len(rs) > 0 {
			blockers++
		}
	}

	hasResources := len(resources) > 0
	hasConstraints := len(constraints) > 0

	switch {
	case !hasResources && hasConstraints:
		return "infeasible", "high"
	case blockers > 4:
		return "partial", "high"
	case hasResources:
		return "feasible", "medium"
	default:
		return "partial", "medium"
	}
}
