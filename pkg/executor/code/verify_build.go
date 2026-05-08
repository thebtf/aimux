package code

// NewBuildCleanCriterion creates the build-clean success criterion.
func NewBuildCleanCriterion(weight float64) Criterion {
	return Criterion{
		Name:        "BuildClean",
		Description: "project build exits successfully",
		Weight:      weight,
		Verify:      VerifyBuildClean,
	}
}

// VerifyBuildClean returns the observed build check result.
func VerifyBuildClean(_ Diff, project Project) (bool, string) {
	return project.BuildClean.Passed, evidenceOrDefault(project.BuildClean.Evidence, "build check failed or did not run")
}
