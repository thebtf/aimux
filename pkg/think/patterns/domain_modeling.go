package patterns

import (
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

type domainModelingPattern struct{}

// NewDomainModelingPattern returns the "domain_modeling" pattern handler.
func NewDomainModelingPattern() think.PatternHandler { return &domainModelingPattern{} }

func (p *domainModelingPattern) Name() string { return "domain_modeling" }

func (p *domainModelingPattern) Description() string {
	return "Model a domain with entities, relationships, rules, and constraints"
}

func (p *domainModelingPattern) Validate(input map[string]any) (map[string]any, error) {
	domainName, ok := input["domainName"]
	if !ok {
		return nil, fmt.Errorf("missing required field: domainName")
	}
	dn, ok := domainName.(string)
	if !ok || dn == "" {
		return nil, fmt.Errorf("field 'domainName' must be a non-empty string")
	}
	out := map[string]any{"domainName": dn}
	if v, ok := input["description"].(string); ok {
		out["description"] = v
	}
	if v, ok := input["entities"].([]any); ok {
		out["entities"] = v
	}
	if v, ok := input["relationships"].([]any); ok {
		out["relationships"] = v
	}
	if v, ok := input["rules"].([]any); ok {
		out["rules"] = v
	}
	if v, ok := input["constraints"].([]any); ok {
		out["constraints"] = v
	}
	return out, nil
}

func (p *domainModelingPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"domainName":  {Type: "string", Required: true, Description: "Name of the domain to model"},
		"description": {Type: "string", Required: false, Description: "Domain description"},
		"entities": {
			Type:        "array",
			Required:    false,
			Description: "List of domain entities",
			Items: map[string]any{
				"oneOf": []map[string]any{
					{"type": "string"},
					{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
		"relationships": {
			Type:        "array",
			Required:    false,
			Description: "List of entity relationships",
			Items: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from":   map[string]any{"type": "string"},
					"to":     map[string]any{"type": "string"},
					"source": map[string]any{"type": "string"},
					"target": map[string]any{"type": "string"},
				},
			},
		},
		"rules":       {Type: "array", Required: false, Description: "List of domain rules", Items: map[string]any{"type": "string"}},
		"constraints": {Type: "array", Required: false, Description: "List of domain constraints", Items: map[string]any{"type": "string"}},
	}
}

func (p *domainModelingPattern) Category() string { return "solo" }

func (p *domainModelingPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	domainName := validInput["domainName"].(string)

	getSlice := func(key string) []any {
		if v, ok := validInput[key].([]any); ok {
			return v
		}
		return nil
	}

	description := ""
	if v, ok := validInput["description"].(string); ok {
		description = v
	}

	entities := getSlice("entities")
	relationships := getSlice("relationships")
	rules := getSlice("rules")
	constraints := getSlice("constraints")

	entityCount := len(entities)
	relationshipCount := len(relationships)
	ruleCount := len(rules)
	constraintCount := len(constraints)

	data := map[string]any{
		"domainName":        domainName,
		"description":       description,
		"entityCount":       entityCount,
		"relationshipCount": relationshipCount,
		"ruleCount":         ruleCount,
		"constraintCount":   constraintCount,
		"totalComponents":   entityCount + relationshipCount + ruleCount + constraintCount,
	}

	// Auto-analysis: when entities are empty, derive suggestions from domain templates.
	var domainTmpl *DomainTemplate // lifted for reuse in text analysis
	if entityCount == 0 {
		extractedKW := ExtractKeywords(domainName)
		domainTmpl = MatchDomainTemplate(domainName)
		var suggestedEntities []string
		var suggestedRelationships []map[string]string
		var autoSource string
		if domainTmpl != nil && len(domainTmpl.Entities) > 0 {
			suggestedEntities = domainTmpl.Entities
			// Generate a simple chain of relationships for suggested entities.
			for i := 0; i+1 < len(suggestedEntities); i++ {
				suggestedRelationships = append(suggestedRelationships, map[string]string{
					"from": suggestedEntities[i],
					"to":   suggestedEntities[i+1],
				})
			}
			autoSource = "domain-template"
		} else {
			autoSource = "keyword-analysis"
		}
		data["suggestedEntities"] = suggestedEntities
		data["suggestedRelationships"] = suggestedRelationships
		autoAnalysis := map[string]any{"source": autoSource}
		if len(extractedKW) > 0 {
			autoAnalysis["keywords"] = extractedKW
		}
		data["autoAnalysis"] = autoAnalysis

		// Run consistency analysis on suggested entities/relationships.
		if len(suggestedEntities) > 0 {
			sugEntAny := make([]any, len(suggestedEntities))
			for i, e := range suggestedEntities {
				sugEntAny[i] = e
			}
			sugRelAny := make([]any, len(suggestedRelationships))
			for i, r := range suggestedRelationships {
				sugRelAny[i] = map[string]any{"from": r["from"], "to": r["to"]}
			}
			orphans, dangling, consistent := validateEntityRelationships(sugEntAny, sugRelAny)
			data["suggestedOrphanEntities"] = orphans
			data["suggestedDanglingRelationships"] = dangling
			data["suggestedConsistent"] = consistent
		}
	}

	if entityCount > 0 || relationshipCount > 0 {
		orphans, dangling, consistent := validateEntityRelationships(entities, relationships)
		data["orphanEntities"] = orphans
		data["danglingRelationships"] = dangling
		data["consistent"] = consistent
	}

	// Guidance — always included.
	data["guidance"] = BuildGuidance("domain_modeling",
		func() string {
			if entityCount > 0 {
				return "full"
			}
			return "basic"
		}(),
		[]string{"entities", "relationships", "rules", "constraints"},
	)

	// Tier 2A: text analysis
	primaryText := validInput["domainName"].(string)
	if analysis := AnalyzeText(primaryText); analysis != nil {
		if domainTmpl != nil {
			analysis.Gaps = DetectGaps(analysis.Entities, domainTmpl)
		}
		data["textAnalysis"] = analysis
	}

	return think.MakeThinkResult("domain_modeling", data, sessionID, nil, "", []string{"totalComponents"}), nil
}

// danglingRelationship describes a relationship whose endpoints are not all in the entity set.
type danglingRelationship struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// validateEntityRelationships checks entity/relationship consistency matching the v2 TypeScript logic.
// It returns orphan entity names, dangling relationships, and a consistency boolean.
func validateEntityRelationships(entities []any, relationships []any) (orphans []string, dangling []danglingRelationship, consistent bool) {
	// Collect entity names from string elements or maps with a "name" key.
	entityNames := make(map[string]struct{}, len(entities))
	for _, e := range entities {
		switch v := e.(type) {
		case string:
			entityNames[v] = struct{}{}
		case map[string]any:
			if name, ok := v["name"].(string); ok {
				entityNames[name] = struct{}{}
			}
		}
	}

	connectedEntities := make(map[string]struct{})

	for _, r := range relationships {
		rel, ok := r.(map[string]any)
		if !ok {
			continue
		}

		// Support both "from"/"to" and "source"/"target" endpoint keys.
		from, _ := coalesceString(rel, "from", "source")
		to, _ := coalesceString(rel, "to", "target")
		if from == "" || to == "" {
			continue
		}

		var reasons []string
		if _, exists := entityNames[from]; !exists {
			reasons = append(reasons, fmt.Sprintf("%q not in entities", from))
		}
		if _, exists := entityNames[to]; !exists {
			reasons = append(reasons, fmt.Sprintf("%q not in entities", to))
		}

		if len(reasons) > 0 {
			dangling = append(dangling, danglingRelationship{
				From:   from,
				To:     to,
				Reason: joinStrings(reasons, ", "),
			})
		} else {
			connectedEntities[from] = struct{}{}
			connectedEntities[to] = struct{}{}
		}
	}

	for name := range entityNames {
		if _, connected := connectedEntities[name]; !connected {
			orphans = append(orphans, name)
		}
	}

	// Return deterministic empty slices rather than nil so callers get []/[] not null.
	if orphans == nil {
		orphans = []string{}
	}
	if dangling == nil {
		dangling = []danglingRelationship{}
	}

	consistent = len(dangling) == 0 && len(orphans) == 0
	return orphans, dangling, consistent
}

// coalesceString returns the first non-empty string value found under any of the given keys.
func coalesceString(m map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v, true
		}
	}
	return "", false
}

// joinStrings joins a slice of strings with sep (avoids importing strings package for a single call).
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}
