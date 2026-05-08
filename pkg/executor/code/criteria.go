package code

import "fmt"

const (
	DefaultBuildCleanWeight      = 0.30
	DefaultTestsPassWeight       = 0.30
	DefaultTypeCheckCleanWeight  = 0.15
	DefaultNoSecurityFindsWeight = 0.15
	DefaultSpecComplianceWeight  = 0.10
)

// Diff is the navigator-visible representation of the proposed code change.
type Diff struct {
	Summary string
	Files   []string
	Patch   string
}

// Project carries verification observations for a code task.
type Project struct {
	CWD              string
	BuildClean       CheckResult
	TestsPass        CheckResult
	TypeCheckClean   CheckResult
	SecurityFindings []SecurityFinding
	SpecCompliance   *CheckResult
}

// CheckResult is the observed outcome of a project verification check.
type CheckResult struct {
	Passed   bool
	Evidence string
}

// SecurityFinding is the minimum shape needed by the no-security-finds criterion.
type SecurityFinding struct {
	Severity string
	Message  string
}

// Verifier evaluates one criterion against a diff and project context.
type Verifier func(diff Diff, project Project) (bool, string)

// Criterion is a weighted binary success check.
type Criterion struct {
	Name        string
	Description string
	Weight      float64
	Verify      Verifier
}

// SuccessCriteria is the Strong-Style navigator's scoring rubric.
type SuccessCriteria struct {
	BuildClean      Criterion
	TestsPass       Criterion
	TypeCheckClean  Criterion
	NoSecurityFinds Criterion
	SpecCompliance  *Criterion
}

// CriterionResult is one verifier outcome after weight normalization.
type CriterionResult struct {
	Name     string
	Pass     bool
	Evidence string
	Weight   float64
}

// Evaluation is the aggregate scoring result for a diff.
type Evaluation struct {
	Confidence float64
	Results    []CriterionResult
}

// DefaultSuccessCriteria returns the ADR-004 default rubric.
func DefaultSuccessCriteria(includeSpecCompliance bool) SuccessCriteria {
	criteria := SuccessCriteria{
		BuildClean:      NewBuildCleanCriterion(DefaultBuildCleanWeight),
		TestsPass:       NewTestsPassCriterion(DefaultTestsPassWeight),
		TypeCheckClean:  NewTypeCheckCleanCriterion(DefaultTypeCheckCleanWeight),
		NoSecurityFinds: NewNoSecurityFindsCriterion(DefaultNoSecurityFindsWeight),
	}
	if includeSpecCompliance {
		criterion := NewSpecComplianceCriterion(DefaultSpecComplianceWeight)
		criteria.SpecCompliance = &criterion
	}
	return criteria.NormalizeWeights()
}

// NormalizeWeights returns a copy whose active criterion weights sum to 1.0.
func (s SuccessCriteria) NormalizeWeights() SuccessCriteria {
	active := s.Criteria()
	total := 0.0
	for _, criterion := range active {
		if criterion.Weight > 0 {
			total += criterion.Weight
		}
	}
	if total == 0 {
		return s
	}

	normalized := s
	normalized.BuildClean.Weight = normalizedWeight(normalized.BuildClean.Weight, total)
	normalized.TestsPass.Weight = normalizedWeight(normalized.TestsPass.Weight, total)
	normalized.TypeCheckClean.Weight = normalizedWeight(normalized.TypeCheckClean.Weight, total)
	normalized.NoSecurityFinds.Weight = normalizedWeight(normalized.NoSecurityFinds.Weight, total)
	if normalized.SpecCompliance != nil {
		spec := *normalized.SpecCompliance
		spec.Weight = normalizedWeight(spec.Weight, total)
		normalized.SpecCompliance = &spec
	}
	return normalized
}

// Criteria returns the active criteria in deterministic scoring order.
func (s SuccessCriteria) Criteria() []Criterion {
	criteria := []Criterion{
		s.BuildClean,
		s.TestsPass,
		s.TypeCheckClean,
		s.NoSecurityFinds,
	}
	if s.SpecCompliance != nil {
		criteria = append(criteria, *s.SpecCompliance)
	}
	return criteria
}

// Evaluate runs all active verifiers and computes aggregate confidence.
func (s SuccessCriteria) Evaluate(diff Diff, project Project) Evaluation {
	normalized := s.NormalizeWeights()
	results := make([]CriterionResult, 0, len(normalized.Criteria()))
	confidence := 0.0

	for _, criterion := range normalized.Criteria() {
		pass, evidence := verifyCriterion(criterion, diff, project)
		if pass {
			confidence += criterion.Weight
		}
		results = append(results, CriterionResult{
			Name:     criterion.Name,
			Pass:     pass,
			Evidence: evidence,
			Weight:   criterion.Weight,
		})
	}

	return Evaluation{
		Confidence: confidence,
		Results:    results,
	}
}

func verifyCriterion(criterion Criterion, diff Diff, project Project) (bool, string) {
	if criterion.Verify == nil {
		return false, fmt.Sprintf("%s verifier is not configured", criterion.Name)
	}
	pass, evidence := criterion.Verify(diff, project)
	if evidence == "" {
		evidence = fmt.Sprintf("%s returned pass=%t without evidence", criterion.Name, pass)
	}
	return pass, evidence
}

func normalizedWeight(weight, total float64) float64 {
	if weight <= 0 {
		return 0
	}
	return weight / total
}

func evidenceOrDefault(evidence, fallback string) string {
	if evidence != "" {
		return evidence
	}
	return fallback
}
