package code

// NewSpecComplianceCriterion creates the optional spec-compliance criterion.
func NewSpecComplianceCriterion(weight float64) Criterion {
	return Criterion{
		Name:        "SpecCompliance",
		Description: "diff satisfies active spec acceptance criteria",
		Weight:      weight,
		Verify:      VerifySpecCompliance,
	}
}

// VerifySpecCompliance returns the observed spec-compliance result.
func VerifySpecCompliance(_ Diff, project Project) (bool, string) {
	if project.SpecCompliance == nil {
		return false, "spec compliance check absent"
	}
	return project.SpecCompliance.Passed, evidenceOrDefault(project.SpecCompliance.Evidence, "spec compliance failed or did not run")
}
