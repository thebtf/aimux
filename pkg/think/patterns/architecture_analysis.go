package patterns

import (
	"encoding/json"
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

const highCouplingThreshold = 2

type architectureAnalysisPattern struct{}

// NewArchitectureAnalysisPattern returns the "architecture_analysis" pattern handler.
func NewArchitectureAnalysisPattern() think.PatternHandler { return &architectureAnalysisPattern{} }

func (p *architectureAnalysisPattern) Name() string { return "architecture_analysis" }

func (p *architectureAnalysisPattern) Description() string {
	return "ATAM-lite architecture analysis with coupling detection"
}

func (p *architectureAnalysisPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"components": {Type: "array", Required: true, Description: "List of system components (strings or objects with name/description/dependencies)"},
	}
}

func (p *architectureAnalysisPattern) Category() string { return "solo" }

func (p *architectureAnalysisPattern) Validate(input map[string]any) (map[string]any, error) {
	// Parse JSON string params from MCP schema
	if s, ok := input["components"].(string); ok && s != "" {
		var parsed []any
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return nil, fmt.Errorf("components: invalid JSON: %w", err)
		}
		input["components"] = parsed
	}

	componentsRaw, ok := input["components"]
	if !ok {
		return nil, fmt.Errorf("missing required field: components")
	}
	components, ok := componentsRaw.([]any)
	if !ok || len(components) == 0 {
		return nil, fmt.Errorf("field 'components' must be a non-empty array")
	}

	// Normalize: accept strings or maps with name/description/dependencies.
	normalized := make([]any, 0, len(components))
	for i, c := range components {
		switch v := c.(type) {
		case string:
			normalized = append(normalized, map[string]any{
				"name":         v,
				"description":  "",
				"dependencies": []any{},
			})
		case map[string]any:
			name, ok := v["name"].(string)
			if !ok || name == "" {
				return nil, fmt.Errorf("components[%d].name must be a non-empty string", i)
			}
			desc, _ := v["description"].(string)
			deps, _ := v["dependencies"].([]any)
			if deps == nil {
				deps = []any{}
			}
			normalized = append(normalized, map[string]any{
				"name":         name,
				"description":  desc,
				"dependencies": deps,
			})
		default:
			return nil, fmt.Errorf("components[%d] must be a string or map", i)
		}
	}

	return map[string]any{"components": normalized}, nil
}

// componentMetric holds Ca/Ce/instability for a single component.
type componentMetric struct {
	Component   string  `json:"component"`
	Ca          int     `json:"ca"`
	Ce          int     `json:"ce"`
	Instability float64 `json:"instability"`
}

func (p *architectureAnalysisPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	components := validInput["components"].([]any)

	// Auto-analysis: when 0 or 1 components are provided, derive suggestions from domain templates.
	var suggestedComponents []string
	var autoAnalysisSource string
	var extractedKW []string
	var domainTmpl *DomainTemplate // lifted for reuse in text analysis
	var primarySearchText string   // lifted for text analysis
	if len(components) <= 1 {
		if len(components) == 1 {
			if m, ok := components[0].(map[string]any); ok {
				primarySearchText, _ = m["name"].(string)
			}
		}
		extractedKW = ExtractKeywords(primarySearchText)
		domainTmpl = MatchDomainTemplate(primarySearchText)
		if domainTmpl != nil && len(domainTmpl.Components) > 0 {
			suggestedComponents = domainTmpl.Components
			autoAnalysisSource = "domain-template"
		} else {
			autoAnalysisSource = "keyword-analysis"
		}
	}

	// ca[name] = afferent coupling — how many others depend on this component.
	// ce[name] = efferent coupling — how many components this one depends on.
	ca := make(map[string]int, len(components))
	ce := make(map[string]int, len(components))
	var componentNames []string

	// Pass 1: register all names so ca entries are not clobbered in pass 2.
	for _, c := range components {
		m := c.(map[string]any)
		name := m["name"].(string)
		componentNames = append(componentNames, name)
		ca[name] = 0
	}

	// Pass 2: count efferent (outgoing) and afferent (incoming) couplings.
	for _, c := range components {
		m := c.(map[string]any)
		name := m["name"].(string)
		deps, _ := m["dependencies"].([]any)
		ce[name] = len(deps)
		for _, d := range deps {
			if ds, ok := d.(string); ok {
				ca[ds]++
			}
		}
	}

	// Compute per-component metrics and detect high coupling.
	metrics := make([]componentMetric, 0, len(components))
	var highlyCoupled []map[string]any

	for _, name := range componentNames {
		caVal := ca[name]
		ceVal := ce[name]
		total := caVal + ceVal
		instability := 0.0
		if total > 0 {
			instability = float64(ceVal) / float64(total)
		}
		metrics = append(metrics, componentMetric{
			Component:   name,
			Ca:          caVal,
			Ce:          ceVal,
			Instability: instability,
		})
		if caVal >= highCouplingThreshold {
			highlyCoupled = append(highlyCoupled, map[string]any{
				"component":  name,
				"dependents": caVal,
			})
		}
	}

	// Importance: sort by Ca descending (for informational ordering).
	importanceAnalysis := make([]map[string]any, 0, len(componentNames))
	for _, name := range componentNames {
		importanceAnalysis = append(importanceAnalysis, map[string]any{
			"component":  name,
			"dependents": ca[name],
		})
	}

	// mostUnstable = highest instability; mostDepended = highest Ca.
	mostUnstable := componentNames[0]
	mostDepended := componentNames[0]
	for _, m := range metrics {
		if m.Instability > metrics[indexOf(metrics, mostUnstable)].Instability {
			mostUnstable = m.Component
		}
		if m.Ca > ca[mostDepended] {
			mostDepended = m.Component
		}
	}

	// Convert metrics to []any for MakeThinkResult.
	metricsAny := make([]any, len(metrics))
	for i, m := range metrics {
		metricsAny[i] = map[string]any{
			"component":   m.Component,
			"ca":          m.Ca,
			"ce":          m.Ce,
			"instability": m.Instability,
		}
	}

	data := map[string]any{
		"componentCount":     len(components),
		"components":         componentNames,
		"highlyCoupled":      highlyCoupled,
		"couplingDetected":   len(highlyCoupled) > 0,
		"importanceAnalysis": importanceAnalysis,
		"componentMetrics":   metricsAny,
		"mostUnstable":       mostUnstable,
		"mostDepended":       mostDepended,
	}

	// Include auto-analysis when triggered (0 or 1 components provided).
	if autoAnalysisSource != "" {
		data["suggestedComponents"] = suggestedComponents
		autoAnalysis := map[string]any{"source": autoAnalysisSource}
		if len(extractedKW) > 0 {
			autoAnalysis["keywords"] = extractedKW
		}
		data["autoAnalysis"] = autoAnalysis
	}

	// Guidance — always included.
	data["guidance"] = BuildGuidance("architecture_analysis",
		func() string {
			if len(components) >= 2 {
				return "full"
			}
			if autoAnalysisSource != "" {
				return "enriched"
			}
			return "basic"
		}(),
		[]string{"components"},
	)

	// Tier 2A: text analysis
	if primarySearchText != "" {
		if analysis := AnalyzeText(primarySearchText); analysis != nil {
			if domainTmpl != nil {
				analysis.Gaps = DetectGaps(analysis.Entities, domainTmpl)
			}
			data["textAnalysis"] = analysis
		}
	}

	return think.MakeThinkResult("architecture_analysis", data, sessionID, nil, "", []string{"highlyCoupled", "couplingDetected"}), nil
}

// indexOf returns the position of the named component in the metrics slice.
func indexOf(metrics []componentMetric, name string) int {
	for i, m := range metrics {
		if m.Component == name {
			return i
		}
	}
	return 0
}
