package code

import (
	"math"
	"testing"
)

func TestSuccessCriteriaKnownPassScoresOne(t *testing.T) {
	criteria := DefaultSuccessCriteria(true)
	eval := criteria.Evaluate(Diff{Summary: "known pass"}, passingProject(true))

	assertFloat(t, eval.Confidence, 1.0)
	if len(eval.Results) != 5 {
		t.Fatalf("result count = %d, want 5", len(eval.Results))
	}
	for _, result := range eval.Results {
		if !result.Pass {
			t.Fatalf("%s pass = false, want true: %s", result.Name, result.Evidence)
		}
		if result.Evidence == "" {
			t.Fatalf("%s evidence is empty", result.Name)
		}
	}
}

func TestSuccessCriteriaOneCriterionFailMatchesFormula(t *testing.T) {
	project := passingProject(true)
	project.TestsPass = CheckResult{Passed: false, Evidence: "unit tests failed"}

	eval := DefaultSuccessCriteria(true).Evaluate(Diff{Summary: "tests fail"}, project)

	assertFloat(t, eval.Confidence, 0.70)
	result := findResult(t, eval, "TestsPass")
	if result.Pass {
		t.Fatal("TestsPass result passed, want failure")
	}
	if result.Evidence != "unit tests failed" {
		t.Fatalf("TestsPass evidence = %q, want failure evidence", result.Evidence)
	}
}

func TestSuccessCriteriaRenormalizesWeightsWithoutSpecCompliance(t *testing.T) {
	criteria := DefaultSuccessCriteria(false)
	active := criteria.Criteria()
	if len(active) != 4 {
		t.Fatalf("active criteria = %d, want 4", len(active))
	}

	sum := 0.0
	for _, criterion := range active {
		sum += criterion.Weight
	}
	assertFloat(t, sum, 1.0)
	assertFloat(t, criteria.BuildClean.Weight, DefaultBuildCleanWeight/0.90)
	assertFloat(t, criteria.TypeCheckClean.Weight, DefaultTypeCheckCleanWeight/0.90)

	project := passingProject(false)
	project.TypeCheckClean = CheckResult{Passed: false, Evidence: "go vet failed"}
	eval := criteria.Evaluate(Diff{Summary: "typecheck fail"}, project)
	assertFloat(t, eval.Confidence, 1.0-(DefaultTypeCheckCleanWeight/0.90))
}

func TestVerifierFailureInjectionAffectsConfidence(t *testing.T) {
	project := passingProject(true)
	project.BuildClean = CheckResult{Passed: false, Evidence: "go build failed"}

	eval := DefaultSuccessCriteria(true).Evaluate(Diff{Summary: "build fail"}, project)

	assertFloat(t, eval.Confidence, 0.70)
	result := findResult(t, eval, "BuildClean")
	if result.Pass {
		t.Fatal("BuildClean result passed, want failure")
	}
	if result.Evidence != "go build failed" {
		t.Fatalf("BuildClean evidence = %q, want build failure", result.Evidence)
	}
}

func TestNoSecurityFindsFailsOnS2Plus(t *testing.T) {
	pass, evidence := VerifyNoSecurityFinds(Diff{}, Project{
		SecurityFindings: []SecurityFinding{
			{Severity: "S1", Message: "low risk note"},
			{Severity: "S2", Message: "unsafe path traversal"},
		},
	})

	if pass {
		t.Fatal("VerifyNoSecurityFinds passed, want failure")
	}
	if evidence != "S2: unsafe path traversal" {
		t.Fatalf("security evidence = %q, want S2 finding", evidence)
	}
}

func passingProject(includeSpec bool) Project {
	project := Project{
		CWD:            "/workspace",
		BuildClean:     CheckResult{Passed: true, Evidence: "build passed"},
		TestsPass:      CheckResult{Passed: true, Evidence: "tests passed"},
		TypeCheckClean: CheckResult{Passed: true, Evidence: "typecheck passed"},
	}
	if includeSpec {
		project.SpecCompliance = &CheckResult{Passed: true, Evidence: "spec matched"}
	}
	return project
}

func findResult(t *testing.T, eval Evaluation, name string) CriterionResult {
	t.Helper()
	for _, result := range eval.Results {
		if result.Name == name {
			return result
		}
	}
	t.Fatalf("missing result %q in %#v", name, eval.Results)
	return CriterionResult{}
}

func assertFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.000001 {
		t.Fatalf("float = %.12f, want %.12f", got, want)
	}
}
