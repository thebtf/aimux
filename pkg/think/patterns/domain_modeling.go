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

func (p *domainModelingPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	domainName := validInput["domainName"].(string)

	countSlice := func(key string) int {
		if v, ok := validInput[key].([]any); ok {
			return len(v)
		}
		return 0
	}

	description := ""
	if v, ok := validInput["description"].(string); ok {
		description = v
	}

	entityCount := countSlice("entities")
	relationshipCount := countSlice("relationships")
	ruleCount := countSlice("rules")
	constraintCount := countSlice("constraints")

	data := map[string]any{
		"domainName":        domainName,
		"description":       description,
		"entityCount":       entityCount,
		"relationshipCount": relationshipCount,
		"ruleCount":         ruleCount,
		"constraintCount":   constraintCount,
		"totalComponents":   entityCount + relationshipCount + ruleCount + constraintCount,
	}
	return think.MakeThinkResult("domain_modeling", data, sessionID, nil, "", []string{"totalComponents"}), nil
}
