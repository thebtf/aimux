package patterns

import (
	"fmt"

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
	return think.MakeThinkResult("stochastic_algorithm", data, sessionID, nil, "", nil), nil
}
