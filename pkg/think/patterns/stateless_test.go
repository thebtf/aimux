package patterns

import (
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
)

// allPatterns returns every stateless pattern for table-driven tests.
func allPatterns() []think.PatternHandler {
	return []think.PatternHandler{
		NewCriticalThinkingPattern(),
		NewDecisionFrameworkPattern(),
		NewProblemDecompositionPattern(),
		NewMentalModelPattern(),
		NewMetacognitiveMonitoringPattern(),
		NewRecursiveThinkingPattern(),
		NewDomainModelingPattern(),
		NewArchitectureAnalysisPattern(),
		NewStochasticAlgorithmPattern(),
		NewTemporalThinkingPattern(),
		NewVisualReasoningPattern(),
	}
}

// validInputFor returns a minimal valid input map for each pattern name.
func validInputFor(name string) map[string]any {
	switch name {
	case "critical_thinking":
		return map[string]any{"issue": "this confirms my hypothesis"}
	case "decision_framework":
		return map[string]any{
			"decision": "choose a database",
			"criteria": []any{
				map[string]any{"name": "speed", "weight": 0.7},
				map[string]any{"name": "cost", "weight": 0.3},
			},
			"options": []any{
				map[string]any{"name": "postgres", "scores": map[string]any{"speed": 8.0, "cost": 6.0}},
				map[string]any{"name": "sqlite", "scores": map[string]any{"speed": 6.0, "cost": 9.0}},
			},
		}
	case "problem_decomposition":
		return map[string]any{"problem": "scale the API"}
	case "mental_model":
		return map[string]any{"modelName": "first_principles", "problem": "reduce costs"}
	case "metacognitive_monitoring":
		return map[string]any{"task": "evaluate options"}
	case "recursive_thinking":
		return map[string]any{"problem": "parse nested structures"}
	case "domain_modeling":
		return map[string]any{"domainName": "e-commerce"}
	case "architecture_analysis":
		return map[string]any{
			"components": []any{
				map[string]any{"name": "api", "dependencies": []any{"db", "cache"}},
				map[string]any{"name": "worker", "dependencies": []any{"db"}},
				map[string]any{"name": "db"},
				map[string]any{"name": "cache"},
			},
		}
	case "stochastic_algorithm":
		return map[string]any{"algorithmType": "mcts", "problemDefinition": "game tree search"}
	case "temporal_thinking":
		return map[string]any{"timeFrame": "Q1 2026"}
	case "visual_reasoning":
		return map[string]any{"operation": "layout"}
	default:
		return nil
	}
}

// requiredFieldFor returns the name of one required field for each pattern.
func requiredFieldFor(name string) string {
	switch name {
	case "critical_thinking":
		return "issue"
	case "decision_framework":
		return "decision"
	case "problem_decomposition":
		return "problem"
	case "mental_model":
		return "modelName"
	case "metacognitive_monitoring":
		return "task"
	case "recursive_thinking":
		return "problem"
	case "domain_modeling":
		return "domainName"
	case "architecture_analysis":
		return "components"
	case "stochastic_algorithm":
		return "algorithmType"
	case "temporal_thinking":
		return "timeFrame"
	case "visual_reasoning":
		return "operation"
	default:
		return ""
	}
}

func TestAllPatterns_NameNotEmpty(t *testing.T) {
	for _, p := range allPatterns() {
		if p.Name() == "" {
			t.Errorf("pattern has empty Name()")
		}
		if p.Description() == "" {
			t.Errorf("pattern %s has empty Description()", p.Name())
		}
	}
}

func TestAllPatterns_ValidateSuccess(t *testing.T) {
	for _, p := range allPatterns() {
		t.Run(p.Name(), func(t *testing.T) {
			input := validInputFor(p.Name())
			if input == nil {
				t.Fatalf("no valid input defined for %s", p.Name())
			}
			out, err := p.Validate(input)
			if err != nil {
				t.Fatalf("Validate() failed: %v", err)
			}
			if out == nil {
				t.Fatal("Validate() returned nil map")
			}
		})
	}
}

func TestAllPatterns_ValidateFailsMissingRequired(t *testing.T) {
	for _, p := range allPatterns() {
		t.Run(p.Name(), func(t *testing.T) {
			field := requiredFieldFor(p.Name())
			if field == "" {
				t.Skipf("no required field defined for %s", p.Name())
			}
			// Empty input — missing the required field.
			_, err := p.Validate(map[string]any{})
			if err == nil {
				t.Fatalf("Validate() should fail when '%s' is missing", field)
			}
		})
	}
}

func TestAllPatterns_HandleReturnsData(t *testing.T) {
	for _, p := range allPatterns() {
		t.Run(p.Name(), func(t *testing.T) {
			input := validInputFor(p.Name())
			validated, err := p.Validate(input)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			result, err := p.Handle(validated, "test-session")
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if result == nil {
				t.Fatal("Handle returned nil")
			}
			if result.Pattern != p.Name() {
				t.Errorf("Pattern = %q, want %q", result.Pattern, p.Name())
			}
			if result.Status != "success" {
				t.Errorf("Status = %q, want success", result.Status)
			}
			if len(result.Data) == 0 {
				t.Error("Data is empty")
			}
		})
	}
}

// --- Pattern-specific tests ---

func TestCriticalThinking_BiasDetection(t *testing.T) {
	p := NewCriticalThinkingPattern()
	input := map[string]any{"issue": "This confirms my hypothesis, as expected"}
	validated, _ := p.Validate(input)
	result, _ := p.Handle(validated, "s1")

	biasCount, ok := result.Data["biasCount"].(int)
	if !ok || biasCount == 0 {
		t.Fatal("expected at least one bias detected")
	}
	biases, ok := result.Data["detectedBiases"].([]map[string]any)
	if !ok || len(biases) == 0 {
		t.Fatal("detectedBiases should be non-empty")
	}
	found := false
	for _, b := range biases {
		if b["bias"] == "confirmation_bias" {
			found = true
		}
	}
	if !found {
		t.Error("expected confirmation_bias to be detected")
	}
}

func TestDecisionFramework_Ranking(t *testing.T) {
	p := NewDecisionFrameworkPattern()
	input := map[string]any{
		"decision": "pick language",
		"criteria": []any{
			map[string]any{"name": "speed", "weight": 1.0},
		},
		"options": []any{
			map[string]any{"name": "go", "scores": map[string]any{"speed": 9.0}},
			map[string]any{"name": "python", "scores": map[string]any{"speed": 5.0}},
		},
	}
	validated, _ := p.Validate(input)
	result, _ := p.Handle(validated, "s1")

	ranked, ok := result.Data["rankedOptions"].([]any)
	if !ok || len(ranked) != 2 {
		t.Fatalf("expected 2 ranked options, got %v", result.Data["rankedOptions"])
	}
	first := ranked[0].(map[string]any)
	if first["name"] != "go" {
		t.Errorf("expected 'go' first, got %v", first["name"])
	}
	if result.Data["hasTies"] != false {
		t.Error("expected no ties")
	}
}

func TestMentalModel_KnownVsUnknown(t *testing.T) {
	p := NewMentalModelPattern()

	// Known model.
	input := map[string]any{"modelName": "inversion", "problem": "test"}
	validated, _ := p.Validate(input)
	result, _ := p.Handle(validated, "s1")
	if result.Data["known"] != true {
		t.Error("expected 'inversion' to be known")
	}
	if result.Data["description"] == "custom model" {
		t.Error("known model should not have 'custom model' description")
	}

	// Unknown model.
	input2 := map[string]any{"modelName": "my_custom_model", "problem": "test"}
	validated2, _ := p.Validate(input2)
	result2, _ := p.Handle(validated2, "s1")
	if result2.Data["known"] != false {
		t.Error("expected 'my_custom_model' to be unknown")
	}
	if result2.Data["description"] != "custom model" {
		t.Errorf("unknown model description = %v, want 'custom model'", result2.Data["description"])
	}
}

func TestMetacognitiveMonitoring_OverconfidenceDetection(t *testing.T) {
	p := NewMetacognitiveMonitoringPattern()

	// High confidence, few claims → warning.
	input := map[string]any{
		"task":       "evaluate",
		"confidence": 0.95,
		"claims":     []any{"one claim"},
	}
	validated, _ := p.Validate(input)
	result, _ := p.Handle(validated, "s1")
	warning, ok := result.Data["overconfidenceWarning"].(string)
	if !ok || warning == "" {
		t.Error("expected overconfidence warning for high confidence + few claims")
	}

	// High confidence, enough claims → no warning.
	input2 := map[string]any{
		"task":       "evaluate",
		"confidence": 0.95,
		"claims":     []any{"a", "b", "c"},
	}
	validated2, _ := p.Validate(input2)
	result2, _ := p.Handle(validated2, "s1")
	warning2 := result2.Data["overconfidenceWarning"].(string)
	if warning2 != "" {
		t.Error("expected no warning when claims >= 3")
	}
}

func TestArchitectureAnalysis_CouplingDetection(t *testing.T) {
	p := NewArchitectureAnalysisPattern()
	input := map[string]any{
		"components": []any{
			map[string]any{"name": "api", "dependencies": []any{"db"}},
			map[string]any{"name": "worker", "dependencies": []any{"db"}},
			map[string]any{"name": "scheduler", "dependencies": []any{"db"}},
			map[string]any{"name": "db"},
		},
	}
	validated, _ := p.Validate(input)
	result, _ := p.Handle(validated, "s1")

	if result.Data["couplingDetected"] != true {
		t.Error("expected coupling detected for 'db' (3 dependents)")
	}
	coupled, ok := result.Data["highlyCoupled"].([]map[string]any)
	if !ok || len(coupled) == 0 {
		t.Fatal("expected highlyCoupled to be non-empty")
	}
	found := false
	for _, c := range coupled {
		if c["component"] == "db" {
			found = true
			if c["dependents"].(int) < 2 {
				t.Error("db should have >= 2 dependents")
			}
		}
	}
	if !found {
		t.Error("expected 'db' in highlyCoupled list")
	}
}

func TestArchitectureAnalysis_StringComponents(t *testing.T) {
	p := NewArchitectureAnalysisPattern()
	input := map[string]any{
		"components": []any{"frontend", "backend", "database"},
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed for string components: %v", err)
	}
	result, err := p.Handle(validated, "s1")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
	if result.Data["componentCount"] != 3 {
		t.Errorf("componentCount = %v, want 3", result.Data["componentCount"])
	}
}

func TestStochasticAlgorithm_InvalidType(t *testing.T) {
	p := NewStochasticAlgorithmPattern()
	input := map[string]any{"algorithmType": "invalid", "problemDefinition": "test"}
	_, err := p.Validate(input)
	if err == nil {
		t.Fatal("expected validation error for invalid algorithmType")
	}
}
