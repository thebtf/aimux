package code

// NewTestsPassCriterion creates the tests-pass success criterion.
func NewTestsPassCriterion(weight float64) Criterion {
	return Criterion{
		Name:        "TestsPass",
		Description: "affected tests pass",
		Weight:      weight,
		Verify:      VerifyTestsPass,
	}
}

// VerifyTestsPass returns the observed test check result.
func VerifyTestsPass(_ Diff, project Project) (bool, string) {
	return project.TestsPass.Passed, evidenceOrDefault(project.TestsPass.Evidence, "test check failed or did not run")
}
