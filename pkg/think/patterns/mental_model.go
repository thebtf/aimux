package patterns

import (
	"fmt"
	"math"

	think "github.com/thebtf/aimux/pkg/think"
)

// knownModels maps model names to their descriptions.
var knownModels = map[string]string{
	"first_principles":       "Break down complex problems into fundamental truths and build up from there",
	"inversion":              "Think backwards — instead of asking how to achieve X, ask what would prevent X",
	"second_order_thinking":  "Consider not just the immediate consequences, but the consequences of consequences",
	"occams_razor":           "Among competing hypotheses, prefer the one with fewest assumptions",
	"pareto_principle":       "Roughly 80% of effects come from 20% of causes",
	"circle_of_competence":   "Know and operate within the areas where you have genuine expertise",
	"opportunity_cost":       "Every choice has a cost — the value of the next best alternative foregone",
	"systems_thinking":       "View the world as interconnected systems rather than isolated events",
	"hanlons_razor":          "Never attribute to malice that which is adequately explained by carelessness",
	"map_is_not_territory":   "The description of reality is not reality itself — models have limitations",
	"jobs_to_be_done":        "Focus on what job the customer is trying to accomplish, not features",
	"via_negativa":           "Improve by removing the harmful rather than adding the good",
	"leverage_points":        "Identify the places within a system where a small shift produces large changes",
	"probabilistic_thinking": "Think in probabilities and distributions, not binary outcomes",
	"margin_of_safety":       "Build buffers between your estimates and the failure threshold",
}

type mentalModelPattern struct{}

// NewMentalModelPattern returns the "mental_model" pattern handler.
func NewMentalModelPattern() think.PatternHandler { return &mentalModelPattern{} }

func (p *mentalModelPattern) Name() string { return "mental_model" }

func (p *mentalModelPattern) Description() string {
	return "Apply one of 15 mental models to analyze a problem"
}

func (p *mentalModelPattern) Validate(input map[string]any) (map[string]any, error) {
	modelName, ok := input["modelName"]
	if !ok {
		return nil, fmt.Errorf("missing required field: modelName")
	}
	mn, ok := modelName.(string)
	if !ok || mn == "" {
		return nil, fmt.Errorf("field 'modelName' must be a non-empty string")
	}

	problem, ok := input["problem"]
	if !ok {
		return nil, fmt.Errorf("missing required field: problem")
	}
	ps, ok := problem.(string)
	if !ok || ps == "" {
		return nil, fmt.Errorf("field 'problem' must be a non-empty string")
	}

	out := map[string]any{"modelName": mn, "problem": ps}
	if v, ok := input["steps"].([]any); ok {
		out["steps"] = v
	}
	if v, ok := input["reasoning"].(string); ok {
		out["reasoning"] = v
	}
	if v, ok := input["conclusion"].(string); ok {
		out["conclusion"] = v
	}
	return out, nil
}

func (p *mentalModelPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	modelName := validInput["modelName"].(string)
	problem := validInput["problem"].(string)

	description := "custom model"
	known := false
	if desc, ok := knownModels[modelName]; ok {
		description = desc
		known = true
	}

	analysisPrompt := fmt.Sprintf("Apply the '%s' mental model (%s) to analyze: %s", modelName, description, problem)

	steps, _ := validInput["steps"].([]any)
	reasoning, _ := validInput["reasoning"].(string)
	conclusion, _ := validInput["conclusion"].(string)

	totalTextLength := len(problem) + len(reasoning) + len(conclusion)
	for _, s := range steps {
		if str, ok := s.(string); ok {
			totalTextLength += len(str)
		}
	}
	stepCount := len(steps)

	completenessScore := math.Min(float64(totalTextLength)/1000.0, 1.0)
	clarityScore := math.Min(float64(stepCount)/10.0, 1.0)
	coherenceScore := (completenessScore + clarityScore) / 2.0

	stepComplexity := float64(stepCount)
	textComplexity := float64(totalTextLength) / 100.0
	totalComplexity := stepComplexity + textComplexity

	complexity := "low"
	if totalComplexity > 10 {
		complexity = "high"
	} else if totalComplexity > 5 {
		complexity = "medium"
	}

	data := map[string]any{
		"modelName":         modelName,
		"problem":           problem,
		"known":             known,
		"description":       description,
		"analysisPrompt":    analysisPrompt,
		"stepCount":         stepCount,
		"completenessScore": completenessScore,
		"clarityScore":      clarityScore,
		"coherenceScore":    coherenceScore,
		"complexity":        complexity,
	}
	return think.MakeThinkResult("mental_model", data, sessionID, nil, "", []string{"known", "description", "completenessScore", "clarityScore", "coherenceScore", "complexity"}), nil
}
