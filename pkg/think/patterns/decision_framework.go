package patterns

import (
	"encoding/json"
	"fmt"
	"sort"

	think "github.com/thebtf/aimux/pkg/think"
)

type decisionFrameworkPattern struct{}

// NewDecisionFrameworkPattern returns the "decision_framework" pattern handler.
func NewDecisionFrameworkPattern() think.PatternHandler { return &decisionFrameworkPattern{} }

func (p *decisionFrameworkPattern) Name() string { return "decision_framework" }

func (p *decisionFrameworkPattern) Description() string {
	return "Weighted multi-criteria decision scoring and ranking"
}

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
		if tmpl != nil && len(tmpl.Criteria) > 0 {
			suggestedCriteria = tmpl.Criteria
		} else {
			suggestedCriteria = []string{"performance", "cost", "maintainability", "scalability"}
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
			"decision":           decision,
			"suggestedCriteria":  suggestedCriteria,
			"optionTemplate":     optionTemplate,
			"autoAnalysis":       map[string]any{"source": func() string {
				if tmpl != nil {
					return "domain-template"
				}
				return "keyword-analysis"
			}(), "keywords": keywords},
			"guidance": BuildGuidance("decision_framework", "basic",
				[]string{"criteria", "options"},
			),
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
	return think.MakeThinkResult("decision_framework", data, sessionID, nil, "", []string{"rankedOptions", "hasTies"}), nil
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
