package patterns

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	think "github.com/thebtf/aimux/pkg/think"
)

// biasCatalogs maps bias names to their trigger phrases.
var biasCatalogs = map[string][]string{
	"confirmation_bias":      {"proves my point", "as expected", "confirms", "validates my", "exactly what i thought", "as i predicted"},
	"anchoring":              {"initial estimate", "first impression", "starting point", "original assumption", "based on the first"},
	"sunk_cost":              {"already invested", "too far to stop", "can't waste", "put too much into", "come this far"},
	"availability_heuristic": {"recent example", "just saw", "heard about", "in the news", "happened recently"},
	"bandwagon":              {"everyone thinks", "popular opinion", "most people", "consensus is", "generally accepted"},
	"dunning_kruger":         {"i know everything", "simple enough", "how hard can", "obviously", "anyone can"},
	"survivorship_bias":      {"successful examples", "winners show", "look at those who made it", "they all did"},
	"framing_effect":         {"depends on how", "way you look at", "perspective changes", "if you frame"},
}

type criticalThinkingPattern struct {
	sampling think.SamplingProvider
}

// NewCriticalThinkingPattern returns the "critical_thinking" pattern handler.
func NewCriticalThinkingPattern() think.PatternHandler { return &criticalThinkingPattern{} }

// SetSampling injects the sampling provider. Implements think.SamplingAwareHandler.
func (p *criticalThinkingPattern) SetSampling(provider think.SamplingProvider) {
	p.sampling = provider
}

func (p *criticalThinkingPattern) Name() string { return "critical_thinking" }

func (p *criticalThinkingPattern) Description() string {
	return "Scan text for cognitive biases using trigger-phrase detection"
}

func (p *criticalThinkingPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"issue":        {Type: "string", Required: true, Description: "Text to scan for cognitive biases"},
		"assumptions":  {Type: "array", Required: false, Description: "List of assumptions to evaluate", Items: map[string]any{"type": "string"}},
		"alternatives": {Type: "array", Required: false, Description: "List of alternative perspectives", Items: map[string]any{"type": "string"}},
		"evidence":     {Type: "array", Required: false, Description: "List of evidence items", Items: map[string]any{"type": "string"}},
		"conclusion":   {Type: "string", Required: false, Description: "Conclusion being evaluated"},
	}
}

func (p *criticalThinkingPattern) Category() string { return "solo" }

func (p *criticalThinkingPattern) Validate(input map[string]any) (map[string]any, error) {
	issue, ok := input["issue"]
	if !ok {
		return nil, fmt.Errorf("missing required field: issue")
	}
	s, ok := issue.(string)
	if !ok || s == "" {
		return nil, fmt.Errorf("field 'issue' must be a non-empty string")
	}
	out := map[string]any{"issue": s}
	// Optional structured reasoning fields — TS v1 parity.
	if v, ok := input["assumptions"].([]any); ok {
		out["assumptions"] = v
	}
	if v, ok := input["alternatives"].([]any); ok {
		out["alternatives"] = v
	}
	if v, ok := input["evidence"].([]any); ok {
		out["evidence"] = v
	}
	if v, ok := input["conclusion"].(string); ok {
		out["conclusion"] = v
	}
	return out, nil
}

func (p *criticalThinkingPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	issue := validInput["issue"].(string)
	lowered := strings.ToLower(issue)

	var detectedBiases []map[string]any
	for biasName, triggers := range biasCatalogs {
		var matched []string
		for _, trigger := range triggers {
			if strings.Contains(lowered, trigger) {
				matched = append(matched, trigger)
			}
		}
		if len(matched) > 0 {
			detectedBiases = append(detectedBiases, map[string]any{
				"bias":     biasName,
				"triggers": matched,
			})
		}
	}

	// Tier 1.5: sampling-enhanced bias detection — merge LLM-detected biases, deduplicate.
	if p.sampling != nil {
		if sampledBiases, err := p.requestSamplingBiases(issue); err == nil {
			// Build set of already-detected bias names for deduplication.
			detected := make(map[string]struct{}, len(detectedBiases))
			for _, b := range detectedBiases {
				if name, ok := b["bias"].(string); ok {
					detected[name] = struct{}{}
				}
			}
			for _, sb := range sampledBiases {
				name, _ := sb["type"].(string)
				if _, dup := detected[name]; !dup && name != "" {
					detectedBiases = append(detectedBiases, map[string]any{
						"bias":     name,
						"evidence": sb["evidence"],
						"severity": sb["severity"],
						"source":   "sampling",
					})
					detected[name] = struct{}{}
				}
			}
		}
		// On error: keyword-detected biases still used — graceful degradation.
	}

	recommendation := "No cognitive biases detected in the text."
	if len(detectedBiases) > 0 {
		recommendation = fmt.Sprintf("Detected %d potential cognitive bias(es). Review flagged phrases for objective reasoning.", len(detectedBiases))
	}

	data := map[string]any{
		"issue":          issue,
		"detectedBiases": detectedBiases,
		"biasCount":      len(detectedBiases),
		"recommendation": recommendation,
		"guidance":       BuildGuidance("critical_thinking", "full", []string{"issue"}),
	}

	// Echo optional structured reasoning fields when provided (TS v1 parity).
	if v, ok := validInput["assumptions"]; ok {
		data["assumptions"] = v
		if arr, ok := v.([]any); ok {
			data["assumptionCount"] = len(arr)
		}
	}
	if v, ok := validInput["alternatives"]; ok {
		data["alternatives"] = v
		if arr, ok := v.([]any); ok {
			data["alternativeCount"] = len(arr)
		}
	}
	if v, ok := validInput["evidence"]; ok {
		data["evidence"] = v
		if arr, ok := v.([]any); ok {
			data["evidenceCount"] = len(arr)
		}
	}
	if v, ok := validInput["conclusion"]; ok {
		data["conclusion"] = v
		data["hasConclusion"] = v.(string) != ""
	}

	// When biases are detected, suggest decision_framework to apply structured evaluation.
	// Always provide nextHint = "structured_argumentation" (TS v1 parity); override when biases detected.
	suggestedNext := "structured_argumentation"
	if len(detectedBiases) > 0 {
		suggestedNext = "decision_framework"
	}

	return think.MakeThinkResult("critical_thinking", data, sessionID, nil, suggestedNext, []string{"detectedBiases", "biasCount"}), nil
}

// samplingBiasResponse is the JSON shape we ask the LLM to return for bias detection.
type samplingBiasResponse struct {
	Biases []struct {
		Type     string `json:"type"`
		Evidence string `json:"evidence"`
		Severity string `json:"severity"`
	} `json:"biases"`
	Fallacies              []string `json:"fallacies"`
	UnsupportedAssumptions []string `json:"unsupportedAssumptions"`
}

// requestSamplingBiases calls the sampling provider to detect additional biases beyond
// the keyword catalog. Returns a slice of bias maps (type, evidence, severity).
// Returns nil, error on any failure so the caller can gracefully degrade.
func (p *criticalThinkingPattern) requestSamplingBiases(issue string) ([]map[string]any, error) {
	tmpl := GetSamplingPrompt("critical_thinking")
	var messages []think.SamplingMessage
	maxTokens := 1500
	if tmpl != nil {
		systemRole, userPrompt := FormatSamplingPrompt(tmpl, issue)
		messages = []think.SamplingMessage{
			{Role: "user", Content: systemRole + "\n\n" + userPrompt},
		}
		maxTokens = tmpl.MaxTokens
	} else {
		messages = []think.SamplingMessage{
			{Role: "user", Content: fmt.Sprintf(
				`Analyze this statement for cognitive biases. Statement: %s. `+
					`Return JSON: {"biases": [{"type": "...", "evidence": "...", "severity": "low|medium|high"}]}`,
				issue,
			)},
		}
	}

	raw, err := p.sampling.RequestSampling(context.Background(), messages, maxTokens)
	if err != nil {
		return nil, err
	}

	var resp samplingBiasResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("sampling JSON parse failed: %w", err)
	}

	biases := make([]map[string]any, 0, len(resp.Biases))
	for _, b := range resp.Biases {
		biases = append(biases, map[string]any{
			"type":     b.Type,
			"evidence": b.Evidence,
			"severity": b.Severity,
		})
	}
	return biases, nil
}
