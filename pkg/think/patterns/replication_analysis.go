package patterns

import (
	"fmt"
	"strings"

	think "github.com/thebtf/aimux/pkg/think"
)

type replicationAnalysisPattern struct{}

// NewReplicationAnalysisPattern returns the "replication_analysis" pattern handler.
func NewReplicationAnalysisPattern() think.PatternHandler { return &replicationAnalysisPattern{} }

func (p *replicationAnalysisPattern) Name() string { return "replication_analysis" }

func (p *replicationAnalysisPattern) Description() string {
	return "Plan replication of a claim, experiment, or benchmark — identify requirements and risks"
}

func (p *replicationAnalysisPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"claim":          {Type: "string", Required: true, Description: "The claim or experiment to replicate"},
		"originalMethod": {Type: "string", Required: false, Description: "Original method description"},
		"resources":      {Type: "array", Required: false, Description: "Available resources for replication", Items: map[string]any{"type": "string"}},
		"constraints":    {Type: "array", Required: false, Description: "Constraints on the replication", Items: map[string]any{"type": "string"}},
	}
}

func (p *replicationAnalysisPattern) Category() string { return "solo" }

func normalizeStringArrayField(field string, raw any) ([]any, error) {
	switch v := raw.(type) {
	case []any:
		for i, item := range v {
			if _, ok := item.(string); !ok {
				return nil, fmt.Errorf("field '%s[%d]' must be a string", field, i)
			}
		}
		return v, nil
	case []string:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out, nil
	default:
		return nil, fmt.Errorf("field '%s' must be an array of strings", field)
	}
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
	if rawResources, exists := input["resources"]; exists {
		resources, err := normalizeStringArrayField("resources", rawResources)
		if err != nil {
			return nil, err
		}
		out["resources"] = resources
	}
	if rawConstraints, exists := input["constraints"]; exists {
		constraints, err := normalizeStringArrayField("constraints", rawConstraints)
		if err != nil {
			return nil, err
		}
		out["constraints"] = constraints
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
	feasibility, effort := assessFeasibility(riskAny, resources)

	// resourceCoverageRatio: how well resources cover requirements.
	resourceCoverageRatio := 0.0
	if len(requirements) > 0 {
		resourceCoverageRatio = float64(len(resources)) / float64(len(requirements))
	}

	data := map[string]any{
		"claim":                  claim,
		"replicationFeasibility": feasibility,
		"requirements":           requirements,
		"risks":                  risks,
		"estimatedEffort":        effort,
		"criticalAssumptions":    criticalAssumptions,
		"resourceCoverageRatio":  resourceCoverageRatio,
		"guidance":               BuildGuidance("replication_analysis", replicationDepth(validInput), []string{"originalMethod", "resources", "constraints"}),
	}

	return think.MakeThinkResult("replication_analysis", data, sessionID, nil, "", nil), nil
}

// replicationDepth returns "full" only when all optional fields are present;
// otherwise returns "basic" so guidance is appropriately scoped.
func replicationDepth(validInput map[string]any) string {
	_, hasMethod := validInput["originalMethod"]
	_, hasResources := validInput["resources"]
	_, hasConstraints := validInput["constraints"]
	if hasMethod && hasResources && hasConstraints {
		return "full"
	}
	return "basic"
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

func assessFeasibility(risks []any, resources []any) (string, string) {
	// Weighted risk scoring: critical risks (data unavailability, methodology ambiguity) carry
	// more weight than minor risks (environment differences, constraints).
	criticalKeywords := []string{"unavailable", "nda", "proprietary", "cannot access", "ambiguity", "diverge"}
	var criticalCount, minorCount int
	for _, r := range risks {
		rs, ok := r.(string)
		if !ok || rs == "" {
			continue
		}
		lowered := strings.ToLower(rs)
		isCritical := false
		for _, kw := range criticalKeywords {
			if strings.Contains(lowered, kw) {
				isCritical = true
				break
			}
		}
		if isCritical {
			criticalCount++
		} else {
			minorCount++
		}
	}
	riskScore := float64(criticalCount)*0.4 + float64(minorCount)*0.1

	effort := "low"
	switch {
	case riskScore > 0.6:
		effort = "high"
	case riskScore > 0.3:
		effort = "medium"
	}

	switch {
	case len(resources) == 0 && riskScore > 0.3:
		return "infeasible", effort
	case riskScore > 0.6:
		return "infeasible", effort
	case riskScore > 0.3:
		return "partial", effort
	default:
		return "feasible", effort
	}
}
