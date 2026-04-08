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

func (p *architectureAnalysisPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	components := validInput["components"].([]any)

	// Build dependency reference counts.
	depCount := make(map[string]int)
	var componentNames []string
	for _, c := range components {
		m := c.(map[string]any)
		name := m["name"].(string)
		componentNames = append(componentNames, name)
		deps := m["dependencies"].([]any)
		for _, d := range deps {
			if ds, ok := d.(string); ok {
				depCount[ds]++
			}
		}
	}

	// Detect high coupling: components referenced by 2+ others.
	var highlyCoupled []map[string]any
	for name, count := range depCount {
		if count >= highCouplingThreshold {
			highlyCoupled = append(highlyCoupled, map[string]any{
				"component":  name,
				"dependents": count,
			})
		}
	}

	// Importance: components with most dependents are most critical.
	var importanceAnalysis []map[string]any
	for _, name := range componentNames {
		importanceAnalysis = append(importanceAnalysis, map[string]any{
			"component":  name,
			"dependents": depCount[name],
		})
	}

	data := map[string]any{
		"componentCount":     len(components),
		"components":         componentNames,
		"highlyCoupled":      highlyCoupled,
		"couplingDetected":   len(highlyCoupled) > 0,
		"importanceAnalysis": importanceAnalysis,
	}
	return think.MakeThinkResult("architecture_analysis", data, sessionID, nil, "", []string{"highlyCoupled", "couplingDetected"}), nil
}
