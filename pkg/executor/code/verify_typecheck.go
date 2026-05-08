package code

// NewTypeCheckCleanCriterion creates the typecheck-clean success criterion.
func NewTypeCheckCleanCriterion(weight float64) Criterion {
	return Criterion{
		Name:        "TypeCheckClean",
		Description: "type checker reports zero errors",
		Weight:      weight,
		Verify:      VerifyTypeCheckClean,
	}
}

// VerifyTypeCheckClean returns the observed typecheck result.
func VerifyTypeCheckClean(_ Diff, project Project) (bool, string) {
	return project.TypeCheckClean.Passed, evidenceOrDefault(project.TypeCheckClean.Evidence, "typecheck failed or did not run")
}
