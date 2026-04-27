package patterns

import (
	"testing"
)


func TestMentalModel_CompletenessScalesByTextLength(t *testing.T) {
	p := NewMentalModelPattern()

	// Short problem → low completenessScore
	shortInput := map[string]any{
		"modelName": "first_principles",
		"problem":   "short",
	}
	validatedShort, _ := p.Validate(shortInput)
	resultShort, _ := p.Handle(validatedShort, "s1")
	shortScore, ok := resultShort.Data["completenessScore"].(float64)
	if !ok {
		t.Fatalf("completenessScore missing or wrong type: %T", resultShort.Data["completenessScore"])
	}

	// Long problem + reasoning + conclusion → higher completenessScore
	longProblem := make([]byte, 600)
	for i := range longProblem {
		longProblem[i] = 'x'
	}
	longInput := map[string]any{
		"modelName":  "inversion",
		"problem":    string(longProblem),
		"reasoning":  "detailed reasoning about the problem covering all angles",
		"conclusion": "a well-reasoned conclusion",
	}
	validatedLong, _ := p.Validate(longInput)
	resultLong, _ := p.Handle(validatedLong, "s1")
	longScore := resultLong.Data["completenessScore"].(float64)

	if longScore <= shortScore {
		t.Errorf("longScore (%.4f) should be greater than shortScore (%.4f)", longScore, shortScore)
	}

	// completenessScore must be capped at 1.0
	if longScore > 1.0 {
		t.Errorf("completenessScore = %.4f, must not exceed 1.0", longScore)
	}
}

func TestMentalModel_ClarityScalesByStepCount(t *testing.T) {
	p := NewMentalModelPattern()

	// No steps → clarityScore = 0
	inputNoSteps := map[string]any{
		"modelName": "occams_razor",
		"problem":   "choose the simplest solution",
	}
	validatedNoSteps, _ := p.Validate(inputNoSteps)
	resultNoSteps, _ := p.Handle(validatedNoSteps, "s1")
	clarityNoSteps, ok := resultNoSteps.Data["clarityScore"].(float64)
	if !ok {
		t.Fatalf("clarityScore missing or wrong type: %T", resultNoSteps.Data["clarityScore"])
	}
	if clarityNoSteps != 0.0 {
		t.Errorf("clarityScore with no steps = %.4f, want 0.0", clarityNoSteps)
	}

	// 5 steps → clarityScore = 1.0 (formula: min(stepCount/5.0, 1.0))
	inputFiveSteps := map[string]any{
		"modelName": "occams_razor",
		"problem":   "choose the simplest solution",
		"steps":     []any{"step1", "step2", "step3", "step4", "step5"},
	}
	validatedFive, _ := p.Validate(inputFiveSteps)
	resultFive, _ := p.Handle(validatedFive, "s1")
	clarityFive := resultFive.Data["clarityScore"].(float64)

	const wantFive = 1.0
	const epsilon = 0.0001
	if clarityFive < wantFive-epsilon || clarityFive > wantFive+epsilon {
		t.Errorf("clarityScore with 5 steps = %.4f, want %.4f", clarityFive, wantFive)
	}

	// 10+ steps → clarityScore capped at 1.0
	steps := make([]any, 12)
	for i := range steps {
		steps[i] = "step"
	}
	inputMany := map[string]any{
		"modelName": "systems_thinking",
		"problem":   "analyze dependencies",
		"steps":     steps,
	}
	validatedMany, _ := p.Validate(inputMany)
	resultMany, _ := p.Handle(validatedMany, "s1")
	clarityMany := resultMany.Data["clarityScore"].(float64)
	if clarityMany > 1.0 {
		t.Errorf("clarityScore = %.4f, must not exceed 1.0", clarityMany)
	}
	if clarityMany != 1.0 {
		t.Errorf("clarityScore with 12 steps = %.4f, want 1.0", clarityMany)
	}
}

func TestMentalModel_ComplexityClassification(t *testing.T) {
	p := NewMentalModelPattern()

	// Low complexity: short problem, no steps
	lowInput := map[string]any{
		"modelName": "hanlons_razor",
		"problem":   "small",
	}
	validatedLow, _ := p.Validate(lowInput)
	resultLow, _ := p.Handle(validatedLow, "s1")
	if resultLow.Data["complexity"] != "low" {
		t.Errorf("expected complexity=low, got %v", resultLow.Data["complexity"])
	}

	// Medium complexity: stepCount + textComplexity > 5 but <= 10
	// 4 steps + ~200 chars problem = 4 + 2 = 6 → medium
	mediumProblem := make([]byte, 200)
	for i := range mediumProblem {
		mediumProblem[i] = 'a'
	}
	mediumInput := map[string]any{
		"modelName": "leverage_points",
		"problem":   string(mediumProblem),
		"steps":     []any{"s1", "s2", "s3", "s4"},
	}
	validatedMedium, _ := p.Validate(mediumInput)
	resultMedium, _ := p.Handle(validatedMedium, "s1")
	if resultMedium.Data["complexity"] != "medium" {
		t.Errorf("expected complexity=medium, got %v", resultMedium.Data["complexity"])
	}

	// High complexity: 8 steps + 500 chars = 8 + 5 = 13 > 10 → high
	highProblem := make([]byte, 500)
	for i := range highProblem {
		highProblem[i] = 'b'
	}
	highSteps := make([]any, 8)
	for i := range highSteps {
		highSteps[i] = "step"
	}
	highInput := map[string]any{
		"modelName": "systems_thinking",
		"problem":   string(highProblem),
		"steps":     highSteps,
	}
	validatedHigh, _ := p.Validate(highInput)
	resultHigh, _ := p.Handle(validatedHigh, "s1")
	if resultHigh.Data["complexity"] != "high" {
		t.Errorf("expected complexity=high, got %v", resultHigh.Data["complexity"])
	}
}

func TestMentalModel_FirstPrinciples_ConcreteTech_R5_1(t *testing.T) {
	p := NewMentalModelPattern()
	in := map[string]any{
		"modelName": "first_principles",
		"problem":   "Hot-swap upgrade на Windows: что фундаментально нужно для атомарного binary replacement через MoveFileEx и handle release?",
	}
	valid, err := p.Validate(in)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	res, err := p.Handle(valid, "")
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	fit, _ := res.Data["modelFit"].(string)
	if fit != "match" {
		t.Errorf("R5-1: concrete-tech first_principles → modelFit=%q, want %q", fit, "match")
	}
}

func TestMentalModel_FirstPrinciples_AbstractStillFits_R5_1_regression(t *testing.T) {
	p := NewMentalModelPattern()
	// "fundamental" and "assumptions" are both trigger words for first_principles → alignmentScore >= 0.3 → "strong".
	in := map[string]any{"modelName": "first_principles", "problem": "What are the fundamental assumptions about fairness?"}
	valid, err := p.Validate(in)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	res, _ := p.Handle(valid, "")
	fit, _ := res.Data["modelFit"].(string)
	if fit != "strong" {
		t.Errorf("regression: abstract first_principles with trigger words must still give 'strong', got %q", fit)
	}
}
