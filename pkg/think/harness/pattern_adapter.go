package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	thinkcore "github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
)

type PatternExecution struct {
	Summary           string             `json:"summary"`
	Data              map[string]any     `json:"data,omitempty"`
	LedgerAdds        KnowledgeLedger    `json:"ledger_adds,omitempty"`
	Objections        []Objection        `json:"objections,omitempty"`
	ConfidenceFactors []ConfidenceFactor `json:"confidence_factors,omitempty"`
}

type PatternAdapter struct{}

func (PatternAdapter) Execute(ctx context.Context, move CognitiveMove, workProduct string, sessionID string) (PatternExecution, error) {
	if err := ctx.Err(); err != nil {
		return PatternExecution{}, err
	}

	patterns.RegisterAll()
	handler := thinkcore.GetPattern(move.Pattern)
	if handler == nil {
		return PatternExecution{}, fmt.Errorf("pattern %q not registered", move.Pattern)
	}

	input, inputErr := inputForPattern(handler.SchemaFields(), workProduct)
	if inputErr != nil {
		return PatternExecution{}, inputErr
	}
	valid, err := handler.Validate(input)
	if err != nil {
		return PatternExecution{}, err
	}
	result, err := handler.Handle(valid, sessionID)
	if err != nil {
		return PatternExecution{}, err
	}

	gateDecision := thinkcore.GateDecision{Status: "complete"}
	advisorRec := thinkcore.Recommendation{Action: "continue", Reason: "stateless invocation"}
	patternSessionID := result.SessionID
	if patternSessionID == "" {
		patternSessionID = sessionID
	}
	if sess := thinkcore.GetSession(patternSessionID); sess != nil {
		gateDecision = thinkcore.NewEnforcementGate().Check(move.Pattern, sess)
		advisorRec = thinkcore.NewPatternAdvisor().Evaluate(sess, result)
		if advisorRec.StatePatch != nil {
			thinkcore.UpdateSessionState(sess.ID, advisorRec.StatePatch)
		}
		if gateDecision.Status == "incomplete" {
			return PatternExecution{}, fmt.Errorf("pattern %q enforcement incomplete: %s", move.Pattern, gateDecision.Reason)
		}
	}

	summary := thinkcore.GenerateSummary(result, "solo")
	return PatternExecution{
		Summary: summary,
		Data:    result.Data,
		LedgerAdds: KnowledgeLedger{
			Checkable: []LedgerEntry{
				{
					ID:     "move_output",
					Text:   summary,
					Source: move.Pattern,
					Status: "observed",
				},
				{
					ID:     "pattern_gate",
					Text:   patternGateText(gateDecision),
					Source: move.Pattern,
					Status: gateDecision.Status,
				},
				{
					ID:     "pattern_advisor",
					Text:   patternAdvisorText(advisorRec),
					Source: move.Pattern,
					Status: advisorRec.Action,
				},
			},
		},
		ConfidenceFactors: []ConfidenceFactor{
			{
				Name:   "move_execution",
				Impact: 0.05,
				Reason: "selected cognitive move executed through the low-level pattern adapter",
			},
			{
				Name:   "pattern_gate",
				Impact: gateConfidenceImpact(gateDecision),
				Reason: patternGateText(gateDecision),
			},
			{
				Name:   "pattern_advisor",
				Impact: advisorConfidenceImpact(advisorRec),
				Reason: patternAdvisorText(advisorRec),
			},
		},
	}, nil
}

func inputForPattern(fields map[string]thinkcore.FieldSchema, primary string) (map[string]any, error) {
	input := make(map[string]any, len(fields))
	structured := parseWorkProductFields(primary)
	for name, schema := range fields {
		if !schema.Required {
			continue
		}
		value, ok := structuredValueForPatternField(structured, name, schema)
		if !ok {
			value, ok = exactValueForPatternField(name, schema, primary)
		}
		if !ok {
			return nil, fmt.Errorf("required field %q cannot be derived from visible work_product", name)
		}
		input[name] = value
	}
	return input, nil
}

func parseWorkProductFields(primary string) map[string]any {
	var fields map[string]any
	if err := json.Unmarshal([]byte(primary), &fields); err != nil {
		return nil
	}
	return fields
}

func structuredValueForPatternField(fields map[string]any, name string, schema thinkcore.FieldSchema) (any, bool) {
	if len(fields) == 0 {
		return nil, false
	}
	value, ok := fields[name]
	if !ok {
		return nil, false
	}

	switch schema.Type {
	case "string":
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, false
		}
		return text, true
	case "array":
		items, ok := value.([]any)
		if !ok || len(items) == 0 {
			return nil, false
		}
		return items, true
	case "object":
		object, ok := value.(map[string]any)
		if !ok || len(object) == 0 {
			return nil, false
		}
		return object, true
	case "number":
		n, ok := value.(float64)
		return n, ok
	case "integer":
		n, ok := value.(float64)
		if !ok || math.Trunc(n) != n {
			return nil, false
		}
		return int(n), true
	case "boolean":
		b, ok := value.(bool)
		return b, ok
	case "enum":
		text, ok := value.(string)
		if !ok {
			return nil, false
		}
		for _, allowed := range schema.EnumValues {
			if strings.EqualFold(text, allowed) {
				return allowed, true
			}
		}
	}
	return nil, false
}

func exactValueForPatternField(name string, schema thinkcore.FieldSchema, primary string) (any, bool) {
	primary = strings.TrimSpace(primary)
	if primary == "" {
		return nil, false
	}

	switch schema.Type {
	case "string":
		if !workProductStringField(name) {
			return nil, false
		}
		return primary, true
	case "enum":
		for _, value := range schema.EnumValues {
			if strings.EqualFold(primary, value) {
				return value, true
			}
		}
		return nil, false
	}
	return nil, false
}

func workProductStringField(name string) bool {
	switch name {
	case "artifact", "claim", "decision", "description", "domainName", "hypothesis", "issue", "operation",
		"problem", "problemDefinition", "task", "thought", "timeFrame", "topic":
		return true
	default:
		return false
	}
}

func patternGateText(decision thinkcore.GateDecision) string {
	if decision.Reason != "" {
		return fmt.Sprintf("enforcement gate %s: %s", decision.Status, decision.Reason)
	}
	return fmt.Sprintf("enforcement gate %s", decision.Status)
}

func patternAdvisorText(rec thinkcore.Recommendation) string {
	if rec.Target != "" {
		return fmt.Sprintf("advisor recommends %s to %s: %s", rec.Action, rec.Target, rec.Reason)
	}
	return fmt.Sprintf("advisor recommends %s: %s", rec.Action, rec.Reason)
}

func gateConfidenceImpact(decision thinkcore.GateDecision) float64 {
	if decision.Status == "complete" {
		return 0.03
	}
	return -0.2
}

func advisorConfidenceImpact(rec thinkcore.Recommendation) float64 {
	if rec.Action == "switch" {
		return -0.05
	}
	return 0
}
