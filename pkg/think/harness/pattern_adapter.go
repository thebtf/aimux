package harness

import (
	"context"
	"fmt"

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

	input := inputForPattern(handler.SchemaFields(), workProduct)
	valid, err := handler.Validate(input)
	if err != nil {
		return PatternExecution{}, err
	}
	result, err := handler.Handle(valid, sessionID)
	if err != nil {
		return PatternExecution{}, err
	}

	summary := thinkcore.GenerateSummary(result, "solo")
	return PatternExecution{
		Summary: summary,
		Data:    result.Data,
		LedgerAdds: KnowledgeLedger{
			Checkable: []LedgerEntry{{
				ID:     "move_output",
				Text:   summary,
				Source: move.Pattern,
				Status: "observed",
			}},
		},
		ConfidenceFactors: []ConfidenceFactor{{
			Name:   "move_execution",
			Impact: 0.05,
			Reason: "selected cognitive move executed through the low-level pattern adapter",
		}},
	}, nil
}

func inputForPattern(fields map[string]thinkcore.FieldSchema, primary string) map[string]any {
	input := make(map[string]any, len(fields))
	for name, schema := range fields {
		if !schema.Required {
			continue
		}
		input[name] = sampleValueForPatternField(schema, primary)
	}
	return input
}

func sampleValueForPatternField(schema thinkcore.FieldSchema, primary string) any {
	switch schema.Type {
	case "array":
		return []any{primary}
	case "object":
		return map[string]any{"value": primary}
	case "number", "integer":
		return 1
	case "boolean":
		return true
	case "enum":
		if len(schema.EnumValues) > 0 {
			return schema.EnumValues[0]
		}
		return primary
	default:
		return primary
	}
}
