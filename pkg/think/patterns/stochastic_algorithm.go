package patterns

import (
	"fmt"
	"math"

	think "github.com/thebtf/aimux/pkg/think"
)

var validAlgorithmTypes = map[string]string{
	"mdp":      "Markov Decision Process — sequential decision-making under uncertainty with states, actions, and rewards",
	"mcts":     "Monte Carlo Tree Search — best-first search using random sampling of the search space",
	"bandit":   "Multi-Armed Bandit — balancing exploration vs exploitation under uncertainty",
	"bayesian": "Bayesian Inference — updating beliefs based on evidence using Bayes' theorem",
	"hmm":      "Hidden Markov Model — modeling systems with hidden states and observable emissions",
}

type stochasticAlgorithmPattern struct{}

// NewStochasticAlgorithmPattern returns the "stochastic_algorithm" pattern handler.
func NewStochasticAlgorithmPattern() think.PatternHandler { return &stochasticAlgorithmPattern{} }

func (p *stochasticAlgorithmPattern) Name() string { return "stochastic_algorithm" }

func (p *stochasticAlgorithmPattern) Description() string {
	return "Analyze stochastic algorithms: MDP, MCTS, bandit, Bayesian, HMM"
}

func (p *stochasticAlgorithmPattern) Validate(input map[string]any) (map[string]any, error) {
	algType, ok := input["algorithmType"]
	if !ok {
		return nil, fmt.Errorf("missing required field: algorithmType")
	}
	at, ok := algType.(string)
	if !ok || at == "" {
		return nil, fmt.Errorf("field 'algorithmType' must be a non-empty string")
	}
	if _, valid := validAlgorithmTypes[at]; !valid {
		return nil, fmt.Errorf("algorithmType must be one of: mdp, mcts, bandit, bayesian, hmm")
	}

	problemDef, ok := input["problemDefinition"]
	if !ok {
		return nil, fmt.Errorf("missing required field: problemDefinition")
	}
	pd, ok := problemDef.(string)
	if !ok || pd == "" {
		return nil, fmt.Errorf("field 'problemDefinition' must be a non-empty string")
	}

	out := map[string]any{
		"algorithmType":     at,
		"problemDefinition": pd,
	}
	if v, ok := input["parameters"].(map[string]any); ok {
		out["parameters"] = v
	}
	if v, err := toFloat64(input["iterations"]); err == nil && v > 0 {
		out["iterations"] = v
	}
	if v, ok := input["result"].(string); ok {
		out["result"] = v
	}
	return out, nil
}

func (p *stochasticAlgorithmPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	algType := validInput["algorithmType"].(string)
	problemDef := validInput["problemDefinition"].(string)
	description := validAlgorithmTypes[algType]

	iterations := 0.0
	if v, ok := validInput["iterations"].(float64); ok {
		iterations = v
	}

	data := map[string]any{
		"algorithmType":     algType,
		"description":       description,
		"problemDefinition": problemDef,
		"iterations":        iterations,
		"analysisPrompt":    fmt.Sprintf("Apply %s (%s) to: %s", algType, description, problemDef),
	}

	// Bandit + Bayesian: compute EV/variance when outcomes are provided in parameters.
	if algType == "bandit" || algType == "bayesian" {
		if params, ok := validInput["parameters"].(map[string]any); ok {
			if ev := computeExpectedValue(params); ev != nil {
				data["expectedValue"] = ev.expectedValue
				data["variance"] = ev.variance
				data["standardDeviation"] = ev.standardDeviation
				data["dominantOutcome"] = ev.dominantOutcome
			} else {
				// outcomes field missing or empty — provide a template.
				data["suggestedOutcomes"] = []map[string]any{
					{"name": "outcome_a", "probability": 0.5, "value": 10.0},
					{"name": "outcome_b", "probability": 0.3, "value": 25.0},
					{"name": "outcome_c", "probability": 0.2, "value": 5.0},
				}
			}
		} else {
			// No parameters at all — provide template.
			data["suggestedOutcomes"] = []map[string]any{
				{"name": "outcome_a", "probability": 0.5, "value": 10.0},
				{"name": "outcome_b", "probability": 0.3, "value": 25.0},
				{"name": "outcome_c", "probability": 0.2, "value": 5.0},
			}
		}
	}

	data["guidance"] = BuildGuidance("stochastic_algorithm", "basic", []string{"parameters.outcomes", "iterations", "result"})

	return think.MakeThinkResult("stochastic_algorithm", data, sessionID, nil, "", nil), nil
}

// outcome is a single probabilistic result with a weight and payoff.
type outcome struct {
	probability float64
	value       float64
}

type expectedValueResult struct {
	expectedValue    float64
	variance         float64
	standardDeviation float64
	dominantOutcome  map[string]any
}

// computeExpectedValue parses parameters.outcomes and computes EV, variance, stddev, and dominant outcome.
// Returns nil if outcomes are absent or malformed.
func computeExpectedValue(parameters map[string]any) *expectedValueResult {
	raw, ok := parameters["outcomes"]
	if !ok {
		return nil
	}
	slice, ok := raw.([]any)
	if !ok || len(slice) == 0 {
		return nil
	}

	outcomes := make([]outcome, 0, len(slice))
	for _, item := range slice {
		m, ok := item.(map[string]any)
		if !ok {
			return nil
		}
		p, err := toFloat64(m["probability"])
		if err != nil {
			return nil
		}
		v, err := toFloat64(m["value"])
		if err != nil {
			return nil
		}
		outcomes = append(outcomes, outcome{probability: p, value: v})
	}

	// EV = Σ p·v
	ev := 0.0
	for _, o := range outcomes {
		ev += o.probability * o.value
	}

	// Variance = Σ p·(v - EV)²
	variance := 0.0
	for _, o := range outcomes {
		diff := o.value - ev
		variance += o.probability * diff * diff
	}

	// Dominant outcome = argmax(p·v)
	dominant := outcomes[0]
	for _, o := range outcomes[1:] {
		if o.probability*o.value > dominant.probability*dominant.value {
			dominant = o
		}
	}

	return &expectedValueResult{
		expectedValue:    ev,
		variance:         variance,
		standardDeviation: math.Sqrt(variance),
		dominantOutcome: map[string]any{
			"probability": dominant.probability,
			"value":       dominant.value,
		},
	}
}
