package think

import (
	"strings"
	"testing"
)

func TestComplexity_EmptyInput(t *testing.T) {
	score := CalculateComplexity("think", map[string]any{}, 60)
	// think is solo-only → bias = -50
	// All components 0 except bias: 0*0.3 + 0*0.3 + 0*0.2 + (-50)*0.2 = -10 → clamped to 0
	if score.Total != 0 {
		t.Errorf("Total = %d, want 0", score.Total)
	}
	if score.Recommendation != "solo" {
		t.Errorf("Recommendation = %q, want solo", score.Recommendation)
	}
}

func TestComplexity_TextLength(t *testing.T) {
	longText := strings.Repeat("x", 500)
	score := CalculateComplexity("critical_thinking", map[string]any{
		"issue": longText,
	}, 60)
	// textLength: 500/500*100 = 100, weight 0.3 → 30
	if score.TextLength != 100 {
		t.Errorf("TextLength = %d, want 100", score.TextLength)
	}
}

func TestComplexity_SubItemCount(t *testing.T) {
	score := CalculateComplexity("decision_framework", map[string]any{
		"options":  []any{"a", "b", "c"},
		"criteria": []any{"x", "y"},
	}, 60)
	// 5 items * 10 = 50
	if score.SubItemCount != 50 {
		t.Errorf("SubItemCount = %d, want 50", score.SubItemCount)
	}
}

func TestComplexity_StructuralDepth(t *testing.T) {
	nested := map[string]any{
		"level1": map[string]any{
			"level2": map[string]any{
				"level3": "value",
			},
		},
	}
	score := CalculateComplexity("think", nested, 60)
	// depth: level1→1, level2→2, level3 is string (no further nesting) = depth 2 * 25 = 50
	if score.StructuralDepth != 50 {
		t.Errorf("StructuralDepth = %d, want 50", score.StructuralDepth)
	}
}

func TestComplexity_PatternBias(t *testing.T) {
	// decision_framework has bias 30
	score := CalculateComplexity("decision_framework", map[string]any{}, 60)
	if score.PatternBias != 30 {
		t.Errorf("PatternBias = %d, want 30", score.PatternBias)
	}

	// think has no dialog config → bias -50
	score2 := CalculateComplexity("think", map[string]any{}, 60)
	if score2.PatternBias != -50 {
		t.Errorf("PatternBias = %d, want -50", score2.PatternBias)
	}
}

func TestComplexity_ConsensusRecommendation(t *testing.T) {
	longText := strings.Repeat("x", 1000)
	score := CalculateComplexity("decision_framework", map[string]any{
		"decision": longText,
		"options":  []any{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},
		"criteria": []any{"x", "y", "z"},
	}, 60)
	// textLength: 1000/500*100=200→cap 100, weight 0.3 → 30
	// subItems: 13*10=130→cap 100, weight 0.3 → 30
	// depth: 1*25=25, weight 0.2 → 5
	// bias: 30, weight 0.2 → 6
	// total: 30+30+5+6 = 71 → consensus
	if score.Recommendation != "consensus" {
		t.Errorf("Recommendation = %q, want consensus (total=%d)", score.Recommendation, score.Total)
	}
}

func TestComplexity_SoloForSimpleInput(t *testing.T) {
	score := CalculateComplexity("critical_thinking", map[string]any{
		"issue": "simple bug",
	}, 60)
	// textLength: 10/500*100=2, weight 0.3 → 0.6
	// subItems: 0, weight 0.3 → 0
	// depth: 0, weight 0.2 → 0
	// bias: 10, weight 0.2 → 2
	// total: ~3 → solo
	if score.Recommendation != "solo" {
		t.Errorf("Recommendation = %q, want solo (total=%d)", score.Recommendation, score.Total)
	}
}
