package patterns

import (
	"testing"
)

// makeEvent builds a simple event map with a "timestamp" field.
func makeEvent(ts float64, name string) map[string]any {
	return map[string]any{"timestamp": ts, "name": name}
}

func TestTemporal_Timeline(t *testing.T) {
	p := NewTemporalThinkingPattern()

	input := map[string]any{
		"timeFrame": "Q1 2024",
		"events": []any{
			makeEvent(30.0, "c"),
			makeEvent(10.0, "a"),
			makeEvent(20.0, "b"),
		},
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-session")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	data := result.Data
	sortedRaw, ok := data["sortedEvents"]
	if !ok {
		t.Fatal("sortedEvents missing from result")
	}
	sorted, ok := sortedRaw.([]map[string]any)
	if !ok {
		t.Fatalf("sortedEvents has unexpected type %T", sortedRaw)
	}
	if len(sorted) != 3 {
		t.Fatalf("expected 3 sorted events, got %d", len(sorted))
	}

	// Verify chronological order: timestamps should be 10, 20, 30.
	expectedTS := []float64{10, 20, 30}
	for i, ev := range sorted {
		ts, ok := ev["timestamp"].(float64)
		if !ok {
			t.Fatalf("sorted[%d] timestamp not float64", i)
		}
		if ts != expectedTS[i] {
			t.Errorf("sorted[%d] timestamp: got %v, want %v", i, ts, expectedTS[i])
		}
	}

	// totalTimespan should be 30 - 10 = 20.
	span, ok := data["totalTimespan"].(float64)
	if !ok {
		t.Fatalf("totalTimespan missing or wrong type")
	}
	if span != 20.0 {
		t.Errorf("totalTimespan: got %v, want 20", span)
	}
}

func TestTemporal_LongestGap(t *testing.T) {
	p := NewTemporalThinkingPattern()

	// Events at 0, 1, 5, 6 — longest gap is between 1 and 5 (duration 4).
	input := map[string]any{
		"timeFrame": "test window",
		"events": []any{
			makeEvent(6.0, "d"),
			makeEvent(0.0, "a"),
			makeEvent(5.0, "c"),
			makeEvent(1.0, "b"),
		},
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-session")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	data := result.Data
	gapRaw, ok := data["longestGap"]
	if !ok {
		t.Fatal("longestGap missing from result")
	}
	gap, ok := gapRaw.(map[string]any)
	if !ok {
		t.Fatalf("longestGap has unexpected type %T", gapRaw)
	}

	wantStart, wantEnd, wantDuration := 1.0, 5.0, 4.0
	if gap["start"].(float64) != wantStart {
		t.Errorf("longestGap.start: got %v, want %v", gap["start"], wantStart)
	}
	if gap["end"].(float64) != wantEnd {
		t.Errorf("longestGap.end: got %v, want %v", gap["end"], wantEnd)
	}
	if gap["duration"].(float64) != wantDuration {
		t.Errorf("longestGap.duration: got %v, want %v", gap["duration"], wantDuration)
	}
}

// TestTemporal_AutoAnalysis_MigrationKeyword verifies that when events are empty and
// timeFrame contains "migration", suggestedPhases includes migration-specific phases.
func TestTemporal_AutoAnalysis_MigrationKeyword(t *testing.T) {
	p := NewTemporalThinkingPattern()

	input := map[string]any{
		"timeFrame": "Q1 2026 migration",
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-auto-migration")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	data := result.Data

	// suggestedPhases must be present and non-empty.
	sp, ok := data["suggestedPhases"].([]string)
	if !ok || len(sp) == 0 {
		t.Errorf("expected non-empty suggestedPhases, got %v (%T)", data["suggestedPhases"], data["suggestedPhases"])
	}

	// autoAnalysis must be present.
	if _, ok := data["autoAnalysis"]; !ok {
		t.Error("expected autoAnalysis field")
	}

	// guidance must be present.
	if _, ok := data["guidance"]; !ok {
		t.Error("expected guidance field")
	}

	// sortedEvents must NOT be present (no events provided).
	if _, ok := data["sortedEvents"]; ok {
		t.Error("sortedEvents must not appear when no events are provided")
	}
}

// TestTemporal_AutoAnalysis_BackwardCompat verifies that when events ARE provided,
// the existing timeline behavior is preserved (no suggestedPhases) and guidance is added.
func TestTemporal_AutoAnalysis_BackwardCompat(t *testing.T) {
	p := NewTemporalThinkingPattern()

	input := map[string]any{
		"timeFrame": "Q1 2024",
		"events": []any{
			makeEvent(10.0, "start"),
			makeEvent(20.0, "end"),
		},
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-compat")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	data := result.Data

	// suggestedPhases must NOT be present when events are provided.
	if _, ok := data["suggestedPhases"]; ok {
		t.Error("suggestedPhases must not appear when events are provided")
	}

	// Existing timeline fields must still be present.
	if _, ok := data["sortedEvents"]; !ok {
		t.Error("sortedEvents must be present when events are provided")
	}
	if _, ok := data["totalTimespan"]; !ok {
		t.Error("totalTimespan must be present when events are provided")
	}

	// guidance must be present.
	if _, ok := data["guidance"]; !ok {
		t.Error("expected guidance field")
	}
}

// TestTemporal_AutoAnalysis_DefaultPhases verifies that unrecognized timeFrame text
// falls back to default phases.
func TestTemporal_AutoAnalysis_DefaultPhases(t *testing.T) {
	p := NewTemporalThinkingPattern()

	input := map[string]any{
		"timeFrame": "some unrecognized context",
	}
	validated, _ := p.Validate(input)
	result, err := p.Handle(validated, "test-auto-default")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	sp, ok := result.Data["suggestedPhases"].([]string)
	if !ok || len(sp) == 0 {
		t.Fatalf("expected suggestedPhases, got %v", result.Data["suggestedPhases"])
	}
	// Default phases must include "planning" and "execution".
	found := map[string]bool{}
	for _, p := range sp {
		found[p] = true
	}
	for _, want := range []string{"planning", "execution"} {
		if !found[want] {
			t.Errorf("expected default phases to include %q, got %v", want, sp)
		}
	}
}

func TestTemporal_NoTimestamps(t *testing.T) {
	p := NewTemporalThinkingPattern()

	// Events without "time" or "timestamp" — should fall back to counts only.
	input := map[string]any{
		"timeFrame": "legacy window",
		"states":    []any{"idle", "running"},
		"events": []any{
			map[string]any{"name": "start"},
			map[string]any{"name": "stop"},
		},
		"transitions": []any{"idle->running"},
		"constraints": []any{"max 1 concurrent"},
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	result, err := p.Handle(validated, "test-session")
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	data := result.Data

	// Timeline fields must NOT be present.
	if _, ok := data["sortedEvents"]; ok {
		t.Error("sortedEvents should not be present when events have no timestamps")
	}
	if _, ok := data["totalTimespan"]; ok {
		t.Error("totalTimespan should not be present when events have no timestamps")
	}
	if _, ok := data["longestGap"]; ok {
		t.Error("longestGap should not be present when events have no timestamps")
	}

	// Counts must be correct.
	if data["stateCount"].(int) != 2 {
		t.Errorf("stateCount: got %v, want 2", data["stateCount"])
	}
	if data["eventCount"].(int) != 2 {
		t.Errorf("eventCount: got %v, want 2", data["eventCount"])
	}
	if data["transitionCount"].(int) != 1 {
		t.Errorf("transitionCount: got %v, want 1", data["transitionCount"])
	}
	if data["constraintCount"].(int) != 1 {
		t.Errorf("constraintCount: got %v, want 1", data["constraintCount"])
	}
	if data["totalComponents"].(int) != 6 {
		t.Errorf("totalComponents: got %v, want 6", data["totalComponents"])
	}
}

func TestTemporal_MermaidDiagram(t *testing.T) {
	p := NewTemporalThinkingPattern()
	input := map[string]any{
		"timeFrame":   "deployment window",
		"states":      []any{"staging", "production"},
		"events":      []any{makeEvent(1.0, "deploy-start"), makeEvent(2.0, "deploy-end")},
		"transitions": []any{"staging -> production"},
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(validated, "test-mermaid")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	diagram, ok := result.Data["mermaidDiagram"].(string)
	if !ok || diagram == "" {
		t.Fatal("expected non-empty mermaidDiagram")
	}
	if !contains(diagram, "sequenceDiagram") {
		t.Error("diagram must start with sequenceDiagram")
	}
	if !contains(diagram, "participant staging") {
		t.Error("diagram must declare staging participant")
	}
	if !contains(diagram, "staging->>production") {
		t.Error("diagram must contain transition arrow")
	}
}

func TestTemporal_MermaidDiagram_Empty(t *testing.T) {
	p := NewTemporalThinkingPattern()
	input := map[string]any{"timeFrame": "empty test"}
	validated, _ := p.Validate(input)
	result, err := p.Handle(validated, "test-mermaid-empty")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if _, ok := result.Data["mermaidDiagram"]; ok {
		t.Error("mermaidDiagram should not be present when no states/events/transitions")
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
