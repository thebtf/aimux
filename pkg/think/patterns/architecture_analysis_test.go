package patterns

import (
	"testing"
)

// helpers to build validated input and invoke Handle.

func buildComponents(entries []map[string]any) map[string]any {
	p := NewArchitectureAnalysisPattern()
	raw := make([]any, len(entries))
	for i, e := range entries {
		raw[i] = e
	}
	validated, err := p.Validate(map[string]any{"components": raw})
	if err != nil {
		panic("unexpected validation error: " + err.Error())
	}
	return validated
}

func runHandle(input map[string]any) map[string]any {
	p := NewArchitectureAnalysisPattern()
	result, err := p.Handle(input, "test-session")
	if err != nil {
		panic("unexpected handle error: " + err.Error())
	}
	return result.Data
}

func getMetric(data map[string]any, name string) map[string]any {
	metrics, ok := data["componentMetrics"].([]any)
	if !ok {
		return nil
	}
	for _, m := range metrics {
		mm := m.(map[string]any)
		if mm["component"].(string) == name {
			return mm
		}
	}
	return nil
}

// TestArchAnalysis_Instability verifies that a component with many outgoing
// dependencies and no incoming ones gets instability close to 1.0.
//
// Architecture:
//
//	Client → [ServiceA, ServiceB, ServiceC]   (Ce=3, Ca=0 → instability=1.0)
//	ServiceA, ServiceB, ServiceC have no deps (Ce=0, Ca=1 → instability=0.0)
func TestArchAnalysis_Instability(t *testing.T) {
	input := buildComponents([]map[string]any{
		{"name": "Client", "dependencies": []any{"ServiceA", "ServiceB", "ServiceC"}},
		{"name": "ServiceA"},
		{"name": "ServiceB"},
		{"name": "ServiceC"},
	})
	data := runHandle(input)

	m := getMetric(data, "Client")
	if m == nil {
		t.Fatal("metric for Client not found")
	}
	ce := m["ce"].(int)
	ca := m["ca"].(int)
	instability := m["instability"].(float64)

	if ce != 3 {
		t.Errorf("Client Ce: want 3, got %d", ce)
	}
	if ca != 0 {
		t.Errorf("Client Ca: want 0, got %d", ca)
	}
	if instability != 1.0 {
		t.Errorf("Client instability: want 1.0, got %f", instability)
	}

	// mostUnstable must be Client
	if data["mostUnstable"] != "Client" {
		t.Errorf("mostUnstable: want Client, got %v", data["mostUnstable"])
	}
}

// TestArchAnalysis_MostDepended verifies that the component with the highest Ca
// is correctly identified as mostDepended.
//
// Architecture:
//
//	A → DB, B → DB, C → DB    (DB has Ca=3)
func TestArchAnalysis_MostDepended(t *testing.T) {
	input := buildComponents([]map[string]any{
		{"name": "A", "dependencies": []any{"DB"}},
		{"name": "B", "dependencies": []any{"DB"}},
		{"name": "C", "dependencies": []any{"DB"}},
		{"name": "DB"},
	})
	data := runHandle(input)

	m := getMetric(data, "DB")
	if m == nil {
		t.Fatal("metric for DB not found")
	}
	if m["ca"].(int) != 3 {
		t.Errorf("DB Ca: want 3, got %d", m["ca"].(int))
	}
	if m["ce"].(int) != 0 {
		t.Errorf("DB Ce: want 0, got %d", m["ce"].(int))
	}
	if data["mostDepended"] != "DB" {
		t.Errorf("mostDepended: want DB, got %v", data["mostDepended"])
	}
}

// TestArchAnalysis_Stable verifies that a component with no outgoing or incoming
// dependencies yields instability=0 (Ca+Ce=0 edge case).
func TestArchAnalysis_Stable(t *testing.T) {
	input := buildComponents([]map[string]any{
		{"name": "Standalone"},
		{"name": "Other"},
	})
	data := runHandle(input)

	m := getMetric(data, "Standalone")
	if m == nil {
		t.Fatal("metric for Standalone not found")
	}
	if m["instability"].(float64) != 0.0 {
		t.Errorf("Standalone instability: want 0.0, got %f", m["instability"].(float64))
	}
}

// TestArchAnalysis_AntiStub verifies that two architecturally distinct graphs
// produce measurably different instability values, proving the metric is computed
// from input rather than being a constant.
func TestArchAnalysis_AntiStub(t *testing.T) {
	// Architecture 1: Hub depends on nothing (stable, instability=0).
	stableInput := buildComponents([]map[string]any{
		{"name": "Hub"},
		{"name": "Leaf1", "dependencies": []any{"Hub"}},
		{"name": "Leaf2", "dependencies": []any{"Hub"}},
	})
	stableData := runHandle(stableInput)

	// Architecture 2: Hub depends on many things (unstable, instability→1).
	unstableInput := buildComponents([]map[string]any{
		{"name": "Hub", "dependencies": []any{"A", "B", "C", "D"}},
		{"name": "A"},
		{"name": "B"},
		{"name": "C"},
		{"name": "D"},
	})
	unstableData := runHandle(unstableInput)

	hubStable := getMetric(stableData, "Hub")
	hubUnstable := getMetric(unstableData, "Hub")

	if hubStable == nil || hubUnstable == nil {
		t.Fatal("Hub metrics missing")
	}

	stableVal := hubStable["instability"].(float64)
	unstableVal := hubUnstable["instability"].(float64)

	if stableVal >= unstableVal {
		t.Errorf("expected stable Hub instability (%f) < unstable Hub instability (%f)", stableVal, unstableVal)
	}

	// Concrete checks: stable Hub has Ce=0 (nothing it depends on), unstable Hub has Ce=4.
	if hubStable["ce"].(int) != 0 {
		t.Errorf("stable Hub Ce: want 0, got %d", hubStable["ce"].(int))
	}
	if hubUnstable["ce"].(int) != 4 {
		t.Errorf("unstable Hub Ce: want 4, got %d", hubUnstable["ce"].(int))
	}
}
