package patterns

import (
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
)

// TestAntiStub_AllRestoredPatterns verifies that each of the 8 restored patterns
// computes genuinely different outputs for different inputs — not stubs that ignore
// their parameters or echo back the input unchanged.
func TestAntiStub_AllRestoredPatterns(t *testing.T) {
	think.ClearSessions()

	// -------------------------------------------------------------------------
	// 1. problem_decomposition — hasCycle must differ between acyclic and cyclic
	// -------------------------------------------------------------------------
	t.Run("problem_decomposition", func(t *testing.T) {
		p := NewProblemDecompositionPattern()

		// Input A: acyclic chain A→B→C
		inputA, err := p.Validate(map[string]any{
			"problem": "plan a feature",
			"dependencies": []any{
				map[string]any{"from": "A", "to": "B"},
				map[string]any{"from": "B", "to": "C"},
			},
		})
		if err != nil {
			t.Fatalf("Validate A: %v", err)
		}
		rA, err := p.Handle(inputA, "stub-decomp-A")
		if err != nil {
			t.Fatalf("Handle A: %v", err)
		}

		// Input B: cyclic A→B→C→A
		inputB, err := p.Validate(map[string]any{
			"problem": "plan a feature",
			"dependencies": []any{
				map[string]any{"from": "A", "to": "B"},
				map[string]any{"from": "B", "to": "C"},
				map[string]any{"from": "C", "to": "A"},
			},
		})
		if err != nil {
			t.Fatalf("Validate B: %v", err)
		}
		rB, err := p.Handle(inputB, "stub-decomp-B")
		if err != nil {
			t.Fatalf("Handle B: %v", err)
		}

		dagA, ok := rA.Data["dag"].(map[string]any)
		if !ok {
			t.Fatal("input A: expected 'dag' key in result data")
		}
		dagB, ok := rB.Data["dag"].(map[string]any)
		if !ok {
			t.Fatal("input B: expected 'dag' key in result data")
		}

		hasCycleA, _ := dagA["hasCycle"].(bool)
		hasCycleB, _ := dagB["hasCycle"].(bool)

		if hasCycleA == hasCycleB {
			t.Errorf("anti-stub: dag.hasCycle must differ — acyclic=%v cyclic=%v", hasCycleA, hasCycleB)
		}
		if hasCycleA {
			t.Errorf("anti-stub: acyclic input must produce hasCycle=false, got true")
		}
		if !hasCycleB {
			t.Errorf("anti-stub: cyclic input must produce hasCycle=true, got false")
		}
	})

	// -------------------------------------------------------------------------
	// 2. domain_modeling — consistent must differ when orphan entity is present
	// -------------------------------------------------------------------------
	t.Run("domain_modeling", func(t *testing.T) {
		p := NewDomainModelingPattern()

		// Input A: all entities appear in relationships → consistent=true
		inputA, err := p.Validate(map[string]any{
			"domainName": "order system",
			"entities": []any{
				map[string]any{"name": "Order"},
				map[string]any{"name": "Customer"},
			},
			"relationships": []any{
				map[string]any{"from": "Order", "to": "Customer"},
			},
		})
		if err != nil {
			t.Fatalf("Validate A: %v", err)
		}
		rA, err := p.Handle(inputA, "stub-domain-A")
		if err != nil {
			t.Fatalf("Handle A: %v", err)
		}

		// Input B: "Product" entity is orphan (not in any relationship) → consistent=false
		inputB, err := p.Validate(map[string]any{
			"domainName": "order system",
			"entities": []any{
				map[string]any{"name": "Order"},
				map[string]any{"name": "Customer"},
				map[string]any{"name": "Product"},
			},
			"relationships": []any{
				map[string]any{"from": "Order", "to": "Customer"},
			},
		})
		if err != nil {
			t.Fatalf("Validate B: %v", err)
		}
		rB, err := p.Handle(inputB, "stub-domain-B")
		if err != nil {
			t.Fatalf("Handle B: %v", err)
		}

		consistentA, _ := rA.Data["consistent"].(bool)
		consistentB, _ := rB.Data["consistent"].(bool)

		if consistentA == consistentB {
			t.Errorf("anti-stub: consistent must differ — all-connected=%v orphan-present=%v", consistentA, consistentB)
		}
		if !consistentA {
			t.Errorf("anti-stub: fully-connected domain must be consistent=true, got false")
		}
		if consistentB {
			t.Errorf("anti-stub: domain with orphan entity must be consistent=false, got true")
		}
	})

	// -------------------------------------------------------------------------
	// 3. architecture_analysis — X instability must differ based on dependency direction
	// -------------------------------------------------------------------------
	t.Run("architecture_analysis", func(t *testing.T) {
		p := NewArchitectureAnalysisPattern()

		// Input A: X depends on Y (Ce=1, Ca=0) → instability(X) = 1.0
		rawA := []any{
			map[string]any{"name": "X", "dependencies": []any{"Y"}},
			map[string]any{"name": "Y", "dependencies": []any{}},
		}
		validA, err := p.Validate(map[string]any{"components": rawA})
		if err != nil {
			t.Fatalf("Validate A: %v", err)
		}
		rA, err := p.Handle(validA, "stub-arch-A")
		if err != nil {
			t.Fatalf("Handle A: %v", err)
		}

		// Input B: Y depends on X (Ce=0, Ca=1 for X) → instability(X) = 0/(0+1) = 0.0
		rawB := []any{
			map[string]any{"name": "X", "dependencies": []any{}},
			map[string]any{"name": "Y", "dependencies": []any{"X"}},
		}
		validB, err := p.Validate(map[string]any{"components": rawB})
		if err != nil {
			t.Fatalf("Validate B: %v", err)
		}
		rB, err := p.Handle(validB, "stub-arch-B")
		if err != nil {
			t.Fatalf("Handle B: %v", err)
		}

		instabilityA := xInstability(t, rA.Data, "X")
		instabilityB := xInstability(t, rB.Data, "X")

		if instabilityA == instabilityB {
			t.Errorf("anti-stub: X instability must differ — A=%v B=%v", instabilityA, instabilityB)
		}
		if instabilityA != 1.0 {
			t.Errorf("anti-stub: X (Ce=1,Ca=0) must have instability=1.0, got %v", instabilityA)
		}
		if instabilityB != 0.0 {
			t.Errorf("anti-stub: X (Ce=0,Ca=1) must have instability=0.0, got %v", instabilityB)
		}
	})

	// -------------------------------------------------------------------------
	// 4. stochastic_algorithm — expectedValue must differ for different outcome sets
	// -------------------------------------------------------------------------
	t.Run("stochastic_algorithm", func(t *testing.T) {
		p := NewStochasticAlgorithmPattern()

		// Input A: EV = 0.5*100 + 0.5*0 = 50
		inputA, err := p.Validate(map[string]any{
			"algorithmType":     "bandit",
			"problemDefinition": "test scenario A",
			"parameters": map[string]any{
				"outcomes": []any{
					map[string]any{"probability": 0.5, "value": 100.0},
					map[string]any{"probability": 0.5, "value": 0.0},
				},
			},
		})
		if err != nil {
			t.Fatalf("Validate A: %v", err)
		}
		rA, err := p.Handle(inputA, "stub-stoch-A")
		if err != nil {
			t.Fatalf("Handle A: %v", err)
		}

		// Input B: EV = 0.9*10 + 0.1*100 = 9 + 10 = 19
		inputB, err := p.Validate(map[string]any{
			"algorithmType":     "bandit",
			"problemDefinition": "test scenario B",
			"parameters": map[string]any{
				"outcomes": []any{
					map[string]any{"probability": 0.9, "value": 10.0},
					map[string]any{"probability": 0.1, "value": 100.0},
				},
			},
		})
		if err != nil {
			t.Fatalf("Validate B: %v", err)
		}
		rB, err := p.Handle(inputB, "stub-stoch-B")
		if err != nil {
			t.Fatalf("Handle B: %v", err)
		}

		evA, okA := rA.Data["expectedValue"].(float64)
		evB, okB := rB.Data["expectedValue"].(float64)
		if !okA {
			t.Fatal("anti-stub: input A must produce 'expectedValue' field")
		}
		if !okB {
			t.Fatal("anti-stub: input B must produce 'expectedValue' field")
		}
		if evA == evB {
			t.Errorf("anti-stub: expectedValue must differ — A=%v B=%v", evA, evB)
		}
		if evA != 50.0 {
			t.Errorf("anti-stub: input A expectedValue must be 50.0, got %v", evA)
		}
		if evB != 19.0 {
			t.Errorf("anti-stub: input B expectedValue must be 19.0, got %v", evB)
		}
	})

	// -------------------------------------------------------------------------
	// 5. temporal_thinking — longestGap.duration must differ based on event spacing
	// -------------------------------------------------------------------------
	t.Run("temporal_thinking", func(t *testing.T) {
		p := NewTemporalThinkingPattern()

		// Input A: events at t=1,2,3 → longestGap.duration = 1
		inputA, err := p.Validate(map[string]any{
			"timeFrame": "day",
			"events": []any{
				map[string]any{"name": "e1", "time": 1.0},
				map[string]any{"name": "e2", "time": 2.0},
				map[string]any{"name": "e3", "time": 3.0},
			},
		})
		if err != nil {
			t.Fatalf("Validate A: %v", err)
		}
		rA, err := p.Handle(inputA, "stub-temporal-A")
		if err != nil {
			t.Fatalf("Handle A: %v", err)
		}

		// Input B: events at t=1,100,101 → longestGap.duration = 99
		inputB, err := p.Validate(map[string]any{
			"timeFrame": "day",
			"events": []any{
				map[string]any{"name": "e1", "time": 1.0},
				map[string]any{"name": "e2", "time": 100.0},
				map[string]any{"name": "e3", "time": 101.0},
			},
		})
		if err != nil {
			t.Fatalf("Validate B: %v", err)
		}
		rB, err := p.Handle(inputB, "stub-temporal-B")
		if err != nil {
			t.Fatalf("Handle B: %v", err)
		}

		gapA, okA := rA.Data["longestGap"].(map[string]any)
		gapB, okB := rB.Data["longestGap"].(map[string]any)
		if !okA {
			t.Fatal("anti-stub: input A must produce 'longestGap' field")
		}
		if !okB {
			t.Fatal("anti-stub: input B must produce 'longestGap' field")
		}

		durA, _ := gapA["duration"].(float64)
		durB, _ := gapB["duration"].(float64)

		if durA == durB {
			t.Errorf("anti-stub: longestGap.duration must differ — A=%v B=%v", durA, durB)
		}
		if durA != 1.0 {
			t.Errorf("anti-stub: evenly-spaced events must have longestGap.duration=1.0, got %v", durA)
		}
		if durB != 99.0 {
			t.Errorf("anti-stub: sparse events must have longestGap.duration=99.0, got %v", durB)
		}
	})

	// -------------------------------------------------------------------------
	// 6. collaborative_reasoning — participationBalance must differ
	// -------------------------------------------------------------------------
	t.Run("collaborative_reasoning", func(t *testing.T) {
		p := NewCollaborativeReasoningPattern()

		// Input A: 2 equal contributions (1 each from Alpha and Beta) → balance=1.0
		sidA := "stub-collab-A"
		for _, persona := range []string{"Alpha", "Beta"} {
			inp, err := p.Validate(map[string]any{
				"topic": "architecture decision",
				"stage": "ideation",
				"contribution": map[string]any{
					"type":    "insight",
					"text":    "this is an insight from " + persona,
					"persona": persona,
				},
			})
			if err != nil {
				t.Fatalf("Validate collab A (%s): %v", persona, err)
			}
			if _, err := p.Handle(inp, sidA); err != nil {
				t.Fatalf("Handle collab A (%s): %v", persona, err)
			}
		}
		// Read final state
		inpFinalA, _ := p.Validate(map[string]any{"topic": "architecture decision"})
		rA, err := p.Handle(inpFinalA, sidA)
		if err != nil {
			t.Fatalf("Handle collab A (final): %v", err)
		}

		// Input B: 3 contributions from "Alice", 0 from "Bob" → balance < 1.0
		sidB := "stub-collab-B"
		for i := 0; i < 3; i++ {
			inp, err := p.Validate(map[string]any{
				"topic": "architecture decision",
				"stage": "ideation",
				"contribution": map[string]any{
					"type":    "suggestion",
					"text":    "suggestion from Alice",
					"persona": "Alice",
				},
			})
			if err != nil {
				t.Fatalf("Validate collab B (Alice %d): %v", i, err)
			}
			if _, err := p.Handle(inp, sidB); err != nil {
				t.Fatalf("Handle collab B (Alice %d): %v", i, err)
			}
		}
		// Bob contributes once so participationBalance includes him
		inpBob, err := p.Validate(map[string]any{
			"topic": "architecture decision",
			"stage": "ideation",
			"contribution": map[string]any{
				"type":    "observation",
				"text":    "observation from Bob",
				"persona": "Bob",
			},
		})
		if err != nil {
			t.Fatalf("Validate collab B (Bob): %v", err)
		}
		rB, err := p.Handle(inpBob, sidB)
		if err != nil {
			t.Fatalf("Handle collab B (Bob): %v", err)
		}

		balanceA, okA := rA.Data["participationBalance"].(float64)
		balanceB, okB := rB.Data["participationBalance"].(float64)
		if !okA {
			t.Fatal("anti-stub: collab A must produce 'participationBalance' field")
		}
		if !okB {
			t.Fatal("anti-stub: collab B must produce 'participationBalance' field")
		}
		if balanceA == balanceB {
			t.Errorf("anti-stub: participationBalance must differ — equal=%v skewed=%v", balanceA, balanceB)
		}
		if balanceA != 1.0 {
			t.Errorf("anti-stub: equal contributions must yield balance=1.0, got %v", balanceA)
		}
		if balanceB >= 1.0 {
			t.Errorf("anti-stub: skewed contributions must yield balance<1.0, got %v", balanceB)
		}
	})

	// -------------------------------------------------------------------------
	// 7. sequential_thinking — stage must differ for thought 1-of-5 vs 5-of-5
	// -------------------------------------------------------------------------
	t.Run("sequential_thinking", func(t *testing.T) {
		p := NewSequentialThinkingPattern()

		// Input A: thought 1 of 5 → stage="initial"
		sidA := "stub-seq-A"
		inputA, err := p.Validate(map[string]any{
			"thought":       "first thought about the problem",
			"thoughtNumber": 1,
			"totalThoughts": 5,
		})
		if err != nil {
			t.Fatalf("Validate A: %v", err)
		}
		rA, err := p.Handle(inputA, sidA)
		if err != nil {
			t.Fatalf("Handle A: %v", err)
		}

		// Input B: thought 5 of 5 → stage="final"
		sidB := "stub-seq-B"
		inputB, err := p.Validate(map[string]any{
			"thought":       "final thought concluding the analysis",
			"thoughtNumber": 5,
			"totalThoughts": 5,
		})
		if err != nil {
			t.Fatalf("Validate B: %v", err)
		}
		rB, err := p.Handle(inputB, sidB)
		if err != nil {
			t.Fatalf("Handle B: %v", err)
		}

		stageA, _ := rA.Data["stage"].(string)
		stageB, _ := rB.Data["stage"].(string)

		if stageA == stageB {
			t.Errorf("anti-stub: stage must differ — thought1=%v thought5=%v", stageA, stageB)
		}
		if stageA != "initial" {
			t.Errorf("anti-stub: thought 1-of-5 must produce stage=initial, got %q", stageA)
		}
		if stageB != "final" {
			t.Errorf("anti-stub: thought 5-of-5 must produce stage=final, got %q", stageB)
		}
	})

	// -------------------------------------------------------------------------
	// 8. scientific_method — stageHistoryLen and entry types must differ
	// -------------------------------------------------------------------------
	t.Run("scientific_method", func(t *testing.T) {
		p := NewScientificMethodPattern()

		// Input A: single call at stage="hypothesis", adds a hypothesis entry
		// → stageHistoryLen=1, entryCount["hypothesis"]=1, entryCount["experiment"]=0
		sidA := "stub-sci-A"
		inputA, err := p.Validate(map[string]any{
			"stage": "hypothesis",
			"entry": map[string]any{
				"type": "hypothesis",
				"text": "plants grow faster with blue light",
			},
		})
		if err != nil {
			t.Fatalf("Validate A: %v", err)
		}
		rA, err := p.Handle(inputA, sidA)
		if err != nil {
			t.Fatalf("Handle A: %v", err)
		}

		// Input B: two calls — hypothesis then prediction (linked) → stageHistoryLen=2,
		// entryCount["hypothesis"]=1, entryCount["prediction"]=1
		sidB := "stub-sci-B"
		inputB1, err := p.Validate(map[string]any{
			"stage": "hypothesis",
			"entry": map[string]any{
				"type": "hypothesis",
				"text": "plants grow faster with blue light",
			},
		})
		if err != nil {
			t.Fatalf("Validate B1: %v", err)
		}
		rB1, err := p.Handle(inputB1, sidB)
		if err != nil {
			t.Fatalf("Handle B1: %v", err)
		}
		hypoID := rB1.Data["entry"].(map[string]any)["id"].(string)

		inputB2, err := p.Validate(map[string]any{
			"stage": "experiment",
			"entry": map[string]any{
				"type":     "prediction",
				"text":     "growth will be 20% higher under blue light",
				"linkedTo": hypoID,
			},
		})
		if err != nil {
			t.Fatalf("Validate B2: %v", err)
		}
		rB, err := p.Handle(inputB2, sidB)
		if err != nil {
			t.Fatalf("Handle B2: %v", err)
		}

		histLenA, _ := rA.Data["stageHistoryLen"].(int)
		histLenB, _ := rB.Data["stageHistoryLen"].(int)

		if histLenA == histLenB {
			t.Errorf("anti-stub: stageHistoryLen must differ — A=%v B=%v", histLenA, histLenB)
		}
		if histLenA != 1 {
			t.Errorf("anti-stub: single-call session must have stageHistoryLen=1, got %v", histLenA)
		}
		if histLenB != 2 {
			t.Errorf("anti-stub: two-call session must have stageHistoryLen=2, got %v", histLenB)
		}

		// Verify entry type breakdown differs between the two sessions
		countA := rA.Data["entryCount"].(map[string]int)
		countB := rB.Data["entryCount"].(map[string]int)

		if countA["hypothesis"] != 1 {
			t.Errorf("anti-stub: session A must have 1 hypothesis entry, got %v", countA["hypothesis"])
		}
		if countA["prediction"] != 0 {
			t.Errorf("anti-stub: session A must have 0 prediction entries, got %v", countA["prediction"])
		}
		if countB["hypothesis"] != 1 {
			t.Errorf("anti-stub: session B must have 1 hypothesis entry, got %v", countB["hypothesis"])
		}
		if countB["prediction"] != 1 {
			t.Errorf("anti-stub: session B must have 1 prediction entry, got %v", countB["prediction"])
		}
	})
}

// xInstability extracts the instability value for a named component from componentMetrics.
func xInstability(t *testing.T, data map[string]any, name string) float64 {
	t.Helper()
	metrics, ok := data["componentMetrics"].([]any)
	if !ok {
		t.Fatalf("componentMetrics missing or wrong type in data")
	}
	for _, m := range metrics {
		entry, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if entry["component"] == name {
			v, ok := entry["instability"].(float64)
			if !ok {
				t.Fatalf("instability for component %q is not float64: %T", name, entry["instability"])
			}
			return v
		}
	}
	t.Fatalf("component %q not found in componentMetrics", name)
	return 0
}
