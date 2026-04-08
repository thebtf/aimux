package patterns

import (
	"math"
	"testing"
)

func baseStochasticInput(algType string) map[string]any {
	return map[string]any{
		"algorithmType":     algType,
		"problemDefinition": "test problem",
	}
}

func withOutcomes(input map[string]any, outcomes []map[string]any) map[string]any {
	raw := make([]any, len(outcomes))
	for i, o := range outcomes {
		raw[i] = map[string]any{"probability": o["probability"], "value": o["value"]}
	}
	out := make(map[string]any, len(input)+1)
	for k, v := range input {
		out[k] = v
	}
	out["parameters"] = map[string]any{"outcomes": raw}
	return out
}

func runStochastic(t *testing.T, input map[string]any) map[string]any {
	t.Helper()
	p := NewStochasticAlgorithmPattern()
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	result, err := p.Handle(validated, "test-session")
	if err != nil {
		t.Fatalf("Handle() error: %v", err)
	}
	return result.Data
}

// TestStochastic_EV: [{p:0.5,v:100},{p:0.5,v:0}] → EV = 50
func TestStochastic_EV(t *testing.T) {
	input := withOutcomes(baseStochasticInput("bandit"), []map[string]any{
		{"probability": 0.5, "value": 100.0},
		{"probability": 0.5, "value": 0.0},
	})
	data := runStochastic(t, input)

	ev, ok := data["expectedValue"].(float64)
	if !ok {
		t.Fatalf("expectedValue not in output, got data: %v", data)
	}
	if math.Abs(ev-50.0) > 1e-9 {
		t.Errorf("expectedValue = %v, want 50", ev)
	}
}

// TestStochastic_Variance: same input → variance=2500, stddev=50
func TestStochastic_Variance(t *testing.T) {
	input := withOutcomes(baseStochasticInput("bandit"), []map[string]any{
		{"probability": 0.5, "value": 100.0},
		{"probability": 0.5, "value": 0.0},
	})
	data := runStochastic(t, input)

	variance, ok := data["variance"].(float64)
	if !ok {
		t.Fatalf("variance not in output, got data: %v", data)
	}
	if math.Abs(variance-2500.0) > 1e-9 {
		t.Errorf("variance = %v, want 2500", variance)
	}

	stddev, ok := data["standardDeviation"].(float64)
	if !ok {
		t.Fatalf("standardDeviation not in output")
	}
	if math.Abs(stddev-50.0) > 1e-9 {
		t.Errorf("standardDeviation = %v, want 50", stddev)
	}
}

// TestStochastic_DominantOutcome: [{p:0.7,v:10},{p:0.3,v:100}] → dominant is {p:0.3,v:100} (max p*v=30 vs 7)
func TestStochastic_DominantOutcome(t *testing.T) {
	input := withOutcomes(baseStochasticInput("bayesian"), []map[string]any{
		{"probability": 0.7, "value": 10.0},
		{"probability": 0.3, "value": 100.0},
	})
	data := runStochastic(t, input)

	dom, ok := data["dominantOutcome"].(map[string]any)
	if !ok {
		t.Fatalf("dominantOutcome not in output, got data: %v", data)
	}

	domP, _ := dom["probability"].(float64)
	domV, _ := dom["value"].(float64)

	if math.Abs(domP-0.3) > 1e-9 || math.Abs(domV-100.0) > 1e-9 {
		t.Errorf("dominantOutcome = {p:%v, v:%v}, want {p:0.3, v:100}", domP, domV)
	}
}

// TestStochastic_NoOutcomes: algorithmType only → no EV fields (backward compat)
func TestStochastic_NoOutcomes(t *testing.T) {
	input := baseStochasticInput("bandit")
	data := runStochastic(t, input)

	if _, ok := data["expectedValue"]; ok {
		t.Error("expectedValue should not be present when no outcomes provided")
	}
	if _, ok := data["variance"]; ok {
		t.Error("variance should not be present when no outcomes provided")
	}
	if _, ok := data["dominantOutcome"]; ok {
		t.Error("dominantOutcome should not be present when no outcomes provided")
	}

	// Core fields must still be present
	if _, ok := data["algorithmType"]; !ok {
		t.Error("algorithmType missing from output")
	}
	if _, ok := data["analysisPrompt"]; !ok {
		t.Error("analysisPrompt missing from output")
	}
}
