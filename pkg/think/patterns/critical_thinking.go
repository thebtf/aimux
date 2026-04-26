package patterns

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	think "github.com/thebtf/aimux/pkg/think"
)

// biasCatalogs maps bias names to their trigger phrases.
// Each bias has multiple formulations to catch real-world phrasing variations.
var biasCatalogs = map[string][]string{
	"confirmation_bias":      {"proves my point", "as expected", "confirms", "validates my", "exactly what i thought", "as i predicted", "supports our view", "aligns with what we"},
	"anchoring":              {"initial estimate", "first impression", "starting point", "original assumption", "based on the first", "our first choice", "started with"},
	"sunk_cost":              {"already invested", "too far to stop", "can't waste", "put too much into", "come this far", "waste that investment", "invested heavily", "thrown away", "wasted effort", "since we've invested", "switching would waste"},
	"availability_heuristic": {"recent example", "just saw", "heard about", "in the news", "happened recently", "i just read", "last time this happened"},
	"bandwagon":              {"everyone thinks", "popular opinion", "most people", "consensus is", "generally accepted", "everyone knows", "everyone uses", "widely adopted", "industry standard"},
	"dunning_kruger":         {"i know everything", "simple enough", "how hard can", "obviously", "anyone can", "trivial to", "easy to implement"},
	"survivorship_bias":      {"successful examples", "winners show", "look at those who made it", "they all did", "success stories", "top companies all"},
	"framing_effect":         {"depends on how", "way you look at", "perspective changes", "if you frame", "spin it as"},
	"appeal_to_authority":    {"the cto said", "the boss said", "expert says", "authority on", "leadership decided", "management says", "senior engineer said", "architect said"},
	"status_quo_bias":        {"always used", "always done it", "has always been", "we've always", "why change", "if it ain't broke", "worked so far", "never had problems with"},
	"hasty_generalization":   {"everyone knows", "is always", "is never", "all of them", "none of them", "is faster than", "is better than", "is slower than", "is always better"},
	"false_dichotomy":        {"only two options", "either we", "the only choice", "no other way", "binary choice", "must choose between"},
	"appeal_to_tradition":    {"we've always done", "traditional approach", "the way it's been", "historically", "legacy technology", "old way works"},
	"ad_hominem":             {"they don't understand", "they're not qualified", "what do they know", "never built anything"},
	"slippery_slope":         {"if we allow", "next thing you know", "where does it end", "will lead to", "opens the door to"},
}

// structuralBiasPatterns detects implicit biases via regex patterns that match
// sentence structure rather than specific trigger phrases.
var structuralBiasPatterns = map[string]*regexp.Regexp{
	"planning_fallacy":          regexp.MustCompile(`(?i)\d+\s*(?:months?|weeks?|sprints?|years?|days?)\s+(?:for\s+(?:the|a|full|complete)\s+)?(?:rewrite|rebuild|replace|migration|refactor|parallelize|optimize|migrate|deploy|implement|complete|finish|deliver|launch|ship)`),
	"time_optimism":             regexp.MustCompile(`(?i)(?:under|less\s+than|only|just)\s+\d+\s*(?:months?|weeks?|sprints?|years?|days?|minutes?|hours?)`),
	"silver_bullet":             regexp.MustCompile(`(?i)(?:should|need\s+to|must|let'?s)\s+(?:rewrite|rebuild|replace|migrate\s+to|switch\s+to|move\s+to|parallelize|adopt|implement)`),
	"overconfidence":            regexp.MustCompile(`(?i)(?:will\s+(?:solve|eliminate|fix\s+all|guarantee|ensure)|completely\s+(?:solve|eliminate|remove))`),
	"correlation_not_causation": regexp.MustCompile(`(?i)because\s+(?:(?:the\s+)?(?:team|company|org(?:anization)?|department))\s+(?:has\s+)?(?:grew|grown|scaled|hired|expanded|doubled|tripled)`),
	"false_precision":           regexp.MustCompile(`(?i)(?:exactly|precisely)\s+\d+`),
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

	// Structural bias detection via regex patterns.
	for biasName, pattern := range structuralBiasPatterns {
		if locs := pattern.FindStringIndex(issue); locs != nil {
			// Check dedup against already-detected biases.
			alreadyDetected := false
			for _, b := range detectedBiases {
				if b["bias"] == biasName {
					alreadyDetected = true
					break
				}
			}
			if !alreadyDetected {
				matched := issue[locs[0]:locs[1]]
				detectedBiases = append(detectedBiases, map[string]any{
					"bias":     biasName,
					"triggers": []string{matched},
					"source":   "structural_pattern",
				})
			}
		}
	}

	// Assumption-evidence cross-reference: flag assumptions that contradict the issue text.
	var contradictedAssumptions []map[string]any
	if assumptions, ok := validInput["assumptions"].([]any); ok && len(assumptions) > 0 {
		issueWords := make(map[string]bool)
		for _, w := range strings.Fields(strings.ToLower(issue)) {
			if len(w) > 3 {
				issueWords[w] = true
			}
		}

		var contradicted []map[string]any
		for _, a := range assumptions {
			as, ok := a.(string)
			if !ok || as == "" {
				continue
			}
			// Check for negation patterns: assumption says X, issue says "not X" or "conflicts/fails/problem"
			aWords := strings.Fields(strings.ToLower(as))
			sharedWords := 0
			for _, w := range aWords {
				if len(w) > 3 && issueWords[w] {
					sharedWords++
				}
			}
			// Assumptions sharing >30% vocabulary with the issue but containing certainty language
			// are potentially contradicted (assumption claims certainty about a problem area)
			certaintyMarkers := []string{"will", "always", "never", "must", "enough", "solve", "guarantee", "eliminate"}
			hasCertainty := false
			for _, marker := range certaintyMarkers {
				if strings.Contains(strings.ToLower(as), marker) {
					hasCertainty = true
					break
				}
			}
			if sharedWords >= 2 && hasCertainty {
				contradicted = append(contradicted, map[string]any{
					"assumption": as,
					"reason":     "Assumption expresses certainty about a contested topic in the issue",
					"source":     "assumption_cross_reference",
				})
			}
		}
		contradictedAssumptions = contradicted
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
		if len(contradictedAssumptions) > 0 {
			data["contradictedAssumptions"] = contradictedAssumptions
			data["contradictedAssumptionCount"] = len(contradictedAssumptions)
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
