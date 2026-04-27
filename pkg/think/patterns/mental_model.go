package patterns

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	think "github.com/thebtf/aimux/pkg/think"
)

// modelTriggerWords maps model names to keywords that indicate a strong fit.
var modelTriggerWords = map[string][]string{
	"first_principles":       {"assumption", "fundamental", "from scratch", "ground truth", "basic", "axiom"},
	"inversion":              {"avoid", "prevent", "failure", "wrong", "opposite", "reverse"},
	"second_order_thinking":  {"consequence", "effect", "ripple", "downstream", "indirect", "cascade"},
	"probabilistic_thinking": {"probability", "risk", "distribution", "chance", "likelihood", "odds"},
	"occams_razor":           {"simple", "simplest", "complexity", "unnecessary", "minimal"},
	"pareto_principle":       {"80", "20", "vital few", "trivial many", "majority", "minority"},
	"circle_of_competence":   {"expertise", "competence", "know", "familiar", "skill", "domain"},
	"opportunity_cost":       {"trade-off", "alternative", "cost", "foregone", "sacrifice", "instead"},
	"systems_thinking":       {"system", "feedback", "loop", "interconnect", "emergent", "network"},
	"hanlons_razor":          {"malice", "careless", "incompetence", "mistake", "error", "accident"},
	"map_is_not_territory":   {"model", "map", "reality", "abstraction", "representation", "limit"},
	"jobs_to_be_done":        {"job", "customer", "hire", "outcome", "goal", "progress"},
	"via_negativa":           {"remove", "subtract", "eliminate", "less", "reduce", "harmful"},
	"leverage_points":        {"leverage", "small change", "large effect", "pivot", "shift", "intervention"},
	"margin_of_safety":       {"buffer", "margin", "safety", "cushion", "threshold", "reserve"},
}

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

// concreteTechRegex matches concrete technical terms in problem statements (R5-1).
var concreteTechRegex = regexp.MustCompile(`(?i)\b(Windows|Linux|macOS|POSIX|kernel|syscall|HTTP|TCP|DNS|TLS|SQL|NTFS|ext4|API|OS|GPU|CPU|ABI|RPC|MCP|stdio|IPC|binary|exec|process|handle|mutex|lock|atom)\w*\b`)

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

func (p *mentalModelPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"modelName":  {Type: "string", Required: true, Description: "Name of the mental model to apply (e.g. first_principles, inversion, occams_razor)"},
		"problem":    {Type: "string", Required: true, Description: "The problem to analyze with the mental model"},
		"steps":      {Type: "array", Required: false, Description: "Application steps", Items: map[string]any{"type": "string"}},
		"reasoning":  {Type: "string", Required: false, Description: "Reasoning text"},
		"conclusion": {Type: "string", Required: false, Description: "Conclusion from applying the model"},
	}
}

func (p *mentalModelPattern) Category() string { return "solo" }

func (p *mentalModelPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	modelName := validInput["modelName"].(string)
	problem := validInput["problem"].(string)

	// Normalize modelName to match TS v1 behaviour: lowercase, spaces and dashes → underscore.
	normalizedName := strings.ToLower(modelName)
	normalizedName = strings.NewReplacer(" ", "_", "-", "_").Replace(normalizedName)

	description := "custom model"
	known := false
	if desc, ok := knownModels[normalizedName]; ok {
		description = desc
		known = true
	}

	analysisPrompt := fmt.Sprintf("Apply the '%s' mental model (%s) to analyze: %s", normalizedName, description, problem)

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
	clarityScore := math.Min(float64(stepCount)/5.0, 1.0)

	// Compute alignment between problem text and model's expected trigger words.
	lowerProblem := strings.ToLower(problem)
	alignmentScore := 0.0
	if triggers, ok := modelTriggerWords[normalizedName]; ok && len(triggers) > 0 {
		matched := 0
		for _, word := range triggers {
			if strings.Contains(lowerProblem, word) {
				matched++
			}
		}
		alignmentScore = float64(matched) / float64(len(triggers))
	}

	modelFit := "mismatch"
	if alignmentScore >= 0.3 {
		modelFit = "strong"
	} else if alignmentScore >= 0.1 {
		modelFit = "weak"
	}

	// R5-1: concrete-tech override — first_principles-family models match on concrete-tech inputs
	// even when abstract trigger words are absent.
	concreteTechModels := map[string]bool{
		"first_principles": true,
		"inversion":        true,
		"occams_razor":     true,
	}
	if modelFit == "mismatch" && concreteTechModels[normalizedName] && concreteTechRegex.MatchString(problem) {
		modelFit = "match"
	}

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
		"alignmentScore":    alignmentScore,
		"modelFit":          modelFit,
	}

	if known {
		data["analysisSteps"] = mentalModelAnalysisSteps(normalizedName, problem)
	}

	data["guidance"] = BuildGuidance("mental_model", "basic", []string{"steps", "reasoning", "conclusion"})

	return think.MakeThinkResult("mental_model", data, sessionID, nil, "", []string{"known", "description", "completenessScore", "clarityScore", "coherenceScore", "complexity"}), nil
}

// mentalModelAnalysisSteps returns structured application steps for a known mental model.
func mentalModelAnalysisSteps(modelName, problem string) []string {
	templates := map[string][]string{
		"first_principles": {
			"1. Identify assumptions currently held about: " + problem,
			"2. Break the problem down to its most fundamental truths",
			"3. Rebuild your solution from those ground truths",
		},
		"inversion": {
			"1. State the goal of: " + problem,
			"2. Ask: what would guarantee failure or the opposite outcome?",
			"3. Remove or avoid those failure conditions",
		},
		"second_order_thinking": {
			"1. Identify the immediate effect of acting on: " + problem,
			"2. Ask: what happens next as a result of that effect?",
			"3. Trace second- and third-order consequences",
		},
		"occams_razor": {
			"1. List all competing explanations or solutions for: " + problem,
			"2. Count assumptions required by each option",
			"3. Prefer the option with the fewest assumptions",
		},
		"pareto_principle": {
			"1. List all inputs and outputs related to: " + problem,
			"2. Identify which 20% of inputs produce 80% of results",
			"3. Prioritize or eliminate based on the 80/20 distribution",
		},
		"systems_thinking": {
			"1. Map all components involved in: " + problem,
			"2. Identify feedback loops and interdependencies",
			"3. Find leverage points where small changes cause large effects",
		},
		"probabilistic_thinking": {
			"1. Define possible outcomes for: " + problem,
			"2. Assign rough probabilities to each outcome",
			"3. Make decisions that maximize expected value across the distribution",
		},
	}

	if steps, ok := templates[modelName]; ok {
		return steps
	}

	// Generic fallback for known-but-untemplateed models.
	return []string{
		"1. Understand the core principle of the " + modelName + " model",
		"2. Apply the model's lens to: " + problem,
		"3. Derive actionable insight or decision from the analysis",
	}
}
