package patterns

import (
	"fmt"
	"math"

	think "github.com/thebtf/aimux/pkg/think"
)

const (
	overconfidenceThreshold    = 0.8 // Confidence level that triggers warning
	minClaimsForHighConfidence = 3   // Minimum claims needed to justify high confidence
)

type metacognitiveMonitoringPattern struct{}

// NewMetacognitiveMonitoringPattern returns the "metacognitive_monitoring" pattern handler.
func NewMetacognitiveMonitoringPattern() think.PatternHandler {
	return &metacognitiveMonitoringPattern{}
}

func (p *metacognitiveMonitoringPattern) Name() string { return "metacognitive_monitoring" }

func (p *metacognitiveMonitoringPattern) Description() string {
	return "Monitor cognitive processes for overconfidence and blind spots"
}

func (p *metacognitiveMonitoringPattern) Validate(input map[string]any) (map[string]any, error) {
	task, ok := input["task"]
	if !ok {
		return nil, fmt.Errorf("missing required field: task")
	}
	ts, ok := task.(string)
	if !ok || ts == "" {
		return nil, fmt.Errorf("field 'task' must be a non-empty string")
	}
	out := map[string]any{"task": ts}
	if v, ok := input["knowledgeAssessment"].(string); ok {
		out["knowledgeAssessment"] = v
	}
	if v, ok := input["claims"].([]any); ok {
		out["claims"] = v
	}
	if v, ok := input["cognitiveProcesses"].([]any); ok {
		out["cognitiveProcesses"] = v
	}
	if v, ok := input["biases"].([]any); ok {
		out["biases"] = v
	}
	if v, ok := input["uncertainties"].([]any); ok {
		out["uncertainties"] = v
	}
	if v, err := toFloat64(input["confidence"]); err == nil {
		out["confidence"] = v
	}
	return out, nil
}

func (p *metacognitiveMonitoringPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	task := validInput["task"].(string)

	countSlice := func(key string) int {
		if v, ok := validInput[key].([]any); ok {
			return len(v)
		}
		return 0
	}

	claimsCount := countSlice("claims")
	biasesCount := countSlice("biases")
	uncertaintiesCount := countSlice("uncertainties")
	processesCount := countSlice("cognitiveProcesses")

	confidence := 0.0
	hasConfidence := false
	if v, ok := validInput["confidence"].(float64); ok {
		confidence = v
		hasConfidence = true
	}

	overconfidenceWarning := ""
	if confidence > overconfidenceThreshold && claimsCount < minClaimsForHighConfidence {
		overconfidenceWarning = fmt.Sprintf(
			"High confidence (%.2f) with few supporting claims (%d). Consider gathering more evidence.",
			confidence, claimsCount,
		)
	}

	data := map[string]any{
		"task":                  task,
		"claimsCount":           claimsCount,
		"biasesCount":           biasesCount,
		"uncertaintiesCount":    uncertaintiesCount,
		"cognitiveProcessCount": processesCount,
		"confidence":            confidence,
		"overconfidenceWarning": overconfidenceWarning,
	}

	computed := []string{"overconfidenceWarning"}

	// When only task is provided (no claims/biases/uncertainties), suggest assessment framework.
	if claimsCount == 0 && biasesCount == 0 && uncertaintiesCount == 0 {
		data["assessmentFramework"] = []string{
			"knowledge depth",
			"assumption quality",
			"evidence strength",
			"reasoning clarity",
		}
		computed = append(computed, "assessmentFramework")
	}

	if hasConfidence {
		cal := computeMetacogCalibration(confidence, uncertaintiesCount, biasesCount, claimsCount)
		data["calibratedConfidence"] = cal.calibratedConfidence
		data["overconfident"] = cal.overconfident
		data["adjustmentReason"] = cal.adjustmentReason
		computed = append(computed, "calibratedConfidence", "overconfident", "adjustmentReason")
	}

	data["guidance"] = BuildGuidance("metacognitive_monitoring", "basic", []string{"claims", "biases", "uncertainties", "cognitiveProcesses", "confidence", "knowledgeAssessment"})

	return think.MakeThinkResult("metacognitive_monitoring", data, sessionID, nil, "", computed), nil
}

type metacogCalibration struct {
	calibratedConfidence float64
	overconfident        bool
	adjustmentReason     string
}

func computeMetacogCalibration(rawConfidence float64, uncertaintyCount, biasCount, claimsCount int) metacogCalibration {
	uncertaintyPenalty := math.Min(float64(uncertaintyCount)*0.05, 0.3)
	biasPenalty := math.Min(float64(biasCount)*0.1, 0.3)
	calibrated := math.Max(0, math.Min(1, rawConfidence-uncertaintyPenalty-biasPenalty))

	overconfident := claimsCount < minClaimsForHighConfidence && calibrated > overconfidenceThreshold

	parts := []string{}
	if uncertaintyPenalty > 0 {
		parts = append(parts, fmt.Sprintf("-%.2f uncertainty", uncertaintyPenalty))
	}
	if biasPenalty > 0 {
		parts = append(parts, fmt.Sprintf("-%.2f bias", biasPenalty))
	}
	if overconfident {
		parts = append(parts, "flagged overconfident: <3 claims with high confidence")
	}
	reason := "no adjustments applied"
	if len(parts) > 0 {
		reason = joinStrings(parts, "; ")
	}

	return metacogCalibration{
		calibratedConfidence: calibrated,
		overconfident:        overconfident,
		adjustmentReason:     reason,
	}
}

