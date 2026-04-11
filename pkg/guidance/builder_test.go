package guidance_test

import (
	"reflect"
	"testing"

	"github.com/thebtf/aimux/pkg/guidance"
)

func TestResponseBuilderBuild_PopulatesEnvelopeAndClonesGuidanceCollections(t *testing.T) {
	builder := guidance.NewResponseBuilder()

	plan := guidance.NextActionPlan{
		State:            "notebook_ready",
		YouAreHere:       "Iteration 0",
		HowThisToolWorks: "This tool is a guided scratchpad.",
		ChooseYourPath: map[string]guidance.PathBranch{
			guidance.BranchSelf: {
				When:     "you want manual control",
				NextCall: "investigate(action=\"finding\", ...)",
				Example:  "investigate(action=\"finding\", ...)",
				Then:     "add more evidence",
			},
		},
		Gaps:           []string{"assumptions", "alternatives"},
		StopConditions: "convergence >= 1.0 AND coverage >= 80%",
		DoNot:          []string{"Do not assume background execution."},
	}
	result := map[string]any{"session_id": "abc", "iteration": 0}

	envelope := builder.Build(plan, guidance.HandlerResult{Result: result})

	if envelope.State != plan.State {
		t.Fatalf("State = %q, want %q", envelope.State, plan.State)
	}
	if envelope.YouAreHere != plan.YouAreHere {
		t.Fatalf("YouAreHere = %q, want %q", envelope.YouAreHere, plan.YouAreHere)
	}
	if envelope.HowThisToolWorks != plan.HowThisToolWorks {
		t.Fatalf("HowThisToolWorks = %q, want %q", envelope.HowThisToolWorks, plan.HowThisToolWorks)
	}
	if !reflect.DeepEqual(envelope.ChooseYourPath, plan.ChooseYourPath) {
		t.Fatalf("ChooseYourPath mismatch: got %#v want %#v", envelope.ChooseYourPath, plan.ChooseYourPath)
	}
	if !reflect.DeepEqual(envelope.Gaps, plan.Gaps) {
		t.Fatalf("Gaps mismatch: got %#v want %#v", envelope.Gaps, plan.Gaps)
	}
	if envelope.StopConditions != plan.StopConditions {
		t.Fatalf("StopConditions = %q, want %q", envelope.StopConditions, plan.StopConditions)
	}
	if !reflect.DeepEqual(envelope.DoNot, plan.DoNot) {
		t.Fatalf("DoNot mismatch: got %#v want %#v", envelope.DoNot, plan.DoNot)
	}
	if !reflect.DeepEqual(envelope.Result, result) {
		t.Fatalf("Result mismatch: got %#v want %#v", envelope.Result, result)
	}

	plan.ChooseYourPath[guidance.BranchSelf] = guidance.PathBranch{When: "mutated"}
	plan.Gaps[0] = "mutated"
	plan.DoNot[0] = "mutated"

	if envelope.ChooseYourPath[guidance.BranchSelf].When == "mutated" {
		t.Fatal("envelope choose_your_path was mutated through input plan")
	}
	if envelope.Gaps[0] == "mutated" {
		t.Fatal("envelope gaps was mutated through input plan")
	}
	if envelope.DoNot[0] == "mutated" {
		t.Fatal("envelope do_not was mutated through input plan")
	}
}

func TestResponseBuilderBuild_AlwaysIncludesResultWithEmptyPlan(t *testing.T) {
	builder := guidance.NewResponseBuilder()
	result := map[string]any{"session_id": "xyz"}

	envelope := builder.Build(guidance.NextActionPlan{}, guidance.HandlerResult{Result: result})

	if !reflect.DeepEqual(envelope.Result, result) {
		t.Fatalf("Result = %#v, want %#v", envelope.Result, result)
	}
	if envelope.State != "" {
		t.Fatalf("State = %q, want empty", envelope.State)
	}
	if envelope.ChooseYourPath != nil {
		t.Fatalf("ChooseYourPath = %#v, want nil when plan empty", envelope.ChooseYourPath)
	}
	if envelope.Gaps != nil {
		t.Fatalf("Gaps = %#v, want nil when plan empty", envelope.Gaps)
	}
	if envelope.DoNot != nil {
		t.Fatalf("DoNot = %#v, want nil when plan empty", envelope.DoNot)
	}
}
