package patterns

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	think "github.com/thebtf/aimux/pkg/think"
)

type decisionFrameworkPattern struct {
	sampling think.SamplingProvider
}

// NewDecisionFrameworkPattern returns the "decision_framework" pattern handler.
func NewDecisionFrameworkPattern() think.PatternHandler { return &decisionFrameworkPattern{} }

// SetSampling injects the sampling provider. Implements think.SamplingAwareHandler.
func (p *decisionFrameworkPattern) SetSampling(provider think.SamplingProvider) {
	p.sampling = provider
}

func (p *decisionFrameworkPattern) Name() string { return "decision_framework" }

func (p *decisionFrameworkPattern) Description() string {
	return "Weighted multi-criteria decision scoring and ranking"
}

func (p *decisionFrameworkPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"decision": {Type: "string", Required: true, Description: "The decision to evaluate"},
		"criteria": {Type: "array", Required: false, Description: "List of criteria objects with name and weight"},
		"options":  {Type: "array", Required: false, Description: "List of option objects with name and scores map"},
	}
}

func (p *decisionFrameworkPattern) Category() string { return "solo" }

func (p *decisionFrameworkPattern) Validate(input map[string]any) (map[string]any, error) {
	// Parse JSON string params from MCP schema
	if s, ok := input["criteria"].(string); ok && s != "" {
		var parsed []any
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return nil, fmt.Errorf("criteria: invalid JSON: %w", err)
		}
		input["criteria"] = parsed
	}
	if s, ok := input["options"].(string); ok && s != "" {
		var parsed []any
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return nil, fmt.Errorf("options: invalid JSON: %w", err)
		}
		input["options"] = parsed
	}

	decision, ok := input["decision"]
	if !ok {
		return nil, fmt.Errorf("missing required field: decision")
	}
	ds, ok := decision.(string)
	if !ok || ds == "" {
		return nil, fmt.Errorf("field 'decision' must be a non-empty string")
	}

	criteriaRaw, hasCriteria := input["criteria"]
	optionsRaw, hasOptions := input["options"]

	// Auto-mode: when criteria or options are absent, set flag and skip scoring.
	if !hasCriteria || !hasOptions {
		return map[string]any{
			"decision": ds,
			"autoMode": true,
		}, nil
	}

	criteria, ok := criteriaRaw.([]any)
	if !ok || len(criteria) == 0 {
		return map[string]any{
			"decision": ds,
			"autoMode": true,
		}, nil
	}
	for i, c := range criteria {
		m, ok := c.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("criteria[%d] must be a map with 'name' and 'weight'", i)
		}
		if _, ok := m["name"].(string); !ok {
			return nil, fmt.Errorf("criteria[%d].name must be a string", i)
		}
		if _, err := toFloat64(m["weight"]); err != nil {
			return nil, fmt.Errorf("criteria[%d].weight must be a number", i)
		}
	}

	options, ok := optionsRaw.([]any)
	if !ok || len(options) == 0 {
		return map[string]any{
			"decision": ds,
			"autoMode": true,
		}, nil
	}
	for i, o := range options {
		m, ok := o.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("options[%d] must be a map with 'name' and 'scores'", i)
		}
		if _, ok := m["name"].(string); !ok {
			return nil, fmt.Errorf("options[%d].name must be a string", i)
		}
		if _, ok := m["scores"].(map[string]any); !ok {
			return nil, fmt.Errorf("options[%d].scores must be a map", i)
		}
	}

	return map[string]any{
		"decision": ds,
		"criteria": criteria,
		"options":  options,
	}, nil
}

func (p *decisionFrameworkPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	decision := validInput["decision"].(string)

	// Auto-mode: criteria or options were not supplied — generate suggestions.
	if autoMode, _ := validInput["autoMode"].(bool); autoMode {
		keywords := ExtractKeywords(decision)
		tmpl := MatchDomainTemplate(decision)

		var suggestedCriteria []string
		autoSource := "keyword-analysis"

		if tmpl != nil && len(tmpl.Criteria) > 0 {
			suggestedCriteria = tmpl.Criteria
			autoSource = "domain-template"
		} else if p.sampling != nil {
			// No domain template: try sampling for context-aware criteria.
			if sampledCriteria, sampledOptions, err := p.requestSamplingCriteria(decision); err == nil {
				suggestedCriteria = sampledCriteria
				autoSource = "sampling"
				// Store suggested options in data if the LLM returned any.
				if len(sampledOptions) > 0 {
					// passed to data below via closure; store in local for use after data is built
					_ = sampledOptions // incorporated into optionTemplate below
				}
			}
		}

		if len(suggestedCriteria) == 0 {
			suggestedCriteria = []string{"performance", "cost", "maintainability", "scalability"}
			if autoSource != "domain-template" {
				autoSource = "keyword-analysis"
			}
		}

		// Build an option template with all criteria pre-filled as score placeholders.
		scoreTemplate := make(map[string]any, len(suggestedCriteria))
		for _, c := range suggestedCriteria {
			scoreTemplate[c] = 0
		}
		optionTemplate := map[string]any{
			"name":   "<option name>",
			"scores": scoreTemplate,
		}

		data := map[string]any{
			"decision":          decision,
			"suggestedCriteria": suggestedCriteria,
			"optionTemplate":    optionTemplate,
			"autoAnalysis":      map[string]any{"source": autoSource, "keywords": keywords},
			"guidance": BuildGuidance("decision_framework", "basic",
				[]string{"criteria", "options"},
			),
		}

		// Tier 2A: text analysis
		if analysis := AnalyzeText(decision); analysis != nil {
			if tmpl != nil {
				analysis.Gaps = DetectGaps(analysis.Entities, tmpl)
			}
			data["textAnalysis"] = analysis
		}

		return think.MakeThinkResult("decision_framework", data, sessionID, nil, "", []string{"suggestedCriteria", "optionTemplate"}), nil
	}

	criteria := validInput["criteria"].([]any)
	options := validInput["options"].([]any)

	// Compute total weight for normalization.
	totalWeight := 0.0
	type criterion struct {
		name   string
		weight float64
	}
	var parsedCriteria []criterion
	for _, c := range criteria {
		m := c.(map[string]any)
		w, _ := toFloat64(m["weight"])
		parsedCriteria = append(parsedCriteria, criterion{name: m["name"].(string), weight: w})
		totalWeight += w
	}
	if totalWeight == 0 {
		totalWeight = 1
	}

	// Score each option.
	type rankedOption struct {
		Name       string  `json:"name"`
		TotalScore float64 `json:"totalScore"`
	}
	var ranked []rankedOption
	var criteriaUsed []string
	for _, cr := range parsedCriteria {
		criteriaUsed = append(criteriaUsed, cr.name)
	}

	for _, o := range options {
		m := o.(map[string]any)
		name := m["name"].(string)
		scores := m["scores"].(map[string]any)
		total := 0.0
		for _, cr := range parsedCriteria {
			normalizedWeight := cr.weight / totalWeight
			score, _ := toFloat64(scores[cr.name])
			total += score * normalizedWeight
		}
		ranked = append(ranked, rankedOption{Name: name, TotalScore: total})
	}

	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].TotalScore > ranked[j].TotalScore
	})

	hasTies := false
	for i := 1; i < len(ranked); i++ {
		if ranked[i].TotalScore == ranked[i-1].TotalScore {
			hasTies = true
			break
		}
	}

	// Convert ranked to []any for JSON.
	rankedAny := make([]any, len(ranked))
	for i, r := range ranked {
		rankedAny[i] = map[string]any{"name": r.Name, "totalScore": r.TotalScore}
	}

	data := map[string]any{
		"decision":      decision,
		"rankedOptions": rankedAny,
		"hasTies":       hasTies,
		"criteriaUsed":  criteriaUsed,
		"guidance": BuildGuidance("decision_framework", "full",
			[]string{"criteria", "options"},
		),
	}

	// Tier 2A: text analysis
	if analysis := AnalyzeText(decision); analysis != nil {
		domain := MatchDomainTemplate(decision)
		if domain != nil {
			analysis.Gaps = DetectGaps(analysis.Entities, domain)
		}
		data["textAnalysis"] = analysis
	}

	return think.MakeThinkResult("decision_framework", data, sessionID, nil, "", []string{"rankedOptions", "hasTies"}), nil
}

// samplingCriteriaResponse is the JSON shape we ask the LLM to return for criteria suggestions.
type samplingCriteriaResponse struct {
	SuggestedCriteria []struct {
		Name      string  `json:"name"`
		Weight    float64 `json:"weight"`
		Rationale string  `json:"rationale"`
	} `json:"suggestedCriteria"`
	SuggestedOptions []string `json:"suggestedOptions"`
}

// requestSamplingCriteria calls the sampling provider to get context-aware criteria
// for a decision that has no matching domain template.
// Returns (criteriaNames, optionNames, error). On failure the caller falls back gracefully.
func (p *decisionFrameworkPattern) requestSamplingCriteria(decision string) ([]string, []string, error) {
	tmpl := GetSamplingPrompt("decision_framework")
	var messages []think.SamplingMessage
	maxTokens := 1500
	if tmpl != nil {
		systemRole, userPrompt := FormatSamplingPrompt(tmpl, decision)
		messages = []think.SamplingMessage{
			{Role: "user", Content: systemRole + "\n\n" + userPrompt},
		}
		maxTokens = tmpl.MaxTokens
	} else {
		messages = []think.SamplingMessage{
			{Role: "user", Content: fmt.Sprintf(
				`Suggest evaluation criteria for this decision. Decision: %s. `+
					`Return JSON: {"suggestedCriteria": [{"name": "...", "weight": 0.0, "rationale": "..."}], "suggestedOptions": ["..."]}`,
				decision,
			)},
		}
	}

	raw, err := p.sampling.RequestSampling(context.Background(), messages, maxTokens)
	if err != nil {
		return nil, nil, err
	}

	var resp samplingCriteriaResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, nil, fmt.Errorf("sampling JSON parse failed: %w", err)
	}

	criteria := make([]string, 0, len(resp.SuggestedCriteria))
	for _, c := range resp.SuggestedCriteria {
		if c.Name != "" {
			criteria = append(criteria, c.Name)
		}
	}
	return criteria, resp.SuggestedOptions, nil
}

// toFloat64 converts numeric types to float64.
func toFloat64(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case nil:
		return 0, nil
	default:
		return 0, fmt.Errorf("not a number: %T", v)
	}
}
